// Package registry resolves the current manifest digest of an image tag
// straight from its registry (Docker Registry HTTP API v2 / OCI dist-spec),
// so Rookery can flag digest drift — "the tag you run moved" — without
// pulling anything. Anonymous bearer-token auth covers the public
// registries (docker.io, ghcr.io, quay.io, lscr.io).
package registry

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

const acceptManifests = "application/vnd.docker.distribution.manifest.list.v2+json, " +
	"application/vnd.oci.image.index.v1+json, " +
	"application/vnd.docker.distribution.manifest.v2+json, " +
	"application/vnd.oci.image.manifest.v1+json"

// Ref is a parsed image reference.
type Ref struct {
	Host string // registry host, e.g. registry-1.docker.io
	Repo string // repository path, e.g. library/nginx
	Tag  string
}

// ParseRef normalizes an image reference the way Podman does: a first
// component with a dot/colon (or "localhost") is a registry host, docker.io
// single-name images get the library/ prefix, and the default tag is
// latest. References pinned by digest are rejected — they cannot drift.
func ParseRef(image string) (Ref, error) {
	if strings.Contains(image, "@") {
		return Ref{}, fmt.Errorf("image %q is pinned by digest and cannot drift", image)
	}
	host := "docker.io"
	rest := image
	if first, tail, ok := strings.Cut(image, "/"); ok &&
		(strings.ContainsAny(first, ".:") || first == "localhost") {
		host, rest = first, tail
	}
	repo, tag := rest, "latest"
	// The tag colon is the one after the last slash.
	if i := strings.LastIndex(rest, ":"); i > strings.LastIndex(rest, "/") {
		repo, tag = rest[:i], rest[i+1:]
	}
	if repo == "" {
		return Ref{}, fmt.Errorf("image %q has no repository", image)
	}
	if host == "docker.io" {
		if !strings.Contains(repo, "/") {
			repo = "library/" + repo
		}
		host = "registry-1.docker.io"
	}
	return Ref{Host: host, Repo: repo, Tag: tag}, nil
}

// scheme returns http only for loopback registries (the common dev/test
// setup); everything else must be TLS.
func (r Ref) scheme() string {
	host := r.Host
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	if host == "localhost" || host == "127.0.0.1" || host == "::1" {
		return "http"
	}
	return "https"
}

// Client resolves digests with anonymous bearer-token auth.
type Client struct {
	http *http.Client
}

func NewClient() *Client {
	return &Client{http: &http.Client{Timeout: 20 * time.Second}}
}

// ResolveDigest returns the manifest digest the registry currently serves
// for the image's tag.
func (c *Client) ResolveDigest(ctx context.Context, image string) (string, error) {
	return c.ResolveDigestWithBasic(ctx, image, "", "")
}

func (c *Client) ResolveDigestWithBasic(ctx context.Context, image, username, password string) (string, error) {
	ref, err := ParseRef(image)
	if err != nil {
		return "", err
	}
	url := fmt.Sprintf("%s://%s/v2/%s/manifests/%s", ref.scheme(), ref.Host, ref.Repo, ref.Tag)

	digest, status, challenge, err := c.headManifest(ctx, url, "", username, password)
	if err != nil {
		return "", err
	}
	if status == http.StatusUnauthorized && challenge != "" {
		token, err := c.fetchToken(ctx, challenge, ref.Repo, username, password)
		if err != nil {
			return "", err
		}
		digest, status, _, err = c.headManifest(ctx, url, token, "", "")
		if err != nil {
			return "", err
		}
	}
	if status == http.StatusUnauthorized && username != "" {
		return "", fmt.Errorf("auth failed for %s", ref.Host)
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("registry %s: status %d for %s/%s:%s", ref.Host, status, ref.Host, ref.Repo, ref.Tag)
	}
	if digest == "" {
		return "", fmt.Errorf("registry %s returned no Docker-Content-Digest header", ref.Host)
	}
	return digest, nil
}

func (c *Client) headManifest(ctx context.Context, url, token, username, password string) (digest string, status int, challenge string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return "", 0, "", err
	}
	req.Header.Set("Accept", acceptManifests)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	} else if username != "" {
		req.SetBasicAuth(username, password)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", 0, "", err
	}
	defer resp.Body.Close()
	return resp.Header.Get("Docker-Content-Digest"), resp.StatusCode, resp.Header.Get("WWW-Authenticate"), nil
}

// fetchToken performs the anonymous bearer-token dance described by the
// WWW-Authenticate challenge.
func (c *Client) fetchToken(ctx context.Context, challenge, repo, username, password string) (string, error) {
	params := parseChallenge(challenge)
	realm := params["realm"]
	if realm == "" {
		return "", fmt.Errorf("unsupported auth challenge %q", challenge)
	}
	url := realm + "?scope=repository:" + repo + ":pull"
	if service := params["service"]; service != "" {
		url += "&service=" + service
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	if username != "" {
		req.SetBasicAuth(username, password)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token endpoint %s: status %d", realm, resp.StatusCode)
	}
	var body struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	if body.Token != "" {
		return body.Token, nil
	}
	return body.AccessToken, nil
}

func BasicFromAuthFiles(host string, paths []string) (string, string, bool) {
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var raw struct {
			Auths map[string]struct {
				Auth     string `json:"auth"`
				Username string `json:"username"`
				Password string `json:"password"`
			} `json:"auths"`
		}
		if json.Unmarshal(data, &raw) != nil {
			continue
		}
		for _, key := range []string{host, "https://" + host, "http://" + host} {
			entry, ok := raw.Auths[key]
			if !ok {
				continue
			}
			if entry.Username != "" {
				return entry.Username, entry.Password, true
			}
			if entry.Auth != "" {
				decoded, err := base64.StdEncoding.DecodeString(entry.Auth)
				if err != nil {
					continue
				}
				user, pass, ok := strings.Cut(string(decoded), ":")
				if ok {
					return user, pass, true
				}
			}
		}
	}
	return "", "", false
}

// parseChallenge reads `Bearer realm="...",service="..."` into a map.
func parseChallenge(header string) map[string]string {
	out := map[string]string{}
	header = strings.TrimPrefix(header, "Bearer ")
	for _, part := range strings.Split(header, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		out[k] = strings.Trim(v, `"`)
	}
	return out
}
