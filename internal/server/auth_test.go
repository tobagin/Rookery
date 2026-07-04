package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tobagin/rookery/internal/systemd"
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
