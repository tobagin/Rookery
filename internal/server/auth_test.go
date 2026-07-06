package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tobagin/rookery/internal/oidc"
	"github.com/tobagin/rookery/internal/systemd"
	"github.com/tobagin/rookery/internal/userstore"
)

func timeNowPlusShareTTL() time.Time { return time.Now().Add(shareTTL) }
func timeNowMinusHour() time.Time    { return time.Now().Add(-time.Hour) }

func newAuthServer(t *testing.T) *Server {
	t.Helper()
	return New(Options{
		Areas:    []Area{{Label: "system", Scope: systemd.Scope{}, Dirs: []string{t.TempDir()}}},
		Systemd:  &fakeSystemd{},
		Validate: okValidator,
		Password: "hunter2",
	})
}

func TestAuthBlocksAPI(t *testing.T) {
	srv := newAuthServer(t)
	rec, _ := doJSON(t, srv, "GET", "/api/units", "")
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated /api/units: status %d, want 401", rec.Code)
	}
	// Static UI and auth endpoints must stay reachable for the login page.
	rec, _ = doJSON(t, srv, "GET", "/", "")
	if rec.Code != http.StatusOK {
		t.Errorf("GET /: status %d, want 200", rec.Code)
	}
	rec, body := doJSON(t, srv, "GET", "/api/auth", "")
	if rec.Code != http.StatusOK || body["required"] != true || body["authenticated"] != false {
		t.Errorf("GET /api/auth: %d %v", rec.Code, body)
	}
}

func TestLoginFlow(t *testing.T) {
	srv := newAuthServer(t)
	rec, _ := doJSON(t, srv, "POST", "/api/login", `{"password":"wrong"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: status %d, want 401", rec.Code)
	}
	rec, _ = doJSON(t, srv, "POST", "/api/login", `{"password":"hunter2"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("login: status %d", rec.Code)
	}
	var cookie string
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			cookie = c.Value
			if !c.HttpOnly {
				t.Error("session cookie must be HttpOnly")
			}
		}
	}
	if cookie == "" {
		t.Fatal("no session cookie set on login")
	}

	req := httptest.NewRequest("GET", "/api/units", strings.NewReader(""))
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	rec2 := httptest.NewRecorder()
	srv.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusOK {
		t.Errorf("authenticated /api/units: status %d, want 200", rec2.Code)
	}

	// Logout revokes the token server-side.
	req = httptest.NewRequest("POST", "/api/logout", strings.NewReader(""))
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	srv.ServeHTTP(httptest.NewRecorder(), req)

	req = httptest.NewRequest("GET", "/api/units", strings.NewReader(""))
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	rec3 := httptest.NewRecorder()
	srv.ServeHTTP(rec3, req)
	if rec3.Code != http.StatusUnauthorized {
		t.Errorf("revoked token still accepted: status %d, want 401", rec3.Code)
	}
}

func TestShareLinks(t *testing.T) {
	srv := newAuthServer(t)

	// Minting needs an admin session.
	rec, _ := doJSON(t, srv, "POST", "/api/share", "{}")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated mint: status %d, want 401", rec.Code)
	}
	token := srv.mintShare(timeNowPlusShareTTL())

	get := func(path, tok string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, strings.NewReader(""))
		req.AddCookie(&http.Cookie{Name: shareCookie, Value: tok})
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		return rec
	}

	if rec := get("/api/units", token); rec.Code != http.StatusOK {
		t.Errorf("share GET /api/units: status %d, want 200", rec.Code)
	}

	// Writes are forbidden, not just hidden.
	req := httptest.NewRequest("POST", "/api/units/system/x.container/action", strings.NewReader(`{"action":"stop"}`))
	req.AddCookie(&http.Cookie{Name: shareCookie, Value: token})
	rec2 := httptest.NewRecorder()
	srv.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusForbidden {
		t.Errorf("share POST: status %d, want 403", rec2.Code)
	}

	// /api/auth reports read-only so the UI can adapt.
	req = httptest.NewRequest("GET", "/api/auth", strings.NewReader(""))
	req.AddCookie(&http.Cookie{Name: shareCookie, Value: token})
	rec3 := httptest.NewRecorder()
	srv.ServeHTTP(rec3, req)
	if !strings.Contains(rec3.Body.String(), `"readOnly":true`) {
		t.Errorf("auth status = %s", rec3.Body.String())
	}

	// Tampered and expired tokens are rejected.
	if rec := get("/api/units", token+"ff"); rec.Code != http.StatusUnauthorized {
		t.Errorf("tampered token: status %d, want 401", rec.Code)
	}
	expired := srv.mintShare(timeNowMinusHour())
	if rec := get("/api/units", expired); rec.Code != http.StatusUnauthorized {
		t.Errorf("expired token: status %d, want 401", rec.Code)
	}

	// ?share= on the first visit sets the cookie.
	req = httptest.NewRequest("GET", "/?share="+token, strings.NewReader(""))
	rec4 := httptest.NewRecorder()
	srv.ServeHTTP(rec4, req)
	found := false
	for _, c := range rec4.Result().Cookies() {
		if c.Name == shareCookie && c.Value == token {
			found = true
		}
	}
	if !found {
		t.Error("visiting /?share= did not set the share cookie")
	}
}

func newUsersServer(t *testing.T) (*Server, *userstore.Store) {
	t.Helper()
	store, err := userstore.Open(filepath.Join(t.TempDir(), "users.json"))
	if err != nil {
		t.Fatal(err)
	}
	srv := New(Options{
		Areas:    []Area{{Label: "system", Scope: systemd.Scope{}, Dirs: []string{t.TempDir()}}},
		Systemd:  &fakeSystemd{},
		Validate: okValidator,
		Users:    store,
	})
	return srv, store
}

func loginCookie(t *testing.T, srv *Server, user, pass string) string {
	t.Helper()
	rec, _ := doJSON(t, srv, "POST", "/api/login", fmt.Sprintf(`{"username":%q,"password":%q}`, user, pass))
	if rec.Code != http.StatusOK {
		t.Fatalf("login %s: %d %s", user, rec.Code, rec.Body.String())
	}
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			return c.Value
		}
	}
	t.Fatal("no session cookie on login")
	return ""
}

func doAs(t *testing.T, srv *Server, cookie, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	return rec
}

func TestSetupWizardFlow(t *testing.T) {
	srv, _ := newUsersServer(t)

	// Fresh instance: open, and asking for setup.
	rec, body := doJSON(t, srv, "GET", "/api/auth", "")
	if rec.Code != http.StatusOK || body["setupNeeded"] != true || body["required"] != false {
		t.Fatalf("pre-setup auth = %v", body)
	}

	rec, _ = doJSON(t, srv, "POST", "/api/setup", `{"username":"tobagin","password":"longpassword"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("setup: %d %s", rec.Code, rec.Body.String())
	}
	var setupCookie string
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			setupCookie = c.Value
		}
	}
	if setupCookie == "" {
		t.Fatal("setup did not log the browser in")
	}

	// Auth is now required; the setup session works; setup is one-shot.
	if rec, _ := doJSON(t, srv, "GET", "/api/units", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("post-setup anonymous access: %d, want 401", rec.Code)
	}
	if rec := doAs(t, srv, setupCookie, "GET", "/api/units", ""); rec.Code != http.StatusOK {
		t.Errorf("setup session: %d, want 200", rec.Code)
	}
	if rec, _ := doJSON(t, srv, "POST", "/api/setup", `{"username":"evil","password":"gimmebackdoor"}`); rec.Code != http.StatusForbidden {
		t.Errorf("second setup: %d, want 403", rec.Code)
	}

	// Normal login works afterwards.
	loginCookie(t, srv, "tobagin", "longpassword")
}

func TestOnboardingBlocksAPIsUntilProfileUpdated(t *testing.T) {
	srv, store := newUsersServer(t)
	if err := store.CreateWithProfile(userstore.User{
		Name:               "admin",
		Email:              "admin@example.com",
		Role:               userstore.RoleAdmin,
		MustChangePassword: true,
		MustSetEmail:       true,
	}, "temporarypass"); err != nil {
		t.Fatal(err)
	}
	cookie := loginCookie(t, srv, "admin@example.com", "temporarypass")
	if rec := doAs(t, srv, cookie, "GET", "/api/units", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("pre-onboarding units: %d, want 403", rec.Code)
	}
	rec := doAs(t, srv, cookie, "GET", "/api/auth", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"onboarding":true`) {
		t.Fatalf("auth onboarding status: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doAs(t, srv, cookie, "POST", "/api/onboarding",
		`{"email":"owner@example.test","currentPassword":"wrong","newPassword":"newadminpass"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("wrong current password: %d, want 400", rec.Code)
	}
	if rec := doAs(t, srv, cookie, "POST", "/api/onboarding",
		`{"email":"owner@example.test","currentPassword":"temporarypass","newPassword":"newadminpass"}`); rec.Code != http.StatusOK {
		t.Fatalf("complete onboarding: %d %s", rec.Code, rec.Body.String())
	}
	if rec := doAs(t, srv, cookie, "GET", "/api/units", ""); rec.Code != http.StatusOK {
		t.Fatalf("post-onboarding units: %d, want 200", rec.Code)
	}
	loginCookie(t, srv, "owner@example.test", "newadminpass")
}

func TestViewerRoleIsReadOnly(t *testing.T) {
	srv, store := newUsersServer(t)
	if err := store.Create("boss", "adminpass123", userstore.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if err := store.Create("couch", "viewerpass1", userstore.RoleViewer); err != nil {
		t.Fatal(err)
	}

	viewer := loginCookie(t, srv, "couch", "viewerpass1")
	if rec := doAs(t, srv, viewer, "GET", "/api/units", ""); rec.Code != http.StatusOK {
		t.Errorf("viewer GET units: %d", rec.Code)
	}
	for _, tc := range []struct{ method, path string }{
		{"POST", "/api/units/system/x.container/action"},
		{"GET", "/api/secrets"},
		{"GET", "/api/users"},
		{"POST", "/api/share"},
	} {
		if rec := doAs(t, srv, viewer, tc.method, tc.path, "{}"); rec.Code != http.StatusForbidden {
			t.Errorf("viewer %s %s: %d, want 403", tc.method, tc.path, rec.Code)
		}
	}

	// Admin manages accounts; the last admin is protected.
	admin := loginCookie(t, srv, "boss", "adminpass123")
	if rec := doAs(t, srv, admin, "GET", "/api/users", ""); rec.Code != http.StatusOK {
		t.Errorf("admin list users: %d", rec.Code)
	}
	if rec := doAs(t, srv, admin, "DELETE", "/api/users/boss", ""); rec.Code != http.StatusBadRequest {
		t.Errorf("delete last admin: %d, want 400", rec.Code)
	}
	if rec := doAs(t, srv, admin, "DELETE", "/api/users/couch", ""); rec.Code != http.StatusOK {
		t.Errorf("delete viewer: %d", rec.Code)
	}

	// Deleting the account changed the store fingerprint: the viewer's old
	// session still lives (in-memory), but share links minted before are
	// dead. Also verify password change revokes shares.
	tokenBefore := srv.mintShare(timeNowPlusShareTTL())
	if err := store.SetPassword("boss", "newadminpass1"); err != nil {
		t.Fatal(err)
	}
	if srv.shareValid(tokenBefore) {
		t.Error("share token survived a password change")
	}
}

func TestSessionSlidingExpiry(t *testing.T) {
	s := newSessions(60 * time.Millisecond)
	tok := s.create("a", "admin")
	for i := 0; i < 3; i++ {
		time.Sleep(40 * time.Millisecond)
		if _, ok := s.get(tok); !ok {
			t.Fatalf("session expired despite activity (round %d)", i)
		}
	}
	time.Sleep(90 * time.Millisecond)
	if _, ok := s.get(tok); ok {
		t.Error("idle session did not expire")
	}
}

func TestNoPasswordMeansOpen(t *testing.T) {
	srv, _, _ := newTestServer(t, okValidator)
	rec, body := doJSON(t, srv, "GET", "/api/auth", "")
	if rec.Code != http.StatusOK || body["required"] != false || body["authenticated"] != true {
		t.Errorf("GET /api/auth without password: %d %v", rec.Code, body)
	}
	rec, _ = doJSON(t, srv, "GET", "/api/units", "")
	if rec.Code != http.StatusOK {
		t.Errorf("open-mode /api/units: status %d", rec.Code)
	}
}

func TestOIDCCountsAsConfiguredAuth(t *testing.T) {
	client, err := oidc.New(oidc.Config{
		Issuer:       "https://idp.example",
		ClientID:     "rookery",
		ClientSecret: "secret",
		ProviderName: "Example SSO",
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := New(Options{
		Areas:    []Area{{Label: "system", Scope: systemd.Scope{}, Dirs: []string{t.TempDir()}}},
		Systemd:  &fakeSystemd{},
		Validate: okValidator,
		OIDC:     client,
	})
	rec, body := doJSON(t, srv, "GET", "/api/auth", "")
	if rec.Code != http.StatusOK || body["required"] != true || body["authenticated"] != false || body["setupNeeded"] != false {
		t.Fatalf("OIDC auth status = %d %v", rec.Code, body)
	}
	if got := body["oidc"].(map[string]any)["name"]; got != "Example SSO" {
		t.Fatalf("OIDC provider name = %v", got)
	}
	rec, _ = doJSON(t, srv, "GET", "/api/units", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("OIDC-only anonymous units = %d, want 401", rec.Code)
	}
}

func TestDisablePasswordLogin(t *testing.T) {
	client, err := oidc.New(oidc.Config{
		Issuer:       "https://idp.example",
		ClientID:     "rookery",
		ClientSecret: "secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := New(Options{
		Areas:                []Area{{Label: "system", Scope: systemd.Scope{}, Dirs: []string{t.TempDir()}}},
		Systemd:              &fakeSystemd{},
		Validate:             okValidator,
		Password:             "hunter2",
		OIDC:                 client,
		DisablePasswordLogin: true,
	})
	rec, body := doJSON(t, srv, "GET", "/api/auth", "")
	if rec.Code != http.StatusOK || body["passwordLogin"] != false {
		t.Fatalf("auth status passwordLogin = %d %v", rec.Code, body)
	}
	rec, _ = doJSON(t, srv, "POST", "/api/login", `{"password":"hunter2"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("disabled password login = %d, want 403", rec.Code)
	}
}
