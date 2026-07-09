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
		Email    string `json:"email"`
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
	if err := s.users.CreateWithProfile(userstore.User{Name: req.Username, Email: req.Email, Role: req.Role}, req.Password); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "user.create", req.Username, map[string]any{"role": req.Role, "emailSet": req.Email != ""})
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
	s.audit(r, "user.delete", r.PathValue("name"), nil)
	writeJSON(w, http.StatusOK, map[string]any{"deleted": r.PathValue("name")})
}

func (s *Server) handlePatchUser(w http.ResponseWriter, r *http.Request) {
	if !s.usersAvailable(w) {
		return
	}
	var req struct {
		Email *string `json:"email"`
		Role  string  `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	current, ok := s.users.Get(r.PathValue("name"))
	if !ok {
		httpError(w, http.StatusBadRequest, "no such user "+r.PathValue("name"))
		return
	}
	if req.Role == "" {
		req.Role = current.Role
	}
	email := current.Email
	if req.Email != nil {
		email = *req.Email
	}
	if err := s.users.Update(r.PathValue("name"), email, req.Role); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "user.update", r.PathValue("name"), map[string]any{"role": req.Role, "emailSet": email != ""})
	writeJSON(w, http.StatusOK, map[string]any{"updated": r.PathValue("name")})
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
	s.audit(r, "user.password", r.PathValue("name"), nil)
	writeJSON(w, http.StatusOK, map[string]any{"updated": r.PathValue("name")})
}

func (s *Server) handleChangeMyPassword(w http.ResponseWriter, r *http.Request) {
	if !s.usersAvailable(w) {
		return
	}
	sess, ok := s.session(r)
	if !ok {
		httpError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var req struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if _, valid := s.users.VerifyUser(sess.user, req.CurrentPassword); !valid {
		httpError(w, http.StatusUnauthorized, "current password is incorrect")
		return
	}
	if err := s.users.SetPassword(sess.user, req.NewPassword); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	currentToken := ""
	if c, err := r.Cookie(sessionCookie); err == nil {
		currentToken = c.Value
	}
	s.sess.revokeUser(sess.user, currentToken)
	s.audit(r, "user.password.self", sess.user, nil)
	writeJSON(w, http.StatusOK, map[string]any{"updated": sess.user})
}
