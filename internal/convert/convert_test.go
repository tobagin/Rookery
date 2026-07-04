package convert

import (
	"reflect"
	"strings"
	"testing"
)

func TestSplitWords(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{`podman run -d nginx`, []string{"podman", "run", "-d", "nginx"}},
		{`podman run -e "A=b c" 'img:latest'`, []string{"podman", "run", "-e", "A=b c", "img:latest"}},
		{"podman run \\\n  -p 80:80 nginx", []string{"podman", "run", "-p", "80:80", "nginx"}},
		{`echo a\ b`, []string{"echo", "a b"}},
	}
	for _, c := range cases {
		got, err := splitWords(c.in)
		if err != nil {
			t.Fatalf("splitWords(%q): %v", c.in, err)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("splitWords(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	for _, bad := range []string{`echo 'unclosed`, `echo "unclosed`, `echo trailing\`} {
		if _, err := splitWords(bad); err == nil {
			t.Errorf("splitWords(%q): want error", bad)
		}
	}
}

func TestFromRunCommand(t *testing.T) {
	unit, err := FromRunCommand(`podman run -d --name jellyfin \
		-p 8096:8096 \
		-v /srv/media:/media:ro \
		-e TZ=Europe/Dublin \
		--device /dev/dri \
		--restart unless-stopped \
		docker.io/jellyfin/jellyfin:latest --some-arg "with space"`)
	if err != nil {
		t.Fatal(err)
	}
	if unit.Name != "jellyfin.container" {
		t.Errorf("Name = %q", unit.Name)
	}
	for _, want := range []string{
		"[Unit]",
		"[Container]",
		"Image=docker.io/jellyfin/jellyfin:latest",
		"ContainerName=jellyfin",
		"PublishPort=8096:8096",
		"Volume=/srv/media:/media:ro",
		"Environment=TZ=Europe/Dublin",
		"AddDevice=/dev/dri",
		`Exec=--some-arg "with space"`,
		"[Service]",
		"Restart=always",
		"[Install]",
		"WantedBy=default.target",
	} {
		if !strings.Contains(unit.Content, want) {
			t.Errorf("content missing %q:\n%s", want, unit.Content)
		}
	}
	if len(unit.Warnings) == 0 || !strings.Contains(strings.Join(unit.Warnings, " "), "unless-stopped") {
		t.Errorf("expected unless-stopped warning, got %v", unit.Warnings)
	}
	// Image must be the first Container entry for readability.
	idx := strings.Index(unit.Content, "[Container]")
	after := unit.Content[idx:]
	if !strings.HasPrefix(after, "[Container]\nImage=") {
		t.Errorf("Image= is not the first [Container] entry:\n%s", unit.Content)
	}
}

func TestFromRunCommandUnknownFlagAndName(t *testing.T) {
	unit, err := FromRunCommand(`podman run --frobnicate=7 docker.io/library/nginx:1.25`)
	if err != nil {
		t.Fatal(err)
	}
	if unit.Name != "nginx.container" {
		t.Errorf("Name = %q, want nginx.container (derived from image)", unit.Name)
	}
	if !strings.Contains(unit.Content, "PodmanArgs=--frobnicate=7") {
		t.Errorf("unknown flag not passed through:\n%s", unit.Content)
	}
	if len(unit.Warnings) == 0 {
		t.Error("expected a warning about the unknown flag")
	}
}

func TestFromRunCommandErrors(t *testing.T) {
	for _, bad := range []string{
		"podman ps",
		"podman run -d",
		"podman run --name",
	} {
		if _, err := FromRunCommand(bad); err == nil {
			t.Errorf("FromRunCommand(%q): want error", bad)
		}
	}
}

func TestParseYAML(t *testing.T) {
	src := `
# a comment
services:
  web:
    image: "nginx:1.25"   # trailing comment
    ports:
      - 8080:80
      - "443:443"
    environment:
      TZ: Europe/Dublin
      EMPTY:
    volumes: [data:/data, /host:/ctr]
  db:
    image: postgres
volumes:
  data:
`
	v, err := parseYAML(src)
	if err != nil {
		t.Fatal(err)
	}
	root := v.(map[string]any)
	web := root["services"].(map[string]any)["web"].(map[string]any)
	if web["image"] != "nginx:1.25" {
		t.Errorf("image = %v", web["image"])
	}
	ports := web["ports"].([]any)
	if len(ports) != 2 || ports[0] != "8080:80" || ports[1] != "443:443" {
		t.Errorf("ports = %v", ports)
	}
	env := web["environment"].(map[string]any)
	if env["TZ"] != "Europe/Dublin" || env["EMPTY"] != nil {
		t.Errorf("environment = %v", env)
	}
	vols := web["volumes"].([]any)
	if len(vols) != 2 || vols[0] != "data:/data" {
		t.Errorf("volumes = %v", vols)
	}
	if _, ok := root["volumes"].(map[string]any)["data"]; !ok {
		t.Errorf("top-level volumes = %v", root["volumes"])
	}
}

func TestParseYAMLListOfMaps(t *testing.T) {
	src := `
items:
  - name: a
    value: 1
  - name: b
`
	v, err := parseYAML(src)
	if err != nil {
		t.Fatal(err)
	}
	items := v.(map[string]any)["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("items = %v", items)
	}
	first := items[0].(map[string]any)
	if first["name"] != "a" || first["value"] != "1" {
		t.Errorf("first = %v", first)
	}
}

func TestParseYAMLUnsupported(t *testing.T) {
	for _, bad := range []string{
		"key: |\n  line1\n  line2",
		"a:\n\tb: 1",
	} {
		if _, err := parseYAML(bad); err == nil {
			t.Errorf("parseYAML(%q): want error", bad)
		}
	}
}

func TestFromCompose(t *testing.T) {
	src := `
services:
  app:
    image: ghcr.io/immich-app/immich-server:release
    container_name: immich
    ports:
      - "2283:2283"
    volumes:
      - upload:/usr/src/app/upload
      - /etc/localtime:/etc/localtime:ro
    environment:
      DB_HOSTNAME: db
    depends_on:
      - db
    restart: always
  db:
    image: docker.io/library/postgres:16
    environment:
      - POSTGRES_PASSWORD=secret
    networks:
      - backend
volumes:
  upload:
networks:
  backend:
`
	units, err := FromCompose(src)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]GeneratedUnit{}
	for _, u := range units {
		byName[u.Name] = u
	}
	if len(units) != 4 {
		t.Fatalf("got %d units (%v), want 4", len(units), sortedKeys(byName))
	}

	app := byName["app.container"]
	for _, want := range []string{
		"Image=ghcr.io/immich-app/immich-server:release",
		"ContainerName=immich",
		"PublishPort=2283:2283",
		"Volume=upload.volume:/usr/src/app/upload", // declared volume rewritten to unit reference
		"Volume=/etc/localtime:/etc/localtime:ro",
		"Environment=DB_HOSTNAME=db",
		"Wants=db.service",
		"After=db.service",
		"Restart=always",
	} {
		if !strings.Contains(app.Content, want) {
			t.Errorf("app.container missing %q:\n%s", want, app.Content)
		}
	}
	db := byName["db.container"]
	for _, want := range []string{
		"Environment=POSTGRES_PASSWORD=secret",
		"Network=backend.network",
	} {
		if !strings.Contains(db.Content, want) {
			t.Errorf("db.container missing %q:\n%s", want, db.Content)
		}
	}
	if _, ok := byName["upload.volume"]; !ok {
		t.Error("missing upload.volume unit")
	}
	if _, ok := byName["backend.network"]; !ok {
		t.Error("missing backend.network unit")
	}
}

func TestFromComposeNoServices(t *testing.T) {
	if _, err := FromCompose("volumes:\n  data:\n"); err == nil {
		t.Error("want error for compose file without services")
	}
}

const sampleInspect = `[{
  "Name": "grafana",
  "ImageName": "docker.io/grafana/grafana:10.4.2",
  "Config": {
    "Env": ["PATH=/usr/bin", "TERM=xterm", "GF_SECURITY_ADMIN_USER=admin"],
    "Cmd": [],
    "Entrypoint": ["/run.sh"],
    "User": "472",
    "Labels": {"maintainer": "Grafana", "com.example.team": "obs"}
  },
  "HostConfig": {
    "PortBindings": {"3000/tcp": [{"HostIp": "", "HostPort": "3000"}]},
    "RestartPolicy": {"Name": "always"},
    "CapAdd": ["NET_RAW"]
  },
  "Mounts": [
    {"Type": "volume", "Name": "grafana-data", "Source": "/var/lib/containers/storage/volumes/grafana-data/_data", "Destination": "/var/lib/grafana", "RW": true},
    {"Type": "bind", "Source": "/etc/grafana.ini", "Destination": "/etc/grafana/grafana.ini", "RW": false}
  ],
  "NetworkSettings": {"Networks": {"monitoring": {}}}
}]`

func TestFromInspect(t *testing.T) {
	unit, err := FromInspect([]byte(sampleInspect))
	if err != nil {
		t.Fatal(err)
	}
	if unit.Name != "grafana.container" {
		t.Errorf("Name = %q", unit.Name)
	}
	for _, want := range []string{
		"Image=docker.io/grafana/grafana:10.4.2",
		"ContainerName=grafana",
		"PublishPort=3000:3000",
		"Volume=grafana-data:/var/lib/grafana",
		"Volume=/etc/grafana.ini:/etc/grafana/grafana.ini:ro",
		"Environment=GF_SECURITY_ADMIN_USER=admin",
		"User=472",
		"Entrypoint=/run.sh",
		"AddCapability=NET_RAW",
		"Network=monitoring",
		"Label=com.example.team=obs",
		"Restart=always",
	} {
		if !strings.Contains(unit.Content, want) {
			t.Errorf("missing %q:\n%s", want, unit.Content)
		}
	}
	for _, absent := range []string{"PATH=", "TERM=", "maintainer"} {
		if strings.Contains(unit.Content, absent) {
			t.Errorf("boring value %q leaked into unit:\n%s", absent, unit.Content)
		}
	}
}
