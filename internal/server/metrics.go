package server

import (
	"fmt"
	"net/http"

	"github.com/tobagin/rookery/internal/quadlet"
)

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if s.authConfigured() && !s.authenticated(r) {
		httpError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	fmt.Fprintf(w, "rookery_build_info{version=%q} 1\n", s.version)
	for _, node := range s.nodeInventory(r) {
		reachable := 1
		if len(node.Errors) > 0 {
			reachable = 0
		}
		fmt.Fprintf(w, "rookery_node_reachable{node=%q} %d\n", node.ID, reachable)
	}
	for _, area := range s.areasSnapshot() {
		found, err := discoverArea(r.Context(), area)
		if err != nil {
			continue
		}
		services := make([]string, len(found))
		for i, d := range found {
			services[i], _ = quadlet.ServiceName(d.unit.Name)
		}
		statuses, _ := s.sysd.Status(r.Context(), area.Scope, services)
		counts := map[string]int{}
		for i := range found {
			state := "unknown"
			if i < len(statuses) {
				state = statuses[i].Active
				if state == "" {
					state = "unknown"
				}
			}
			counts[state]++
		}
		for state, count := range counts {
			fmt.Fprintf(w, "rookery_units{scope=%q,state=%q} %d\n", area.Label, state, count)
		}
	}
	drift := 0
	for _, row := range s.driftRows(r.Context()) {
		if row.UpdateAvailable {
			drift++
		}
	}
	fmt.Fprintf(w, "rookery_update_drift_units %d\n", drift)
}
