package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/tobagin/rookery/internal/appdb"
	"github.com/tobagin/rookery/internal/quadlet"
)

type PolicyFinding struct {
	Key          string `json:"key"`
	Policy       string `json:"policy"`
	Severity     string `json:"severity"`
	Node         string `json:"node"`
	Scope        string `json:"scope"`
	Unit         string `json:"unit"`
	Message      string `json:"message"`
	Waived       bool   `json:"waived"`
	WaiverReason string `json:"waiverReason,omitempty"`
	WaivedBy     string `json:"waivedBy,omitempty"`
}

func (s *Server) policyFindings(r *http.Request) []PolicyFinding {
	var findings []PolicyFinding
	for _, area := range s.areas {
		node := "local"
		if area.Remote() {
			node = area.Scope.SSH
		}
		found, err := discoverArea(r.Context(), area)
		if err != nil {
			findings = append(findings, PolicyFinding{
				Policy: "scope-readable", Severity: "warn", Node: node, Scope: area.Label,
				Message: "Cannot inspect scope: " + err.Error(),
			})
			continue
		}
		for _, d := range found {
			f, err := quadlet.Parse(d.data)
			if err != nil {
				findings = append(findings, PolicyFinding{
					Policy: "unit-parse", Severity: "warn", Node: node, Scope: area.Label, Unit: d.unit.Name,
					Message: "Cannot parse unit for policy checks: " + err.Error(),
				})
				continue
			}
			findings = append(findings, checkImagePolicy(node, area.Label, d.unit.Name, f)...)
			findings = append(findings, checkPrivilegePolicy(node, area.Label, d.unit.Name, f)...)
			for _, hint := range quadlet.VolumeHints(string(d.data)) {
				findings = append(findings, PolicyFinding{
					Policy: "selinux-bind-mount", Severity: "warn", Node: node, Scope: area.Label, Unit: d.unit.Name,
					Message: hint,
				})
			}
		}
	}
	for i := range findings {
		findings[i].Key = policyFindingKey(findings[i])
	}
	if s.users == nil {
		return findings
	}
	waivers, err := appdb.GetPolicyWaivers(s.users.DB())
	if err != nil {
		return findings
	}
	for i := range findings {
		if w, ok := waivers[findings[i].Key]; ok {
			findings[i].Waived = true
			findings[i].WaiverReason = w.Reason
			findings[i].WaivedBy = w.CreatedBy
		}
	}
	return findings
}

func checkImagePolicy(node, scope, unit string, f *quadlet.File) []PolicyFinding {
	var out []PolicyFinding
	for _, section := range []string{"Container", "Image", "Build"} {
		for _, image := range f.All(section, "Image") {
			if image == "" || strings.Contains(image, "@sha256:") {
				continue
			}
			last := image[strings.LastIndex(image, "/")+1:]
			if !strings.Contains(last, ":") {
				out = append(out, PolicyFinding{Policy: "unpinned-image", Severity: "info", Node: node, Scope: scope, Unit: unit, Message: "Image " + image + " has no explicit tag or digest"})
				continue
			}
			if strings.HasSuffix(last, ":latest") {
				out = append(out, PolicyFinding{Policy: "latest-tag", Severity: "warn", Node: node, Scope: scope, Unit: unit, Message: "Image " + image + " uses the mutable latest tag"})
			}
		}
	}
	return out
}

func checkPrivilegePolicy(node, scope, unit string, f *quadlet.File) []PolicyFinding {
	var out []PolicyFinding
	for _, section := range []string{"Container", "Pod"} {
		if v, ok := f.Get(section, "Privileged"); ok && strings.EqualFold(v, "true") {
			out = append(out, PolicyFinding{Policy: "privileged-container", Severity: "critical", Node: node, Scope: scope, Unit: unit, Message: "Privileged=true gives the workload broad host access"})
		}
		for _, args := range f.All(section, "PodmanArgs") {
			if strings.Contains(args, "--privileged") {
				out = append(out, PolicyFinding{Policy: "privileged-container", Severity: "critical", Node: node, Scope: scope, Unit: unit, Message: "PodmanArgs includes --privileged"})
			}
		}
	}
	return out
}

func (s *Server) handlePolicies(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"findings": s.policyFindings(r)})
}

func (s *Server) handleWaivePolicy(w http.ResponseWriter, r *http.Request) {
	if s.users == nil {
		httpError(w, http.StatusServiceUnavailable, "no app database configured")
		return
	}
	var req struct {
		Key    string `json:"key"`
		Reason string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Key == "" {
		httpError(w, http.StatusBadRequest, "missing policy finding key")
		return
	}
	found := false
	for _, finding := range s.policyFindings(r) {
		if finding.Key == req.Key {
			found = true
			break
		}
	}
	if !found {
		httpError(w, http.StatusNotFound, "policy finding not found")
		return
	}
	actor := "system"
	if sess, ok := s.session(r); ok {
		actor = sess.user
	}
	if err := appdb.PutPolicyWaiver(s.users.DB(), req.Key, strings.TrimSpace(req.Reason), actor); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.auditAs(actor, "policy.waive", req.Key, map[string]any{"reasonSet": strings.TrimSpace(req.Reason) != ""})
	writeJSON(w, http.StatusOK, map[string]any{"updated": true, "findings": s.policyFindings(r)})
}

func (s *Server) handleDeletePolicyWaiver(w http.ResponseWriter, r *http.Request) {
	if s.users == nil {
		httpError(w, http.StatusServiceUnavailable, "no app database configured")
		return
	}
	key := r.PathValue("key")
	if key == "" {
		httpError(w, http.StatusBadRequest, "missing policy finding key")
		return
	}
	if err := appdb.DeletePolicyWaiver(s.users.DB(), key); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "policy.unwaive", key, nil)
	writeJSON(w, http.StatusOK, map[string]any{"updated": true, "findings": s.policyFindings(r)})
}

func policyFindingKey(f PolicyFinding) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{f.Policy, f.Node, f.Scope, f.Unit}, "\x00")))
	return hex.EncodeToString(sum[:])
}
