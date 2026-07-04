package registry

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseRef(t *testing.T) {
	cases := map[string]Ref{
		"nginx":                          {Host: "registry-1.docker.io", Repo: "library/nginx", Tag: "latest"},
		"nginx:1.25":                     {Host: "registry-1.docker.io", Repo: "library/nginx", Tag: "1.25"},
		"docker.io/jellyfin/jellyfin":    {Host: "registry-1.docker.io", Repo: "jellyfin/jellyfin", Tag: "latest"},
		"ghcr.io/immich-app/server:v1.2": {Host: "ghcr.io", Repo: "immich-app/server", Tag: "v1.2"},
		"localhost:5000/myapp:dev":       {Host: "localhost:5000", Repo: "myapp", Tag: "dev"},
		"quay.io/podman/stable":          {Host: "quay.io", Repo: "podman/stable", Tag: "latest"},
	}
	for in, want := range cases {
		got, err := ParseRef(in)
		if err != nil {
			t.Fatalf("ParseRef(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("ParseRef(%q) = %+v, want %+v", in, got, want)
		}
	}
	if _, err := ParseRef("nginx@sha256:abc"); err == nil {
		t.Error("digest-pinned ref: want error")
	}
}

// TestResolveDigest runs the full anonymous token dance against a fake
// registry: 401 challenge -> token fetch -> authorized HEAD.
func TestResolveDigest(t *testing.T) {
	const digest = "sha256:d1e5c0e8f1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f70819aabbcc"
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("scope") != "repository:myapp:pull" {
			t.Errorf("token scope = %q", r.URL.Query().Get("scope"))
		}
		fmt.Fprintf(w, `{"token":"tok123"}`)
	})
	mux.HandleFunc("HEAD /v2/myapp/manifests/dev", func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept"), "manifest") {
			t.Errorf("Accept = %q", r.Header.Get("Accept"))
		}
		if r.Header.Get("Authorization") != "Bearer tok123" {
			w.Header().Set("WWW-Authenticate",
				fmt.Sprintf(`Bearer realm="%s/token",service="test-registry"`, srv.URL))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Docker-Content-Digest", digest)
		w.WriteHeader(http.StatusOK)
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "http://") // 127.0.0.1:port -> http scheme path
	got, err := NewClient().ResolveDigest(context.Background(), host+"/myapp:dev")
	if err != nil {
		t.Fatal(err)
	}
	if got != digest {
		t.Errorf("digest = %q, want %q", got, digest)
	}
}

func TestResolveDigestNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	if _, err := NewClient().ResolveDigest(context.Background(), host+"/gone:latest"); err == nil {
		t.Error("want error for 404")
	}
}
