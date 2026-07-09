// Package podman is a minimal client for Podman's native REST API over its
// unix socket — deliberately not the Docker-compat shim. Rookery only reads
// from it (host info, container counts); all mutations go through systemd.
package podman

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
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
	// pulls share the transport but need a far longer deadline than
	// metadata queries — image pulls legitimately take minutes.
	pullHTTP *http.Client
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
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", path)
		},
	}
	return &Client{
		http:     &http.Client{Timeout: 10 * time.Second, Transport: transport},
		pullHTTP: &http.Client{Timeout: 15 * time.Minute, Transport: transport},
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

type ContainerStats struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	CPUPct   float64 `json:"cpuPct"`
	MemBytes int64   `json:"memBytes"`
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

func (c *Client) Stats(ctx context.Context) ([]ContainerStats, error) {
	resp, err := c.get(ctx, "/containers/stats?stream=false")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var raw []map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	out := []ContainerStats{}
	for _, row := range raw {
		out = append(out, ContainerStats{
			ID:       firstString(row, "ContainerID", "ID", "Id", "id"),
			Name:     firstString(row, "Name", "name"),
			CPUPct:   percentValue(row["CPU"], row["CPUPerc"], row["cpu_percent"]),
			MemBytes: int64(numberValue(row["MemUsage"], row["MemUsageBytes"], row["mem_usage"])),
		})
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

func (c *Client) StopContainer(ctx context.Context, nameOrID string) error {
	u := "http://d/v5.0.0/libpod/containers/" + url.PathEscape(nameOrID) + "/stop"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusNotModified {
		return fmt.Errorf("podman stop %s: %s: %s", nameOrID, resp.Status, apiErrorBody(resp.Body))
	}
	return nil
}

func firstString(row map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := row[k].(string); ok {
			return v
		}
	}
	return ""
}

func percentValue(values ...any) float64 {
	for _, v := range values {
		switch x := v.(type) {
		case float64:
			return x
		case string:
			x = strings.TrimSpace(strings.TrimSuffix(x, "%"))
			f, _ := strconv.ParseFloat(x, 64)
			return f
		}
	}
	return 0
}

func numberValue(values ...any) float64 {
	for _, v := range values {
		switch x := v.(type) {
		case float64:
			return x
		case string:
			parts := strings.Split(x, "/")
			fields := strings.Fields(strings.TrimSpace(parts[0]))
			if len(fields) == 0 {
				return 0
			}
			f, _ := strconv.ParseFloat(fields[0], 64)
			unit := ""
			if len(fields) > 1 {
				unit = strings.ToUpper(strings.TrimSuffix(fields[1], "B"))
			}
			switch unit {
			case "K", "KI", "KB", "KIB":
				f *= 1024
			case "M", "MI", "MB", "MIB":
				f *= 1024 * 1024
			case "G", "GI", "GB", "GIB":
				f *= 1024 * 1024 * 1024
			}
			return f
		}
	}
	return 0
}

// ImageDigests returns every digest the local store associates with ref
// (RepoDigests plus the image digest) for drift comparison against the
// registry. An error usually means the image isn't pulled yet.
func (c *Client) ImageDigests(ctx context.Context, ref string) ([]string, error) {
	resp, err := c.get(ctx, "/images/"+url.PathEscape(ref)+"/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var raw struct {
		Digest      string   `json:"Digest"`
		RepoDigests []string `json:"RepoDigests"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	digests := raw.RepoDigests
	if raw.Digest != "" {
		digests = append(digests, raw.Digest)
	}
	return digests, nil
}

// StaleImages reports the dangling images old updates leave behind:
// how many, and how many bytes pruning would reclaim.
func (c *Client) StaleImages(ctx context.Context) (count int, size int64, err error) {
	resp, err := c.get(ctx, "/images/json?filters="+url.QueryEscape(`{"dangling":["true"]}`))
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()
	var raw []struct {
		Size int64 `json:"Size"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return 0, 0, err
	}
	for _, img := range raw {
		size += img.Size
	}
	return len(raw), size, nil
}

// PruneImages removes dangling images and returns the bytes reclaimed.
func (c *Client) PruneImages(ctx context.Context) (int64, error) {
	u := "http://d/v5.0.0/libpod/images/prune?filters=" + url.QueryEscape(`{"dangling":["true"]}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return 0, err
	}
	resp, err := c.pullHTTP.Do(req) // pruning many layers can be slow
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return 0, fmt.Errorf("podman image prune: %s: %s", resp.Status, apiErrorBody(resp.Body))
	}
	var raw []struct {
		Size int64  `json:"Size"`
		Err  string `json:"Err"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return 0, err
	}
	var reclaimed int64
	for _, r := range raw {
		if r.Err == "" {
			reclaimed += r.Size
		}
	}
	return reclaimed, nil
}

// Secret is one podman secret as the secrets page lists it. Values are
// write-only — the API deliberately never returns secret data.
type Secret struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Driver    string `json:"driver"`
	CreatedAt string `json:"createdAt"`
}

// Secrets lists the host's podman secrets.
func (c *Client) Secrets(ctx context.Context) ([]Secret, error) {
	resp, err := c.get(ctx, "/secrets/json")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var raw []struct {
		ID        string `json:"ID"`
		CreatedAt string `json:"CreatedAt"`
		Spec      struct {
			Name   string `json:"Name"`
			Driver struct {
				Name string `json:"Name"`
			} `json:"Driver"`
		} `json:"Spec"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	out := []Secret{}
	for _, s := range raw {
		out = append(out, Secret{ID: s.ID, Name: s.Spec.Name, Driver: s.Spec.Driver.Name, CreatedAt: s.CreatedAt})
	}
	return out, nil
}

// CreateSecret stores data under name. Podman rejects duplicates.
func (c *Client) CreateSecret(ctx context.Context, name string, data []byte) error {
	u := "http://d/v5.0.0/libpod/secrets/create?name=" + url.QueryEscape(name)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(data))
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("podman secret create %s: %s: %s", name, resp.Status, apiErrorBody(resp.Body))
	}
	return nil
}

// RemoveSecret deletes the named secret.
func (c *Client) RemoveSecret(ctx context.Context, name string) error {
	u := "http://d/v5.0.0/libpod/secrets/" + url.PathEscape(name)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("podman secret rm %s: %s: %s", name, resp.Status, apiErrorBody(resp.Body))
	}
	return nil
}

// apiErrorBody extracts libpod's error message from a failed response.
func apiErrorBody(r io.Reader) string {
	var e struct {
		Message string `json:"message"`
		Cause   string `json:"cause"`
	}
	if err := json.NewDecoder(r).Decode(&e); err != nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	return e.Cause
}

// PullImage pulls ref through the Podman API and waits for completion.
func (c *Client) PullImage(ctx context.Context, ref string) error {
	u := "http://d/v5.0.0/libpod/images/pull?reference=" + url.QueryEscape(ref) + "&quiet=true"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	if err != nil {
		return err
	}
	resp, err := c.pullHTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("podman pull %s: %s", ref, resp.Status)
	}
	// The body is a stream of JSON progress objects; any "error" entry
	// means the pull failed even though the HTTP status was 200.
	dec := json.NewDecoder(resp.Body)
	for dec.More() {
		var msg struct {
			Error string `json:"error"`
		}
		if err := dec.Decode(&msg); err != nil {
			break
		}
		if msg.Error != "" {
			return fmt.Errorf("podman pull %s: %s", ref, msg.Error)
		}
	}
	return nil
}
