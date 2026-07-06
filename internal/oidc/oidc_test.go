package oidc

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestExchangeVerifiesIDTokenAndMapsAdminRole(t *testing.T) {
	key := testKey(t)
	now := time.Unix(1_700_000_000, 0)
	var issuer string
	var gotCode, gotRedirect string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			writeTestJSON(w, map[string]any{
				"issuer":                 issuer,
				"authorization_endpoint": issuer + "/auth",
				"token_endpoint":         issuer + "/token",
				"jwks_uri":               issuer + "/jwks",
			})
		case "/jwks":
			writeTestJSON(w, map[string]any{"keys": []any{testJWK(&key.PublicKey, "k1")}})
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			gotCode = r.Form.Get("code")
			gotRedirect = r.Form.Get("redirect_uri")
			idToken := signTestJWT(t, key, "k1", map[string]any{
				"iss":                issuer,
				"sub":                "user-123",
				"aud":                []string{"rookery", "other-client"},
				"azp":                "rookery",
				"exp":                now.Add(time.Hour).Unix(),
				"iat":                now.Unix(),
				"nonce":              "nonce-1",
				"email":              "admin@example.com",
				"preferred_username": "admin",
				"groups":             []string{"ops"},
			})
			writeTestJSON(w, map[string]any{"id_token": idToken})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	issuer = srv.URL

	client, err := New(Config{
		Issuer:       issuer,
		ClientID:     "rookery",
		ClientSecret: "secret",
		RedirectURL:  "https://rookery.example/api/oidc/callback",
		AdminGroups:  []string{"ops"},
		HTTPClient:   srv.Client(),
		Now:          func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	claims, err := client.Exchange(context.Background(), "code-1", "", "nonce-1")
	if err != nil {
		t.Fatal(err)
	}
	if gotCode != "code-1" || gotRedirect != "https://rookery.example/api/oidc/callback" {
		t.Fatalf("token request code/redirect = %q/%q", gotCode, gotRedirect)
	}
	if claims.Username != "admin" || claims.Role != RoleAdmin {
		t.Fatalf("claims username/role = %q/%q", claims.Username, claims.Role)
	}
}

func TestVerifyIDTokenRejectsBadNonceAndAudience(t *testing.T) {
	key := testKey(t)
	now := time.Unix(1_700_000_000, 0)
	var issuer string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			writeTestJSON(w, map[string]any{
				"issuer":                 issuer,
				"authorization_endpoint": issuer + "/auth",
				"token_endpoint":         issuer + "/token",
				"jwks_uri":               issuer + "/jwks",
			})
		case "/jwks":
			writeTestJSON(w, map[string]any{"keys": []any{testJWK(&key.PublicKey, "k1")}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	issuer = srv.URL

	client, err := New(Config{
		Issuer:       issuer,
		ClientID:     "rookery",
		ClientSecret: "secret",
		HTTPClient:   srv.Client(),
		Now:          func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	good := signTestJWT(t, key, "k1", map[string]any{
		"iss": issuer, "sub": "u", "aud": "rookery",
		"exp": now.Add(time.Hour).Unix(), "nonce": "good",
	})
	if _, err := client.VerifyIDToken(context.Background(), good, "wrong"); err == nil || !strings.Contains(err.Error(), "nonce") {
		t.Fatalf("bad nonce error = %v", err)
	}
	badAud := signTestJWT(t, key, "k1", map[string]any{
		"iss": issuer, "sub": "u", "aud": "someone-else",
		"exp": now.Add(time.Hour).Unix(), "nonce": "good",
	})
	if _, err := client.VerifyIDToken(context.Background(), badAud, "good"); err == nil || !strings.Contains(err.Error(), "audience") {
		t.Fatalf("bad audience error = %v", err)
	}
}

func TestAuthCodeURLUsesDiscoveryAndScopes(t *testing.T) {
	var issuer string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeTestJSON(w, map[string]any{
			"issuer":                 issuer,
			"authorization_endpoint": issuer + "/auth",
			"token_endpoint":         issuer + "/token",
			"jwks_uri":               issuer + "/jwks",
		})
	}))
	defer srv.Close()
	issuer = srv.URL
	client, err := New(Config{Issuer: issuer, ClientID: "rookery", ClientSecret: "secret", HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := client.AuthCodeURL(context.Background(), "https://r/cb", "state-1", "nonce-1")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if u.Path != "/auth" || u.Query().Get("client_id") != "rookery" ||
		u.Query().Get("redirect_uri") != "https://r/cb" ||
		u.Query().Get("state") != "state-1" ||
		u.Query().Get("nonce") != "nonce-1" ||
		u.Query().Get("scope") != "openid email profile" {
		t.Fatalf("unexpected auth URL %s", raw)
	}
}

func TestPartialConfigFails(t *testing.T) {
	if _, err := New(Config{Issuer: "https://idp.example", ClientID: "rookery"}); err == nil {
		t.Fatal("partial OIDC config should fail")
	}
}

func TestIssuerTrailingSlashIsAccepted(t *testing.T) {
	key := testKey(t)
	now := time.Unix(1_700_000_000, 0)
	var issuer string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			writeTestJSON(w, map[string]any{
				"issuer":                 issuer + "/",
				"authorization_endpoint": issuer + "/auth",
				"token_endpoint":         issuer + "/token",
				"jwks_uri":               issuer + "/jwks",
			})
		case "/jwks":
			writeTestJSON(w, map[string]any{"keys": []any{testJWK(&key.PublicKey, "k1")}})
		case "/token":
			idToken := signTestJWT(t, key, "k1", map[string]any{
				"iss": issuer + "/", "sub": "u", "aud": "rookery",
				"exp": now.Add(time.Hour).Unix(), "nonce": "n",
			})
			writeTestJSON(w, map[string]any{"id_token": idToken})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	issuer = srv.URL
	client, err := New(Config{
		Issuer:       issuer,
		ClientID:     "rookery",
		ClientSecret: "secret",
		HTTPClient:   srv.Client(),
		Now:          func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Exchange(context.Background(), "code", "https://r/cb", "n"); err != nil {
		t.Fatal(err)
	}
}

func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func signTestJWT(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	header, _ := json.Marshal(map[string]any{"alg": "RS256", "typ": "JWT", "kid": kid})
	payload, _ := json.Marshal(claims)
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	sum := sha256.Sum256([]byte(unsigned))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func testJWK(pub *rsa.PublicKey, kid string) map[string]any {
	e := bigEndian(pub.E)
	return map[string]any{
		"kty": "RSA", "use": "sig", "alg": "RS256", "kid": kid,
		"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(e),
	}
}

func bigEndian(n int) []byte {
	if n == 0 {
		return []byte{0}
	}
	var out []byte
	for n > 0 {
		out = append([]byte{byte(n)}, out...)
		n >>= 8
	}
	return out
}

func writeTestJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		panic(fmt.Sprintf("write json: %v", err))
	}
}
