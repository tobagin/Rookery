// Package hostinfo reads the small set of host metrics the dashboard strip
// shows, straight from /proc — no exporters, no polling daemon.
package hostinfo

import (
	"os"
	"strconv"
	"strings"
)

// Metrics is a point-in-time snapshot of host health.
type Metrics struct {
	Hostname      string  `json:"hostname"`
	Kernel        string  `json:"kernel"`
	Load1         float64 `json:"load1"`
	MemTotalKB    int64   `json:"memTotalKb"`
	MemAvailKB    int64   `json:"memAvailKb"`
	UptimeSeconds int64   `json:"uptimeSeconds"`
}

// Read gathers metrics; fields it cannot read are left zero rather than
// failing the whole snapshot.
func Read() Metrics {
	var m Metrics
	m.Hostname, _ = os.Hostname()
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
