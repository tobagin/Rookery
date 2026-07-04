// Package systemd controls units through systemctl, for the system manager
// and for per-user managers (rootless Quadlets). It shells out rather than
// speaking D-Bus directly so a rootful Rookery can reach other users'
// session managers via systemctl's --machine user@.host switch; the Manager
// surface is an interface-shaped seam where a native D-Bus client can be
// swapped in later.
package systemd

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"os/user"
	"strings"
)

// Scope selects which systemd manager to talk to: the system manager
// (User == "") or a specific user's session manager.
type Scope struct {
	User string
}

func (s Scope) IsSystem() bool { return s.User == "" }

func (s Scope) String() string {
	if s.IsSystem() {
		return "system"
	}
	return s.User
}

// Runner executes a command and returns its stdout. It exists so tests can
// observe exactly what would be run without a live systemd.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), fmt.Errorf("%s %s: %s", name, strings.Join(args, " "), msg)
	}
	return stdout.String(), nil
}

// UnitStatus is the subset of `systemctl show` state Rookery surfaces.
type UnitStatus struct {
	Load     string `json:"load"`     // loaded, not-found, ...
	Active   string `json:"active"`   // active, inactive, failed, activating, ...
	Sub      string `json:"sub"`      // running, exited, dead, ...
	UnitFile string `json:"unitFile"` // enabled, disabled, generated, ...
}

// Manager runs systemctl against a chosen scope.
type Manager struct {
	runner      Runner
	currentUser string
}

// NewManager returns a Manager that executes the real systemctl.
func NewManager() *Manager {
	current := ""
	if u, err := user.Current(); err == nil {
		current = u.Username
	}
	return &Manager{runner: execRunner{}, currentUser: current}
}

// NewManagerWithRunner is the test seam.
func NewManagerWithRunner(r Runner, currentUser string) *Manager {
	return &Manager{runner: r, currentUser: currentUser}
}

// args prefixes systemctl arguments for the scope: nothing for the system
// manager, --user for our own session, --user --machine for another user's
// session (requires root).
func (m *Manager) args(scope Scope, rest ...string) []string {
	var a []string
	switch {
	case scope.IsSystem():
	case scope.User == m.currentUser:
		a = append(a, "--user")
	default:
		a = append(a, "--user", "--machine", scope.User+"@.host")
	}
	return append(a, rest...)
}

func (m *Manager) run(ctx context.Context, scope Scope, rest ...string) (string, error) {
	return m.runner.Run(ctx, "systemctl", m.args(scope, rest...)...)
}

func (m *Manager) Start(ctx context.Context, scope Scope, unit string) error {
	_, err := m.run(ctx, scope, "start", unit)
	return err
}

func (m *Manager) Stop(ctx context.Context, scope Scope, unit string) error {
	_, err := m.run(ctx, scope, "stop", unit)
	return err
}

func (m *Manager) Restart(ctx context.Context, scope Scope, unit string) error {
	_, err := m.run(ctx, scope, "restart", unit)
	return err
}

func (m *Manager) Enable(ctx context.Context, scope Scope, unit string) error {
	_, err := m.run(ctx, scope, "enable", unit)
	return err
}

func (m *Manager) Disable(ctx context.Context, scope Scope, unit string) error {
	_, err := m.run(ctx, scope, "disable", unit)
	return err
}

// DaemonReload re-runs the generators; required after any unit-file write.
func (m *Manager) DaemonReload(ctx context.Context, scope Scope) error {
	_, err := m.run(ctx, scope, "daemon-reload")
	return err
}

// Status fetches state for units in one systemctl invocation. The result
// has the same length and order as units.
func (m *Manager) Status(ctx context.Context, scope Scope, units []string) ([]UnitStatus, error) {
	if len(units) == 0 {
		return nil, nil
	}
	args := append([]string{"show", "--property=LoadState,ActiveState,SubState,UnitFileState"}, units...)
	out, err := m.run(ctx, scope, args...)
	if err != nil {
		return nil, err
	}
	blocks := parseShowBlocks(out)
	statuses := make([]UnitStatus, len(units))
	for i := range units {
		if i >= len(blocks) {
			statuses[i] = UnitStatus{Load: "unknown"}
			continue
		}
		b := blocks[i]
		statuses[i] = UnitStatus{
			Load:     b["LoadState"],
			Active:   b["ActiveState"],
			Sub:      b["SubState"],
			UnitFile: b["UnitFileState"],
		}
	}
	return statuses, nil
}

// parseShowBlocks splits `systemctl show` output into per-unit key=value
// maps; systemctl separates units with a blank line, in argument order.
func parseShowBlocks(out string) []map[string]string {
	var blocks []map[string]string
	cur := map[string]string{}
	flush := func() {
		if len(cur) > 0 {
			blocks = append(blocks, cur)
			cur = map[string]string{}
		}
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			flush()
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			cur[k] = v
		}
	}
	flush()
	return blocks
}
