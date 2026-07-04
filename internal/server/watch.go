package server

import (
	"context"
	"fmt"
	"time"

	"github.com/tobagin/rookery/internal/quadlet"
)

// WatchFailures polls every area's unit states and calls notify when a
// unit transitions into or out of "failed". The first poll only takes a
// baseline — restarting Rookery must not re-announce a long-dead unit.
func (s *Server) WatchFailures(ctx context.Context, interval time.Duration, notify func(title, message string)) {
	prev := map[string]string{}
	first := true
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		s.pollFailures(ctx, prev, first, notify)
		first = false
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) pollFailures(ctx context.Context, prev map[string]string, baseline bool, notify func(title, message string)) {
	for _, area := range s.areas {
		found, err := discoverArea(ctx, area)
		if err != nil {
			continue // an unreachable scope is not a unit failure
		}
		services := make([]string, len(found))
		for i, d := range found {
			services[i], _ = quadlet.ServiceName(d.unit.Name)
		}
		statuses, err := s.sysd.Status(ctx, area.Scope, services)
		if err != nil {
			continue
		}
		for i, d := range found {
			key := area.Label + "/" + d.unit.Name
			state := statuses[i].Active
			was := prev[key]
			prev[key] = state
			if baseline {
				continue
			}
			switch {
			case state == "failed" && was != "failed" && was != "":
				msg := fmt.Sprintf("%s (scope %s) failed", d.unit.Name, area.Label)
				if statuses[i].ExitCode != 0 {
					msg += fmt.Sprintf(" — exit code %d", statuses[i].ExitCode)
				}
				if statuses[i].Restarts > 0 {
					msg += fmt.Sprintf(", %d restarts", statuses[i].Restarts)
				}
				notify("Rookery: unit failed", msg)
			case state == "active" && was == "failed":
				notify("Rookery: unit recovered", fmt.Sprintf("%s (scope %s) is running again", d.unit.Name, area.Label))
			}
		}
	}
}
