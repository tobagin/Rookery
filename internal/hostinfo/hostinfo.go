// Package hostinfo reads the small set of host metrics the dashboard strip
// shows, straight from /proc — no exporters, no polling daemon.
package hostinfo

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// Metrics is a point-in-time snapshot of host health.
type Metrics struct {
	Hostname      string  `json:"hostname"`
	Kernel        string  `json:"kernel"`
	Load1         float64 `json:"load1"`
	Cores         int     `json:"cores"`
	CPUPct        int     `json:"cpuPct"` // -1 until two samples exist
	MemTotalKB    int64   `json:"memTotalKb"`
	MemAvailKB    int64   `json:"memAvailKb"`
	UptimeSeconds int64   `json:"uptimeSeconds"`
}

// CPU utilization needs two /proc/stat samples; keep the previous one
// between requests. The first reading reports -1 (unknown), the dashboard's
// refresh supplies the delta a few seconds later.
var (
	cpuMu     sync.Mutex
	prevIdle  uint64
	prevTotal uint64
)

func cpuPercent() int {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return -1
	}
	line, _, _ := strings.Cut(string(b), "\n")
	idle, total, ok := parseCPUStat(line)
	if !ok {
		return -1
	}
	cpuMu.Lock()
	defer cpuMu.Unlock()
	dIdle, dTotal := idle-prevIdle, total-prevTotal
	first := prevTotal == 0
	prevIdle, prevTotal = idle, total
	if first || dTotal == 0 || dIdle > dTotal {
		return -1
	}
	return int(100 * (dTotal - dIdle) / dTotal)
}

// parseCPUStat reads the aggregate "cpu ..." line: idle is idle+iowait,
// total is the sum of all fields.
func parseCPUStat(line string) (idle, total uint64, ok bool) {
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return 0, 0, false
	}
	for i, f := range fields[1:] {
		n, err := strconv.ParseUint(f, 10, 64)
		if err != nil {
			return 0, 0, false
		}
		total += n
		if i == 3 || i == 4 { // idle, iowait
			idle += n
		}
	}
	return idle, total, true
}

// Read gathers metrics; fields it cannot read are left zero rather than
// failing the whole snapshot.
func Read() Metrics {
	var m Metrics
	m.Hostname, _ = os.Hostname()
	m.Cores = runtime.NumCPU()
	m.CPUPct = cpuPercent()
	if b, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		m.Kernel = strings.TrimSpace(string(b))
	}
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		if fields := strings.Fields(string(b)); len(fields) > 0 {
			m.Load1, _ = strconv.ParseFloat(fields[0], 64)
		}
	}
	if b, err := os.ReadFile("/proc/uptime"); err == nil {
		if fields := strings.Fields(string(b)); len(fields) > 0 {
			f, _ := strconv.ParseFloat(fields[0], 64)
			m.UptimeSeconds = int64(f)
		}
	}
	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			k, v, ok := strings.Cut(line, ":")
			if !ok {
				continue
			}
			fields := strings.Fields(v)
			if len(fields) == 0 {
				continue
			}
			n, _ := strconv.ParseInt(fields[0], 10, 64)
			switch k {
			case "MemTotal":
				m.MemTotalKB = n
			case "MemAvailable":
				m.MemAvailKB = n
			}
		}
	}
	return m
}
