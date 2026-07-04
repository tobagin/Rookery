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
	mk("card2", "0x10de", map[string]string{}) // NVIDIA: presence even without nvidia-smi
	// connector entries must be ignored
	if err := os.MkdirAll(filepath.Join(root, "card0-HDMI-A-1"), 0o755); err != nil {
		t.Fatal(err)
	}

	devices := detectDRM(root)
	if len(devices) != 3 {
		t.Fatalf("got %d devices (%+v), want 3", len(devices), devices)
	}
	amd := devices[0]
	if amd.Vendor != "amd" || amd.MemoryTotalMB != 16368 || amd.MemoryUsedMB != 1024 || amd.UtilizationPct != 42 {
		t.Errorf("amd = %+v", amd)
	}
	if devices[1].Vendor != "intel" || devices[1].MemoryTotalMB != -1 {
		t.Errorf("intel = %+v", devices[1])
	}
	if devices[2].Vendor != "nvidia" || devices[2].MemoryTotalMB != -1 || devices[2].UtilizationPct != -1 {
		t.Errorf("nvidia fallback = %+v", devices[2])
	}
}

func TestParseRemoteProbe(t *testing.T) {
	out := `0, NVIDIA T400, 2048, 100, 3
__ROOKERY_DRM__
card0|0x10de|||
card1|0x1002|2147483648|1073741824|42
card1-HDMI-A-1|0x1002|||
`
	devices := ParseRemoteProbe(out)
	if len(devices) != 2 {
		t.Fatalf("got %d devices (%+v), want 2 (nvidia deduped by smi)", len(devices), devices)
	}
	if devices[0].Vendor != "nvidia" || devices[0].Name != "NVIDIA T400" || devices[0].MemoryTotalMB != 2048 {
		t.Errorf("smi device = %+v", devices[0])
	}
	if devices[1].Vendor != "amd" || devices[1].MemoryTotalMB != 2048 || devices[1].UtilizationPct != 42 {
		t.Errorf("amd device = %+v", devices[1])
	}

	// No nvidia-smi on the remote host: the DRM fallback keeps the card.
	noSMI := ParseRemoteProbe("\n__ROOKERY_DRM__\ncard0|0x10de|||\n")
	if len(noSMI) != 1 || noSMI[0].Vendor != "nvidia" || noSMI[0].MemoryTotalMB != -1 {
		t.Errorf("fallback = %+v", noSMI)
	}
}
