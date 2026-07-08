package journal

import (
	"context"
	"strings"
	"testing"

	"github.com/tobagin/rookery/internal/systemd"
)

func TestCommandSystem(t *testing.T) {
	cmd, err := Command(context.Background(), systemd.Scope{}, "web.service", 100, true)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(cmd.Args, " ")
	for _, want := range []string{"journalctl", "-o json", "-n 100", "-f", "-u web.service"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args %q missing %q", joined, want)
		}
	}
}

func TestCommandRemote(t *testing.T) {
	cmd, err := Command(context.Background(), systemd.Scope{User: "bob", SSH: "bob@nas"}, "web.service", 50, false)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(cmd.Args, " ")
	if !strings.HasPrefix(joined, "ssh ") || !strings.Contains(joined, "bob@nas") {
		t.Errorf("remote journal cmd = %q", joined)
	}
	for _, want := range []string{"sh -c", "journalctl", "_SYSTEMD_USER_UNIT=web.service", "_UID=$(id -u)", "XDG_RUNTIME_DIR"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args %q missing %q", joined, want)
		}
	}
	if strings.Contains(joined, "-f") && strings.Contains(joined, "'-f'") {
		t.Errorf("follow flag present without follow: %q", joined)
	}
}
