package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tobagin/rookery/internal/quadlet"
	"github.com/tobagin/rookery/internal/systemd"
)

// fakeSystemd records calls and serves canned per-unit states.
type fakeSystemd struct {
	calls  []string
	states map[string]systemd.UnitStatus
	err    error
}

func (f *fakeSystemd) record(op string, scope systemd.Scope, unit string) error {
	f.calls = append(f.calls, fmt.Sprintf("%s %s %s", op, scope, unit))
	return f.err
}

func (f *fakeSystemd) Start(_ context.Context, s systemd.Scope, u string) error {
	return f.record("start", s, u)
}
func (f *fakeSystemd) Stop(_ context.Context, s systemd.Scope, u string) error {
	return f.record("stop", s, u)
}
func (f *fakeSystemd) Restart(_ context.Context, s systemd.Scope, u string) error {
	return f.record("restart", s, u)
}
func (f *fakeSystemd) Enable(_ context.Context, s systemd.Scope, u string) error {
	return f.record("enable", s, u)
}
func (f *fakeSystemd) Disable(_ context.Context, s systemd.Scope, u string) error {
	return f.record("disable", s, u)
}
func (f *fakeSystemd) DaemonReload(_ context.Context, s systemd.Scope) error {
	return f.record("daemon-reload", s, "")
}
func (f *fakeSystemd) Status(_ context.Context, _ systemd.Scope, units []string) ([]systemd.UnitStatus, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]systemd.UnitStatus, len(units))
	for i, u := range units {
		if st, ok := f.states[u]; ok {
			out[i] = st
		} else {
			out[i] = systemd.UnitStatus{Load: "not-found", Active: "inactive", Sub: "dead"}
		}
	}
	return out, nil
}

func okValidator(_ context.Context, _ bool, _, _ string) (quadlet.ValidationResult, error) {
	return quadlet.ValidationResult{Available: true, Valid: true, Output: "ok"}, nil
}

func rejectValidator(_ context.Context, _ bool, _, _ string) (quadlet.ValidationResult, error) {
	return quadlet.ValidationResult{Available: true, Valid: false, Output: "bad key"}, nil
}

func newTestServer(t *testing.T, validate ValidateFunc) (*Server, *fakeSystemd, string) {
	t.Helper()
	dir := t.TempDir()
	unit := "[Unit]\nDescription=Jellyfin\n\n[Container]\nImage=docker.io/jellyfin/jellyfin:latest\n"
	if err := os.WriteFile(filepath.Join(dir, "jellyfin.container"), []byte(unit), 0o644); err != nil {
		t.Fatal(err)
	}
	sysd := &fakeSystemd{states: map[string]systemd.UnitStatus{
		"jellyfin.service": {Load: "loaded", Active: "active", Sub: "running", UnitFile: "generated"},
	}}
	srv := New(Options{
		Areas:    []Area{{Label: "system", Scope: systemd.Scope{}, Dirs: []string{dir}}},
		Systemd:  sysd,
		Validate: validate,
		Version:  "test",
	})
	return srv, sysd, dir
}

func doJSON(t *testing.T, srv *Server, method, path string, body string) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var rd *strings.Reader
	if body == "" {
		rd = strings.NewReader("")
	} else {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	out := map[string]any{}
	if rec.Body.Len() > 0 && strings.Contains(rec.Header().Get("Content-Type"), "json") {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s %s: invalid JSON response: %v\n%s", method, path, err, rec.Body.String())
		}
	}
	return rec, out
}

func TestListUnits(t *testing.T) {
	srv, _, _ := newTestServer(t, okValidator)
	rec, body := doJSON(t, srv, "GET", "/api/units", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	units := body["units"].([]any)
	if len(units) != 1 {
		t.Fatalf("got %d units, want 1", len(units))
	}
	u := units[0].(map[string]any)
	for k, want := range map[string]string{
		"name": "jellyfin.container", "kind": "container", "scope": "system",
		"service": "jellyfin.service", "active": "active", "sub": "running",
		"description": "Jellyfin", "image": "docker.io/jellyfin/jellyfin:latest",
	} {
		if u[k] != want {
			t.Errorf("unit[%q] = %v, want %q", k, u[k], want)
		}
	}
}

func TestListUnitsSystemdDown(t *testing.T) {
	srv, sysd, _ := newTestServer(t, okValidator)
	sysd.err = fmt.Errorf("dbus is on fire")
	rec, body := doJSON(t, srv, "GET", "/api/units", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	units := body["units"].([]any)
	if len(units) != 1 {
		t.Fatalf("files on disk must still be listed; got %d units", len(units))
	}
	if u := units[0].(map[string]any); u["load"] != "unknown" {
		t.Errorf("load = %v, want unknown", u["load"])
	}
	if errs := body["scopeErrors"].(map[string]any); !strings.Contains(errs["system"].(string), "on fire") {
		t.Errorf("scopeErrors = %v", errs)
	}
}

func TestGetUnit(t *testing.T) {
	srv, _, _ := newTestServer(t, okValidator)
	rec, body := doJSON(t, srv, "GET", "/api/units/system/jellyfin.container", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(body["content"].(string), "Image=docker.io/jellyfin") {
		t.Errorf("content = %q", body["content"])
	}
	rec, _ = doJSON(t, srv, "GET", "/api/units/system/nope.container", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("missing unit: status %d, want 404", rec.Code)
	}
	rec, _ = doJSON(t, srv, "GET", "/api/units/mars/jellyfin.container", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown scope: status %d, want 404", rec.Code)
	}
}

func TestPutUnitWritesAndReloads(t *testing.T) {
	srv, sysd, dir := newTestServer(t, okValidator)
	content := "[Container]\nImage=docker.io/library/nginx:latest\n"
	rec, body := doJSON(t, srv, "PUT", "/api/units/system/web.container",
		fmt.Sprintf(`{"content": %q, "restart": true}`, content))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if body["created"] != true {
		t.Error("created flag not set for a new unit")
	}
	got, err := os.ReadFile(filepath.Join(dir, "web.container"))
	if err != nil || string(got) != content {
		t.Fatalf("file on disk = %q, %v", got, err)
	}
	joined := strings.Join(sysd.calls, "; ")
	if !strings.Contains(joined, "daemon-reload") || !strings.Contains(joined, "restart system web.service") {
		t.Errorf("systemd calls = %q; want daemon-reload then restart", joined)
	}
}

func TestPutUnitValidationRejection(t *testing.T) {
	srv, sysd, dir := newTestServer(t, rejectValidator)
	rec, body := doJSON(t, srv, "PUT", "/api/units/system/bad.container", `{"content": "[Container]\n"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status %d, want 422", rec.Code)
	}
	v := body["validation"].(map[string]any)
	if v["valid"] != false || v["output"] != "bad key" {
		t.Errorf("validation = %v", v)
	}
	if _, err := os.Stat(filepath.Join(dir, "bad.container")); !os.IsNotExist(err) {
		t.Error("rejected unit must not be written to disk")
	}
	if len(sysd.calls) != 0 {
		t.Errorf("rejected unit must not touch systemd; calls = %v", sysd.calls)
	}
}

func TestPutUnitBadName(t *testing.T) {
	srv, _, _ := newTestServer(t, okValidator)
	for _, name := range []string{"evil.txt", "..%2Fowned.container", ".hidden.container"} {
		rec, _ := doJSON(t, srv, "PUT", "/api/units/system/"+name, `{"content": ""}`)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("PUT %q: status %d, want 400", name, rec.Code)
		}
	}
}

func TestAction(t *testing.T) {
	srv, sysd, _ := newTestServer(t, okValidator)
	rec, _ := doJSON(t, srv, "POST", "/api/units/system/jellyfin.container/action", `{"action":"stop"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if want := "stop system jellyfin.service"; len(sysd.calls) == 0 || sysd.calls[0] != want {
		t.Errorf("calls = %v, want [%q]", sysd.calls, want)
	}
	rec, _ = doJSON(t, srv, "POST", "/api/units/system/jellyfin.container/action", `{"action":"self-destruct"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unknown action: status %d, want 400", rec.Code)
	}
}

func TestDeleteUnit(t *testing.T) {
	srv, sysd, dir := newTestServer(t, okValidator)
	rec, _ := doJSON(t, srv, "DELETE", "/api/units/system/jellyfin.container", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "jellyfin.container")); !os.IsNotExist(err) {
		t.Error("unit file still on disk after DELETE")
	}
	joined := strings.Join(sysd.calls, "; ")
	if !strings.Contains(joined, "stop system jellyfin.service") || !strings.Contains(joined, "daemon-reload") {
		t.Errorf("systemd calls = %q; want stop then daemon-reload", joined)
	}
}

func TestValidateEndpoint(t *testing.T) {
	srv, _, _ := newTestServer(t, rejectValidator)
	rec, body := doJSON(t, srv, "POST", "/api/validate",
		`{"scope":"system","name":"x.container","content":"[Container]\n"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if v := body["validation"].(map[string]any); v["valid"] != false {
		t.Errorf("validation = %v", v)
	}
}

func TestReadOnlyShadowedUnit(t *testing.T) {
	primary := t.TempDir()
	secondary := t.TempDir()
	if err := os.WriteFile(filepath.Join(secondary, "vendored.container"), []byte("[Container]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := New(Options{
		Areas:    []Area{{Label: "system", Scope: systemd.Scope{}, Dirs: []string{primary, secondary}}},
		Systemd:  &fakeSystemd{},
		Validate: okValidator,
	})
	rec, _ := doJSON(t, srv, "PUT", "/api/units/system/vendored.container", `{"content":"[Container]\n"}`)
	if rec.Code != http.StatusConflict {
		t.Errorf("PUT to read-only dir: status %d, want 409", rec.Code)
	}
	rec, _ = doJSON(t, srv, "DELETE", "/api/units/system/vendored.container", "")
	if rec.Code != http.StatusConflict {
		t.Errorf("DELETE in read-only dir: status %d, want 409", rec.Code)
	}
}

func TestUIIsServed(t *testing.T) {
	srv, _, _ := newTestServer(t, okValidator)
	rec, _ := doJSON(t, srv, "GET", "/", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Rookery") {
		t.Errorf("GET /: status %d", rec.Code)
	}
}
