package server

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tobagin/rookery/internal/appdb"
	"github.com/tobagin/rookery/internal/quadlet"
	"github.com/tobagin/rookery/internal/rhost/rhosttest"
	"github.com/tobagin/rookery/internal/systemd"
	"github.com/tobagin/rookery/internal/userstore"
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

func TestListUnitsPodMembership(t *testing.T) {
	srv, _, dir := newTestServer(t, okValidator)
	pod := "[Pod]\nPublishPort=2283:2283\n"
	member := "[Container]\nImage=ghcr.io/immich-app/server:release\nPod=immich.pod\n"
	for name, content := range map[string]string{"immich.pod": pod, "immich-server.container": member} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, body := doJSON(t, srv, "GET", "/api/units", "")
	pods := map[string]string{}
	for _, raw := range body["units"].([]any) {
		u := raw.(map[string]any)
		pods[u["name"].(string)], _ = u["pod"].(string)
	}
	if pods["immich-server.container"] != "immich.pod" {
		t.Errorf("member pod ref = %q, want immich.pod", pods["immich-server.container"])
	}
	if pods["jellyfin.container"] != "" {
		t.Errorf("standalone container has pod ref %q", pods["jellyfin.container"])
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

func TestLicenseStatusCountsUniqueManagedNodes(t *testing.T) {
	srv, _, _ := newTestServer(t, okValidator)
	srv.areas = append(srv.areas,
		Area{Label: "alice", Scope: systemd.Scope{User: "alice"}, Dirs: []string{t.TempDir()}},
		Area{Label: "nas", Scope: systemd.Scope{SSH: "root@nas.local"}, Dirs: []string{"/etc/containers/systemd"}},
		Area{Label: "nas-user", Scope: systemd.Scope{User: "podman", SSH: "root@nas.local"}, Dirs: []string{"/home/podman/.config/containers/systemd"}},
	)
	rec, body := doJSON(t, srv, "GET", "/api/license", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	lic := body["license"].(map[string]any)
	if got := int(lic["managedNodes"].(float64)); got != 2 {
		t.Fatalf("managedNodes = %d, want 2", got)
	}
	if got := int(lic["nodesRemaining"].(float64)); got != 1 {
		t.Fatalf("nodesRemaining = %d, want 1", got)
	}
	if got := int(lic["nodesOverLimit"].(float64)); got != 0 {
		t.Fatalf("nodesOverLimit = %d, want 0", got)
	}
	if lic["enterpriseAvailable"] != true {
		t.Fatalf("enterpriseAvailable = %v, want true", lic["enterpriseAvailable"])
	}
	if got := int(lic["localUserLimit"].(float64)); got != 0 {
		t.Fatalf("localUserLimit = %d, want 0 for unlimited", got)
	}
	if got := int(lic["ssoUserLimit"].(float64)); got != 0 {
		t.Fatalf("ssoUserLimit = %d, want 0 for unlimited", got)
	}
	nodes := lic["nodes"].([]any)
	if len(nodes) != 2 || nodes[0] != "local" || nodes[1] != "root@nas.local" {
		t.Fatalf("nodes = %#v, want local/root@nas.local", nodes)
	}
}

func TestLicenseStatusFlagsAboveFreeAllowanceWithoutEnforcement(t *testing.T) {
	srv, _, _ := newTestServer(t, okValidator)
	srv.areas = []Area{
		{Label: "system", Scope: systemd.Scope{}, Dirs: []string{t.TempDir()}},
		{Label: "one", Scope: systemd.Scope{SSH: "root@one"}, Dirs: []string{"/etc/containers/systemd"}},
		{Label: "two", Scope: systemd.Scope{SSH: "root@two"}, Dirs: []string{"/etc/containers/systemd"}},
		{Label: "three", Scope: systemd.Scope{SSH: "root@three"}, Dirs: []string{"/etc/containers/systemd"}},
	}
	_, body := doJSON(t, srv, "GET", "/api/license", "")
	lic := body["license"].(map[string]any)
	if got := int(lic["managedNodes"].(float64)); got != 4 {
		t.Fatalf("managedNodes = %d, want 4", got)
	}
	if got := int(lic["nodesRemaining"].(float64)); got != 0 {
		t.Fatalf("nodesRemaining = %d, want 0", got)
	}
	if got := int(lic["nodesOverLimit"].(float64)); got != 1 {
		t.Fatalf("nodesOverLimit = %d, want 1", got)
	}
	if lic["enterpriseAvailable"] != false {
		t.Fatalf("enterpriseAvailable = %v, want false", lic["enterpriseAvailable"])
	}
	if lic["enforcement"] != "disabled" {
		t.Fatalf("enforcement = %v, want disabled", lic["enforcement"])
	}
	if got := int(lic["ssoUserLimit"].(float64)); got != 0 {
		t.Fatalf("ssoUserLimit = %d, want unlimited even above node allowance", got)
	}
}

func TestNodeInventoryGroupsScopesByManagedNode(t *testing.T) {
	rhosttest.InstallShim(t)
	srv, _, dir := newTestServer(t, okValidator)
	if err := os.WriteFile(filepath.Join(dir, "failed.container"), []byte("[Container]\nImage=example.test/failed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv.sysd.(*fakeSystemd).states["failed.service"] = systemd.UnitStatus{Load: "loaded", Active: "failed", Sub: "failed"}
	remoteDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(remoteDir, "remote.container"), []byte("[Container]\nImage=example.test/remote\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv.areas = append(srv.areas,
		Area{Label: "alice", Scope: systemd.Scope{User: "alice"}, Dirs: []string{t.TempDir()}},
		Area{Label: "nas", Scope: systemd.Scope{SSH: "root@nas.local"}, Dirs: []string{remoteDir}},
	)
	srv.sysd.(*fakeSystemd).states["remote.service"] = systemd.UnitStatus{Load: "loaded", Active: "active", Sub: "running"}

	rec, body := doJSON(t, srv, "GET", "/api/nodes", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	nodes := body["nodes"].([]any)
	if len(nodes) != 2 {
		t.Fatalf("got %d nodes, want local and remote", len(nodes))
	}
	local := nodes[0].(map[string]any)
	if local["id"] != "local" || int(local["units"].(float64)) != 2 || int(local["failed"].(float64)) != 1 {
		t.Fatalf("local node = %#v", local)
	}
	remote := nodes[1].(map[string]any)
	if remote["id"] != "root@nas.local" || remote["local"] != false || int(remote["running"].(float64)) != 1 {
		t.Fatalf("remote node = %#v", remote)
	}
}

func TestNodeLabelsPersistInAppDB(t *testing.T) {
	srv, _, _ := newTestServer(t, okValidator)
	store, err := userstore.Open(filepath.Join(t.TempDir(), "users.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DB().Close() })
	srv.users = store

	rec, body := doJSON(t, srv, "PATCH", "/api/nodes/local/labels", `{"labels":["prod","gpu","prod"," GPU "]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH status %d: %s", rec.Code, rec.Body.String())
	}
	nodes := body["nodes"].([]any)
	labels := nodes[0].(map[string]any)["labels"].([]any)
	if fmt.Sprint(labels) != "[gpu prod]" {
		t.Fatalf("labels = %v, want [gpu prod]", labels)
	}

	rec, body = doJSON(t, srv, "GET", "/api/nodes", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status %d: %s", rec.Code, rec.Body.String())
	}
	labels = body["nodes"].([]any)[0].(map[string]any)["labels"].([]any)
	if fmt.Sprint(labels) != "[gpu prod]" {
		t.Fatalf("persisted labels = %v, want [gpu prod]", labels)
	}
}

func TestNodeGroupsDerivedFromLabels(t *testing.T) {
	srv, _, _ := newTestServer(t, okValidator)
	store, err := userstore.Open(filepath.Join(t.TempDir(), "users.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DB().Close() })
	srv.users = store
	if err := appdb.PutNodeLabels(store.DB(), "local", []string{"prod", "gpu"}); err != nil {
		t.Fatal(err)
	}
	rec, body := doJSON(t, srv, "GET", "/api/groups", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	groups := body["groups"].([]any)
	seen := map[string]bool{}
	for _, raw := range groups {
		g := raw.(map[string]any)
		seen[g["label"].(string)] = true
		if g["label"] == "prod" && int(g["units"].(float64)) != 1 {
			t.Fatalf("prod group = %#v", g)
		}
	}
	if !seen["gpu"] || !seen["prod"] {
		t.Fatalf("groups = %#v, want gpu and prod", groups)
	}
}

func TestAuditEventsRecordedForMutations(t *testing.T) {
	srv, _, _ := newTestServer(t, okValidator)
	store, err := userstore.Open(filepath.Join(t.TempDir(), "users.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DB().Close() })
	srv.users = store

	rec, _ := doJSON(t, srv, "PATCH", "/api/nodes/local/labels", `{"labels":["prod"]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PATCH status %d: %s", rec.Code, rec.Body.String())
	}
	rec, body := doJSON(t, srv, "GET", "/api/audit", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET audit status %d: %s", rec.Code, rec.Body.String())
	}
	events := body["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
	ev := events[0].(map[string]any)
	if ev["action"] != "node.labels" || ev["target"] != "local" {
		t.Fatalf("event = %#v", ev)
	}
}

func TestAuditEventsRecordedForLogin(t *testing.T) {
	srv, _, _ := newTestServer(t, okValidator)
	store, err := userstore.Open(filepath.Join(t.TempDir(), "users.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DB().Close() })
	if err := store.Create("admin", "correct-password", userstore.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	srv.users = store

	rec, _ := doJSON(t, srv, "POST", "/api/login", `{"username":"admin","password":"correct-password"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status %d: %s", rec.Code, rec.Body.String())
	}
	events, err := appdb.ListAuditEvents(store.DB(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Actor != "admin" || events[0].Action != "auth.login" {
		t.Fatalf("events = %#v", events)
	}
}

func TestBackupIncludesManifestAndQuadlets(t *testing.T) {
	srv, _, _ := newTestServer(t, okValidator)
	rec, _ := doJSON(t, srv, "GET", "/api/backup", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/gzip" {
		t.Fatalf("content-type = %q", ct)
	}
	gz, err := gzip.NewReader(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	seen := map[string]string{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		seen[h.Name] = string(data)
	}
	if !strings.Contains(seen["manifest.json"], `"files"`) {
		t.Fatalf("manifest missing or invalid: %q", seen["manifest.json"])
	}
	if !strings.Contains(seen["quadlets/system/jellyfin.container"], "Image=docker.io/jellyfin") {
		t.Fatalf("quadlet missing from backup: keys=%v", seen)
	}
}

func TestPolicyFindings(t *testing.T) {
	srv, _, dir := newTestServer(t, okValidator)
	risky := `[Container]
Image=docker.io/library/postgres:latest
Privileged=true
Volume=/srv/db:/var/lib/postgresql/data
`
	if err := os.WriteFile(filepath.Join(dir, "db.container"), []byte(risky), 0o644); err != nil {
		t.Fatal(err)
	}
	rec, body := doJSON(t, srv, "GET", "/api/policies", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	findings := body["findings"].([]any)
	got := map[string]bool{}
	for _, raw := range findings {
		f := raw.(map[string]any)
		if f["unit"] == "db.container" {
			got[f["policy"].(string)] = true
		}
	}
	for _, policy := range []string{"latest-tag", "privileged-container", "selinux-bind-mount"} {
		if !got[policy] {
			t.Fatalf("missing policy %s in findings %#v", policy, findings)
		}
	}
}

func TestPolicyWaivers(t *testing.T) {
	srv, _, dir := newTestServer(t, okValidator)
	store, err := userstore.Open(filepath.Join(t.TempDir(), "users.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.DB().Close() })
	srv.users = store
	if err := os.WriteFile(filepath.Join(dir, "db.container"), []byte("[Container]\nImage=docker.io/library/postgres:latest\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, body := doJSON(t, srv, "GET", "/api/policies", "")
	var key string
	for _, raw := range body["findings"].([]any) {
		f := raw.(map[string]any)
		if f["unit"] == "db.container" && f["policy"] == "latest-tag" {
			key = f["key"].(string)
		}
	}
	if key == "" {
		t.Fatal("latest-tag finding key not found")
	}
	rec, body := doJSON(t, srv, "POST", "/api/policies/waivers", fmt.Sprintf(`{"key":%q,"reason":"accepted for lab"}`, key))
	if rec.Code != http.StatusOK {
		t.Fatalf("waive status %d: %s", rec.Code, rec.Body.String())
	}
	foundWaived := false
	for _, raw := range body["findings"].([]any) {
		f := raw.(map[string]any)
		if f["key"] == key && f["waived"] == true && f["waiverReason"] == "accepted for lab" {
			foundWaived = true
		}
	}
	if !foundWaived {
		t.Fatalf("waived finding not returned: %#v", body["findings"])
	}
	rec, body = doJSON(t, srv, "DELETE", "/api/policies/waivers/"+key, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("unwaive status %d: %s", rec.Code, rec.Body.String())
	}
	for _, raw := range body["findings"].([]any) {
		f := raw.(map[string]any)
		if f["key"] == key && f["waived"] == true {
			t.Fatalf("finding still waived after delete: %#v", f)
		}
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

func TestPutUnitStaleBaseRejected(t *testing.T) {
	srv, _, dir := newTestServer(t, okValidator)
	loaded := "[Container]\nImage=docker.io/jellyfin/jellyfin:latest\n"
	// Someone else changes the file after our editor loaded it.
	changed := "[Container]\nImage=docker.io/jellyfin/jellyfin:10.9\n"
	if err := os.WriteFile(filepath.Join(dir, "jellyfin.container"), []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}
	rec, body := doJSON(t, srv, "PUT", "/api/units/system/jellyfin.container",
		fmt.Sprintf(`{"content": "[Container]\nImage=x\n", "baseContent": %q}`, loaded))
	if rec.Code != http.StatusConflict {
		t.Fatalf("stale save: status %d (%v), want 409", rec.Code, body)
	}
	if got, _ := os.ReadFile(filepath.Join(dir, "jellyfin.container")); string(got) != changed {
		t.Error("stale save modified the file")
	}
	// Matching base saves fine; omitted base keeps the old unchecked behavior.
	rec, _ = doJSON(t, srv, "PUT", "/api/units/system/jellyfin.container",
		fmt.Sprintf(`{"content": "[Container]\nImage=y\n", "baseContent": %q}`, changed))
	if rec.Code != http.StatusOK {
		t.Errorf("fresh save: status %d, want 200", rec.Code)
	}
	rec, _ = doJSON(t, srv, "PUT", "/api/units/system/jellyfin.container", `{"content": "[Container]\nImage=z\n"}`)
	if rec.Code != http.StatusOK {
		t.Errorf("no-base save: status %d, want 200", rec.Code)
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

func TestActionEnableDisableMutatesInstallSection(t *testing.T) {
	srv, sysd, dir := newTestServer(t, okValidator)
	path := filepath.Join(dir, "jellyfin.container")
	if err := os.WriteFile(path, []byte("[Container]\nImage=x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec, _ := doJSON(t, srv, "POST", "/api/units/system/jellyfin.container/action", `{"action":"enable"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("enable: status %d: %s", rec.Code, rec.Body.String())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); !strings.Contains(got, "[Install]\nWantedBy=multi-user.target\n") {
		t.Fatalf("enable content = %q", got)
	}
	if len(sysd.calls) != 1 || !strings.HasPrefix(sysd.calls[0], "daemon-reload system") {
		t.Fatalf("enable systemd calls = %v, want daemon-reload only", sysd.calls)
	}

	rec, _ = doJSON(t, srv, "POST", "/api/units/system/jellyfin.container/action", `{"action":"disable"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("disable: status %d: %s", rec.Code, rec.Body.String())
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); strings.Contains(got, "WantedBy=") || strings.Contains(got, "RequiredBy=") || strings.Contains(got, "Alias=") {
		t.Fatalf("disable content still has install links = %q", got)
	}
}

func TestSetInstallEnabledPreservesExistingInstall(t *testing.T) {
	content := "# keep\n[Unit]\nDescription=x\n\n[Install]\n# keep install comment\nWantedBy=default.target\nAlias=x.service\n"
	got := setInstallEnabled(content, false, "multi-user.target")
	if !strings.Contains(got, "# keep install comment") {
		t.Fatalf("disable dropped comments: %q", got)
	}
	if strings.Contains(got, "WantedBy=") || strings.Contains(got, "Alias=") {
		t.Fatalf("disable kept install links: %q", got)
	}
	got = setInstallEnabled(got, true, "multi-user.target")
	if !strings.Contains(got, "WantedBy=multi-user.target") {
		t.Fatalf("enable did not add wanted target: %q", got)
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
