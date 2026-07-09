package server

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/rookerylabs/rookery/internal/quadlet"
	"github.com/rookerylabs/rookery/internal/systemd"

	papi "github.com/rookerylabs/rookery-agent-api"
)

// This file is the agent connector's half of the server: everything that
// treats an area reached through a rookery-agent differently from a local or
// ssh area. The agent returns units with status already attached, so these
// paths make one HTTP call instead of discover + `systemctl show`.

// agentUnitJSON maps one agent-reported unit into the API's unit shape. Agent
// units are read-only for now: the agent serves status and lifecycle, not file
// contents, so editing/validation stay on local and ssh areas.
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
		ReadOnly:  true,
		Load:      "unknown",
	}
	uj.fillStatus(systemd.UnitStatus{
		Load: u.Status.Load, Active: u.Status.Active, Sub: u.Status.Sub,
		UnitFile: u.Status.UnitFile, Result: u.Status.Result,
		ExitCode: u.Status.ExitCode, Restarts: u.Status.Restarts,
	})
	return uj
}

// appendAgentUnits fetches an agent area's units and appends them to out. A
// fetch error is recorded per-scope, mirroring the local path, so one dead
// agent never blanks the whole list.
func appendAgentUnits(r *http.Request, area Area, out []unitJSON, scopeErrors map[string]string) []unitJSON {
	units, err := area.Agent.Units(r.Context())
	if err != nil {
		scopeErrors[area.Label] = err.Error()
		return out
	}
	for _, u := range units {
		out = append(out, agentUnitJSON(area, u))
	}
	return out
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
	// enable/disable edit the quadlet's [Install] section on local/ssh areas;
	// the agent serves no file contents yet, so restrict agent areas to the
	// runtime verbs rather than fake a half-working enable.
	switch req.Action {
	case papi.ActionStart, papi.ActionStop, papi.ActionRestart:
	case papi.ActionEnable, papi.ActionDisable:
		httpError(w, http.StatusNotImplemented, req.Action+" is not supported on agent scopes yet")
		return
	default:
		httpError(w, http.StatusBadRequest, "unknown action "+strconv.Quote(req.Action))
		return
	}
	service, _ := quadlet.ServiceName(name)
	if err := area.Agent.Action(r.Context(), service, req.Action); err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.audit(r, "unit."+req.Action, area.Label+"/"+name, map[string]any{
		"scope": area.Label, "unit": name, "service": service, "via": "agent",
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
