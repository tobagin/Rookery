package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tobagin/rookery/internal/podman"
	"github.com/tobagin/rookery/internal/systemd"
)

// fakeSecretsPodman satisfies Podman plus the optional secrets/images
// interfaces the way the real client does.
type fakeSecretsPodman struct {
	secrets []podman.Secret
	removed []string
	created map[string]string
	stale   int
	bytes   int64
	pruned  bool
}

func (f *fakeSecretsPodman) Info(context.Context) (*podman.Info, error) { return &podman.Info{}, nil }
func (f *fakeSecretsPodman) Containers(context.Context) ([]podman.ContainerSummary, error) {
	return nil, nil
}
func (f *fakeSecretsPodman) Stats(context.Context) ([]podman.ContainerStats, error) {
	return nil, nil
}
func (f *fakeSecretsPodman) InspectContainer(context.Context, string) ([]byte, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *fakeSecretsPodman) StopContainer(context.Context, string) error { return nil }
func (f *fakeSecretsPodman) ImageDigests(context.Context, string) ([]string, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *fakeSecretsPodman) PullImage(context.Context, string) error { return nil }

func (f *fakeSecretsPodman) Secrets(context.Context) ([]podman.Secret, error) {
	return f.secrets, nil
}
func (f *fakeSecretsPodman) CreateSecret(_ context.Context, name string, data []byte) error {
	if f.created == nil {
		f.created = map[string]string{}
	}
	f.created[name] = string(data)
	return nil
}
func (f *fakeSecretsPodman) RemoveSecret(_ context.Context, name string) error {
	f.removed = append(f.removed, name)
	return nil
}
func (f *fakeSecretsPodman) StaleImages(context.Context) (int, int64, error) {
	return f.stale, f.bytes, nil
}
func (f *fakeSecretsPodman) PruneImages(context.Context) (int64, error) {
	f.pruned = true
	reclaimed := f.bytes
	f.stale, f.bytes = 0, 0
	return reclaimed, nil
}

func newSecretsServer(t *testing.T, pod Podman) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	unit := "[Container]\nImage=docker.io/library/postgres:16\nSecret=db-password,type=env,target=POSTGRES_PASSWORD\n"
	if err := os.WriteFile(filepath.Join(dir, "db.container"), []byte(unit), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := New(Options{
		Areas:    []Area{{Label: "system", Scope: systemd.Scope{}, Dirs: []string{dir}}},
		Systemd:  &fakeSystemd{},
		Validate: okValidator,
		Podman:   pod,
	})
	return srv, dir
}

func TestSecretsCRUDAndUsage(t *testing.T) {
	pod := &fakeSecretsPodman{secrets: []podman.Secret{
		{ID: "1", Name: "db-password", Driver: "file"},
		{ID: "2", Name: "unused-token", Driver: "file"},
	}}
	srv, _ := newSecretsServer(t, pod)

	rec, body := doJSON(t, srv, "GET", "/api/secrets", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	used := body["usedBy"].(map[string]any)
	refs, _ := used["db-password"].([]any)
	if len(refs) != 1 || refs[0] != "system/db.container" {
		t.Errorf("usedBy[db-password] = %v", used["db-password"])
	}

	// A referenced secret cannot be deleted.
	rec, _ = doJSON(t, srv, "DELETE", "/api/secrets/db-password", "")
	if rec.Code != http.StatusConflict {
		t.Errorf("delete in-use secret: %d, want 409", rec.Code)
	}
	if len(pod.removed) != 0 {
		t.Errorf("in-use secret was removed: %v", pod.removed)
	}

	rec, _ = doJSON(t, srv, "DELETE", "/api/secrets/unused-token", "")
	if rec.Code != http.StatusOK || len(pod.removed) != 1 || pod.removed[0] != "unused-token" {
		t.Errorf("delete unused: %d, removed=%v", rec.Code, pod.removed)
	}

	rec, _ = doJSON(t, srv, "POST", "/api/secrets", `{"name":"api-key","data":"hunter2"}`)
	if rec.Code != http.StatusOK || pod.created["api-key"] != "hunter2" {
		t.Errorf("create: %d, created=%v", rec.Code, pod.created)
	}
	for _, bad := range []string{`{"name":"","data":"x"}`, `{"name":"ok","data":""}`, `{"name":"../evil","data":"x"}`} {
		rec, _ = doJSON(t, srv, "POST", "/api/secrets", bad)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("create %s: %d, want 400", bad, rec.Code)
		}
	}
}

func TestStaleImages(t *testing.T) {
	pod := &fakeSecretsPodman{stale: 3, bytes: 2 << 30}
	srv, _ := newSecretsServer(t, pod)

	rec, body := doJSON(t, srv, "GET", "/api/images/stale", "")
	if rec.Code != http.StatusOK || body["count"] != float64(3) {
		t.Fatalf("stale: %d %v", rec.Code, body)
	}
	rec, body = doJSON(t, srv, "POST", "/api/images/prune", "{}")
	if rec.Code != http.StatusOK || !pod.pruned || body["reclaimedBytes"] != float64(2<<30) {
		t.Errorf("prune: %d %v pruned=%v", rec.Code, body, pod.pruned)
	}
}

func TestSecretsUnavailableWithoutPodman(t *testing.T) {
	srv, _, _ := newTestServer(t, okValidator) // nil Podman
	rec, _ := doJSON(t, srv, "GET", "/api/secrets", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("secrets without podman: %d, want 503", rec.Code)
	}
	rec, _ = doJSON(t, srv, "GET", "/api/images/stale", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("stale without podman: %d, want 503", rec.Code)
	}
}

func TestShareCannotListSecrets(t *testing.T) {
	pod := &fakeSecretsPodman{}
	srv, _ := newSecretsServer(t, pod)
	srv.password = "hunter2"
	token := srv.mintShare(timeNowPlusShareTTL())
	req := httptest.NewRequest("GET", "/api/secrets", strings.NewReader(""))
	req.AddCookie(&http.Cookie{Name: shareCookie, Value: token})
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("share GET /api/secrets: %d, want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "read-only") {
		t.Errorf("body = %s", rec.Body.String())
	}
}
