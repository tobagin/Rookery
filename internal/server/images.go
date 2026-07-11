package server

import (
	"context"
	"net/http"
)

// imagesAPI is the optional slice of the Podman client that stale-image
// management needs; asserted at runtime so test fakes stay small.
type imagesAPI interface {
	StaleImages(ctx context.Context) (count int, size int64, err error)
	PruneImages(ctx context.Context) (int64, error)
	PruneAllImages(ctx context.Context) (count int, reclaimed int64, err error)
}

func (s *Server) imagesClient(w http.ResponseWriter) (imagesAPI, bool) {
	ip, ok := s.pod.(imagesAPI)
	if !ok || s.pod == nil {
		httpError(w, http.StatusServiceUnavailable, "podman API socket not available")
		return nil, false
	}
	return ip, true
}

func (s *Server) handleStaleImages(w http.ResponseWriter, r *http.Request) {
	ip, ok := s.imagesClient(w)
	if !ok {
		return
	}
	count, size, err := ip.StaleImages(r.Context())
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"count": count, "bytes": size})
}

func (s *Server) handlePruneImages(w http.ResponseWriter, r *http.Request) {
	// ?all=true prunes every unused image (the "prune unused" button) across
	// every scope: local stores natively, agent scopes by deleting each image
	// the agent reports unused (the agent API has no prune endpoint); the
	// default stays dangling-only so the stale-image button is unchanged.
	if r.URL.Query().Get("all") == "true" {
		var count int
		var reclaimed int64
		scopeErrors := map[string]string{}
		for _, area := range s.areasSnapshot() {
			switch {
			case area.ViaAgent():
				res, err := area.Agent.Resources(r.Context(), area.AgentScope)
				if err != nil {
					scopeErrors[area.Label] = err.Error()
					continue
				}
				for _, rr := range res {
					if rr.Kind != "image" || rr.Used {
						continue
					}
					if err := area.Agent.DeleteResource(r.Context(), area.AgentScope, "image", rr.Name); err != nil {
						scopeErrors[area.Label] = err.Error()
						continue
					}
					count++
				}
			case !area.Remote() && (area.Scope.IsSystem() || area.LocalRootless()):
				lp, ok := s.localBackend(area).(imagesAPI)
				if !ok {
					continue
				}
				n, b, err := lp.PruneAllImages(r.Context())
				if err != nil {
					scopeErrors[area.Label] = err.Error()
					continue
				}
				count += n
				reclaimed += b
			}
		}
		s.audit(r, "images.prune", "all", map[string]any{"all": true, "removed": count})
		writeJSON(w, http.StatusOK, map[string]any{"reclaimedBytes": reclaimed, "removed": count, "scopeErrors": scopeErrors})
		return
	}
	ip, ok := s.imagesClient(w)
	if !ok {
		return
	}
	reclaimed, err := ip.PruneImages(r.Context())
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reclaimedBytes": reclaimed})
}
