package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/tobagin/rookery/internal/podman"
	"github.com/tobagin/rookery/internal/quadlet"
)

// secretsAPI is the optional slice of the Podman client the secrets page
// needs; asserted at runtime so test fakes without it keep compiling.
type secretsAPI interface {
	Secrets(ctx context.Context) ([]podman.Secret, error)
	CreateSecret(ctx context.Context, name string, data []byte) error
	RemoveSecret(ctx context.Context, name string) error
}

var secretNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

func (s *Server) secretsClient(w http.ResponseWriter) (secretsAPI, bool) {
	sp, ok := s.pod.(secretsAPI)
	if !ok || s.pod == nil {
		httpError(w, http.StatusServiceUnavailable, "podman API socket not available")
		return nil, false
	}
	return sp, true
}

// secretUsage maps secret name -> "scope/unit" references, from Secret=
// lines in local container units (a secret's value format is
// "name[,type=...,target=...]"; the name is the first comma field).
func (s *Server) secretUsage(ctx context.Context) map[string][]string {
	used := map[string][]string{}
	for _, area := range s.areas {
		if area.Remote() {
			continue // podman secrets are host-local
		}
		found, err := discoverArea(ctx, area)
		if err != nil {
			continue
		}
		for _, d := range found {
			if d.unit.Kind != quadlet.KindContainer || d.data == nil {
				continue
			}
			f, err := quadlet.Parse(d.data)
			if err != nil {
				continue
			}
			for _, v := range f.All("Container", "Secret") {
				name, _, _ := strings.Cut(v, ",")
				if name = strings.TrimSpace(name); name != "" {
					used[name] = append(used[name], area.Label+"/"+d.unit.Name)
				}
			}
		}
	}
	return used
}

func (s *Server) handleListSecrets(w http.ResponseWriter, r *http.Request) {
	sp, ok := s.secretsClient(w)
	if !ok {
		return
	}
	secrets, err := sp.Secrets(r.Context())
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"secrets": secrets,
		"usedBy":  s.secretUsage(r.Context()),
	})
}

func (s *Server) handleCreateSecret(w http.ResponseWriter, r *http.Request) {
	sp, ok := s.secretsClient(w)
	if !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
		Data string `json:"data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	if !secretNameRe.MatchString(req.Name) {
		httpError(w, http.StatusBadRequest, "invalid secret name")
		return
	}
	if req.Data == "" {
		httpError(w, http.StatusBadRequest, "secret value must not be empty")
		return
	}
	if err := sp.CreateSecret(r.Context(), req.Name, []byte(req.Data)); err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"created": req.Name})
}

func (s *Server) handleDeleteSecret(w http.ResponseWriter, r *http.Request) {
	sp, ok := s.secretsClient(w)
	if !ok {
		return
	}
	name := r.PathValue("name")
	if !secretNameRe.MatchString(name) {
		httpError(w, http.StatusBadRequest, "invalid secret name")
		return
	}
	if refs := s.secretUsage(r.Context())[name]; len(refs) > 0 {
		httpError(w, http.StatusConflict,
			fmt.Sprintf("secret %s is referenced by %s — remove the Secret= line first", name, strings.Join(refs, ", ")))
		return
	}
	if err := sp.RemoveSecret(r.Context(), name); err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": name})
}
