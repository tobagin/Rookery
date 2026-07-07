package server

import "net/http"

const enterpriseFreeNodeLimit = 3

type LicenseStatus struct {
	Edition             string   `json:"edition"`
	Plan                string   `json:"plan"`
	ManagedNodes        int      `json:"managedNodes"`
	NodeLimit           int      `json:"nodeLimit"`
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
	status := LicenseStatus{
		Edition:             "Enterprise Free",
		Plan:                "enterprise_free",
		ManagedNodes:        len(nodes),
		NodeLimit:           enterpriseFreeNodeLimit,
		Nodes:               nodes,
		EnterpriseAvailable: len(nodes) <= enterpriseFreeNodeLimit,
		Enforcement:         "disabled",
		Message:             "Full Enterprise feature set is planned to be free for up to three managed nodes; enforcement is not active in this alpha.",
	}
	if !status.EnterpriseAvailable {
		status.Message = "This instance is above the planned three-node Enterprise Free allowance; enforcement is not active in this alpha."
	}
	return status
}

func (s *Server) handleLicense(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"license": s.licenseStatus()})
}
