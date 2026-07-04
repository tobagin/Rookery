package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/tobagin/rookery/internal/convert"
	"github.com/tobagin/rookery/internal/hostinfo"
	"github.com/tobagin/rookery/internal/journal"
	"github.com/tobagin/rookery/internal/quadlet"
	"github.com/tobagin/rookery/internal/systemd"
)

// unitJSON is one unit as the API reports it: the file on disk plus live
// systemd state.
type unitJSON struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	Scope       string `json:"scope"`
	Service     string `json:"service"`
	Path        string `json:"path"`
	ReadOnly    bool   `json:"readOnly"`
	Description string `json:"description,omitempty"`
	Image       string `json:"image,omitempty"`
	Load        string `json:"load"`
	Active      string `json:"active"`
	Sub         string `json:"sub"`
	UnitFile    string `json:"unitFile"`
	Result      string `json:"result,omitempty"`
	ExitCode    int    `json:"exitCode"`
	Restarts    int    `json:"restarts"`
}

func (u *unitJSON) fillStatus(st systemd.UnitStatus) {
	u.Load, u.Active, u.Sub, u.UnitFile = st.Load, st.Active, st.Sub, st.UnitFile
	u.Result, u.ExitCode, u.Restarts = st.Result, st.ExitCode, st.Restarts
}

func (s *Server) handleListUnits(w http.ResponseWriter, r *http.Request) {
	var out []unitJSON
	scopeErrors := map[string]string{}
	for _, area := range s.areas {
		units, err := quadlet.Discover(area.Dirs)
		if err != nil {
			scopeErrors[area.Label] = err.Error()
			continue
		}
		services := make([]string, len(units))
		for i, u := range units {
			services[i], _ = quadlet.ServiceName(u.Name)
		}
		statuses, err := s.sysd.Status(r.Context(), area.Scope, services)
		if err != nil {
			// Files on disk are still worth showing when systemd is
			// unreachable; state stays "unknown".
			scopeErrors[area.Label] = err.Error()
			statuses = nil
		}
		for i, u := range units {
			uj := unitJSON{
				Name:     u.Name,
				Kind:     string(u.Kind),
				Scope:    area.Label,
				Service:  services[i],
				Path:     u.Path,
				ReadOnly: filepath.Dir(u.Path) != area.Dirs[0],
				Load:     "unknown",
			}
			if i < len(statuses) {
				uj.fillStatus(statuses[i])
			}
			if data, err := os.ReadFile(u.Path); err == nil {
				if f, err := quadlet.Parse(data); err == nil {
					uj.Description, _ = f.Get("Unit", "Description")
					uj.Image, _ = f.Get(sectionForKind(u.Kind), "Image")
				}
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
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return area, name, p, true, true
		}
	}
	return area, name, filepath.Join(area.Dirs[0], name), false, true
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
	data, err := os.ReadFile(path)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	uj := s.unitJSONFor(r, area, name, path)
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
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if exists && filepath.Dir(path) != area.Dirs[0] {
		httpError(w, http.StatusConflict, fmt.Sprintf("%s lives in read-only directory %s", name, filepath.Dir(path)))
		return
	}

	validation, err := s.validate(r.Context(), !area.Scope.IsSystem(), name, req.Content)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "validation failed to run: "+err.Error())
		return
	}
	if !validation.Valid {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"validation": validation})
		return
	}

	if err := writeFileAtomic(path, []byte(req.Content)); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	warnings := []string{}
	if err := s.sysd.DaemonReload(r.Context(), area.Scope); err != nil {
		warnings = append(warnings, "daemon-reload: "+err.Error())
	}
	if req.Restart {
		service, _ := quadlet.ServiceName(name)
		if err := s.sysd.Restart(r.Context(), area.Scope, service); err != nil {
			warnings = append(warnings, "restart: "+err.Error())
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"unit":       s.unitJSONFor(r, area, name, path),
		"validation": validation,
		"created":    !exists,
		"warnings":   warnings,
		"hints":      s.selinuxHints(req.Content),
	})
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
	if err := os.Remove(path); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	warnings := []string{}
	if err := s.sysd.DaemonReload(r.Context(), area.Scope); err != nil {
		warnings = append(warnings, "daemon-reload: "+err.Error())
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": name, "warnings": warnings})
}

func (s *Server) handleAction(w http.ResponseWriter, r *http.Request) {
	area, name, _, exists, ok := s.resolveUnit(w, r)
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
		err = s.sysd.Enable(r.Context(), area.Scope, service)
	case "disable":
		err = s.sysd.Disable(r.Context(), area.Scope, service)
	default:
		httpError(w, http.StatusBadRequest, "unknown action "+strconv.Quote(req.Action))
		return
	}
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	path := filepath.Join(area.Dirs[0], name)
	writeJSON(w, http.StatusOK, map[string]any{"unit": s.unitJSONFor(r, area, name, path)})
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
	userScope := req.Scope != "system"
	validation, err := s.validate(r.Context(), userScope, req.Name, req.Content)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"validation": validation,
		"hints":      s.selinuxHints(req.Content),
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
