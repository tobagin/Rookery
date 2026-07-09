package server

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"

	"github.com/tobagin/rookery/internal/appdb"
	"github.com/tobagin/rookery/internal/userstore"
)

func randomBearerToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	if s.users == nil {
		httpError(w, http.StatusServiceUnavailable, "no app database configured")
		return
	}
	tokens, err := appdb.ListAPITokens(s.users.DB())
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": tokens})
}

func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	if s.users == nil {
		httpError(w, http.StatusServiceUnavailable, "no app database configured")
		return
	}
	var req struct {
		Name      string `json:"name"`
		Role      string `json:"role"`
		ExpiresAt string `json:"expiresAt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Role == "" {
		req.Role = userstore.RoleViewer
	}
	if req.Role != userstore.RoleAdmin && req.Role != userstore.RoleViewer {
		httpError(w, http.StatusBadRequest, "role must be admin or viewer")
		return
	}
	var exp time.Time
	if req.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, req.ExpiresAt)
		if err != nil {
			httpError(w, http.StatusBadRequest, "expiresAt must be RFC3339")
			return
		}
		exp = t
	}
	value, err := randomBearerToken()
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := appdb.PutAPIToken(s.users.DB(), bearerHash(value), req.Name, req.Role, exp); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "token.create", req.Name, map[string]any{"role": req.Role, "expiresAt": req.ExpiresAt})
	writeJSON(w, http.StatusOK, map[string]any{"token": value, "name": req.Name, "role": req.Role})
}

func (s *Server) handleDeleteToken(w http.ResponseWriter, r *http.Request) {
	if s.users == nil {
		httpError(w, http.StatusServiceUnavailable, "no app database configured")
		return
	}
	name := r.PathValue("name")
	if err := appdb.DeleteAPIToken(s.users.DB(), name); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.audit(r, "token.delete", name, nil)
	writeJSON(w, http.StatusOK, map[string]any{"deleted": name})
}
