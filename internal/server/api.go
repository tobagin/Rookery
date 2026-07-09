package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tobagin/rookery/internal/convert"
	"github.com/tobagin/rookery/internal/hostinfo"
	"github.com/tobagin/rookery/internal/journal"
	"github.com/tobagin/rookery/internal/quadlet"
	"github.com/tobagin/rookery/internal/systemd"
)

// unitJSON is one unit as the API reports it: the file on disk plus live
// systemd state.
type unitJSON struct {
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`
	Scope       string   `json:"scope"`
	ScopeKind   string   `json:"scopeKind"`
	ScopeUser   string   `json:"scopeUser,omitempty"`
	Service     string   `json:"service"`
	Path        string   `json:"path"`
	ReadOnly    bool     `json:"readOnly"`
	Description string   `json:"description,omitempty"`
	Image       string   `json:"image,omitempty"`
	Pod         string   `json:"pod,omitempty"` // Pod= reference, e.g. "immich.pod"
	Load        string   `json:"load"`
	Active      string   `json:"active"`
	Sub         string   `json:"sub"`
	UnitFile    string   `json:"unitFile"`
	Result      string   `json:"result,omitempty"`
	ExitCode    int      `json:"exitCode"`
	Restarts    int      `json:"restarts"`
	GPUs        []string `json:"gpus,omitempty"`
}

func (u *unitJSON) fillStatus(st systemd.UnitStatus) {
	u.Load, u.Active, u.Sub, u.UnitFile = st.Load, st.Active, st.Sub, st.UnitFile
	u.Result, u.ExitCode, u.Restarts = st.Result, st.ExitCode, st.Restarts
}

func (s *Server) handleListUnits(w http.ResponseWriter, r *http.Request) {
	var out []unitJSON
	scopeErrors := map[string]string{}
	for _, area := range s.areas {
		found, err := discoverArea(r.Context(), area)
		if err != nil {
			scopeErrors[area.Label] = err.Error()
			continue
		}
		services := make([]string, len(found))
		for i, d := range found {
			services[i], _ = quadlet.ServiceName(d.unit.Name)
		}
		statuses, err := s.sysd.Status(r.Context(), area.Scope, services)
		if err != nil {
			// Files on disk are still worth showing when systemd is
			// unreachable; state stays "unknown".
			scopeErrors[area.Label] = err.Error()
			statuses = nil
		}
		for i, d := range found {
			scopeKind := "rootless"
			if area.Scope.IsSystem() {
				scopeKind = "rootful"
			}
			uj := unitJSON{
				Name:      d.unit.Name,
				Kind:      string(d.unit.Kind),
				Scope:     area.Label,
				ScopeKind: scopeKind,
				ScopeUser: area.Scope.User,
				Service:   services[i],
				Path:      d.unit.Path,
				ReadOnly:  filepath.Dir(d.unit.Path) != area.Dirs[0],
				Load:      "unknown",
			}
			if i < len(statuses) {
				uj.fillStatus(statuses[i])
			}
			if f, err := quadlet.Parse(d.data); d.data != nil && err == nil {
				uj.Description, _ = f.Get("Unit", "Description")
				uj.Image, _ = f.Get(sectionForKind(d.unit.Kind), "Image")
				uj.Pod, _ = f.Get("Container", "Pod")
				uj.GPUs = quadlet.GPURefs(f)
			}
			out = append(out, uj)
		}
	}
	if out == nil {
		out = []unitJSON{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"units": out, "scopeErrors": scopeErrors})
}

// sectionForKind maps a unit kind to the Quadlet section that names its
// image, when it has one.
func sectionForKind(k quadlet.Kind) string {
	switch k {
	case quadlet.KindContainer:
		return "Container"
	case quadlet.KindImage:
		return "Image"
	case quadlet.KindBuild:
		return "Build"
	}
	return string(k)
}

// resolveUnit maps {scope}/{name} path segments to an area and the unit's
// path on disk. The returned path may not exist yet (creation via PUT);
// exists reports which.
func (s *Server) resolveUnit(w http.ResponseWriter, r *http.Request) (area Area, name, path string, exists, ok bool) {
	area, found := s.area(r.PathValue("scope"))
	if !found {
		httpError(w, http.StatusNotFound, "unknown scope")
		return area, "", "", false, false
	}
	name = r.PathValue("name")
	if err := quadlet.CheckName(name); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return area, "", "", false, false
	}
	for _, dir := range area.Dirs {
		p := joinUnitPath(area, dir, name)
		found, err := areaExists(r.Context(), area, p)
		if err != nil {
			httpError(w, http.StatusBadGateway, err.Error())
			return area, "", "", false, false
		}
		if found {
			return area, name, p, true, true
		}
	}
	return area, name, joinUnitPath(area, area.Dirs[0], name), false, true
}

func (s *Server) unitJSONFor(r *http.Request, area Area, name, path string) unitJSON {
	service, _ := quadlet.ServiceName(name)
	uj := unitJSON{
		Name:     name,
		Kind:     string(quadlet.KindFromName(name)),
		Scope:    area.Label,
		Service:  service,
		Path:     path,
		ReadOnly: filepath.Dir(path) != area.Dirs[0],
		Load:     "unknown",
	}
	if statuses, err := s.sysd.Status(r.Context(), area.Scope, []string{service}); err == nil && len(statuses) == 1 {
		uj.fillStatus(statuses[0])
	}
	return uj
}

func (s *Server) handleGetUnit(w http.ResponseWriter, r *http.Request) {
	area, name, path, exists, ok := s.resolveUnit(w, r)
	if !ok {
		return
	}
	if !exists {
		httpError(w, http.StatusNotFound, "unit file not found")
		return
	}
	data, err := areaReadFile(r.Context(), area, path)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	uj := s.unitJSONFor(r, area, name, path)
	if f, err := quadlet.Parse(data); err == nil {
		uj.Description, _ = f.Get("Unit", "Description")
		uj.Image, _ = f.Get(sectionForKind(quadlet.KindFromName(name)), "Image")
		uj.Pod, _ = f.Get("Container", "Pod")
		uj.GPUs = quadlet.GPURefs(f)
	}
	writeJSON(w, http.StatusOK, map[string]any{"unit": uj, "content": string(data)})
}

func (s *Server) handlePutUnit(w http.ResponseWriter, r *http.Request) {
	area, name, path, exists, ok := s.resolveUnit(w, r)
	if !ok {
		return
	}
	var req struct {
		Content string `json:"content"`
		Restart bool   `json:"restart"`
		// BaseContent, when present, is the content the editor loaded.
		// If the file on disk no longer matches, the save is rejected —
		// a stale browser tab must not silently revert someone's changes
		// (this bit the dogfooded rookery.container itself, twice).
		BaseContent *string `json:"baseContent"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if exists && filepath.Dir(path) != area.Dirs[0] {
		httpError(w, http.StatusConflict, fmt.Sprintf("%s lives in read-only directory %s", name, filepath.Dir(path)))
		return
	}
	if exists && req.BaseContent != nil {
		if cur, err := areaReadFile(r.Context(), area, path); err == nil && string(cur) != *req.BaseContent {
			httpError(w, http.StatusConflict,
				name+" changed on disk since this editor loaded it — reload the unit, then re-apply your edit")
			return
		}
	}

	msg := "rookery: save " + name
	if !exists {
		msg = "rookery: create " + name
	}
	validation, warnings, saved, err := s.applySave(r, area, name, path, req.Content, req.Restart, msg)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !saved {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"validation": validation})
		return
	}
	s.audit(r, "unit.save", area.Label+"/"+name, map[string]any{"scope": area.Label, "unit": name, "created": !exists, "restart": req.Restart})
	writeJSON(w, http.StatusOK, map[string]any{
		"unit":       s.unitJSONFor(r, area, name, path),
		"validation": validation,
		"created":    !exists,
		"warnings":   warnings,
		"hints":      s.areaHints(area, req.Content),
	})
}

// areaHints returns SELinux hints for local areas only — we can't see a
// remote host's enforcement state cheaply, and a wrong hint is worse than
// none.
func (s *Server) areaHints(area Area, content string) []string {
	if area.Remote() {
		return nil
	}
	return s.selinuxHints(content)
}

// applySave is the one write path for unit content: validate with the host
// generator, write atomically, daemon-reload, optionally restart, and
// record the change in git when the area tracks history. Invalid content
// returns saved=false with no side effects; infrastructure trouble after
// the write (reload, restart, git) degrades to warnings because the file —
// the source of truth — is already updated.
func (s *Server) applySave(r *http.Request, area Area, name, path, content string, restart bool, commitMsg string) (validation quadlet.ValidationResult, warnings []string, saved bool, err error) {
	ctx := r.Context()
	validation, err = s.areaValidate(ctx, area, name, content)
	if err != nil {
		return validation, nil, false, fmt.Errorf("validation failed to run: %w", err)
	}
	if !validation.Valid {
		return validation, nil, false, nil
	}
	if err := areaWriteFile(ctx, area, path, []byte(content)); err != nil {
		return validation, nil, false, err
	}
	warnings = []string{}
	if err := s.sysd.DaemonReload(ctx, area.Scope); err != nil {
		warnings = append(warnings, "daemon-reload: "+err.Error())
	}
	if restart {
		service, _ := quadlet.ServiceName(name)
		if err := s.sysd.Restart(ctx, area.Scope, service); err != nil {
			warnings = append(warnings, "restart: "+err.Error())
		}
	}
	if area.Git != nil {
		if err := area.Git.CommitFile(ctx, name, commitMsg); err != nil {
			warnings = append(warnings, "git: "+err.Error())
		}
	}
	return validation, warnings, true, nil
}

// selinuxHints flags unlabeled bind mounts, but only on hosts where that
// will actually bite (SELinux enforcing).
func (s *Server) selinuxHints(content string) []string {
	if !s.selinux() {
		return nil
	}
	return quadlet.VolumeHints(content)
}

func (s *Server) handleDeleteUnit(w http.ResponseWriter, r *http.Request) {
	area, name, path, exists, ok := s.resolveUnit(w, r)
	if !ok {
		return
	}
	if !exists {
		httpError(w, http.StatusNotFound, "unit file not found")
		return
	}
	if filepath.Dir(path) != area.Dirs[0] {
		httpError(w, http.StatusConflict, "unit lives in a read-only directory")
		return
	}
	service, _ := quadlet.ServiceName(name)
	// Best-effort stop before removal so the container doesn't outlive its
	// definition; a stop error (e.g. already stopped) must not block delete.
	_ = s.sysd.Stop(r.Context(), area.Scope, service)
	if err := areaRemove(r.Context(), area, path); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	warnings := []string{}
	if err := s.sysd.DaemonReload(r.Context(), area.Scope); err != nil {
		warnings = append(warnings, "daemon-reload: "+err.Error())
	}
	if area.Git != nil {
		if err := area.Git.CommitFile(r.Context(), name, "rookery: delete "+name); err != nil {
			warnings = append(warnings, "git: "+err.Error())
		}
	}
	s.audit(r, "unit.delete", area.Label+"/"+name, map[string]any{"scope": area.Label, "unit": name})
	writeJSON(w, http.StatusOK, map[string]any{"deleted": name, "warnings": warnings})
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	area, name, _, _, ok := s.resolveUnit(w, r)
	if !ok {
		return
	}
	if area.Git == nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false, "commits": []any{}})
		return
	}
	commits, err := area.Git.History(r.Context(), name, 50)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "commits": commits})
}

func (s *Server) handleHistoryShow(w http.ResponseWriter, r *http.Request) {
	area, name, _, _, ok := s.resolveUnit(w, r)
	if !ok {
		return
	}
	if area.Git == nil {
		httpError(w, http.StatusNotFound, "git history is not enabled for this scope")
		return
	}
	content, err := area.Git.Show(r.Context(), r.PathValue("commit"), name)
	if err != nil {
		httpError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"content": content})
}

// handleRollback restores a unit to its content at a given commit, through
// the exact same validate -> write -> reload pipeline as a manual save (so
// a rollback that no longer validates on today's Podman is rejected, not
// blindly applied). It works for deleted units too — rollback recreates
// the file in the primary directory.
func (s *Server) handleRollback(w http.ResponseWriter, r *http.Request) {
	area, name, path, exists, ok := s.resolveUnit(w, r)
	if !ok {
		return
	}
	if area.Git == nil {
		httpError(w, http.StatusNotFound, "git history is not enabled for this scope")
		return
	}
	if exists && filepath.Dir(path) != area.Dirs[0] {
		httpError(w, http.StatusConflict, "unit lives in a read-only directory")
		return
	}
	var req struct {
		Commit  string `json:"commit"`
		Restart bool   `json:"restart"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	content, err := area.Git.Show(r.Context(), req.Commit, name)
	if err != nil {
		httpError(w, http.StatusNotFound, err.Error())
		return
	}
	short := req.Commit
	if len(short) > 12 {
		short = short[:12]
	}
	validation, warnings, saved, err := s.applySave(r, area, name, path, content, req.Restart,
		fmt.Sprintf("rookery: rollback %s to %s", name, short))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !saved {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"validation": validation})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"unit":       s.unitJSONFor(r, area, name, path),
		"content":    content,
		"validation": validation,
		"warnings":   warnings,
	})
}

func (s *Server) handleAction(w http.ResponseWriter, r *http.Request) {
	area, name, path, exists, ok := s.resolveUnit(w, r)
	if !ok {
		return
	}
	if !exists {
		httpError(w, http.StatusNotFound, "unit file not found")
		return
	}
	var req struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	service, _ := quadlet.ServiceName(name)
	var err error
	switch req.Action {
	case "start":
		err = s.sysd.Start(r.Context(), area.Scope, service)
	case "stop":
		err = s.sysd.Stop(r.Context(), area.Scope, service)
	case "restart":
		err = s.sysd.Restart(r.Context(), area.Scope, service)
	case "enable":
		s.setQuadletInstall(w, r, area, name, path, true)
		return
	case "disable":
		s.setQuadletInstall(w, r, area, name, path, false)
		return
	default:
		httpError(w, http.StatusBadRequest, "unknown action "+strconv.Quote(req.Action))
		return
	}
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.audit(r, "unit."+req.Action, area.Label+"/"+name, map[string]any{"scope": area.Label, "unit": name, "service": service})
	writeJSON(w, http.StatusOK, map[string]any{"unit": s.unitJSONFor(r, area, name, path)})
}

func (s *Server) setQuadletInstall(w http.ResponseWriter, r *http.Request, area Area, name, path string, enabled bool) {
	if filepath.Dir(path) != area.Dirs[0] {
		httpError(w, http.StatusConflict, "unit lives in a read-only directory")
		return
	}
	data, err := areaReadFile(r.Context(), area, path)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	target := "multi-user.target"
	if !area.Scope.IsSystem() {
		target = "default.target"
	}
	content := setInstallEnabled(string(data), enabled, target)
	action := "disable"
	if enabled {
		action = "enable"
	}
	validation, warnings, saved, err := s.applySave(r, area, name, path, content, false, "rookery: "+action+" "+name)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !saved {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"validation": validation})
		return
	}
	service, _ := quadlet.ServiceName(name)
	s.audit(r, "unit."+action, area.Label+"/"+name, map[string]any{"scope": area.Label, "unit": name, "service": service})
	writeJSON(w, http.StatusOK, map[string]any{"unit": s.unitJSONFor(r, area, name, path), "warnings": warnings})
}

func setInstallEnabled(content string, enabled bool, target string) string {
	lines := strings.Split(content, "\n")
	inInstall := false
	installSeen := false
	hasWantedBy := false
	inserted := false
	out := make([]string, 0, len(lines)+3)

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		isSection := strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]")
		if isSection {
			if enabled && inInstall && !hasWantedBy && !inserted {
				out = append(out, "WantedBy="+target)
				inserted = true
			}
			inInstall = trimmed == "[Install]"
			if inInstall {
				installSeen = true
				hasWantedBy = false
			}
			out = append(out, line)
			continue
		}
		if inInstall {
			key, _, hasValue := strings.Cut(trimmed, "=")
			if hasValue {
				switch key {
				case "WantedBy", "RequiredBy":
					if !enabled {
						continue
					}
					hasWantedBy = true
				case "Alias":
					if !enabled {
						continue
					}
				}
			}
		}
		out = append(out, line)
	}
	if enabled {
		switch {
		case inInstall && !hasWantedBy && !inserted:
			out = append(out, "WantedBy="+target)
		case !installSeen:
			if len(out) > 0 && strings.TrimSpace(out[len(out)-1]) != "" {
				out = append(out, "")
			}
			out = append(out, "[Install]", "WantedBy="+target)
		}
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n") + "\n"
}

func (s *Server) handleValidate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Scope   string `json:"scope"`
		Name    string `json:"name"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if err := quadlet.CheckName(req.Name); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	area, found := s.area(req.Scope)
	if !found {
		httpError(w, http.StatusNotFound, "unknown scope")
		return
	}
	validation, err := s.areaValidate(r.Context(), area, req.Name, req.Content)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"validation": validation,
		"hints":      s.areaHints(area, req.Content),
	})
}

func (s *Server) handleConvert(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind  string `json:"kind"`
		Input string `json:"input"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	var units []convert.GeneratedUnit
	var err error
	switch req.Kind {
	case "run":
		var u convert.GeneratedUnit
		u, err = convert.FromRunCommand(req.Input)
		units = []convert.GeneratedUnit{u}
	case "compose":
		units, err = convert.FromCompose(req.Input)
	case "container":
		if s.pod == nil {
			httpError(w, http.StatusServiceUnavailable, "podman API socket not available")
			return
		}
		var raw []byte
		raw, err = s.pod.InspectContainer(r.Context(), req.Input)
		if err == nil {
			var u convert.GeneratedUnit
			u, err = convert.FromInspect(raw)
			units = []convert.GeneratedUnit{u}
		}
	default:
		httpError(w, http.StatusBadRequest, "kind must be run, compose, or container")
		return
	}
	if err != nil {
		httpError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"units": units})
}

func (s *Server) handleImportContainers(w http.ResponseWriter, r *http.Request) {
	if s.pod == nil {
		httpError(w, http.StatusServiceUnavailable, "podman API socket not available")
		return
	}
	list, err := s.pod.Containers(r.Context())
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	type row struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Image   string `json:"image"`
		State   string `json:"state"`
		Managed bool   `json:"managed"`
	}
	rows := []row{}
	for _, c := range list {
		if c.IsInfra {
			continue
		}
		id := c.ID
		if len(id) > 12 {
			id = id[:12]
		}
		rows = append(rows, row{ID: id, Name: c.Name(), Image: c.Image, State: c.State, Managed: c.Managed()})
	}
	writeJSON(w, http.StatusOK, map[string]any{"containers": rows})
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	area, name, _, exists, ok := s.resolveUnit(w, r)
	if !ok {
		return
	}
	if !exists {
		httpError(w, http.StatusNotFound, "unit file not found")
		return
	}
	flusher, canFlush := w.(http.Flusher)
	if !canFlush {
		httpError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	lines := 200
	if n, err := strconv.Atoi(r.URL.Query().Get("lines")); err == nil && n > 0 && n <= 5000 {
		lines = n
	}
	follow := r.URL.Query().Get("follow") == "1"

	service, _ := quadlet.ServiceName(name)
	cmd, err := journal.Command(r.Context(), area.Scope, service, lines, follow)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := cmd.Start(); err != nil {
		httpError(w, http.StatusInternalServerError, "journalctl: "+err.Error())
		return
	}
	defer cmd.Wait()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		fmt.Fprintf(w, "data: %s\n\n", scanner.Bytes())
		flusher.Flush()
	}
	// Request context cancellation kills journalctl and ends the scan.
}

// handleGPUs inventories local GPUs plus one ssh probe per distinct remote
// target, labeled with the area so the panel says whose card it is. A slow
// or dead host times out rather than hanging the panel.
func (s *Server) handleGPUs(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	devices := s.gpus(ctx)
	seen := map[string]bool{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, a := range s.areas {
		if !a.Remote() || seen[a.Scope.SSH] {
			continue
		}
		seen[a.Scope.SSH] = true
		wg.Add(1)
		go func(label, target string) {
			defer wg.Done()
			remote := s.remoteGPUs(ctx, target)
			mu.Lock()
			defer mu.Unlock()
			for _, d := range remote {
				d.Host = label
				devices = append(devices, d)
			}
		}(a.Label, a.Scope.SSH)
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, map[string]any{"devices": devices})
}

func (s *Server) handleHost(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{
		"version":            s.version,
		"metrics":            hostinfo.Read(),
		"generatorAvailable": quadlet.FindGenerator() != "",
		"selinuxEnforcing":   s.selinux(),
		"scopes":             s.scopeLabels(),
	}
	if s.pod != nil {
		if info, err := s.pod.Info(r.Context()); err == nil {
			resp["podman"] = info
		} else {
			resp["podmanError"] = err.Error()
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) scopeLabels() []string {
	labels := make([]string, len(s.areas))
	for i, a := range s.areas {
		labels[i] = a.Label
	}
	return labels
}

// writeFileAtomic writes via a temp file + rename in the target directory
// so systemd's generator never sees a half-written unit. The primary dir is
// created on demand (fresh hosts won't have ~/.config/containers/systemd).
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".rookery-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
