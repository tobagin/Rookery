package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tobagin/rookery/internal/systemd"
)

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
