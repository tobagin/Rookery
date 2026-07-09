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
	"encoding/json"
	"fmt"
	"os/exec"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/tobagin/rookery/internal/hostinfo"
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
	return append(out, target, "--", "sh", "-c", Quote(script))
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

func Metrics(ctx context.Context, target string) (hostinfo.Metrics, error) {
	script := `python3 - <<'PY'
import json, os, platform
m={"hostname":platform.node(),"kernel":platform.release(),"cores":os.cpu_count() or 0,"cpuPct":-1,"load1":0,"memTotalKb":0,"memAvailKb":0,"uptimeSeconds":0}
try:
  m["load1"]=float(open("/proc/loadavg").read().split()[0])
except Exception: pass
try:
  m["uptimeSeconds"]=int(float(open("/proc/uptime").read().split()[0]))
except Exception: pass
try:
  for line in open("/proc/meminfo"):
    k,v=line.split(":",1); n=int(v.split()[0])
    if k=="MemTotal": m["memTotalKb"]=n
    if k=="MemAvailable": m["memAvailKb"]=n
except Exception: pass
print(json.dumps(m))
PY`
	out, err := Run(ctx, target, script, nil)
	if err != nil {
		return hostinfo.Metrics{}, err
	}
	var m hostinfo.Metrics
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		return hostinfo.Metrics{}, err
	}
	return m, nil
}

type ContainerRuntime struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Service  string  `json:"service"`
	CPUPct   float64 `json:"cpuPct"`
	MemBytes int64   `json:"memBytes"`
	Health   string  `json:"health,omitempty"`
}

func ContainerStats(ctx context.Context, target string, userSession bool) ([]ContainerRuntime, error) {
	script := Script(userSession, []string{"sh", "-c", `python3 - <<'PY'
import json, subprocess
def run(args):
  try: return subprocess.check_output(args, text=True, stderr=subprocess.DEVNULL)
  except Exception: return ""
stats={}
for line in run(["podman","stats","--no-stream","--format","json"]).splitlines():
  try:
    row=json.loads(line)
    cid=row.get("ContainerID") or row.get("ID") or row.get("id") or ""
    name=row.get("Name") or row.get("name") or ""
    cpu=str(row.get("CPU") or row.get("CPUPerc") or "0").strip().rstrip("%")
    mem=str(row.get("MemUsage") or row.get("MemUsageBytes") or "0").split("/")[0].strip().split()
    mult={"KB":1024,"KIB":1024,"MB":1048576,"MIB":1048576,"GB":1073741824,"GIB":1073741824}
    mb=float(mem[0]) if mem else 0
    unit=mem[1].upper() if len(mem)>1 else ""
    stats[cid or name]={"id":cid,"name":name,"cpuPct":float(cpu or 0),"memBytes":int(mb*mult.get(unit,1))}
  except Exception: pass
out=[]
for line in run(["podman","ps","--all","--format","json"]).splitlines():
  try:
    row=json.loads(line)
    cid=row.get("Id") or row.get("ID") or row.get("id") or ""
    names=row.get("Names") or []
    name=names[0] if isinstance(names,list) and names else (row.get("Names") or row.get("Name") or cid)
    labels=row.get("Labels") or {}
    svc=labels.get("PODMAN_SYSTEMD_UNIT") or ""
    if not svc: continue
    item=stats.get(cid) or stats.get(name) or {"id":cid,"name":name,"cpuPct":0,"memBytes":0}
    item["service"]=svc
    inspect=run(["podman","inspect",cid or name])
    try:
      raw=json.loads(inspect); obj=raw[0] if isinstance(raw,list) else raw
      item["health"]=(((obj.get("State") or {}).get("Health") or {}).get("Status") or "")
    except Exception: pass
    out.append(item)
  except Exception: pass
print(json.dumps(out))
PY`})
	out, err := Run(ctx, target, script, nil)
	if err != nil {
		return nil, err
	}
	var rows []ContainerRuntime
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		return nil, err
	}
	return rows, nil
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
