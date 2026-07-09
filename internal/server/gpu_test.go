package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/rookerylabs/rookery/internal/gpu"
	"github.com/rookerylabs/rookery/internal/systemd"
)

func TestGPUEndpointAndUnitRefs(t *testing.T) {
	dir := t.TempDir()
	unit := "[Container]\nImage=x\nAddDevice=nvidia.com/gpu=all\nAddDevice=/dev/dri\nAddDevice=/dev/null\n"
	if err := os.WriteFile(filepath.Join(dir, "llm.container"), []byte(unit), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := New(Options{
		Areas:    []Area{{Label: "system", Scope: systemd.Scope{}, Dirs: []string{dir}}},
		Systemd:  &fakeSystemd{},
		Validate: okValidator,
		GPUs: func(context.Context) []gpu.Device {
			return []gpu.Device{{Index: 0, Vendor: "nvidia", Name: "RTX 3080", MemoryTotalMB: 10240, MemoryUsedMB: 2048, UtilizationPct: 37}}
		},
	})

	rec, body := doJSON(t, srv, "GET", "/api/gpus", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	devices := body["devices"].([]any)
	if len(devices) != 1 || devices[0].(map[string]any)["name"] != "RTX 3080" {
		t.Errorf("devices = %v", devices)
	}

	rec, body = doJSON(t, srv, "GET", "/api/units", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	u := body["units"].([]any)[0].(map[string]any)
	gpus := u["gpus"].([]any)
	if len(gpus) != 2 || gpus[0] != "nvidia.com/gpu=all" || gpus[1] != "/dev/dri" {
		t.Errorf("unit gpu refs = %v; /dev/null must not count as a GPU", gpus)
	}
}
