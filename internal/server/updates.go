package server

import (
	"context"
	"encoding/json"
	"fmt"
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

// handleUpdates checks every container unit's image tag against its
// registry: an update is available when none of the digests stored on the
// unit's host — local Podman API, or podman over ssh for remote areas —
// match what the registry serves for the tag today.
func (s *Server) handleUpdates(w http.ResponseWriter, r *http.Request) {
	type job struct {
		area  Area
		name  string
		image string
	}
	var jobs []job
	skippedScopes := []string{}
	for _, area := range s.areas {
		if !area.Remote() && s.pod == nil {
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
			jobs = append(jobs, job{area: area, name: d.unit.Name, image: image})
		}
	}
	if len(jobs) == 0 && len(skippedScopes) == len(s.areas) && s.pod == nil {
		httpError(w, http.StatusServiceUnavailable, "podman API socket not available")
		return
	}

	rows := make([]updateRow, len(jobs))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4) // don't hammer registries or ssh targets
	for i, j := range jobs {
		wg.Add(1)
		go func(i int, j job) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			rows[i] = s.checkDrift(r.Context(), j.area, j.name, j.image)
		}(i, j)
	}
	wg.Wait()

	writeJSON(w, http.StatusOK, map[string]any{
		"updates":       rows,
		"skippedScopes": skippedScopes,
	})
}

// hostDigests returns the digests the unit's own host stores for image.
func (s *Server) hostDigests(ctx context.Context, area Area, image string) ([]string, error) {
	if area.Remote() {
		return s.remoteDigests(ctx, area.Scope.SSH, area.Scope.User != "", image)
	}
	return s.pod.ImageDigests(ctx, image)
}

func (s *Server) checkDrift(ctx context.Context, area Area, name, image string) updateRow {
	row := updateRow{Scope: area.Label, Name: name, Image: image}
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
	local, err := s.hostDigests(ctx, area, image)
	if err != nil {
		row.Note = "image not in host store: " + err.Error()
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
	image, warnings, err := s.updateUnit(r.Context(), area, name, path)
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"pulled":   image,
		"unit":     s.unitJSONFor(r, area, name, path),
		"warnings": warnings,
	})
}

func (s *Server) updateUnit(ctx context.Context, area Area, name, path string) (string, []string, error) {
	if !area.Remote() && s.pod == nil {
		return "", nil, fmt.Errorf("podman API socket not available")
	}
	data, err := areaReadFile(ctx, area, path)
	if err != nil {
		return "", nil, err
	}
	f, err := quadlet.Parse(data)
	if err != nil {
		return "", nil, fmt.Errorf("unit does not parse: %w", err)
	}
	image, okImage := f.Get("Container", "Image")
	if !okImage || image == "" {
		return "", nil, fmt.Errorf("unit has no Image= to pull")
	}
	if area.Remote() {
		err = s.remotePull(ctx, area.Scope.SSH, area.Scope.User != "", image)
	} else {
		err = s.pod.PullImage(ctx, image)
	}
	if err != nil {
		return image, nil, err
	}
	warnings := []string{}
	service, _ := quadlet.ServiceName(name)
	if err := s.sysd.Restart(ctx, area.Scope, service); err != nil {
		warnings = append(warnings, "restart: "+err.Error())
	}
	return image, warnings, nil
}

type updateApplyResult struct {
	Scope    string   `json:"scope"`
	Name     string   `json:"name"`
	OK       bool     `json:"ok"`
	Pulled   string   `json:"pulled,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
	Error    string   `json:"error,omitempty"`
}

func (s *Server) handleApplyUpdates(w http.ResponseWriter, r *http.Request) {
	var req struct {
		AllDrifted bool      `json:"allDrifted"`
		Units      []unitRef `json:"units"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	refs := req.Units
	if req.AllDrifted {
		for _, row := range s.driftRows(r.Context()) {
			if row.UpdateAvailable {
				refs = append(refs, unitRef{Scope: row.Scope, Name: row.Name})
			}
		}
	}
	if len(refs) == 0 {
		httpError(w, http.StatusBadRequest, "no updates selected")
		return
	}
	results := make([]updateApplyResult, len(refs))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 3)
	for i, ref := range refs {
		wg.Add(1)
		go func(i int, ref unitRef) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			res := updateApplyResult{Scope: ref.Scope, Name: ref.Name}
			area, found := s.area(ref.Scope)
			if !found {
				res.Error = "unknown scope"
				results[i] = res
				return
			}
			if err := quadlet.CheckName(ref.Name); err != nil {
				res.Error = err.Error()
				results[i] = res
				return
			}
			target := joinUnitPath(area, area.Dirs[0], ref.Name)
			if ok, err := areaExists(r.Context(), area, target); err != nil {
				res.Error = err.Error()
				results[i] = res
				return
			} else if !ok {
				res.Error = "unit file not found"
				results[i] = res
				return
			}
			image, warnings, err := s.updateUnit(r.Context(), area, ref.Name, target)
			if err != nil {
				res.Error = err.Error()
			} else {
				res.OK = true
				res.Pulled = image
				res.Warnings = warnings
			}
			results[i] = res
		}(i, ref)
	}
	wg.Wait()
	s.audit(r, "updates.apply", "updates", map[string]any{"allDrifted": req.AllDrifted, "units": refs, "results": results})
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (s *Server) driftRows(ctx context.Context) []updateRow {
	type job struct {
		area  Area
		name  string
		image string
	}
	var jobs []job
	for _, area := range s.areas {
		if !area.Remote() && s.pod == nil {
			continue
		}
		found, err := discoverArea(ctx, area)
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
			if ok && image != "" {
				jobs = append(jobs, job{area: area, name: d.unit.Name, image: image})
			}
		}
	}
	rows := make([]updateRow, len(jobs))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, j := range jobs {
		wg.Add(1)
		go func(i int, j job) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			rows[i] = s.checkDrift(ctx, j.area, j.name, j.image)
		}(i, j)
	}
	wg.Wait()
	return rows
}
