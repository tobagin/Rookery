// Package rhost runs Rookery's operations on remote hosts over plain ssh —
// the PRD's agentless multi-host model: nothing to install or babysit on
// the target beyond an SSH server and Podman. Every operation is a single
// ssh invocation wrapping a POSIX shell script; file transfer rides stdin.
package rhost

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os/exec"
	"path"
	"sort"
	"strconv"
	"strings"
)

// SSHCommand is the ssh invocation prefix. BatchMode keeps a missing key or
// password prompt from hanging the server. Tests point this at a shim.
var SSHCommand = []string{
	"ssh",
	"-o", "BatchMode=yes",
	"-o", "ConnectTimeout=8",
}

// Quote single-quotes s for a POSIX shell.
func Quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// QuoteArgv renders argv as one shell-safe command string — ssh joins its
// remote-command arguments with spaces and hands them to the remote shell,
// so quoting is on us.
func QuoteArgv(argv []string) string {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = Quote(a)
	}
	return strings.Join(quoted, " ")
}

// Script renders argv for remote execution. userSession additionally
// exports the environment a user-manager command (systemctl --user,
// journalctl --user) needs in a non-interactive ssh session; the target
// user should have lingering enabled (loginctl enable-linger).
func Script(userSession bool, argv []string) string {
	s := QuoteArgv(argv)
	if userSession {
		return `export XDG_RUNTIME_DIR="/run/user/$(id -u)"; export DBUS_SESSION_BUS_ADDRESS="unix:path=$XDG_RUNTIME_DIR/bus"; ` + s
	}
	return s
}

// Argv builds the full local ssh invocation that runs script on target.
func Argv(target, script string) []string {
	out := append([]string{}, SSHCommand...)
	return append(out, target, "--", script)
}

// Error is a failed remote execution. ssh reserves exit code 255 for
// transport failures (unreachable host, auth), so callers can tell "the
// command failed over there" from "we never got there".
type Error struct {
	Target   string
	ExitCode int
	Stderr   string
}

func (e *Error) Error() string {
	msg := e.Stderr
	if msg == "" {
		msg = fmt.Sprintf("exit code %d", e.ExitCode)
	}
	return fmt.Sprintf("ssh %s: %s", e.Target, msg)
}

// Transport reports whether the failure was reaching the host at all.
func (e *Error) Transport() bool { return e.ExitCode == 255 || e.ExitCode == -1 }

// Run executes script on target. stdin, when non-nil, is piped to the
// remote command. Output is the remote stdout; a non-zero remote exit
// returns an *Error carrying remote stderr and the exit code.
func Run(ctx context.Context, target, script string, stdin []byte) (string, error) {
	argv := Argv(target, script)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		code := -1
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), &Error{Target: target, ExitCode: code, Stderr: msg}
	}
	return stdout.String(), nil
}

// ReadFile reads a remote file.
func ReadFile(ctx context.Context, target, p string) ([]byte, error) {
	out, err := Run(ctx, target, "cat -- "+Quote(p), nil)
	return []byte(out), err
}

// WriteFileAtomic writes data to a remote path via temp file + rename, the
// same never-half-written guarantee the local writer gives the generator.
func WriteFileAtomic(ctx context.Context, target, p string, data []byte) error {
	dir := path.Dir(p)
	script := fmt.Sprintf(
		`d=%s && mkdir -p -- "$d" && t=$(mktemp "$d/.rookery-XXXXXX") && cat > "$t" && chmod 644 -- "$t" && mv -- "$t" %s`,
		Quote(dir), Quote(p))
	_, err := Run(ctx, target, script, data)
	return err
}

// Remove deletes a remote file.
func Remove(ctx context.Context, target, p string) error {
	_, err := Run(ctx, target, "rm -- "+Quote(p), nil)
	return err
}

// Exists reports whether a remote regular file exists. The script exits 0
// either way, so a returned error means transport trouble, not absence.
func Exists(ctx context.Context, target, p string) (bool, error) {
	out, err := Run(ctx, target, fmt.Sprintf(`if [ -f %s ]; then echo yes; else echo no; fi`, Quote(p)), nil)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "yes", nil
}

// ReadDirFiles fetches every regular file in a remote directory in ONE ssh
// round trip (a per-file fetch would make the dashboard unusable over
// WAN latency). Entries are separated by a per-call random token, so file
// content cannot forge a boundary. A missing directory yields an empty map.
func ReadDirFiles(ctx context.Context, target, dir string) (map[string][]byte, error) {
	tokenBytes := make([]byte, 16)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, err
	}
	token := "ROOKERY-" + hex.EncodeToString(tokenBytes)
	script := fmt.Sprintf(
		`if [ -d %s ]; then for f in %s/*; do [ -f "$f" ] || continue; printf '\n%s %%s\n' "$f"; cat -- "$f"; done; fi`,
		Quote(dir), Quote(dir), token)
	out, err := Run(ctx, target, script, nil)
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	for _, chunk := range strings.Split(out, "\n"+token+" ")[1:] {
		name, content, ok := strings.Cut(chunk, "\n")
		if !ok {
			continue
		}
		files[name] = []byte(content)
	}
	return files, nil
}

// SortedNames returns the map keys ordered, for deterministic listings.
func SortedNames(files map[string][]byte) []string {
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// ImageDigests returns the RepoDigests the remote host's podman stores for
// image — the remote counterpart of the local Podman API lookup that drives
// update checks.
func ImageDigests(ctx context.Context, target string, userSession bool, image string) ([]string, error) {
	script := Script(userSession, []string{
		"podman", "image", "inspect", "--format", "{{range .RepoDigests}}{{println .}}{{end}}", image})
	out, err := Run(ctx, target, script, nil)
	if err != nil {
		return nil, err
	}
	var digests []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			digests = append(digests, l)
		}
	}
	return digests, nil
}

// PullImage pulls image on the remote host.
func PullImage(ctx context.Context, target string, userSession bool, image string) error {
	_, err := Run(ctx, target, Script(userSession, []string{"podman", "pull", "-q", image}), nil)
	return err
}

// Probe identifies the ssh account on target: uid decides whether Rookery
// manages the system Quadlet tree or the account's rootless one.
func Probe(ctx context.Context, target string) (uid int, home, user string, err error) {
	out, err := Run(ctx, target, `printf '%s\n%s\n%s\n' "$(id -u)" "$HOME" "$(id -un)"`, nil)
	if err != nil {
		return 0, "", "", err
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 3 {
		return 0, "", "", fmt.Errorf("ssh %s: unexpected probe output %q", target, out)
	}
	uid, err = strconv.Atoi(strings.TrimSpace(lines[0]))
	if err != nil {
		return 0, "", "", fmt.Errorf("ssh %s: bad uid %q", target, lines[0])
	}
	return uid, strings.TrimSpace(lines[1]), strings.TrimSpace(lines[2]), nil
}
