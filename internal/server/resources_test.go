package server

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/rookerylabs/rookery/internal/podman"
	"github.com/rookerylabs/rookery/internal/systemd"
)

func TestListResources(t *testing.T) {
	dir := t.TempDir()
	// A foo.network quadlet manages the podman network "systemd-foo".
	if err := os.WriteFile(filepath.Join(dir, "foo.network"), []byte("[Network]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pod := &fakePodman{
		networks: []podman.NetworkSummary{
			{Name: "systemd-foo", Driver: "bridge"}, // managed (matches foo.network)
			{Name: "podman", Driver: "bridge"},      // unmanaged
		},
		volumes: []podman.VolumeSummary{
			{Name: "data", Driver: "local", Mountpoint: "/mnt/data"}, // unmanaged
		},
	}
	srv := New(Options{
		Areas:    []Area{{Label: "system", Scope: systemd.Scope{}, Dirs: []string{dir}}},
		Systemd:  &fakeSystemd{states: map[string]systemd.UnitStatus{}},
		Validate: okValidator,
		Podman:   pod,
	})
	rec, body := doJSON(t, srv, "GET", "/api/resources", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	managed := map[string]bool{}
	for _, raw := range body["resources"].([]any) {
		r := raw.(map[string]any)
		managed[r["kind"].(string)+":"+r["name"].(string)] = r["managed"].(bool)
	}
	if len(managed) != 3 {
		t.Fatalf("got %d resources, want 3: %v", len(managed), managed)
	}
	if !managed["network:systemd-foo"] {
		t.Errorf("systemd-foo should be managed (foo.network exists)")
	}
	if managed["network:podman"] {
		t.Errorf("podman network should be unmanaged")
	}
	if managed["volume:data"] {
		t.Errorf("data volume should be unmanaged")
	}
}
