// Package gpu inventories the host's GPUs for the dashboard panel — the
// piece of the PRD no other Quadlet UI covers. NVIDIA cards are read via
// nvidia-smi; AMD (and bare Intel presence) via the DRM sysfs tree. Hosts
// without GPUs simply return an empty list.
package gpu

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Device is one GPU as the panel shows it. Unknown metrics are -1 so the
// UI can distinguish "0%" from "can't tell".
type Device struct {
	Index          int    `json:"index"`
	Vendor         string `json:"vendor"` // nvidia, amd, intel
	Name           string `json:"name"`
	MemoryTotalMB  int    `json:"memoryTotalMb"`  // -1 unknown
	MemoryUsedMB   int    `json:"memoryUsedMb"`   // -1 unknown
	UtilizationPct int    `json:"utilizationPct"` // -1 unknown
}

// Detect returns every GPU it can see. Failures of individual probes are
// swallowed — a broken nvidia-smi must not hide the AMD card next to it.
func Detect(ctx context.Context) []Device {
	devices := detectNvidia(ctx)
	haveSMI := len(devices) > 0
	for _, d := range detectDRM("/sys/class/drm") {
		if d.Vendor == "nvidia" && haveSMI {
			continue // nvidia-smi already reported it, with metrics
		}
		devices = append(devices, d)
	}
	if devices == nil {
		devices = []Device{}
	}
	return devices
}

func detectNvidia(ctx context.Context) []Device {
	smi, err := exec.LookPath("nvidia-smi")
	if err != nil {
		return nil
	}
	out, err := exec.CommandContext(ctx, smi,
		"--query-gpu=index,name,memory.total,memory.used,utilization.gpu",
		"--format=csv,noheader,nounits").Output()
	if err != nil {
		return nil
	}
	return ParseNvidiaSMI(string(out))
}

// ParseNvidiaSMI reads nvidia-smi's csv,noheader,nounits output.
func ParseNvidiaSMI(out string) []Device {
	var devices []Device
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Split(line, ",")
		if len(fields) != 5 {
			continue
		}
		for i := range fields {
			fields[i] = strings.TrimSpace(fields[i])
		}
		idx, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		devices = append(devices, Device{
			Index:          idx,
			Vendor:         "nvidia",
			Name:           fields[1],
			MemoryTotalMB:  atoiOr(fields[2], -1),
			MemoryUsedMB:   atoiOr(fields[3], -1),
			UtilizationPct: atoiOr(fields[4], -1),
		})
	}
	return devices
}

// detectDRM walks /sys/class/drm/card* and reports AMD cards (with amdgpu
// VRAM/busy metrics), Intel cards (presence only), and NVIDIA cards
// (presence only — a card must never vanish just because nvidia-smi is
// missing; Detect drops these duplicates when nvidia-smi answered).
func detectDRM(root string) []Device {
	cards, _ := filepath.Glob(filepath.Join(root, "card[0-9]*"))
	var devices []Device
	for _, card := range cards {
		base := filepath.Base(card)
		if strings.Contains(base, "-") {
			continue // connector entries like card0-HDMI-A-1
		}
		dev := filepath.Join(card, "device")
		vendorID := strings.TrimSpace(readFile(filepath.Join(dev, "vendor")))
		idx := atoiOr(strings.TrimPrefix(base, "card"), 0)
		switch vendorID {
		case "0x1002": // AMD
			d := Device{
				Index:          idx,
				Vendor:         "amd",
				Name:           "AMD GPU (" + base + ")",
				MemoryTotalMB:  bytesToMB(readFile(filepath.Join(dev, "mem_info_vram_total"))),
				MemoryUsedMB:   bytesToMB(readFile(filepath.Join(dev, "mem_info_vram_used"))),
				UtilizationPct: atoiOr(strings.TrimSpace(readFile(filepath.Join(dev, "gpu_busy_percent"))), -1),
			}
			devices = append(devices, d)
		case "0x8086": // Intel
			devices = append(devices, Device{
				Index:          idx,
				Vendor:         "intel",
				Name:           "Intel GPU (" + base + ")",
				MemoryTotalMB:  -1,
				MemoryUsedMB:   -1,
				UtilizationPct: -1,
			})
		case "0x10de": // NVIDIA — presence only, metrics need nvidia-smi
			devices = append(devices, Device{
				Index:          idx,
				Vendor:         "nvidia",
				Name:           "NVIDIA GPU (" + base + ")",
				MemoryTotalMB:  -1,
				MemoryUsedMB:   -1,
				UtilizationPct: -1,
			})
		}
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].Index < devices[j].Index })
	return devices
}

func readFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

func atoiOr(s string, fallback int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return n
}

func bytesToMB(s string) int {
	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return -1
	}
	return int(n / (1024 * 1024))
}
