package server

import (
	"net/http"
	"strings"
	"testing"
)

func TestConvertRunEndpoint(t *testing.T) {
	srv, _, _ := newTestServer(t, okValidator)
	rec, body := doJSON(t, srv, "POST", "/api/convert",
		`{"kind":"run","input":"podman run -d --name web -p 8080:80 docker.io/library/nginx:latest"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	units := body["units"].([]any)
	if len(units) != 1 {
		t.Fatalf("got %d units", len(units))
	}
	u := units[0].(map[string]any)
	if u["name"] != "web.container" {
		t.Errorf("name = %v", u["name"])
	}
	if !strings.Contains(u["content"].(string), "PublishPort=8080:80") {
		t.Errorf("content = %v", u["content"])
	}
}

func TestConvertComposeEndpoint(t *testing.T) {
	srv, _, _ := newTestServer(t, okValidator)
	rec, body := doJSON(t, srv, "POST", "/api/convert",
		`{"kind":"compose","input":"services:\n  db:\n    image: postgres:16\n"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	units := body["units"].([]any)
	if len(units) != 1 || units[0].(map[string]any)["name"] != "db.container" {
		t.Errorf("units = %v", units)
	}
}

func TestConvertErrors(t *testing.T) {
	srv, _, _ := newTestServer(t, okValidator)
	rec, _ := doJSON(t, srv, "POST", "/api/convert", `{"kind":"run","input":"podman ps"}`)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad run command: status %d, want 422", rec.Code)
	}
	rec, _ = doJSON(t, srv, "POST", "/api/convert", `{"kind":"teleport","input":"x"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unknown kind: status %d, want 400", rec.Code)
	}
	// container conversion without a podman socket must 503, not crash.
	rec, _ = doJSON(t, srv, "POST", "/api/convert", `{"kind":"container","input":"web"}`)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("container without podman: status %d, want 503", rec.Code)
	}
}

func TestSELinuxHints(t *testing.T) {
	dir := t.TempDir()
	srv := New(Options{
		Areas:    []Area{{Label: "system", Dirs: []string{dir}}},
		Systemd:  &fakeSystemd{},
		Validate: okValidator,
		SELinux:  func() bool { return true },
	})
	rec, body := doJSON(t, srv, "POST", "/api/validate",
		`{"scope":"system","name":"x.container","content":"[Container]\nImage=x\nVolume=/srv/data:/data\nVolume=named:/n\n"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	hints := body["hints"].([]any)
	if len(hints) != 1 || !strings.Contains(hints[0].(string), "/srv/data:/data") {
		t.Errorf("hints = %v; want exactly one, about the bind mount", hints)
	}

	// Same request on a non-enforcing host: no hints.
	srvOff := New(Options{
		Areas:    []Area{{Label: "system", Dirs: []string{dir}}},
		Systemd:  &fakeSystemd{},
		Validate: okValidator,
		SELinux:  func() bool { return false },
	})
	rec, body = doJSON(t, srvOff, "POST", "/api/validate",
		`{"scope":"system","name":"x.container","content":"[Container]\nVolume=/srv/data:/data\n"}`)
	if rec.Code != http.StatusOK || body["hints"] != nil {
		t.Errorf("non-enforcing host: hints = %v, want null", body["hints"])
	}
}

func TestStatusFieldsExposed(t *testing.T) {
	srv, sysd, _ := newTestServer(t, okValidator)
	st := sysd.states["jellyfin.service"]
	st.Result, st.ExitCode, st.Restarts = "exit-code", 143, 7
	st.Active, st.Sub = "activating", "auto-restart"
	sysd.states["jellyfin.service"] = st

	rec, body := doJSON(t, srv, "GET", "/api/units", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	u := body["units"].([]any)[0].(map[string]any)
	if u["result"] != "exit-code" || u["exitCode"] != float64(143) || u["restarts"] != float64(7) {
		t.Errorf("restart-loop fields not surfaced: %v", u)
	}
}
