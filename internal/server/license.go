package server

import "net/http"

const enterpriseFreeNodeLimit = 3

type LicenseStatus struct {
	Edition             string   `json:"edition"`
	Plan                string   `json:"plan"`
	ManagedNodes        int      `json:"managedNodes"`
	NodeLimit           int      `json:"nodeLimit"`
	NodesRemaining      int      `json:"nodesRemaining"`
	NodesOverLimit      int      `json:"nodesOverLimit"`
	LocalUserLimit      int      `json:"localUserLimit"`
	SSOUserLimit        int      `json:"ssoUserLimit"`
	Nodes               []string `json:"nodes"`
	EnterpriseAvailable bool     `json:"enterpriseAvailable"`
	Enforcement         string   `json:"enforcement"`
	Message             string   `json:"message"`
}

func (s *Server) licenseStatus() LicenseStatus {
	seen := map[string]bool{}
	nodes := []string{}
	for _, area := range s.areas {
		node := "local"
		if area.Remote() {
			node = area.Scope.SSH
		}
		if seen[node] {
			continue
		}
		seen[node] = true
		nodes = append(nodes, node)
	}
	remaining := enterpriseFreeNodeLimit - len(nodes)
	over := 0
	if remaining < 0 {
		over = -remaining
		remaining = 0
	}
	status := LicenseStatus{
		Edition:             "Enterprise Free",
		Plan:                "enterprise_free",
		ManagedNodes:        len(nodes),
		NodeLimit:           enterpriseFreeNodeLimit,
		NodesRemaining:      remaining,
		NodesOverLimit:      over,
		LocalUserLimit:      0,
		SSOUserLimit:        0,
		Nodes:               nodes,
		EnterpriseAvailable: len(nodes) <= enterpriseFreeNodeLimit,
		Enforcement:         "disabled",
		Message:             "Enterprise Free includes unlimited local and SSO users for up to three managed nodes; enforcement is not active in this alpha.",
	}
	if !status.EnterpriseAvailable {
		status.Message = "This instance is above the planned three-node Enterprise Free allowance; users and SSO remain unlimited, and enforcement is not active in this alpha."
	}
	return status
}

func (s *Server) handleLicense(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"license": s.licenseStatus()})
}
