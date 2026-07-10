package server

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rookerylabs/rookery/internal/podman"
	"github.com/rookerylabs/rookery/internal/systemd"
)

// fakePodman serves canned digests and records pulls.
type fakePodman struct {
	digests  map[string][]string
	pulled   []string
	pullErr  error
	networks []podman.NetworkSummary
	volumes  []podman.VolumeSummary
}

func (f *fakePodman) Networks(context.Context) ([]podman.NetworkSummary, error) {
	return f.networks, nil
}
func (f *fakePodman) Volumes(context.Context) ([]podman.VolumeSummary, error) {
	return f.volumes, nil
}

func (f *fakePodman) Info(context.Context) (*podman.Info, error) {
	return &podman.Info{Version: "5.0-test"}, nil
}
func (f *fakePodman) Containers(context.Context) ([]podman.ContainerSummary, error) {
	return nil, nil
}
func (f *fakePodman) Stats(context.Context) ([]podman.ContainerStats, error) {
	return nil, nil
}
func (f *fakePodman) InspectContainer(context.Context, string) ([]byte, error) {
	return nil, fmt.Errorf("not implemented")
}
func (f *fakePodman) StopContainer(context.Context, string) error {
	return nil
}
func (f *fakePodman) ImageDigests(_ context.Context, ref string) ([]string, error) {
	d, ok := f.digests[ref]
	if !ok {
		return nil, fmt.Errorf("no such image %s", ref)
	}
	return d, nil
}
func (f *fakePodman) PullImage(_ context.Context, ref string) error {
	if f.pullErr != nil {
		return f.pullErr
	}
	f.pulled = append(f.pulled, ref)
	return nil
}

const (
	oldDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	newDigest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func newUpdatesServer(t *testing.T) (*Server, *fakePodman, *fakeSystemd, string) {
	t.Helper()
	dir := t.TempDir()
	write := func(name, image string) {
		t.Helper()
		content := fmt.Sprintf("[Container]\nImage=%s\n", image)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("stale.container", "docker.io/library/nginx:latest")
	write("fresh.container", "docker.io/library/redis:7")
	write("pinned.container", "docker.io/library/postgres@"+oldDigest)

	pod := &fakePodman{digests: map[string][]string{
		"docker.io/library/nginx:latest": {"docker.io/library/nginx@" + oldDigest},
		"docker.io/library/redis:7":      {"docker.io/library/redis@" + newDigest},
	}}
	sysd := &fakeSystemd{states: map[string]systemd.UnitStatus{}}
	srv := New(Options{
		Areas:    []Area{{Label: "system", Scope: systemd.Scope{}, Dirs: []string{dir}}},
		Systemd:  sysd,
		Validate: okValidator,
		Podman:   pod,
		ResolveDigest: func(_ context.Context, image string) (string, error) {
			return newDigest, nil // the registry has moved every tag to newDigest
		},
	})
	return srv, pod, sysd, dir
}

func TestUpdatesCheck(t *testing.T) {
	srv, _, _, _ := newUpdatesServer(t)
	rec, body := doJSON(t, srv, "GET", "/api/updates", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	rows := body["updates"].([]any)
	byName := map[string]map[string]any{}
	for _, r := range rows {
		row := r.(map[string]any)
		byName[row["name"].(string)] = row
	}
	if len(byName) != 3 {
		t.Fatalf("got %d rows: %v", len(byName), byName)
	}
	if byName["stale.container"]["updateAvailable"] != true {
		t.Errorf("stale unit not flagged: %v", byName["stale.container"])
	}
	if byName["fresh.container"]["updateAvailable"] != false {
		t.Errorf("fresh unit wrongly flagged: %v", byName["fresh.container"])
	}
	pinned := byName["pinned.container"]
	if pinned["updateAvailable"] != false || !strings.Contains(pinned["note"].(string), "pinned") {
		t.Errorf("pinned unit: %v", pinned)
	}
}

func TestUpdatesWithoutPodman(t *testing.T) {
	srv, _, _ := newTestServer(t, okValidator)
	rec, _ := doJSON(t, srv, "GET", "/api/updates", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status %d, want 503", rec.Code)
	}
}

func TestUpdateUnitPullsAndRestarts(t *testing.T) {
	srv, pod, sysd, _ := newUpdatesServer(t)
	rec, body := doJSON(t, srv, "POST", "/api/units/system/stale.container/update", "{}")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	if body["pulled"] != "docker.io/library/nginx:latest" {
		t.Errorf("pulled = %v", body["pulled"])
	}
	if len(pod.pulled) != 1 || pod.pulled[0] != "docker.io/library/nginx:latest" {
		t.Errorf("podman pulls = %v", pod.pulled)
	}
	joined := strings.Join(sysd.calls, "; ")
	if !strings.Contains(joined, "restart system stale.service") {
		t.Errorf("systemd calls = %v; want restart after pull", sysd.calls)
	}
}

func TestUpdateUnitPullFailure(t *testing.T) {
	srv, pod, sysd, _ := newUpdatesServer(t)
	pod.pullErr = fmt.Errorf("registry timeout")
	rec, _ := doJSON(t, srv, "POST", "/api/units/system/stale.container/update", "{}")
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status %d, want 502", rec.Code)
	}
	if len(sysd.calls) != 0 {
		t.Errorf("failed pull must not restart the service; calls = %v", sysd.calls)
	}
}
