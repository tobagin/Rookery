// Package journal streams unit logs by running journalctl, which handles
// permissions, rotation, and follow mode for us.
package journal

import (
	"context"
	"fmt"
	"os/exec"
	"os/user"

	"github.com/tobagin/rookery/internal/systemd"
)

// Command builds a journalctl invocation for a unit's logs in the given
// scope. Output is one JSON object per line (-o json). For another user's
// session (rootful Rookery), journal fields are matched directly since
// `journalctl --user` cannot cross users.
func Command(ctx context.Context, scope systemd.Scope, unit string, lines int, follow bool) (*exec.Cmd, error) {
	args := []string{"-o", "json", "--no-pager", "-n", fmt.Sprint(lines)}
	if follow {
		args = append(args, "-f")
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
