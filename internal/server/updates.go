package server

import (
	"context"
	"net/http"
	"strings"
	"sync"

	"github.com/tobagin/rookery/internal/quadlet"
)

// updateRow is one unit's drift-check result.
type updateRow struct {
	Scope           string `json:"scope"`
	Name            string `json:"name"`
	Image           string `json:"image"`
	RemoteDigest    string `json:"remoteDigest,omitempty"`
	UpdateAvailable bool   `json:"updateAvailable"`
	Note            string `json:"note,omitempty"`
}

// handleUpdates checks every local container unit's image tag against its
// registry: an update is available when none of the locally stored digests
// match what the registry serves for the tag today. Remote areas are
// skipped — their image store lives on the other host.
func (s *Server) handleUpdates(w http.ResponseWriter, r *http.Request) {
	if s.pod == nil {
		httpError(w, http.StatusServiceUnavailable, "podman API socket not available")
		return
	}
	type job struct {
		scope, name, image string
	}
	var jobs []job
	skippedScopes := []string{}
	for _, area := range s.areas {
		if area.Remote() {
			skippedScopes = append(skippedScopes, area.Label)
			continue
		}
		found, err := discoverArea(r.Context(), area)
		if err != nil {
			continue
		}
		for _, d := range found {
			if d.unit.Kind != quadlet.KindContainer || d.data == nil {
				continue
			}
			f, err := quadlet.Parse(d.data)
			if err != nil {
				continue
			}
			image, ok := f.Get("Container", "Image")
			if !ok || image == "" {
				continue
			}
			jobs = append(jobs, job{scope: area.Label, name: d.unit.Name, image: image})
		}
	}

	rows := make([]updateRow, len(jobs))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4) // don't hammer registries
	for i, j := range jobs {
		wg.Add(1)
		go func(i int, j job) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			rows[i] = s.checkDrift(r.Context(), j.scope, j.name, j.image)
		}(i, j)
	}
	wg.Wait()

	writeJSON(w, http.StatusOK, map[string]any{
		"updates":       rows,
		"skippedScopes": skippedScopes,
	})
}

func (s *Server) checkDrift(ctx context.Context, scope, name, image string) updateRow {
	row := updateRow{Scope: scope, Name: name, Image: image}
	if strings.HasSuffix(image, ".image") || strings.HasSuffix(image, ".build") {
		row.Note = "image comes from another unit; not checked"
		return row
	}
	if strings.Contains(image, "@") {
		row.Note = "pinned by digest; cannot drift"
		return row
	}
	remote, err := s.resolve(ctx, image)
	if err != nil {
		row.Note = "registry: " + err.Error()
		return row
	}
	row.RemoteDigest = remote
	local, err := s.pod.ImageDigests(ctx, image)
	if err != nil {
		row.Note = "image not in local store: " + err.Error()
		return row
	}
	row.UpdateAvailable = true
	for _, d := range local {
		if strings.HasSuffix(d, remote) {
			row.UpdateAvailable = false
			break
		}
	}
	return row
}

// handleUpdateUnit is the one-click follow-through: pull the unit's image
// and restart its service so systemd brings up the new digest.
func (s *Server) handleUpdateUnit(w http.ResponseWriter, r *http.Request) {
	area, name, path, exists, ok := s.resolveUnit(w, r)
	if !ok {
		return
	}
	if !exists {
		httpError(w, http.StatusNotFound, "unit file not found")
		return
	}
	if area.Remote() {
		httpError(w, http.StatusNotImplemented, "updating images on remote hosts is not supported yet — run the update on that host")
		return
	}
	if s.pod == nil {
		httpError(w, http.StatusServiceUnavailable, "podman API socket not available")
		return
	}
	data, err := areaReadFile(r.Context(), area, path)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	f, err := quadlet.Parse(data)
	if err != nil {
		httpError(w, http.StatusUnprocessableEntity, "unit does not parse: "+err.Error())
		return
	}
	image, okImage := f.Get("Container", "Image")
	if !okImage || image == "" {
		httpError(w, http.StatusUnprocessableEntity, "unit has no Image= to pull")
		return
	}
	if err := s.pod.PullImage(r.Context(), image); err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	warnings := []string{}
	service, _ := quadlet.ServiceName(name)
	if err := s.sysd.Restart(r.Context(), area.Scope, service); err != nil {
		warnings = append(warnings, "restart: "+err.Error())
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"pulled":   image,
		"unit":     s.unitJSONFor(r, area, name, path),
		"warnings": warnings,
	})
}
