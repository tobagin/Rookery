package server

import (
	"encoding/json"
	"net/http"

	"github.com/tobagin/rookery/internal/userstore"
)

// usersAvailable gates account management: it needs a store on disk, and
// non-GET is already admin-only via ServeHTTP.
func (s *Server) usersAvailable(w http.ResponseWriter) bool {
	if s.users == nil {
		httpError(w, http.StatusServiceUnavailable, "no user store configured (running with a legacy -password only)")
		return false
	}
	return true
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	if !s.usersAvailable(w) {
		return
	}
	sess, _ := s.session(r)
	me := ""
	if sess != nil {
		me = sess.user
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": s.users.List(), "me": me})
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	if !s.usersAvailable(w) {
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Role == "" {
		req.Role = userstore.RoleViewer
	}
	if err := s.users.Create(req.Username, req.Password, req.Role); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"created": req.Username, "role": req.Role})
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	if !s.usersAvailable(w) {
		return
	}
	if err := s.users.Delete(r.PathValue("name")); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": r.PathValue("name")})
}

func (s *Server) handleSetUserPassword(w http.ResponseWriter, r *http.Request) {
	if !s.usersAvailable(w) {
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := s.users.SetPassword(r.PathValue("name"), req.Password); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": r.PathValue("name")})
}
