package server

import (
	"net/http"
	"strconv"

	"github.com/rookerylabs/rookery/internal/appdb"
)

func (s *Server) audit(r *http.Request, action, target string, detail any) {
	if s.users == nil {
		return
	}
	actor := "system"
	if sess, ok := s.session(r); ok && sess.user != "" {
		actor = sess.user
	}
	s.auditAs(actor, action, target, detail)
}

func (s *Server) auditAs(actor, action, target string, detail any) {
	if s.users == nil {
		return
	}
	_ = appdb.AddAuditEvent(s.users.DB(), actor, action, target, detail)
}

func (s *Server) handleAuditEvents(w http.ResponseWriter, r *http.Request) {
	if s.users == nil {
		httpError(w, http.StatusServiceUnavailable, "no app database configured")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	events, err := appdb.ListAuditEvents(s.users.DB(), limit)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": events})
}
