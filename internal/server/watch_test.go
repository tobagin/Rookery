package server

import (
	"context"
	"strings"
	"testing"

	"github.com/tobagin/rookery/internal/systemd"
)

func TestPollFailuresTransitions(t *testing.T) {
	srv, sysd, _ := newTestServer(t, okValidator)
	var got []string
	notify := func(title, msg string) { got = append(got, title+": "+msg) }
	prev := map[string]string{}
	ctx := context.Background()

	// Baseline poll: a unit already failed at startup is recorded silently.
	sysd.states["jellyfin.service"] = systemd.UnitStatus{Load: "loaded", Active: "failed", Sub: "failed", ExitCode: 143, Restarts: 4}
	srv.pollFailures(ctx, prev, true, notify)
	if len(got) != 0 {
		t.Fatalf("baseline poll must not alert; got %v", got)
	}

	// Recovery after baseline-failed alerts.
	sysd.states["jellyfin.service"] = systemd.UnitStatus{Load: "loaded", Active: "active", Sub: "running"}
	srv.pollFailures(ctx, prev, false, notify)
	if len(got) != 1 || !strings.Contains(got[0], "recovered") {
		t.Fatalf("recovery alert missing; got %v", got)
	}

	// Steady state: no repeat alerts.
	srv.pollFailures(ctx, prev, false, notify)
	if len(got) != 1 {
		t.Fatalf("steady state alerted; got %v", got)
	}

	// New failure alerts with exit code and restart count.
	sysd.states["jellyfin.service"] = systemd.UnitStatus{Load: "loaded", Active: "failed", Sub: "failed", ExitCode: 1, Restarts: 2}
	srv.pollFailures(ctx, prev, false, notify)
	if len(got) != 2 || !strings.Contains(got[1], "failed") ||
		!strings.Contains(got[1], "exit code 1") || !strings.Contains(got[1], "2 restarts") {
		t.Fatalf("failure alert wrong; got %v", got)
	}
}
