// Package podman is a minimal client for Podman's native REST API over its
// unix socket — deliberately not the Docker-compat shim. Rookery only reads
// from it (host info, container counts); all mutations go through systemd.
package podman

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
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

func (c *Client) get(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://d/v5.0.0/libpod"+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("podman GET %s: %s", path, resp.Status)
	}
	return resp, nil
}

// Info queries /libpod/info. An error usually just means the Podman API
// socket service isn't enabled; callers should degrade gracefully.
func (c *Client) Info(ctx context.Context) (*Info, error) {
	resp, err := c.get(ctx, "/info")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
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

// ContainerSummary is one row of `podman ps --all`, as the import picker
// needs it.
type ContainerSummary struct {
	ID      string            `json:"Id"`
	Names   []string          `json:"Names"`
	Image   string            `json:"Image"`
	State   string            `json:"State"`
	IsInfra bool              `json:"IsInfra"`
	Labels  map[string]string `json:"Labels"`
}

// Name returns the primary container name.
func (c ContainerSummary) Name() string {
	if len(c.Names) > 0 {
		return c.Names[0]
	}
	return c.ID
}

// Managed reports whether the container already belongs to a systemd unit
// (Quadlet or podman generate systemd) and so needs no import.
func (c ContainerSummary) Managed() bool {
	_, ok := c.Labels["PODMAN_SYSTEMD_UNIT"]
	return ok
}

// Containers lists all containers, including stopped ones.
func (c *Client) Containers(ctx context.Context) ([]ContainerSummary, error) {
	resp, err := c.get(ctx, "/containers/json?all=true")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out []ContainerSummary
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// InspectContainer returns the raw inspect JSON for a container; the
// convert package extracts what it needs from it.
func (c *Client) InspectContainer(ctx context.Context, nameOrID string) ([]byte, error) {
	resp, err := c.get(ctx, "/containers/"+url.PathEscape(nameOrID)+"/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}
