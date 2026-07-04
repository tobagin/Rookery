package server

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tobagin/rookery/internal/rhost/rhosttest"
	"github.com/tobagin/rookery/internal/systemd"
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

func TestRemoteHistoryDisabled(t *testing.T) {
	srv, _, _ := newRemoteServer(t)
	rec, body := doJSON(t, srv, "GET", "/api/units/nas/app.container/history", "")
	if rec.Code != http.StatusOK || body["enabled"] != false {
		t.Errorf("remote history: %d %v — git must be off for remote areas", rec.Code, body)
	}
}
