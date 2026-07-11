package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/rookerylabs/rookery/internal/podman"
	"github.com/rookerylabs/rookery/internal/quadlet"
)

// resourcesAPI is the slice of the Podman client the resource pages need,
// asserted at runtime so test fakes stay small (mirrors imagesAPI).
type resourcesAPI interface {
	Networks(ctx context.Context) ([]podman.NetworkSummary, error)
	Volumes(ctx context.Context) ([]podman.VolumeSummary, error)
	Images(ctx context.Context) ([]podman.ImageSummary, error)
	ResourceUsage(ctx context.Context) podman.Usage
}

// humanBytes renders a byte count like "1.4 GB" for the resource Detail field.
func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// imageResourceName picks a display name for an image: its first real tag, or
// "" for a dangling/intermediate image (which the list skips).
func imageResourceName(repoTags []string) string {
	for _, t := range repoTags {
		if t != "" && t != "<none>:<none>" {
			return t
		}
	}
	return ""
}

// resourceJSON is one live podman object (network or volume) as the API reports
// it, tagged whether a Quadlet unit manages it.
type resourceJSON struct {
	Kind    string `json:"kind"` // "network" | "volume"
	Name    string `json:"name"`
	Scope   string `json:"scope"`
	Node    string `json:"node,omitempty"`
	Driver  string `json:"driver,omitempty"`
	Detail  string `json:"detail,omitempty"` // subnet for networks, mountpoint for volumes
	Managed bool   `json:"managed"`
	Used    bool   `json:"used"` // referenced by a container/unit
}

// handleListResources lists live podman networks and volumes so the typed pages
// show real objects, not just the (usually empty) set of .network/.volume
// Quadlet units. Every scope is covered: the local rootful store via s.pod,
// each local rootless user session via its own /run/user socket, and remote
// hosts via their agent.
func (s *Server) handleListResources(w http.ResponseWriter, r *http.Request) {
	out := []resourceJSON{}
	scopeErrors := map[string]string{}
	for _, area := range s.areasSnapshot() {
		switch {
		case area.ViaAgent():
			// Remote agent-backed scopes are served by a rookery-agent, which
			// computes managed itself (it has the podman store + units).
			out = s.appendAgentResources(r, area, out, scopeErrors)
		case !area.Remote() && (area.Scope.IsSystem() || area.LocalRootless()):
			// Local scopes: the rootful system store via the shared s.pod
			// client, and each rootless user session via its own /run/user
			// socket. localPod picks the right one.
			out = s.appendLocalResources(r, area, out, scopeErrors)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"resources": out, "scopeErrors": scopeErrors})
}

func (s *Server) appendLocalResources(r *http.Request, area Area, out []resourceJSON, scopeErrors map[string]string) []resourceJSON {
	rp := s.localPod(area)
	if rp == nil {
		return out
	}
	managed := s.managedResourceNames(r.Context(), area)
	node := areaNodeID(area)
	used := rp.ResourceUsage(r.Context())
	if nets, err := rp.Networks(r.Context()); err != nil {
		scopeErrors[area.Label] = err.Error()
	} else {
		for _, n := range nets {
			detail := ""
			if len(n.Subnets) > 0 {
				detail = n.Subnets[0].Subnet
			}
			out = append(out, resourceJSON{Kind: "network", Name: n.Name, Scope: area.Label, Node: node, Driver: n.Driver, Detail: detail, Managed: managed["network:"+n.Name], Used: used.Networks[n.Name]})
		}
	}
	if vols, err := rp.Volumes(r.Context()); err != nil {
		scopeErrors[area.Label] = err.Error()
	} else {
		for _, v := range vols {
			out = append(out, resourceJSON{Kind: "volume", Name: v.Name, Scope: area.Label, Node: node, Driver: v.Driver, Detail: v.Mountpoint, Managed: managed["volume:"+v.Name], Used: used.Volumes[v.Name]})
		}
	}
	if imgs, err := rp.Images(r.Context()); err != nil {
		scopeErrors[area.Label] = err.Error()
	} else {
		for _, im := range imgs {
			name := imageResourceName(im.RepoTags)
			if name == "" {
				continue // skip dangling/intermediate images
			}
			out = append(out, resourceJSON{Kind: "image", Name: name, Scope: area.Label, Node: node, Detail: humanBytes(im.Size), Used: used.Images[name]})
		}
	}
	return out
}

// resourceInspector is the inspect slice of the Podman client, asserted at runtime.
type resourceInspector interface {
	InspectNetwork(ctx context.Context, name string) ([]byte, error)
	InspectVolume(ctx context.Context, name string) ([]byte, error)
	InspectImage(ctx context.Context, name string) ([]byte, error)
	Containers(ctx context.Context) ([]podman.ContainerSummary, error)
}

type resourceField struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type containerRef struct{ Image, Name string }

func inspectByKind(ins resourceInspector, ctx context.Context, kind, name string) ([]byte, error) {
	switch kind {
	case "network":
		return ins.InspectNetwork(ctx, name)
	case "volume":
		return ins.InspectVolume(ctx, name)
	case "image":
		return ins.InspectImage(ctx, name)
	}
	return nil, fmt.Errorf("unknown resource kind %q", kind)
}

// buildResourceDetail turns a raw podman inspect body into display fields +
// "used by". Networks list their attached containers from the inspect itself;
// images match against the scope's container list. Shared by the local and
// agent paths so both render identically.
func buildResourceDetail(kind, name string, raw []byte, containers []containerRef) ([]resourceField, []string) {
	fields := []resourceField{}
	usedBy := []string{}
	add := func(m map[string]any, jsonKey, label string) {
		if v, ok := m[jsonKey]; ok && v != nil && fmt.Sprint(v) != "" {
			fields = append(fields, resourceField{label, fmt.Sprint(v)})
		}
	}
	switch kind {
	case "network":
		var n map[string]any
		json.Unmarshal(raw, &n)
		add(n, "driver", "driver")
		add(n, "network_interface", "interface")
		if subs, ok := n["subnets"].([]any); ok && len(subs) > 0 {
			if m, ok := subs[0].(map[string]any); ok {
				add(m, "subnet", "subnet")
				add(m, "gateway", "gateway")
			}
		}
		add(n, "internal", "internal")
		add(n, "dns_enabled", "dns")
		add(n, "ipv6_enabled", "ipv6")
		if cs, ok := n["containers"].(map[string]any); ok {
			for _, cv := range cs {
				if m, ok := cv.(map[string]any); ok {
					if nm, ok := m["name"].(string); ok {
						usedBy = append(usedBy, nm)
					}
				}
			}
		}
	case "volume":
		var v map[string]any
		json.Unmarshal(raw, &v)
		add(v, "Driver", "driver")
		add(v, "Mountpoint", "mountpoint")
		add(v, "CreatedAt", "created")
		if labels, ok := v["Labels"].(map[string]any); ok && len(labels) > 0 {
			parts := make([]string, 0, len(labels))
			for k, val := range labels {
				parts = append(parts, fmt.Sprintf("%s=%v", k, val))
			}
			fields = append(fields, resourceField{"labels", strings.Join(parts, ", ")})
		}
	case "image":
		var im map[string]any
		json.Unmarshal(raw, &im)
		if id, ok := im["Id"].(string); ok {
			trimmed := strings.TrimPrefix(id, "sha256:")
			fields = append(fields, resourceField{"id", trimmed[:min(12, len(trimmed))]})
		}
		add(im, "Created", "created")
		if arch, ok := im["Architecture"].(string); ok {
			os, _ := im["Os"].(string)
			fields = append(fields, resourceField{"platform", strings.TrimRight(os+"/"+arch, "/")})
		}
		if size, ok := im["Size"].(float64); ok {
			fields = append(fields, resourceField{"size", humanBytes(int64(size))})
		}
		for _, c := range containers {
			if c.Image == name {
				usedBy = append(usedBy, c.Name)
			}
		}
	}
	return fields, usedBy
}

// handleInspectResource returns display fields + "used by" for one resource,
// via s.pod for the local rootful scope or the agent for remote/rootless scopes.
func (s *Server) handleInspectResource(w http.ResponseWriter, r *http.Request) {
	scope, kind, name := r.URL.Query().Get("scope"), r.URL.Query().Get("kind"), r.URL.Query().Get("name")
	if scope == "" || kind == "" || name == "" {
		httpError(w, http.StatusBadRequest, "scope, kind, and name are required")
		return
	}
	var area *Area
	for _, a := range s.areasSnapshot() {
		if a.Label == scope {
			found := a
			area = &found
			break
		}
	}
	if area == nil {
		httpError(w, http.StatusNotFound, "unknown scope")
		return
	}
	var raw []byte
	var containers []containerRef
	var err error
	switch {
	case area.ViaAgent():
		raw, err = area.Agent.InspectResource(r.Context(), area.AgentScope, kind, name)
		if err == nil && kind == "image" {
			if cs, e := area.Agent.Containers(r.Context(), area.AgentScope); e == nil {
				for _, c := range cs {
					nm := c.ID
					if len(c.Names) > 0 {
						nm = c.Names[0]
					}
					containers = append(containers, containerRef{c.Image, nm})
				}
			}
		}
	case !area.Remote() && (area.Scope.IsSystem() || area.LocalRootless()):
		ins, ok := s.localBackend(*area).(resourceInspector)
		if !ok {
			httpError(w, http.StatusServiceUnavailable, "podman API socket not available")
			return
		}
		raw, err = inspectByKind(ins, r.Context(), kind, name)
		if err == nil && kind == "image" {
			if cs, e := ins.Containers(r.Context()); e == nil {
				for _, c := range cs {
					containers = append(containers, containerRef{c.Image, c.Name()})
				}
			}
		}
	default:
		httpError(w, http.StatusBadRequest, "detailed inspect is not available for this scope")
		return
	}
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	fields, usedBy := buildResourceDetail(kind, name, raw, containers)
	writeJSON(w, http.StatusOK, map[string]any{"fields": fields, "usedBy": usedBy})
}

// resourceMutator is the delete slice of the Podman client, asserted at runtime.
type resourceMutator interface {
	RemoveNetwork(ctx context.Context, name string) error
	RemoveVolume(ctx context.Context, name string) error
	RemoveImage(ctx context.Context, name string) error
}

func (s *Server) handleDeleteResource(w http.ResponseWriter, r *http.Request) {
	scope := r.URL.Query().Get("scope")
	kind := r.URL.Query().Get("kind")
	name := r.URL.Query().Get("name")
	if scope == "" || kind == "" || name == "" {
		httpError(w, http.StatusBadRequest, "scope, kind, and name are required")
		return
	}
	var area *Area
	for _, a := range s.areasSnapshot() {
		if a.Label == scope {
			found := a
			area = &found
			break
		}
	}
	if area == nil {
		httpError(w, http.StatusNotFound, "unknown scope")
		return
	}
	var err error
	switch {
	case area.ViaAgent():
		err = area.Agent.DeleteResource(r.Context(), area.AgentScope, kind, name)
	case !area.Remote() && (area.Scope.IsSystem() || area.LocalRootless()):
		rm, ok := s.localBackend(*area).(resourceMutator)
		if !ok {
			httpError(w, http.StatusServiceUnavailable, "podman API socket not available")
			return
		}
		switch kind {
		case "network":
			err = rm.RemoveNetwork(r.Context(), name)
		case "volume":
			err = rm.RemoveVolume(r.Context(), name)
		case "image":
			err = rm.RemoveImage(r.Context(), name)
		default:
			httpError(w, http.StatusBadRequest, "unknown resource kind")
			return
		}
	default:
		httpError(w, http.StatusBadRequest, "this scope does not support resource deletion")
		return
	}
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.audit(r, "resource.delete", scope, map[string]any{"kind": kind, "name": name})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) appendAgentResources(r *http.Request, area Area, out []resourceJSON, scopeErrors map[string]string) []resourceJSON {
	res, err := area.Agent.Resources(r.Context(), area.AgentScope)
	if err != nil {
		scopeErrors[area.Label] = err.Error()
		return out
	}
	node := areaNodeID(area)
	for _, rr := range res {
		out = append(out, resourceJSON{Kind: rr.Kind, Name: rr.Name, Scope: area.Label, Node: node, Driver: rr.Driver, Detail: rr.Detail, Managed: rr.Managed, Used: rr.Used})
	}
	return out
}

// managedResourceNames returns the podman resource identities a Quadlet unit in
// this area owns, keyed "network:<name>" / "volume:<name>". Quadlet names the
// object after the unit base, prefixed with "systemd-" unless overridden, so
// both forms are recorded. ponytail: name heuristic; misses units that set an
// explicit NetworkName=/VolumeName=.
func (s *Server) managedResourceNames(ctx context.Context, area Area) map[string]bool {
	set := map[string]bool{}
	found, err := discoverArea(ctx, area)
	if err != nil {
		return set
	}
	for _, d := range found {
		var kind string
		switch d.unit.Kind {
		case quadlet.KindNetwork:
			kind = "network"
		case quadlet.KindVolume:
			kind = "volume"
		default:
			continue
		}
		base := strings.TrimSuffix(d.unit.Name, "."+kind)
		set[kind+":"+base] = true
		set[kind+":systemd-"+base] = true
	}
	return set
}
