package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	papi "github.com/rookerylabs/rookery-agent-api"
	"github.com/rookerylabs/rookery/internal/agent"
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
	return httptest.NewServer(mux)
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
	if u.Name != "ntfy.container" || u.Active != "active" || u.Scope != "pi" || !u.ReadOnly {
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

func TestAgentActionRejectsEnable(t *testing.T) {
	ts := fakeAgent(t, "tok", nil)
	defer ts.Close()
	s := &Server{areas: []Area{{Label: "pi", Scope: systemd.Scope{User: "pi"}, Agent: agent.New(ts.URL, "tok")}}}
	r := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(`{"action":"enable"}`))
	r.SetPathValue("scope", "pi")
	r.SetPathValue("name", "ntfy.container")
	w := httptest.NewRecorder()
	s.handleAction(w, r)
	if w.Code != http.StatusNotImplemented {
		t.Errorf("enable via agent: status %d, want 501", w.Code)
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
