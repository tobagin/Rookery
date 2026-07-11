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
	ip, ok := s.imagesClient(w)
	if !ok {
		return
	}
	// ?all=true prunes every unused image (the "prune unused" button); the
	// default stays dangling-only so the stale-image button is unchanged.
	if r.URL.Query().Get("all") == "true" {
		count, reclaimed, err := ip.PruneAllImages(r.Context())
		if err != nil {
			httpError(w, http.StatusBadGateway, err.Error())
			return
		}
		s.audit(r, "images.prune", "local", map[string]any{"all": true, "removed": count})
		writeJSON(w, http.StatusOK, map[string]any{"reclaimedBytes": reclaimed, "removed": count})
		return
	}
	reclaimed, err := ip.PruneImages(r.Context())
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reclaimedBytes": reclaimed})
}
