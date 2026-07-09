package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tobagin/rookery/internal/appdb"
	"github.com/tobagin/rookery/internal/oidc"
	"github.com/tobagin/rookery/internal/userstore"
)

const (
	sessionCookie     = "rookery_session"
	shareCookie       = "rookery_share"
	defaultSessionTTL = 24 * time.Hour
	shareTTL          = 7 * 24 * time.Hour
)

// session is one logged-in browser.
type session struct {
	user   string
	role   string
	expiry time.Time
}

// sessions is an in-memory token store — deliberate: restarting Rookery
// logs everyone out and leaves nothing on disk. Expiry is sliding: every
// authenticated request pushes it out by the TTL, so "session timeout"
// means idle timeout.
type sessions struct {
	mu     sync.Mutex
	tokens map[string]*session
	ttl    time.Duration
	db     *sql.DB
}

type oidcState struct {
	nonce  string
	expiry time.Time
}

type oidcStates struct {
	mu     sync.Mutex
	states map[string]oidcState
}

func newOIDCStates() *oidcStates {
	return &oidcStates{states: map[string]oidcState{}}
}

func (s *oidcStates) create(nonce string) (string, error) {
	state, err := oidc.RandomToken()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, v := range s.states {
		if now.After(v.expiry) {
			delete(s.states, k)
		}
	}
	s.states[state] = oidcState{nonce: nonce, expiry: now.Add(10 * time.Minute)}
	return state, nil
}

func (s *oidcStates) take(state string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.states[state]
	if !ok {
		return "", false
	}
	delete(s.states, state)
	if time.Now().After(v.expiry) {
		return "", false
	}
	return v.nonce, true
}

func newSessions(ttl time.Duration) *sessions {
	if ttl <= 0 {
		ttl = defaultSessionTTL
	}
	return &sessions{tokens: map[string]*session{}, ttl: ttl}
}

func (s *sessions) useDB(db *sql.DB) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.db = db
	_ = appdb.DeleteExpiredSessions(db, time.Now())
}

func sessionHash(token string) string {
	sum := sha256.Sum256([]byte("rookery-session:" + token))
	return hex.EncodeToString(sum[:])
}

func (s *sessions) create(user, role string) string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		panic(err) // crypto/rand failure is not a recoverable state
	}
	token := hex.EncodeToString(buf)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if s.db != nil {
		_ = appdb.DeleteExpiredSessions(s.db, now)
		_ = appdb.PutSession(s.db, sessionHash(token), user, role, now.Add(s.ttl), now)
		return token
	}
	for t, sess := range s.tokens { // lazy cleanup
		if now.After(sess.expiry) {
			delete(s.tokens, t)
		}
	}
	s.tokens[token] = &session{user: user, role: role, expiry: now.Add(s.ttl)}
	return token
}

// get returns the live session for token and slides its expiry.
func (s *sessions) get(token string) (*session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if s.db != nil {
		row, ok, err := appdb.GetSession(s.db, sessionHash(token))
		if err != nil || !ok {
			return nil, false
		}
		if now.After(row.ExpiresAt) {
			_ = appdb.DeleteSession(s.db, row.IDHash)
			return nil, false
		}
		expiry := now.Add(s.ttl)
		_ = appdb.PutSession(s.db, row.IDHash, row.Username, row.Role, expiry, now)
		return &session{user: row.Username, role: row.Role, expiry: expiry}, true
	}
	sess, ok := s.tokens[token]
	if !ok {
		return nil, false
	}
	if now.After(sess.expiry) {
		delete(s.tokens, token)
		return nil, false
	}
	sess.expiry = now.Add(s.ttl)
	return sess, true
}

func (s *sessions) revoke(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db != nil {
		_ = appdb.DeleteSession(s.db, sessionHash(token))
	}
	delete(s.tokens, token)
}

func (s *sessions) revokeUser(user, exceptToken string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	exceptHash := ""
	if exceptToken != "" {
		exceptHash = sessionHash(exceptToken)
	}
	if s.db != nil {
		_ = appdb.DeleteUserSessions(s.db, user, exceptHash)
	}
	for token, sess := range s.tokens {
		if sess.user == user && token != exceptToken {
			delete(s.tokens, token)
		}
	}
}

/* ---------- access decisions ---------- */

// authConfigured reports whether any credential exists: a legacy -password
// or at least one account in the user store.
func (s *Server) authConfigured() bool {
	return s.oidc != nil || (!s.noPassword && (s.password != "" || (s.users != nil && !s.users.Empty())))
}

// setupNeeded reports whether the first-run wizard should run: a user store
// is wired up, has no accounts, and no legacy password covers for it.
func (s *Server) setupNeeded() bool {
	return !s.noPassword && s.users != nil && s.users.Empty() && s.password == "" && s.oidc == nil
}

// authRequired reports whether the request must carry a valid session.
// Static assets and the auth/setup endpoints stay reachable so the login
// and wizard pages can render.
func (s *Server) authRequired(r *http.Request) bool {
	if !s.authConfigured() {
		return false
	}
	if !strings.HasPrefix(r.URL.Path, "/api/") {
		return false
	}
	switch r.URL.Path {
	case "/api/login", "/api/auth", "/api/setup", "/api/onboarding", "/api/oidc/login", "/api/oidc/callback":
		return false
	}
	return true
}

// session returns the request's live session, if any.
func (s *Server) session(r *http.Request) (*session, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil, false
	}
	return s.sess.get(c.Value)
}

func (s *Server) authenticated(r *http.Request) bool {
	_, ok := s.session(r)
	return ok
}

// readOnlyRequest reports whether a GET-only principal (viewer role or
// share link) may perform this request.
func readOnlyAllowed(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	// No secrets metadata and no account list for read-only principals.
	return !strings.HasPrefix(r.URL.Path, "/api/secrets") &&
		!strings.HasPrefix(r.URL.Path, "/api/users") &&
		!strings.HasPrefix(r.URL.Path, "/api/settings") &&
		!strings.HasPrefix(r.URL.Path, "/api/audit") &&
		!strings.HasPrefix(r.URL.Path, "/api/backup")
}

/* ---------- read-only share links ----------
   A share token is expiry.HMAC(expiry), keyed off the credential material:
   it survives restarts with no state of its own, and changing any password
   revokes every outstanding link. Enforcement lives in ServeHTTP — the UI
   hiding buttons is cosmetics, this is the boundary. */

func (s *Server) shareKey() []byte {
	material := "rookery-share:" + s.password
	if s.users != nil {
		material += ":" + s.users.Fingerprint()
	}
	h := sha256.Sum256([]byte(material))
	return h[:]
}

func (s *Server) mintShare(expiry time.Time) string {
	msg := strconv.FormatInt(expiry.Unix(), 10)
	mac := hmac.New(sha256.New, s.shareKey())
	mac.Write([]byte("share:" + msg))
	return msg + "." + hex.EncodeToString(mac.Sum(nil))
}

func (s *Server) shareValid(token string) bool {
	expStr, sig, ok := strings.Cut(token, ".")
	if !ok {
		return false
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return false
	}
	mac := hmac.New(sha256.New, s.shareKey())
	mac.Write([]byte("share:" + expStr))
	want, err := hex.DecodeString(sig)
	return err == nil && hmac.Equal(mac.Sum(nil), want)
}

// shareAccess reports whether the request carries a valid share token
// (query param on first visit, cookie afterwards).
func (s *Server) shareAccess(r *http.Request) bool {
	if !s.authConfigured() {
		return false
	}
	if tok := r.URL.Query().Get("share"); tok != "" && s.shareValid(tok) {
		return true
	}
	c, err := r.Cookie(shareCookie)
	return err == nil && s.shareValid(c.Value)
}

func (s *Server) handleShare(w http.ResponseWriter, r *http.Request) {
	if !s.authConfigured() {
		httpError(w, http.StatusBadRequest, "no credentials are set — this instance is already open")
		return
	}
	expiry := time.Now().Add(shareTTL)
	token := s.mintShare(expiry)
	s.audit(r, "share.create", "dashboard", map[string]any{"expires": expiry.Unix()})
	writeJSON(w, http.StatusOK, map[string]any{
		"token":   token,
		"expires": expiry.Unix(),
	})
}

/* ---------- endpoints ---------- */

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	sess, loggedIn := s.session(r)
	share := s.authConfigured() && !loggedIn && s.shareAccess(r)
	onboarding := false
	email := ""
	if loggedIn && s.users != nil {
		if u, ok := s.users.Get(sess.user); ok {
			onboarding = u.MustChangePassword || u.MustSetEmail
			email = u.Email
		}
	}
	resp := map[string]any{
		"required":      s.authConfigured(),
		"authenticated": !s.authConfigured() || loggedIn || share,
		"readOnly":      share || (loggedIn && sess.role == userstore.RoleViewer),
		"setupNeeded":   s.setupNeeded(),
		"passwordLogin": !s.noPassword,
		"onboarding":    onboarding,
	}
	if s.oidc != nil {
		resp["oidc"] = map[string]any{"enabled": true, "name": s.oidc.ProviderName()}
	}
	if loggedIn {
		resp["username"] = sess.user
		resp["role"] = sess.role
		resp["email"] = email
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleSetup is the first-run wizard's endpoint: it creates the initial
// admin account and logs the browser straight in. It only works while no
// credentials exist at all, so it can never be used to add a backdoor.
func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusOK, map[string]any{"needed": s.setupNeeded()})
		return
	}
	if !s.setupNeeded() {
		httpError(w, http.StatusForbidden, "setup has already been completed")
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := s.users.Create(req.Username, req.Password, userstore.RoleAdmin); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.setSessionCookie(w, r, s.sess.create(req.Username, userstore.RoleAdmin))
	s.auditAs(req.Username, "setup.complete", req.Username, nil)
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": true, "username": req.Username, "role": userstore.RoleAdmin})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if s.noPassword {
		httpError(w, http.StatusForbidden, "username/password login is disabled")
		return
	}
	if !s.authConfigured() {
		writeJSON(w, http.StatusOK, map[string]any{"authenticated": true})
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	user, role, ok := "", "", false
	if s.users != nil && !s.users.Empty() {
		if u, valid := s.users.VerifyUser(req.Username, req.Password); valid {
			user, role, ok = u.Name, u.Role, true
		}
	}
	if !ok && s.password != "" {
		// Legacy single-password mode (ROOKERY_PASSWORD): any/empty
		// username, compared via digests so length doesn't leak timing.
		got := sha256.Sum256([]byte(req.Password))
		want := sha256.Sum256([]byte(s.password))
		if subtle.ConstantTimeCompare(got[:], want[:]) == 1 {
			user, role, ok = "admin", userstore.RoleAdmin, true
		}
	}
	if !ok {
		time.Sleep(300 * time.Millisecond) // slow down brute force
		httpError(w, http.StatusUnauthorized, "wrong username or password")
		return
	}
	s.setSessionCookie(w, r, s.sess.create(user, role))
	onboarding := s.users != nil && s.users.NeedsOnboarding(user)
	s.auditAs(user, "auth.login", user, map[string]any{"role": role, "onboarding": onboarding})
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": true, "username": user, "role": role, "onboarding": onboarding})
}

func (s *Server) handleOnboarding(w http.ResponseWriter, r *http.Request) {
	sess, loggedIn := s.session(r)
	if !loggedIn {
		httpError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if s.users == nil {
		httpError(w, http.StatusServiceUnavailable, "no local user store configured")
		return
	}
	if !s.users.NeedsOnboarding(sess.user) {
		writeJSON(w, http.StatusOK, map[string]any{"updated": sess.user})
		return
	}
	var req struct {
		Email           string `json:"email"`
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := s.users.CompleteOnboarding(sess.user, req.Email, req.CurrentPassword, req.NewPassword); err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.audit(r, "onboarding.complete", sess.user, map[string]any{"emailSet": req.Email != ""})
	writeJSON(w, http.StatusOK, map[string]any{"updated": sess.user})
}

func (s *Server) handleOIDCLogin(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		http.NotFound(w, r)
		return
	}
	nonce, err := oidc.RandomToken()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "could not create OIDC nonce")
		return
	}
	state, err := s.oidcStates.create(nonce)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "could not create OIDC state")
		return
	}
	u, err := s.oidc.AuthCodeURL(r.Context(), s.oidcRedirectURL(r), state, nonce)
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	http.Redirect(w, r, u, http.StatusFound)
}

func (s *Server) handleOIDCCallback(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		http.NotFound(w, r)
		return
	}
	if msg := r.URL.Query().Get("error"); msg != "" {
		httpError(w, http.StatusUnauthorized, "OIDC login failed: "+msg)
		return
	}
	nonce, ok := s.oidcStates.take(r.URL.Query().Get("state"))
	if !ok {
		httpError(w, http.StatusBadRequest, "OIDC state is missing or expired")
		return
	}
	claims, err := s.oidc.Exchange(r.Context(), r.URL.Query().Get("code"), s.oidcRedirectURL(r), nonce)
	if err != nil {
		httpError(w, http.StatusUnauthorized, err.Error())
		return
	}
	user, role := s.resolveOIDCIdentity(claims)
	s.setSessionCookie(w, r, s.sess.create(user, role))
	s.auditAs(user, "auth.oidc.login", user, map[string]any{"role": role, "provider": s.oidc.ProviderName()})
	http.Redirect(w, r, "/", http.StatusFound)
}

func (s *Server) resolveOIDCIdentity(claims oidc.Claims) (string, string) {
	if s.users != nil {
		emailTrusted := claims.EmailVerified == nil || *claims.EmailVerified
		if emailTrusted {
			if u, ok := s.users.GetByEmail(claims.Email); ok {
				return u.Name, u.Role
			}
		}
	}
	return claims.Username, claims.Role
}

func (s *Server) oidcRedirectURL(r *http.Request) string {
	if s.oidc != nil && s.oidc.RedirectURL() != "" {
		return s.oidc.RedirectURL()
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if proto := r.Header.Get("X-Forwarded-Proto"); proto == "http" || proto == "https" {
		scheme = proto
	}
	host := r.Host
	if forwarded := r.Header.Get("X-Forwarded-Host"); forwarded != "" {
		host = forwarded
	}
	return scheme + "://" + host + "/api/oidc/callback"
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   r.TLS != nil,
		MaxAge:   int(s.sess.ttl.Seconds()),
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if sess, ok := s.session(r); ok {
		s.auditAs(sess.user, "auth.logout", sess.user, nil)
	}
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.sess.revoke(c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, MaxAge: -1,
	})
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": false})
}
