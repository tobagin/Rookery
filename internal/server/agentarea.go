package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/rookerylabs/rookery/internal/quadlet"
	"github.com/rookerylabs/rookery/internal/systemd"

	papi "github.com/rookerylabs/rookery-agent-api"
)

// This file is the agent connector's half of the server: everything that
// treats an area reached through a rookery-agent differently from a local or
// ssh area. The agent returns units with status already attached, so these
// paths make one HTTP call instead of discover + `systemctl show`.

// agentUnitJSON maps one agent-reported unit into the API's unit shape. Agent
// units are editable — the agent serves file read/write — so they are not
// marked read-only; history stays disabled since the agent keeps no git.
// areaNodeID is the node identity an area belongs to, matching the Fleet node
// list: an explicit NodeID wins, else a remote area is keyed by its ssh target,
// else it is the local host.
func areaNodeID(area Area) string {
	if area.NodeID != "" {
		return area.NodeID
	}
	if area.Remote() {
		return area.Scope.SSH
	}
	return "local"
}

func agentUnitJSON(area Area, u papi.Unit) unitJSON {
	scopeKind := "rootless"
	if area.Scope.IsSystem() {
		scopeKind = "rootful"
	}
	uj := unitJSON{
		Name:      u.Name,
		Kind:      u.Kind,
		Scope:     area.Label,
		ScopeKind: scopeKind,
		ScopeUser: area.Scope.User,
		Service:   u.Service,
		Path:      u.Path,
		Node:      areaNodeID(area),
		Load:      "unknown",
	}
	uj.fillStatus(systemd.UnitStatus{
		Load: u.Status.Load, Active: u.Status.Active, Sub: u.Status.Sub,
		UnitFile: u.Status.UnitFile, Result: u.Status.Result,
		ExitCode: u.Status.ExitCode, Restarts: u.Status.Restarts,
	})
	return uj
}

// agentUnit fetches one unit's live metadata (status) from the agent, or a
// minimal record if the agent doesn't list it. Shared by the get/save paths
// so the refreshed unit they return looks like the list's.
func (s *Server) agentUnit(r *http.Request, area Area, name string) unitJSON {
	units, _ := area.Agent.Units(r.Context(), area.AgentScope)
	for _, u := range units {
		if u.Name == name {
			return agentUnitJSON(area, u)
		}
	}
	uj := unitJSON{Name: name, Scope: area.Label, ScopeUser: area.Scope.User, Load: "unknown"}
	if kind := quadlet.KindFromName(name); kind != "" {
		uj.Kind = string(kind)
	}
	uj.ScopeKind = "rootless"
	if area.Scope.IsSystem() {
		uj.ScopeKind = "rootful"
	}
	uj.Service, _ = quadlet.ServiceName(name)
	return uj
}

// enrichFromContent fills the description/image/pod/gpu fields the UI shows
// from a unit's file contents.
func enrichFromContent(uj *unitJSON, name string, data []byte) {
	if f, err := quadlet.Parse(data); err == nil {
		uj.Description, _ = f.Get("Unit", "Description")
		uj.Image, _ = f.Get(sectionForKind(quadlet.KindFromName(name)), "Image")
		uj.Pod, _ = f.Get("Container", "Pod")
		uj.GPUs = quadlet.GPURefs(f)
	}
}

// appendAgentUnits fetches an agent area's units and appends them to out,
// attaching live stats. A fetch error is recorded per-scope, mirroring the
// local path, so one dead agent never blanks the whole list.
func (s *Server) appendAgentUnits(r *http.Request, area Area, out []unitJSON, scopeErrors map[string]string) []unitJSON {
	units, err := area.Agent.Units(r.Context(), area.AgentScope)
	if err != nil {
		scopeErrors[area.Label] = err.Error()
		return out
	}
	runtime := s.runtimeByService(r.Context(), area)
	for _, u := range units {
		uj := agentUnitJSON(area, u)
		if rt, ok := runtime[u.Service]; ok {
			uj.Stats = rt.Stats
			uj.Health = rt.Health
		}
		out = append(out, uj)
	}
	return out
}

// agentRuntimeByService maps an agent scope's containers to per-service stats
// and health, mirroring the local runtimeByService: match PODMAN_SYSTEMD_UNIT
// to a stats row by container id or name. The agent resolves health locally,
// so it arrives on the container without an extra round trip.
func agentRuntimeByService(ctx context.Context, area Area) map[string]unitRuntime {
	out := map[string]unitRuntime{}
	containers, err := area.Agent.Containers(ctx, area.AgentScope)
	if err != nil {
		return out
	}
	statsRows, _ := area.Agent.Stats(ctx, area.AgentScope)
	stats := map[string]unitStats{}
	for _, row := range statsRows {
		st := unitStats{CPUPct: row.CPUPct, MemBytes: row.MemBytes}
		if row.ID != "" {
			stats[row.ID] = st
		}
		if row.Name != "" {
			stats[row.Name] = st
		}
	}
	for _, c := range containers {
		service := c.Labels["PODMAN_SYSTEMD_UNIT"]
		if service == "" {
			continue
		}
		rt := unitRuntime{Health: c.Health}
		if st, ok := stats[c.ID]; ok {
			rt.Stats = &st
		} else if len(c.Names) > 0 {
			if st, ok := stats[c.Names[0]]; ok {
				rt.Stats = &st
			}
		}
		out[service] = rt
	}
	return out
}

// handleAgentGetUnit serves a single agent unit's file contents plus its live
// status. Two agent calls (file + units); the file lives on the agent's host,
// so resolveUnit's local-disk stat is bypassed.
func (s *Server) handleAgentGetUnit(w http.ResponseWriter, r *http.Request, area Area) {
	name := r.PathValue("name")
	if err := quadlet.CheckName(name); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	data, err := area.Agent.UnitFile(r.Context(), area.AgentScope, name)
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	uj := s.agentUnit(r, area, name)
	enrichFromContent(&uj, name, data)
	writeJSON(w, http.StatusOK, map[string]any{"unit": uj, "content": string(data)})
}

// handleAgentPutUnit saves a unit's contents through the agent: best-effort
// validation on the control plane's generator (a syntax check; the agent's
// own Podman may differ), then write + daemon-reload on the agent, then an
// optional restart. No git history — the agent keeps none.
func (s *Server) handleAgentPutUnit(w http.ResponseWriter, r *http.Request, area Area) {
	name := r.PathValue("name")
	if err := quadlet.CheckName(name); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	var req struct {
		Content     string  `json:"content"`
		Restart     bool    `json:"restart"`
		BaseContent *string `json:"baseContent"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if req.BaseContent != nil {
		if cur, err := area.Agent.UnitFile(r.Context(), area.AgentScope, name); err == nil && string(cur) != *req.BaseContent {
			httpError(w, http.StatusConflict,
				name+" changed on the agent since this editor loaded it — reload the unit, then re-apply your edit")
			return
		}
	}
	created := true
	if _, err := area.Agent.UnitFile(r.Context(), area.AgentScope, name); err == nil {
		created = false
	}
	validation, err := s.areaValidate(r.Context(), area, name, req.Content)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "validation failed to run: "+err.Error())
		return
	}
	if !validation.Valid {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"validation": validation})
		return
	}
	if err := area.Agent.WriteUnitFile(r.Context(), area.AgentScope, name, []byte(req.Content)); err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	warnings := []string{}
	if req.Restart {
		service, _ := quadlet.ServiceName(name)
		if err := area.Agent.Action(r.Context(), area.AgentScope, service, papi.ActionRestart); err != nil {
			warnings = append(warnings, "restart: "+err.Error())
		}
	}
	s.audit(r, "unit.save", area.Label+"/"+name, map[string]any{"scope": area.Label, "unit": name, "created": created, "restart": req.Restart, "via": "agent"})
	uj := s.agentUnit(r, area, name)
	enrichFromContent(&uj, name, []byte(req.Content))
	writeJSON(w, http.StatusOK, map[string]any{"unit": uj, "validation": validation, "created": created, "warnings": warnings})
}

// handleAgentDeleteUnit stops the service and removes its file on the agent.
func (s *Server) handleAgentDeleteUnit(w http.ResponseWriter, r *http.Request, area Area) {
	name := r.PathValue("name")
	if err := quadlet.CheckName(name); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	service, _ := quadlet.ServiceName(name)
	_ = area.Agent.Action(r.Context(), area.AgentScope, service, papi.ActionStop) // best effort
	if err := area.Agent.DeleteUnitFile(r.Context(), area.AgentScope, name); err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.audit(r, "unit.delete", area.Label+"/"+name, map[string]any{"scope": area.Label, "unit": name, "via": "agent"})
	writeJSON(w, http.StatusOK, map[string]any{"deleted": name, "warnings": []string{}})
}

// setAgentInstall enables/disables a unit by rewriting its [Install] section
// through the agent, mirroring the local setQuadletInstall.
func (s *Server) setAgentInstall(w http.ResponseWriter, r *http.Request, area Area, name string, enabled bool) {
	data, err := area.Agent.UnitFile(r.Context(), area.AgentScope, name)
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	target := "multi-user.target"
	if !area.Scope.IsSystem() {
		target = "default.target"
	}
	content := setInstallEnabled(string(data), enabled, target)
	if err := area.Agent.WriteUnitFile(r.Context(), area.AgentScope, name, []byte(content)); err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	action := "disable"
	if enabled {
		action = "enable"
	}
	service, _ := quadlet.ServiceName(name)
	s.audit(r, "unit."+action, area.Label+"/"+name, map[string]any{"scope": area.Label, "unit": name, "service": service, "via": "agent"})
	uj := s.agentUnit(r, area, name)
	enrichFromContent(&uj, name, []byte(content))
	writeJSON(w, http.StatusOK, map[string]any{"unit": uj, "warnings": []string{}})
}

// handleAgentLogs serves an agent unit's journal as a one-shot SSE stream.
// The agent returns a tail, not a live feed, so follow=1 is ignored for now —
// the viewer still renders it because the wire format matches the local path.
func (s *Server) handleAgentLogs(w http.ResponseWriter, r *http.Request, area Area) {
	name := r.PathValue("name")
	if err := quadlet.CheckName(name); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	lines := 200
	if n, err := strconv.Atoi(r.URL.Query().Get("lines")); err == nil && n > 0 && n <= 5000 {
		lines = n
	}
	out, err := area.Agent.Logs(r.Context(), area.AgentScope, name, lines, strings.TrimSpace(r.URL.Query().Get("since")))
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		fmt.Fprintf(w, "data: %s\n\n", line)
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// handleAgentAction runs a lifecycle verb against an agent-backed unit. It
// bypasses resolveUnit (which stats the file on local disk) because an agent
// area's files live on the agent's host, not here.
func (s *Server) handleAgentAction(w http.ResponseWriter, r *http.Request, area Area) {
	name := r.PathValue("name")
	if err := quadlet.CheckName(name); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	var req struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	// enable/disable mean "rewrite the [Install] section", same as local/ssh
	// areas — route them through the file-edit path on the agent.
	switch req.Action {
	case papi.ActionStart, papi.ActionStop, papi.ActionRestart:
	case papi.ActionEnable:
		s.setAgentInstall(w, r, area, name, true)
		return
	case papi.ActionDisable:
		s.setAgentInstall(w, r, area, name, false)
		return
	default:
		httpError(w, http.StatusBadRequest, "unknown action "+strconv.Quote(req.Action))
		return
	}
	service, _ := quadlet.ServiceName(name)
	if err := area.Agent.Action(r.Context(), area.AgentScope, service, req.Action); err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.audit(r, "unit."+req.Action, area.Label+"/"+name, map[string]any{
		"scope": area.Label, "unit": name, "service": service, "via": "agent",
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
