package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/tobagin/rookery/internal/appdb"
	"github.com/tobagin/rookery/internal/hostinfo"
	"github.com/tobagin/rookery/internal/quadlet"
	"github.com/tobagin/rookery/internal/rhost"
	"github.com/tobagin/rookery/internal/systemd"
)

type NodeScope struct {
	Label  string `json:"label"`
	User   string `json:"user,omitempty"`
	System bool   `json:"system"`
	Kind   string `json:"kind"`
}

type NodeCounts struct {
	Units   int `json:"units"`
	Running int `json:"running"`
	Failed  int `json:"failed"`
	Unknown int `json:"unknown"`
}

type NodeInventory struct {
	ID       string            `json:"id"`
	Address  string            `json:"address,omitempty"`
	Local    bool              `json:"local"`
	Scopes   []NodeScope       `json:"scopes"`
	Labels   []string          `json:"labels"`
	UnitDirs []string          `json:"unitDirs"`
	Units    int               `json:"units"`
	Running  int               `json:"running"`
	Failed   int               `json:"failed"`
	Unknown  int               `json:"unknown"`
	Rootful  NodeCounts        `json:"rootful"`
	Rootless NodeCounts        `json:"rootless"`
	Metrics  *hostinfo.Metrics `json:"metrics,omitempty"`
	Errors   []string          `json:"errors,omitempty"`
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
		if area.NodeID != "" {
			id = area.NodeID
		}
		i, ok := index[id]
		if !ok {
			nodes = append(nodes, NodeInventory{ID: id, Address: area.Scope.SSH, Local: !area.Remote()})
			i = len(nodes) - 1
			index[id] = i
			if area.Remote() {
				if m, err := rhost.Metrics(r.Context(), area.Scope.SSH); err == nil {
					nodes[i].Metrics = &m
				}
			} else {
				m := hostinfo.Read()
				nodes[i].Metrics = &m
			}
		}
		node := &nodes[i]
		if node.Address == "" && area.Scope.SSH != "" {
			node.Address = area.Scope.SSH
		}
		scopeKind := "rootless"
		counts := &node.Rootless
		if area.Scope.IsSystem() {
			scopeKind = "rootful"
			counts = &node.Rootful
		}
		node.Scopes = append(node.Scopes, NodeScope{Label: area.Label, User: area.Scope.User, System: area.Scope.IsSystem(), Kind: scopeKind})
		node.UnitDirs = append(node.UnitDirs, area.Dirs...)

		found, err := discoverArea(r.Context(), area)
		if err != nil {
			node.Errors = append(node.Errors, area.Label+": "+err.Error())
			continue
		}
		node.Units += len(found)
		counts.Units += len(found)
		services := make([]string, len(found))
		for i, d := range found {
			services[i], _ = quadlet.ServiceName(d.unit.Name)
		}
		statuses, err := s.sysd.Status(r.Context(), area.Scope, services)
		if err != nil {
			node.Unknown += len(found)
			counts.Unknown += len(found)
			node.Errors = append(node.Errors, area.Label+": "+err.Error())
			continue
		}
		for _, st := range statuses {
			switch st.Active {
			case "active":
				node.Running++
				counts.Running++
			case "failed":
				node.Failed++
				counts.Failed++
			default:
				if st.Load == "unknown" {
					node.Unknown++
					counts.Unknown++
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

func (s *Server) handleAddNode(w http.ResponseWriter, r *http.Request) {
	if s.users == nil {
		httpError(w, http.StatusServiceUnavailable, "no app database configured")
		return
	}
	if s.remotesLocked() {
		httpError(w, http.StatusConflict, "remote nodes are locked by flag or environment configuration")
		return
	}
	var req struct {
		ID     string `json:"id"`
		Target string `json:"target"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	req.ID = strings.TrimSpace(req.ID)
	req.Target = strings.TrimSpace(req.Target)
	if req.ID == "" || req.Target == "" {
		httpError(w, http.StatusBadRequest, "id and target are required")
		return
	}
	area, err := remoteAreaFromTarget(r.Context(), req.ID, req.Target)
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	for _, existing := range s.areas {
		if existing.Label == area.Label {
			httpError(w, http.StatusConflict, "node scope already exists")
			return
		}
	}
	s.areas = append(s.areas, area)
	if err := s.persistRuntimeRemotes(); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "node.add", area.NodeID, map[string]any{"target": req.Target, "scope": area.Label})
	writeJSON(w, http.StatusOK, map[string]any{"nodes": s.nodeInventory(r)})
}

func (s *Server) handleDeleteNode(w http.ResponseWriter, r *http.Request) {
	if s.users == nil {
		httpError(w, http.StatusServiceUnavailable, "no app database configured")
		return
	}
	if s.remotesLocked() {
		httpError(w, http.StatusConflict, "remote nodes are locked by flag or environment configuration")
		return
	}
	id := r.PathValue("id")
	next := s.areas[:0]
	removed := false
	for _, area := range s.areas {
		nodeID := area.NodeID
		if nodeID == "" && area.Remote() {
			nodeID = area.Scope.SSH
		}
		if area.Remote() && (nodeID == id || area.Label == id) {
			removed = true
			continue
		}
		next = append(next, area)
	}
	if !removed {
		httpError(w, http.StatusNotFound, "unknown editable remote node")
		return
	}
	s.areas = next
	if err := s.persistRuntimeRemotes(); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "node.delete", id, nil)
	writeJSON(w, http.StatusOK, map[string]any{"nodes": s.nodeInventory(r)})
}

func remoteAreaFromTarget(ctx context.Context, alias, target string) (Area, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	uid, home, remoteUser, err := rhost.Probe(ctx, target)
	if err != nil {
		return Area{}, err
	}
	nodeID := alias
	if node, scope, ok := strings.Cut(alias, "."); ok && node != "" && groupedRemoteScope(scope) {
		nodeID = node
	}
	area := Area{Label: alias, NodeID: nodeID}
	if uid == 0 {
		area.Scope = systemd.Scope{SSH: target}
		area.Dirs = quadlet.SystemDirs()
	} else {
		area.Scope = systemd.Scope{User: remoteUser, SSH: target}
		area.Dirs = quadlet.UserDirs(home)
	}
	return area, nil
}

func groupedRemoteScope(scope string) bool {
	switch scope {
	case "root", "rootful", "user", "rootless":
		return true
	}
	return false
}

func (s *Server) remotesLocked() bool {
	for _, group := range s.settings {
		for _, item := range group.Items {
			if item.Key == "remotes" {
				return item.Locked
			}
		}
	}
	return false
}

func (s *Server) persistRuntimeRemotes() error {
	if s.users == nil {
		return fmt.Errorf("no app database configured")
	}
	var entries []string
	for _, area := range s.areas {
		if !area.Remote() {
			continue
		}
		entries = append(entries, area.Label+"="+area.Scope.SSH)
	}
	value := strings.Join(entries, ",")
	for gi := range s.settings {
		for ii := range s.settings[gi].Items {
			if s.settings[gi].Items[ii].Key == "remotes" {
				s.settings[gi].Items[ii].Value = value
				s.settings[gi].Items[ii].Source = "db"
				s.settings[gi].Items[ii].RestartRequired = false
			}
		}
	}
	return appdb.PutSetting(s.users.DB(), "remotes", value)
}
