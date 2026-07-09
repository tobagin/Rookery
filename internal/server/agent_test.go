package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	papi "github.com/rookerylabs/rookery-agent-api"
	"github.com/rookerylabs/rookery/internal/agent"
	"github.com/rookerylabs/rookery/internal/quadlet"
	"github.com/rookerylabs/rookery/internal/systemd"
)

// fakeAgent serves the subset of the rookery-agent API that nodeInventory
// touches, so the agent connector can be exercised without a live podman.
func fakeAgent(t *testing.T, token string, units []papi.Unit) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	guard := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+token {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("GET "+papi.PathInfo, guard(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(papi.Info{Scope: "user:pi", User: "pi", ContainersTotal: len(units)})
	}))
	mux.HandleFunc("GET "+papi.PathUnits, guard(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(units)
	}))
	mux.HandleFunc("POST "+papi.PathUnitsPrefix+"{name}/{action}", guard(func(w http.ResponseWriter, r *http.Request) {
		gotAction.name = r.PathValue("name")
		gotAction.action = r.PathValue("action")
		_ = json.NewEncoder(w).Encode(papi.ActionResult{Unit: gotAction.name, Action: gotAction.action, OK: true})
	}))
	// One container + stats row per unit, labelled with its service, so the
	// runtimeByService mapping has something to match.
	mux.HandleFunc("GET "+papi.PathContainers, guard(func(w http.ResponseWriter, _ *http.Request) {
		cs := make([]papi.Container, 0, len(units))
		for _, u := range units {
			cs = append(cs, papi.Container{ID: u.Name, Names: []string{u.Name}, Labels: map[string]string{"PODMAN_SYSTEMD_UNIT": u.Service}})
		}
		_ = json.NewEncoder(w).Encode(cs)
	}))
	mux.HandleFunc("GET "+papi.PathStats, guard(func(w http.ResponseWriter, _ *http.Request) {
		st := make([]papi.Stat, 0, len(units))
		for _, u := range units {
			st = append(st, papi.Stat{ID: u.Name, Name: u.Name, CPUPct: 5, MemBytes: 1 << 20})
		}
		_ = json.NewEncoder(w).Encode(st)
	}))
	mux.HandleFunc("GET "+papi.PathUnitsPrefix+"{name}/file", guard(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("[Container]\nImage=example.com/" + r.PathValue("name") + "\n"))
	}))
	mux.HandleFunc("GET "+papi.PathUnitsPrefix+"{name}/logs", guard(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("line one\nline two\n"))
	}))
	mux.HandleFunc("PUT "+papi.PathUnitsPrefix+"{name}/file", guard(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotWrite = written{name: r.PathValue("name"), content: string(b)}
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	}))
	mux.HandleFunc("DELETE "+papi.PathUnitsPrefix+"{name}/file", guard(func(w http.ResponseWriter, r *http.Request) {
		gotWrite = written{deleted: r.PathValue("name")}
		_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
	}))
	return httptest.NewServer(mux)
}

// written captures the last file mutation the fake agent received.
type written struct{ name, content, deleted string }

var gotWrite written

// validStub is a validation func that always passes, for save-path tests that
// don't exercise the generator.
func validStub(context.Context, bool, string, string) (quadlet.ValidationResult, error) {
	return quadlet.ValidationResult{Valid: true}, nil
}

// recorded captures the last lifecycle call the fake agent received.
type recorded struct{ name, action string }

var gotAction recorded

func TestNodeInventoryViaAgent(t *testing.T) {
	units := []papi.Unit{
		{Name: "ntfy.container", Service: "ntfy.service", Status: papi.Status{Load: "loaded", Active: "active"}},
		{Name: "kuma.container", Service: "kuma.service", Status: papi.Status{Load: "loaded", Active: "active"}},
		{Name: "broken.container", Service: "broken.service", Status: papi.Status{Load: "loaded", Active: "failed"}},
	}
	ts := fakeAgent(t, "tok", units)
	defer ts.Close()

	s := &Server{areas: []Area{{
		Label: "pi",
		Scope: systemd.Scope{User: "pi"},
		Agent: agent.New(ts.URL, "tok"),
	}}}

	nodes := s.nodeInventory(httptest.NewRequest(http.MethodGet, "/", nil))
	if len(nodes) != 1 {
		t.Fatalf("got %d nodes, want 1", len(nodes))
	}
	n := nodes[0]
	if n.Units != 3 || n.Running != 2 || n.Failed != 1 {
		t.Errorf("counts = units:%d running:%d failed:%d, want 3/2/1", n.Units, n.Running, n.Failed)
	}
	// An agent user scope must land in the rootless bucket, not rootful.
	if n.Rootless.Units != 3 || n.Rootful.Units != 0 {
		t.Errorf("bucket = rootless:%d rootful:%d, want 3/0", n.Rootless.Units, n.Rootful.Units)
	}
	if len(n.Errors) != 0 {
		t.Errorf("unexpected errors: %v", n.Errors)
	}
}

func TestListUnitsViaAgent(t *testing.T) {
	units := []papi.Unit{
		{Name: "ntfy.container", Kind: "container", Service: "ntfy.service", Status: papi.Status{Load: "loaded", Active: "active", Sub: "running"}},
	}
	ts := fakeAgent(t, "tok", units)
	defer ts.Close()
	s := &Server{areas: []Area{{Label: "pi", Scope: systemd.Scope{User: "pi"}, Agent: agent.New(ts.URL, "tok")}}}

	w := httptest.NewRecorder()
	s.handleListUnits(w, httptest.NewRequest(http.MethodGet, "/api/units", nil))
	var body struct {
		Units []unitJSON `json:"units"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Units) != 1 {
		t.Fatalf("got %d units, want 1", len(body.Units))
	}
	u := body.Units[0]
	if u.Name != "ntfy.container" || u.Active != "active" || u.Scope != "pi" || u.ReadOnly {
		t.Errorf("agent unit mapped wrong: %+v", u)
	}
	// Stats from the agent's containers+stats must reach the unit.
	if u.Stats == nil || u.Stats.CPUPct != 5 || u.Stats.MemBytes != 1<<20 {
		t.Errorf("agent unit stats not attached: %+v", u.Stats)
	}
}

func TestAgentStatsEndpoint(t *testing.T) {
	units := []papi.Unit{
		{Name: "ntfy.container", Service: "ntfy.service", Status: papi.Status{Active: "active"}},
	}
	ts := fakeAgent(t, "tok", units)
	defer ts.Close()
	s := &Server{areas: []Area{{Label: "pi", Scope: systemd.Scope{User: "pi"}, Agent: agent.New(ts.URL, "tok")}}}

	w := httptest.NewRecorder()
	s.handleStats(w, httptest.NewRequest(http.MethodGet, "/api/stats", nil))
	var body struct {
		Stats map[string]unitStats `json:"stats"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	st, ok := body.Stats["pi/ntfy.container"]
	if !ok || st.CPUPct != 5 {
		t.Errorf("handleStats agent entry missing/wrong: %+v", body.Stats)
	}
}

func TestAgentActionRoutesToAgent(t *testing.T) {
	ts := fakeAgent(t, "tok", nil)
	defer ts.Close()
	gotAction = recorded{}
	s := &Server{areas: []Area{{Label: "pi", Scope: systemd.Scope{User: "pi"}, Agent: agent.New(ts.URL, "tok")}}}

	r := httptest.NewRequest(http.MethodPost, "/api/units/pi/ntfy.container/action",
		strings.NewReader(`{"action":"restart"}`))
	r.SetPathValue("scope", "pi")
	r.SetPathValue("name", "ntfy.container")
	w := httptest.NewRecorder()
	s.handleAction(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", w.Code, w.Body)
	}
	if gotAction.action != "restart" || gotAction.name != "ntfy.service" {
		t.Errorf("agent got %+v, want name=ntfy.service action=restart", gotAction)
	}
}

func TestGetUnitViaAgent(t *testing.T) {
	units := []papi.Unit{{Name: "ntfy.container", Kind: "container", Service: "ntfy.service", Status: papi.Status{Active: "active"}}}
	ts := fakeAgent(t, "tok", units)
	defer ts.Close()
	s := &Server{areas: []Area{{Label: "pi", Scope: systemd.Scope{User: "pi"}, Agent: agent.New(ts.URL, "tok")}}}

	r := httptest.NewRequest(http.MethodGet, "/api/units/pi/ntfy.container", nil)
	r.SetPathValue("scope", "pi")
	r.SetPathValue("name", "ntfy.container")
	w := httptest.NewRecorder()
	s.handleGetUnit(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", w.Code, w.Body)
	}
	var body struct {
		Unit    unitJSON `json:"unit"`
		Content string   `json:"content"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body.Content, "Image=example.com/ntfy.container") {
		t.Errorf("content not from agent: %q", body.Content)
	}
	if body.Unit.Active != "active" || body.Unit.Image != "example.com/ntfy.container" {
		t.Errorf("unit not enriched from agent+file: %+v", body.Unit)
	}
}

func TestLogsViaAgent(t *testing.T) {
	units := []papi.Unit{{Name: "ntfy.container", Service: "ntfy.service"}}
	ts := fakeAgent(t, "tok", units)
	defer ts.Close()
	s := &Server{areas: []Area{{Label: "pi", Scope: systemd.Scope{User: "pi"}, Agent: agent.New(ts.URL, "tok")}}}

	r := httptest.NewRequest(http.MethodGet, "/api/logs/pi/ntfy.container", nil)
	r.SetPathValue("scope", "pi")
	r.SetPathValue("name", "ntfy.container")
	w := httptest.NewRecorder()
	s.handleLogs(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	// SSE: each journal line becomes a data: event.
	body := w.Body.String()
	if !strings.Contains(body, "data: line one") || !strings.Contains(body, "data: line two") {
		t.Errorf("agent logs not streamed as SSE: %q", body)
	}
}

func TestPutUnitViaAgent(t *testing.T) {
	ts := fakeAgent(t, "tok", nil)
	defer ts.Close()
	gotWrite = written{}
	s := &Server{
		areas:    []Area{{Label: "pi", Scope: systemd.Scope{User: "pi"}, Agent: agent.New(ts.URL, "tok")}},
		validate: validStub,
	}
	r := httptest.NewRequest(http.MethodPut, "/api/units/pi/web.container",
		strings.NewReader(`{"content":"[Container]\nImage=example.com/web\n"}`))
	r.SetPathValue("scope", "pi")
	r.SetPathValue("name", "web.container")
	w := httptest.NewRecorder()
	s.handlePutUnit(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", w.Code, w.Body)
	}
	if gotWrite.name != "web.container" || !strings.Contains(gotWrite.content, "Image=example.com/web") {
		t.Errorf("agent did not receive the write: %+v", gotWrite)
	}
}

func TestDeleteUnitViaAgent(t *testing.T) {
	ts := fakeAgent(t, "tok", nil)
	defer ts.Close()
	gotWrite = written{}
	s := &Server{areas: []Area{{Label: "pi", Scope: systemd.Scope{User: "pi"}, Agent: agent.New(ts.URL, "tok")}}}
	r := httptest.NewRequest(http.MethodDelete, "/api/units/pi/web.container", nil)
	r.SetPathValue("scope", "pi")
	r.SetPathValue("name", "web.container")
	w := httptest.NewRecorder()
	s.handleDeleteUnit(w, r)
	if w.Code != http.StatusOK || gotWrite.deleted != "web.container" {
		t.Errorf("delete not routed to agent: code=%d %+v", w.Code, gotWrite)
	}
}

func TestAgentEnableRewritesInstall(t *testing.T) {
	// fakeAgent's GET file returns a unit with no [Install]; enable must add
	// WantedBy and write it back through the agent.
	ts := fakeAgent(t, "tok", nil)
	defer ts.Close()
	gotWrite = written{}
	s := &Server{areas: []Area{{Label: "pi", Scope: systemd.Scope{User: "pi"}, Agent: agent.New(ts.URL, "tok")}}}
	r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"action":"enable"}`))
	r.SetPathValue("scope", "pi")
	r.SetPathValue("name", "web.container")
	w := httptest.NewRecorder()
	s.handleAction(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("enable status %d, body %s", w.Code, w.Body)
	}
	if !strings.Contains(gotWrite.content, "WantedBy=default.target") {
		t.Errorf("enable did not write [Install] WantedBy: %q", gotWrite.content)
	}
}

func TestNodeInventoryAgentUnreachable(t *testing.T) {
	// A dead agent must surface an error on the node, not panic or drop it.
	s := &Server{areas: []Area{{
		Label: "pi",
		Scope: systemd.Scope{User: "pi"},
		Agent: agent.New("http://127.0.0.1:1", "tok"),
	}}}
	nodes := s.nodeInventory(httptest.NewRequest(http.MethodGet, "/", nil))
	if len(nodes) != 1 || len(nodes[0].Errors) == 0 {
		t.Fatalf("want 1 node carrying an error, got %+v", nodes)
	}
}
