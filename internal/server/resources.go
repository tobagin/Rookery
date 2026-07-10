package server

import (
	"context"
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
}

// handleListResources lists live podman networks and volumes so the typed pages
// show real objects, not just the (usually empty) set of .network/.volume
// Quadlet units. ponytail: Slice 1 covers only the local rootful scope via the
// single s.pod client; rootless-local and remote scopes fill in once the agent
// grows a resources endpoint.
func (s *Server) handleListResources(w http.ResponseWriter, r *http.Request) {
	out := []resourceJSON{}
	scopeErrors := map[string]string{}
	for _, area := range s.areasSnapshot() {
		switch {
		case area.ViaAgent():
			// Remote and rootless-local scopes are served by a rookery-agent,
			// which computes managed itself (it has the podman store + units).
			out = s.appendAgentResources(r, area, out, scopeErrors)
		case !area.Remote() && area.Scope.IsSystem():
			// The local rootful scope is the one the single s.pod client backs.
			out = s.appendLocalResources(r, area, out, scopeErrors)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"resources": out, "scopeErrors": scopeErrors})
}

func (s *Server) appendLocalResources(r *http.Request, area Area, out []resourceJSON, scopeErrors map[string]string) []resourceJSON {
	rp, ok := s.pod.(resourcesAPI)
	if !ok || s.pod == nil {
		return out
	}
	managed := s.managedResourceNames(r.Context(), area)
	node := areaNodeID(area)
	if nets, err := rp.Networks(r.Context()); err != nil {
		scopeErrors[area.Label] = err.Error()
	} else {
		for _, n := range nets {
			detail := ""
			if len(n.Subnets) > 0 {
				detail = n.Subnets[0].Subnet
			}
			out = append(out, resourceJSON{Kind: "network", Name: n.Name, Scope: area.Label, Node: node, Driver: n.Driver, Detail: detail, Managed: managed["network:"+n.Name]})
		}
	}
	if vols, err := rp.Volumes(r.Context()); err != nil {
		scopeErrors[area.Label] = err.Error()
	} else {
		for _, v := range vols {
			out = append(out, resourceJSON{Kind: "volume", Name: v.Name, Scope: area.Label, Node: node, Driver: v.Driver, Detail: v.Mountpoint, Managed: managed["volume:"+v.Name]})
		}
	}
	return out
}

func (s *Server) appendAgentResources(r *http.Request, area Area, out []resourceJSON, scopeErrors map[string]string) []resourceJSON {
	res, err := area.Agent.Resources(r.Context(), area.AgentScope)
	if err != nil {
		scopeErrors[area.Label] = err.Error()
		return out
	}
	node := areaNodeID(area)
	for _, rr := range res {
		out = append(out, resourceJSON{Kind: rr.Kind, Name: rr.Name, Scope: area.Label, Node: node, Driver: rr.Driver, Detail: rr.Detail, Managed: rr.Managed})
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
