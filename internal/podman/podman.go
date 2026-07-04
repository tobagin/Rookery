// Package podman is a minimal client for Podman's native REST API over its
// unix socket — deliberately not the Docker-compat shim. Rookery only reads
// from it (host info, container counts); all mutations go through systemd.
package podman

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

// Info is the subset of `podman info` the dashboard shows.
type Info struct {
	Version           string `json:"version"`
	ContainersRunning int    `json:"containersRunning"`
	ContainersTotal   int    `json:"containersTotal"`
}

// Client talks to one Podman socket.
type Client struct {
	http *http.Client
}

// SocketPath returns the conventional native socket location for this
// process's privilege level.
func SocketPath() string {
	if os.Geteuid() == 0 {
		return "/run/podman/podman.sock"
	}
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return dir + "/podman/podman.sock"
	}
	return fmt.Sprintf("/run/user/%d/podman/podman.sock", os.Getuid())
}

// New returns a client for the socket at path.
func New(path string) *Client {
	return &Client{
		http: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "unix", path)
				},
			},
		},
	}
}

// Info queries /libpod/info. An error usually just means the Podman API
// socket service isn't enabled; callers should degrade gracefully.
func (c *Client) Info(ctx context.Context) (*Info, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://d/v5.0.0/libpod/info", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("podman info: %s", resp.Status)
	}
	var raw struct {
		Version struct {
			Version string `json:"Version"`
		} `json:"version"`
		Store struct {
			ContainerStore struct {
				Number  int `json:"number"`
				Running int `json:"running"`
			} `json:"containerStore"`
		} `json:"store"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	return &Info{
		Version:           raw.Version.Version,
		ContainersRunning: raw.Store.ContainerStore.Running,
		ContainersTotal:   raw.Store.ContainerStore.Number,
	}, nil
}
