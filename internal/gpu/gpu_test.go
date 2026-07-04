package gpu

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseNvidiaSMI(t *testing.T) {
	out := "0, NVIDIA GeForce RTX 3080, 10240, 2048, 37\n1, NVIDIA T400, 2048, 100, 0\n"
	devices := ParseNvidiaSMI(out)
	if len(devices) != 2 {
		t.Fatalf("got %d devices, want 2", len(devices))
	}
	d := devices[0]
	if d.Vendor != "nvidia" || d.Name != "NVIDIA GeForce RTX 3080" ||
		d.MemoryTotalMB != 10240 || d.MemoryUsedMB != 2048 || d.UtilizationPct != 37 {
		t.Errorf("devices[0] = %+v", d)
	}
	if devices[1].Index != 1 || devices[1].UtilizationPct != 0 {
		t.Errorf("devices[1] = %+v", devices[1])
	}
	// nvidia-smi sometimes reports [N/A] fields; they become -1, not 0.
	na := ParseNvidiaSMI("0, Foo, [N/A], [N/A], [N/A]\n")
	if len(na) != 1 || na[0].MemoryTotalMB != -1 || na[0].UtilizationPct != -1 {
		t.Errorf("N/A parsing = %+v", na)
	}
	if got := ParseNvidiaSMI(""); len(got) != 0 {
		t.Errorf("empty output = %+v", got)
	}
}

func TestDetectDRM(t *testing.T) {
	root := t.TempDir()
	mk := func(card, vendor string, files map[string]string) {
		t.Helper()
		dev := filepath.Join(root, card, "device")
		if err := os.MkdirAll(dev, 0o755); err != nil {
			t.Fatal(err)
		}
		files["vendor"] = vendor
		for name, content := range files {
			if err := os.WriteFile(filepath.Join(dev, name), []byte(content+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	mk("card0", "0x1002", map[string]string{
		"mem_info_vram_total": "17163091968", // 16368 MB
		"mem_info_vram_used":  "1073741824",  // 1024 MB
		"gpu_busy_percent":    "42",
	})
	mk("card1", "0x8086", map[string]string{})
	mk("card2", "0x10de", map[string]string{}) // NVIDIA: skipped, nvidia-smi owns it
	// connector entries must be ignored
	if err := os.MkdirAll(filepath.Join(root, "card0-HDMI-A-1"), 0o755); err != nil {
		t.Fatal(err)
	}

	devices := detectDRM(root)
	if len(devices) != 2 {
		t.Fatalf("got %d devices (%+v), want 2", len(devices), devices)
	}
	amd := devices[0]
	if amd.Vendor != "amd" || amd.MemoryTotalMB != 16368 || amd.MemoryUsedMB != 1024 || amd.UtilizationPct != 42 {
		t.Errorf("amd = %+v", amd)
	}
	if devices[1].Vendor != "intel" || devices[1].MemoryTotalMB != -1 {
		t.Errorf("intel = %+v", devices[1])
	}
}
