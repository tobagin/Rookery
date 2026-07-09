// Package agent is the control-plane side of the rookery-agent connector: a
// thin HTTP client that reaches one agent, which speaks for one rootless (or
// system) podman/systemd scope on some host. It is the third transport beside
// local (this process's own scope) and rhost (ssh) — the one that needs no
// privilege crossing, because the agent already runs inside the target scope.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	api "github.com/rookerylabs/rookery-agent-api"
)

// Client talks to one rookery-agent.
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

// Info returns the scope identity and podman summary.
func (c *Client) Info(ctx context.Context) (api.Info, error) {
	var info api.Info
	err := c.do(ctx, http.MethodGet, api.PathInfo, &info)
	return info, err
}

// Units returns every Quadlet unit in the scope with live systemd status.
func (c *Client) Units(ctx context.Context) ([]api.Unit, error) {
	var units []api.Unit
	err := c.do(ctx, http.MethodGet, api.PathUnits, &units)
	return units, err
}

// Containers lists the scope's containers.
func (c *Client) Containers(ctx context.Context) ([]api.Container, error) {
	var cs []api.Container
	err := c.do(ctx, http.MethodGet, api.PathContainers, &cs)
	return cs, err
}

// Stats returns a live resource sample per container.
func (c *Client) Stats(ctx context.Context) ([]api.Stat, error) {
	var st []api.Stat
	err := c.do(ctx, http.MethodGet, api.PathStats, &st)
	return st, err
}

// Action runs a lifecycle verb (start/stop/restart/enable/disable) against a
// unit or service name in the scope.
func (c *Client) Action(ctx context.Context, unit, action string) error {
	if !api.ValidAction(action) {
		return fmt.Errorf("unknown action %q", action)
	}
	var res api.ActionResult
	if err := c.do(ctx, http.MethodPost, api.PathUnitsPrefix+unit+"/"+action, &res); err != nil {
		return err
	}
	if !res.OK {
		return fmt.Errorf("agent %s %s: %s", action, unit, res.Error)
	}
	return nil
}

// DaemonReload re-runs the scope's Quadlet generator.
func (c *Client) DaemonReload(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, api.PathDaemonReload, nil)
}
