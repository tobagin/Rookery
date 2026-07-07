package server

import (
	"encoding/json"
	"net/http"
	"sort"

	"github.com/tobagin/rookery/internal/appdb"
	"github.com/tobagin/rookery/internal/quadlet"
)

type NodeScope struct {
	Label  string `json:"label"`
	User   string `json:"user,omitempty"`
	System bool   `json:"system"`
}

type NodeInventory struct {
	ID       string      `json:"id"`
	Address  string      `json:"address,omitempty"`
	Local    bool        `json:"local"`
	Scopes   []NodeScope `json:"scopes"`
	Labels   []string    `json:"labels"`
	UnitDirs []string    `json:"unitDirs"`
	Units    int         `json:"units"`
	Running  int         `json:"running"`
	Failed   int         `json:"failed"`
	Unknown  int         `json:"unknown"`
	Errors   []string    `json:"errors,omitempty"`
}

type NodeGroup struct {
	Label   string   `json:"label"`
	Nodes   []string `json:"nodes"`
	Units   int      `json:"units"`
	Running int      `json:"running"`
	Failed  int      `json:"failed"`
	Unknown int      `json:"unknown"`
}

func (s *Server) nodeInventory(r *http.Request) []NodeInventory {
	index := map[string]int{}
	var nodes []NodeInventory
	for _, area := range s.areas {
		id := "local"
		if area.Remote() {
			id = area.Scope.SSH
		}
		i, ok := index[id]
		if !ok {
			nodes = append(nodes, NodeInventory{ID: id, Address: area.Scope.SSH, Local: !area.Remote()})
			i = len(nodes) - 1
			index[id] = i
		}
		node := &nodes[i]
		node.Scopes = append(node.Scopes, NodeScope{Label: area.Label, User: area.Scope.User, System: area.Scope.IsSystem()})
		node.UnitDirs = append(node.UnitDirs, area.Dirs...)

		found, err := discoverArea(r.Context(), area)
		if err != nil {
			node.Errors = append(node.Errors, area.Label+": "+err.Error())
			continue
		}
		node.Units += len(found)
		services := make([]string, len(found))
		for i, d := range found {
			services[i], _ = quadlet.ServiceName(d.unit.Name)
		}
		statuses, err := s.sysd.Status(r.Context(), area.Scope, services)
		if err != nil {
			node.Unknown += len(found)
			node.Errors = append(node.Errors, area.Label+": "+err.Error())
			continue
		}
		for _, st := range statuses {
			switch st.Active {
			case "active":
				node.Running++
			case "failed":
				node.Failed++
			default:
				if st.Load == "unknown" {
					node.Unknown++
				}
			}
		}
	}
	if s.users == nil {
		return nodes
	}
	meta, err := appdb.GetNodeMetadata(s.users.DB())
	if err != nil {
		return nodes
	}
	for i := range nodes {
		if m, ok := meta[nodes[i].ID]; ok {
			nodes[i].Labels = m.Labels
		}
	}
	return nodes
}

func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"nodes": s.nodeInventory(r), "license": s.licenseStatus()})
}

func (s *Server) nodeGroups(r *http.Request) []NodeGroup {
	byLabel := map[string]*NodeGroup{}
	for _, node := range s.nodeInventory(r) {
		labels := node.Labels
		if len(labels) == 0 {
			labels = []string{"unlabeled"}
		}
		for _, label := range labels {
			g := byLabel[label]
			if g == nil {
				g = &NodeGroup{Label: label}
				byLabel[label] = g
			}
			g.Nodes = append(g.Nodes, node.ID)
			g.Units += node.Units
			g.Running += node.Running
			g.Failed += node.Failed
			g.Unknown += node.Unknown
		}
	}
	out := make([]NodeGroup, 0, len(byLabel))
	for _, group := range byLabel {
		out = append(out, *group)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Label < out[j].Label })
	return out
}

func (s *Server) handleNodeGroups(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"groups": s.nodeGroups(r)})
}

func (s *Server) handleNodeLabels(w http.ResponseWriter, r *http.Request) {
	if s.users == nil {
		httpError(w, http.StatusServiceUnavailable, "no app database configured")
		return
	}
	id := r.PathValue("id")
	if id == "" {
		httpError(w, http.StatusBadRequest, "missing node id")
		return
	}
	var known bool
	for _, node := range s.nodeInventory(r) {
		if node.ID == id {
			known = true
			break
		}
	}
	if !known {
		httpError(w, http.StatusNotFound, "unknown node")
		return
	}
	var req struct {
		Labels []string `json:"labels"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := appdb.PutNodeLabels(s.users.DB(), id, req.Labels); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "node.labels", id, map[string]any{"labels": req.Labels})
	writeJSON(w, http.StatusOK, map[string]any{"updated": true, "nodes": s.nodeInventory(r)})
}
