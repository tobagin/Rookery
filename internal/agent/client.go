// Package agent is the control-plane side of the rookery-agent connector: a
// thin HTTP client that reaches one per-host agent. The agent serves every
// scope on its host (system + each user), so every call names a scope; the
// control plane discovers the scopes with Scopes() and turns each into a node
// area. This is the third transport beside local (this process's own scope)
// and rhost (ssh) — the one that needs no privilege crossing, because the
// agent already runs as root inside the target host.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	api "github.com/rookerylabs/rookery-agent-api"
)

// Client talks to one rookery-agent (one host).
type Client struct {
	base  string
	token string
	http  *http.Client
}

// New returns a client for the agent at baseURL (e.g. http://10.0.0.5:7666),
// authenticating with token.
func New(baseURL, token string) *Client {
	return &Client{
		base:  strings.TrimRight(baseURL, "/"),
		token: token,
		http:  &http.Client{Timeout: 10 * time.Second},
	}
}

// scoped appends ?scope=<id> to a path.
func scoped(path, scope string) string {
	return path + "?" + api.ScopeParam + "=" + url.QueryEscape(scope)
}

func (c *Client) do(ctx context.Context, method, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set(api.HeaderAuth, "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("agent %s %s: %s: %s", method, path, resp.Status, bytes.TrimSpace(body))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Scopes discovers the host's scopes (system + each user).
func (c *Client) Scopes(ctx context.Context) (api.HostInfo, error) {
	var info api.HostInfo
	err := c.do(ctx, http.MethodGet, api.PathScopes, &info)
	return info, err
}

// Units returns a scope's Quadlet units with live systemd status.
func (c *Client) Units(ctx context.Context, scope string) ([]api.Unit, error) {
	var units []api.Unit
	err := c.do(ctx, http.MethodGet, scoped(api.PathUnits, scope), &units)
	return units, err
}

// Containers lists a scope's containers.
func (c *Client) Containers(ctx context.Context, scope string) ([]api.Container, error) {
	var cs []api.Container
	err := c.do(ctx, http.MethodGet, scoped(api.PathContainers, scope), &cs)
	return cs, err
}

// Stats returns a live resource sample per container in a scope.
func (c *Client) Stats(ctx context.Context, scope string) ([]api.Stat, error) {
	var st []api.Stat
	err := c.do(ctx, http.MethodGet, scoped(api.PathStats, scope), &st)
	return st, err
}

// Resources lists a scope's live podman networks and volumes.
func (c *Client) Resources(ctx context.Context, scope string) ([]api.Resource, error) {
	var res []api.Resource
	err := c.do(ctx, http.MethodGet, scoped(api.PathResources, scope), &res)
	return res, err
}

// Metrics returns the agent host's health snapshot (host-level, no scope).
func (c *Client) Metrics(ctx context.Context) (api.HostMetrics, error) {
	var m api.HostMetrics
	err := c.do(ctx, http.MethodGet, api.PathMetrics, &m)
	return m, err
}

// GPUs returns the agent host's GPU inventory (host-level, no scope).
func (c *Client) GPUs(ctx context.Context) ([]api.GPUDevice, error) {
	var g []api.GPUDevice
	err := c.do(ctx, http.MethodGet, api.PathGPUs, &g)
	return g, err
}

// DeleteResource removes a network/volume/image from a scope's podman store.
func (c *Client) DeleteResource(ctx context.Context, scope, kind, name string) error {
	path := scoped(api.PathResources, scope) + "&kind=" + url.QueryEscape(kind) + "&name=" + url.QueryEscape(name)
	return c.do(ctx, http.MethodDelete, path, nil)
}

// InspectResource returns the raw podman inspect JSON for a resource in a scope.
func (c *Client) InspectResource(ctx context.Context, scope, kind, name string) ([]byte, error) {
	path := api.PathResources + "/inspect?" + api.ScopeParam + "=" + url.QueryEscape(scope) + "&kind=" + url.QueryEscape(kind) + "&name=" + url.QueryEscape(name)
	var raw json.RawMessage
	err := c.do(ctx, http.MethodGet, path, &raw)
	return raw, err
}

// Action runs a lifecycle verb against a unit or service in a scope.
func (c *Client) Action(ctx context.Context, scope, unit, action string) error {
	if !api.ValidAction(action) {
		return fmt.Errorf("unknown action %q", action)
	}
	var res api.ActionResult
	if err := c.do(ctx, http.MethodPost, scoped(api.PathUnitsPrefix+unit+"/"+action, scope), &res); err != nil {
		return err
	}
	if !res.OK {
		return fmt.Errorf("agent %s %s: %s", action, unit, res.Error)
	}
	return nil
}

// DaemonReload re-runs a scope's Quadlet generator.
func (c *Client) DaemonReload(ctx context.Context, scope string) error {
	return c.do(ctx, http.MethodPost, scoped(api.PathDaemonReload, scope), nil)
}

func (c *Client) raw(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set(api.HeaderAuth, "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("agent %s %s: %s: %s", method, path, resp.Status, bytes.TrimSpace(b))
	}
	return io.ReadAll(resp.Body)
}

// UnitFile returns the raw Quadlet file contents for a unit in a scope.
func (c *Client) UnitFile(ctx context.Context, scope, name string) ([]byte, error) {
	return c.raw(ctx, http.MethodGet, scoped(api.UnitFileURL(name), scope), nil)
}

// WriteUnitFile writes a unit's contents in a scope and daemon-reloads there.
func (c *Client) WriteUnitFile(ctx context.Context, scope, name string, data []byte) error {
	_, err := c.raw(ctx, http.MethodPut, scoped(api.UnitFileURL(name), scope), data)
	return err
}

// DeleteUnitFile removes a unit file in a scope and daemon-reloads there.
func (c *Client) DeleteUnitFile(ctx context.Context, scope, name string) error {
	_, err := c.raw(ctx, http.MethodDelete, scoped(api.UnitFileURL(name), scope), nil)
	return err
}

// Logs returns the journal tail for a unit in a scope.
func (c *Client) Logs(ctx context.Context, scope, name string, lines int, since string) (string, error) {
	q := url.Values{}
	q.Set(api.ScopeParam, scope)
	if lines > 0 {
		q.Set("lines", strconv.Itoa(lines))
	}
	if since != "" {
		q.Set("since", since)
	}
	b, err := c.raw(ctx, http.MethodGet, api.UnitLogsURL(name)+"?"+q.Encode(), nil)
	return string(b), err
}
