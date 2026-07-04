package server

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	sessionCookie = "rookery_session"
	sessionTTL    = 7 * 24 * time.Hour
)

// sessions is an in-memory token store — deliberate: restarting Rookery
// logs everyone out and leaves nothing on disk.
type sessions struct {
	mu     sync.Mutex
	tokens map[string]time.Time // token -> expiry
}

func newSessions() *sessions {
	return &sessions{tokens: map[string]time.Time{}}
}

func (s *sessions) create() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		panic(err) // crypto/rand failure is not a recoverable state
	}
	token := hex.EncodeToString(buf)
	s.mu.Lock()
	defer s.mu.Unlock()
	for t, exp := range s.tokens { // lazy cleanup
		if time.Now().After(exp) {
			delete(s.tokens, t)
		}
	}
	s.tokens[token] = time.Now().Add(sessionTTL)
	return token
}

func (s *sessions) valid(token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	exp, ok := s.tokens[token]
	return ok && time.Now().Before(exp)
}

func (s *sessions) revoke(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tokens, token)
}

// authRequired reports whether the request must carry a valid session.
// Static assets and the auth endpoints themselves stay reachable so the
// login page can render.
func (s *Server) authRequired(r *http.Request) bool {
	if s.password == "" {
		return false
	}
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		return false
	}
	switch r.URL.Path {
	case "/api/login", "/api/auth":
		return false
	}
	return true
}

func (s *Server) authenticated(r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	return err == nil && s.sess.valid(c.Value)
}

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"required":      s.password != "",
		"authenticated": s.password == "" || s.authenticated(r),
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.password == "" {
		writeJSON(w, http.StatusOK, map[string]any{"authenticated": true})
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	// Compare digests so length differences don't leak timing.
	got := sha256.Sum256([]byte(req.Password))
	want := sha256.Sum256([]byte(s.password))
	if subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
		time.Sleep(300 * time.Millisecond) // slow down brute force
		httpError(w, http.StatusUnauthorized, "wrong password")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    s.sess.create(),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
		MaxAge:   int(sessionTTL.Seconds()),
	})
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.sess.revoke(c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, MaxAge: -1,
	})
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": false})
}
