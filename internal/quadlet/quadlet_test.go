package quadlet

import (
	"os"
	"path/filepath"
	"testing"
)

func TestServiceName(t *testing.T) {
	cases := map[string]string{
		"jellyfin.container": "jellyfin.service",
		"media.pod":          "media-pod.service",
		"lan.network":        "lan-network.service",
		"cache.volume":       "cache-volume.service",
		"stack.kube":         "stack.service",
		"base.image":         "base-image.service",
		"app.build":          "app-build.service",
	}
	for in, want := range cases {
		got, err := ServiceName(in)
		if err != nil {
			t.Fatalf("ServiceName(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("ServiceName(%q) = %q, want %q", in, got, want)
		}
	}
	if _, err := ServiceName("notes.txt"); err == nil {
		t.Error("ServiceName(notes.txt): want error, got nil")
	}
}

func TestCheckName(t *testing.T) {
	for _, good := range []string{"a.container", "my-app.pod", "net1.network"} {
		if err := CheckName(good); err != nil {
			t.Errorf("CheckName(%q): unexpected error %v", good, err)
		}
	}
	for _, bad := range []string{"", "a.txt", "../evil.container", "a/b.container", ".hidden.container", "noext"} {
		if err := CheckName(bad); err == nil {
			t.Errorf("CheckName(%q): want error, got nil", bad)
		}
	}
}

func TestParse(t *testing.T) {
	src := `# Jellyfin media server
[Unit]
Description=Jellyfin

[Container]
Image=docker.io/jellyfin/jellyfin:latest
; repeated keys must all be kept
Volume=/srv/media:/media:Z
Volume=/srv/config:/config:Z
PublishPort=8096:8096
Exec=--some-flag \
     --continued-flag

[Install]
WantedBy=default.target
`
	f, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Sections) != 3 {
		t.Fatalf("got %d sections, want 3", len(f.Sections))
	}
	if desc, _ := f.Get("Unit", "Description"); desc != "Jellyfin" {
		t.Errorf("Description = %q", desc)
	}
	vols := f.All("Container", "Volume")
	if len(vols) != 2 || vols[0] != "/srv/media:/media:Z" {
		t.Errorf("Volume values = %v", vols)
	}
	if exec, _ := f.Get("Container", "Exec"); exec != "--some-flag --continued-flag" {
		t.Errorf("continuation not joined: %q", exec)
	}
}

func TestParseErrors(t *testing.T) {
	for _, src := range []string{
		"[Unit\nDescription=x",   // malformed header
		"Description=x",          // entry outside section
		"[Unit]\nno equals here", // not key=value
	} {
		if _, err := Parse([]byte(src)); err == nil {
			t.Errorf("Parse(%q): want error, got nil", src)
		}
	}
}

func TestDiscover(t *testing.T) {
	primary := t.TempDir()
	secondary := t.TempDir()
	missing := filepath.Join(primary, "does-not-exist")

	write := func(dir, name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte("[Container]\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(primary, "b.container")
	write(primary, "notes.txt") // ignored: not a quadlet extension
	write(secondary, "a.pod")
	write(secondary, "b.container") // shadowed by primary
	if err := os.Mkdir(filepath.Join(primary, "sub.container"), 0o755); err != nil {
		t.Fatal(err)
	}

	units, err := Discover([]string{primary, secondary, missing})
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 2 {
		t.Fatalf("got %d units (%v), want 2", len(units), units)
	}
	if units[0].Name != "a.pod" || units[0].Kind != KindPod {
		t.Errorf("units[0] = %+v", units[0])
	}
	if units[1].Name != "b.container" || units[1].Path != filepath.Join(primary, "b.container") {
		t.Errorf("units[1] = %+v: primary dir must shadow secondary", units[1])
	}
}
