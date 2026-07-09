package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	return httptest.NewServer(mux)
}

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
