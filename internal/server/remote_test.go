package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rookerylabs/rookery/internal/gpu"
	"github.com/rookerylabs/rookery/internal/rhost/rhosttest"
	"github.com/rookerylabs/rookery/internal/systemd"
)

// newRemoteServer builds a server whose only area lives on a "remote" host
// reached through the ssh shim — every file operation crosses the full
// quoting/transport path instead of plain os calls.
func newRemoteServer(t *testing.T) (*Server, *fakeSystemd, string) {
	t.Helper()
	rhosttest.InstallShim(t)
	dir := t.TempDir()
	unit := "[Unit]\nDescription=Remote app\n\n[Container]\nImage=docker.io/library/nginx:latest\n"
	if err := os.WriteFile(filepath.Join(dir, "app.container"), []byte(unit), 0o644); err != nil {
		t.Fatal(err)
	}
	sysd := &fakeSystemd{states: map[string]systemd.UnitStatus{
		"app.service": {Load: "loaded", Active: "active", Sub: "running"},
	}}
	srv := New(Options{
		Areas:   []Area{{Label: "nas", Scope: systemd.Scope{SSH: "root@nas"}, Dirs: []string{dir}}},
		Systemd: sysd,
		// no Validate injection: the remote path must use ValidateRemote
		// through the shim (which degrades to unavailable — no generator).
	})
	return srv, sysd, dir
}

func TestRemoteList(t *testing.T) {
	srv, _, _ := newRemoteServer(t)
	rec, body := doJSON(t, srv, "GET", "/api/units", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	units := body["units"].([]any)
	if len(units) != 1 {
		t.Fatalf("got %d units, want 1", len(units))
	}
	u := units[0].(map[string]any)
	if u["scope"] != "nas" || u["name"] != "app.container" || u["description"] != "Remote app" || u["active"] != "active" {
		t.Errorf("unit = %v", u)
	}
}

func TestRemoteCRUD(t *testing.T) {
	srv, sysd, dir := newRemoteServer(t)

	rec, body := doJSON(t, srv, "GET", "/api/units/nas/app.container", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET: %d %s", rec.Code, rec.Body.String())
	}
	if body["content"].(string) == "" {
		t.Fatal("empty content over remote read")
	}

	content := "[Container]\nImage=docker.io/library/redis:7\n"
	rec, body = doJSON(t, srv, "PUT", "/api/units/nas/cache.container",
		fmt.Sprintf(`{"content": %q, "restart": true}`, content))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT: %d %s", rec.Code, rec.Body.String())
	}
	v := body["validation"].(map[string]any)
	if v["available"] != false {
		t.Errorf("remote validation should be unavailable through the shim host: %v", v)
	}
	onDisk, err := os.ReadFile(filepath.Join(dir, "cache.container"))
	if err != nil || string(onDisk) != content {
		t.Fatalf("remote write landed as %q, %v", onDisk, err)
	}
	// daemon-reload and restart must have been issued against the remote scope.
	joined := fmt.Sprint(sysd.calls)
	if !strings.Contains(joined, "daemon-reload root@nas") || !strings.Contains(joined, "restart root@nas cache.service") {
		t.Errorf("systemd calls = %v", sysd.calls)
	}

	rec, _ = doJSON(t, srv, "DELETE", "/api/units/nas/cache.container", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE: %d %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "cache.container")); !os.IsNotExist(err) {
		t.Error("remote delete left the file behind")
	}
}

func TestRemoteUpdates(t *testing.T) {
	srv, sysd, _ := newRemoteServer(t)
	srv.resolve = func(_ context.Context, _ string) (string, error) { return "sha256:new", nil }
	var askedTarget string
	srv.remoteDigests = func(_ context.Context, target string, _ bool, _ string) ([]string, error) {
		askedTarget = target
		return []string{"docker.io/library/nginx@sha256:old"}, nil
	}

	rec, body := doJSON(t, srv, "GET", "/api/updates", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/updates: %d %s", rec.Code, rec.Body.String())
	}
	if askedTarget != "root@nas" {
		t.Errorf("digest lookup target = %q, want root@nas", askedTarget)
	}
	updates := body["updates"].([]any)
	if len(updates) != 1 {
		t.Fatalf("got %d update rows, want 1 (remote scope must not be skipped)", len(updates))
	}
	row := updates[0].(map[string]any)
	if row["scope"] != "nas" || row["updateAvailable"] != true {
		t.Errorf("row = %v", row)
	}
	if skipped := body["skippedScopes"].([]any); len(skipped) != 0 {
		t.Errorf("skippedScopes = %v, want none", skipped)
	}

	pulled := ""
	srv.remotePull = func(_ context.Context, target string, _ bool, image string) error {
		pulled = target + " " + image
		return nil
	}
	rec, _ = doJSON(t, srv, "POST", "/api/units/nas/app.container/update", "{}")
	if rec.Code != http.StatusOK {
		t.Fatalf("POST update: %d %s", rec.Code, rec.Body.String())
	}
	if pulled != "root@nas docker.io/library/nginx:latest" {
		t.Errorf("pulled = %q", pulled)
	}
	if joined := fmt.Sprint(sysd.calls); !strings.Contains(joined, "restart root@nas app.service") {
		t.Errorf("systemd calls = %v; want remote restart", sysd.calls)
	}
}

func TestRemoteGPUs(t *testing.T) {
	srv, _, _ := newRemoteServer(t)
	srv.gpus = func(_ context.Context) []gpu.Device { return nil }
	srv.remoteGPUs = func(_ context.Context, target string) []gpu.Device {
		if target != "root@nas" {
			t.Errorf("probe target = %q", target)
		}
		return []gpu.Device{{Vendor: "nvidia", Name: "NVIDIA T400", MemoryTotalMB: 2048, MemoryUsedMB: -1, UtilizationPct: -1}}
	}
	rec, body := doJSON(t, srv, "GET", "/api/gpus", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	devices := body["devices"].([]any)
	if len(devices) != 1 {
		t.Fatalf("got %d devices, want 1", len(devices))
	}
	if d := devices[0].(map[string]any); d["host"] != "nas" || d["vendor"] != "nvidia" {
		t.Errorf("device = %v", d)
	}
}

func TestRemoteHistoryDisabled(t *testing.T) {
	srv, _, _ := newRemoteServer(t)
	rec, body := doJSON(t, srv, "GET", "/api/units/nas/app.container/history", "")
	if rec.Code != http.StatusOK || body["enabled"] != false {
		t.Errorf("remote history: %d %v — git must be off for remote areas", rec.Code, body)
	}
}
