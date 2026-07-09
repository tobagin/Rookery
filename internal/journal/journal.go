// Package journal streams unit logs by running journalctl, which handles
// permissions, rotation, and follow mode for us.
package journal

import (
	"context"
	"fmt"
	"os/exec"
	"os/user"

	"github.com/tobagin/rookery/internal/rhost"
	"github.com/tobagin/rookery/internal/systemd"
)

// Command builds a journalctl invocation for a unit's logs in the given
// scope. Output is one JSON object per line (-o json). For another local
// user's session (rootful Rookery), journal fields are matched directly
// since `journalctl --user` cannot cross users; for remote scopes the
// whole command runs over ssh as the target account.
func Command(ctx context.Context, scope systemd.Scope, unit string, lines int, follow bool, since string) (*exec.Cmd, error) {
	args := []string{"-o", "json", "--no-pager", "-n", fmt.Sprint(lines)}
	if since != "" {
		args = append(args, "--since", since)
	}
	if follow {
		args = append(args, "-f")
	}
	if scope.IsRemote() {
		inner := append([]string{"journalctl"}, args...)
		if scope.IsSystem() {
			inner = append(inner, "-u", unit)
			argv := rhost.Argv(scope.SSH, rhost.Script(false, inner))
			return exec.CommandContext(ctx, argv[0], argv[1:]...), nil
		}
		inner = append(inner, "_SYSTEMD_USER_UNIT="+unit)
		script := `export XDG_RUNTIME_DIR="/run/user/$(id -u)"; export DBUS_SESSION_BUS_ADDRESS="unix:path=$XDG_RUNTIME_DIR/bus"; ` +
			rhost.QuoteArgv(inner) + ` "_UID=$(id -u)"`
		argv := rhost.Argv(scope.SSH, script)
		return exec.CommandContext(ctx, argv[0], argv[1:]...), nil
	}
	switch {
	case scope.IsSystem():
		args = append(args, "-u", unit)
	case isCurrentUser(scope.User):
		args = append(args, "--user", "-u", unit)
	default:
		u, err := user.Lookup(scope.User)
		if err != nil {
			return nil, fmt.Errorf("look up user %q: %w", scope.User, err)
		}
		args = append(args, "_SYSTEMD_USER_UNIT="+unit, "_UID="+u.Uid)
	}
	return exec.CommandContext(ctx, "journalctl", args...), nil
}

func isCurrentUser(name string) bool {
	u, err := user.Current()
	return err == nil && u.Username == name
}
