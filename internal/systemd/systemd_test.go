package systemd

import (
	"context"
	"strings"
	"testing"
)

type fakeRunner struct {
	calls [][]string
	out   string
	err   error
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	return f.out, f.err
}

func TestScopePrefixes(t *testing.T) {
	cases := []struct {
		scope Scope
		want  string
	}{
		{Scope{}, "systemctl start x.service"},
		{Scope{User: "alice"}, "systemctl --user start x.service"},
		{Scope{User: "bob"}, "systemctl --user --machine bob@.host start x.service"},
	}
	for _, c := range cases {
		f := &fakeRunner{}
		m := NewManagerWithRunner(f, "alice")
		if err := m.Start(context.Background(), c.scope, "x.service"); err != nil {
			t.Fatal(err)
		}
		got := strings.Join(f.calls[0], " ")
		if got != c.want {
			t.Errorf("scope %q: got %q, want %q", c.scope, got, c.want)
		}
	}
}

func TestRemoteScope(t *testing.T) {
	// Remote system scope: ssh-wrapped plain systemctl.
	f := &fakeRunner{}
	m := NewManagerWithRunner(f, "alice")
	if err := m.Start(context.Background(), Scope{SSH: "root@nas"}, "x.service"); err != nil {
		t.Fatal(err)
	}
	call := strings.Join(f.calls[0], " ")
	if !strings.HasPrefix(call, "ssh ") || !strings.Contains(call, "root@nas") ||
		!strings.Contains(call, "sh -c") ||
		!strings.Contains(call, "systemctl") ||
		!strings.Contains(call, "start") ||
		!strings.Contains(call, "x.service") {
		t.Errorf("remote system call = %q", call)
	}
	if strings.Contains(call, "--user") {
		t.Errorf("remote system scope must not use --user: %q", call)
	}

	// Remote user scope: --user plus the session env a headless ssh needs.
	f2 := &fakeRunner{}
	m2 := NewManagerWithRunner(f2, "alice")
	if err := m2.Stop(context.Background(), Scope{User: "bob", SSH: "bob@nas"}, "x.service"); err != nil {
		t.Fatal(err)
	}
	call = strings.Join(f2.calls[0], " ")
	if !strings.Contains(call, "sh -c") ||
		!strings.Contains(call, "systemctl") ||
		!strings.Contains(call, "--user") ||
		!strings.Contains(call, "stop") ||
		!strings.Contains(call, "x.service") ||
		!strings.Contains(call, "XDG_RUNTIME_DIR") {
		t.Errorf("remote user call = %q", call)
	}
	if strings.Contains(call, "--machine") {
		t.Errorf("remote user scope must not use --machine: %q", call)
	}
}

func TestDaemonReload(t *testing.T) {
	f := &fakeRunner{}
	m := NewManagerWithRunner(f, "alice")
	if err := m.DaemonReload(context.Background(), Scope{User: "alice"}); err != nil {
		t.Fatal(err)
	}
	want := "systemctl --user daemon-reload"
	if got := strings.Join(f.calls[0], " "); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestStatus(t *testing.T) {
	f := &fakeRunner{out: `LoadState=loaded
ActiveState=active
SubState=running
UnitFileState=enabled

LoadState=not-found
ActiveState=inactive
SubState=dead
UnitFileState=
`}
	m := NewManagerWithRunner(f, "alice")
	got, err := m.Status(context.Background(), Scope{}, []string{"a.service", "b.service"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d statuses, want 2", len(got))
	}
	if got[0].Active != "active" || got[0].Sub != "running" || got[0].UnitFile != "enabled" {
		t.Errorf("got[0] = %+v", got[0])
	}
	if got[1].Load != "not-found" || got[1].Active != "inactive" {
		t.Errorf("got[1] = %+v", got[1])
	}
	call := strings.Join(f.calls[0], " ")
	if !strings.Contains(call, "show --property=") || !strings.Contains(call, "a.service b.service") {
		t.Errorf("unexpected systemctl call: %q", call)
	}
}

func TestStatusEmpty(t *testing.T) {
	f := &fakeRunner{}
	m := NewManagerWithRunner(f, "alice")
	got, err := m.Status(context.Background(), Scope{}, nil)
	if err != nil || got != nil {
		t.Errorf("Status(nil units) = %v, %v; want nil, nil", got, err)
	}
	if len(f.calls) != 0 {
		t.Error("Status(nil units) must not invoke systemctl")
	}
}
