// Package oidc implements the small slice of OpenID Connect Rookery needs:
// discovery, authorization-code exchange, and RS256 ID-token verification.
package oidc

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	RoleAdmin  = "admin"
	RoleViewer = "viewer"
)

// Config is an OIDC relying-party configuration.
type Config struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	Scopes       []string
	ProviderName string
	DefaultRole  string
	AdminUsers   []string
	AdminGroups  []string

	HTTPClient *http.Client
	Now        func() time.Time
}

func (c Config) Enabled() bool {
	return c.Issuer != "" && c.ClientID != "" && c.ClientSecret != ""
}

func (c Config) Validate() error {
	if c.Issuer != "" || c.ClientID != "" || c.ClientSecret != "" {
		if c.Issuer == "" || c.ClientID == "" || c.ClientSecret == "" {
			return errors.New("oidc issuer, client ID, and client secret must be set together")
		}
	}
	if !c.Enabled() {
		return nil
	}
	if _, err := url.ParseRequestURI(c.Issuer); err != nil {
		return fmt.Errorf("oidc issuer: %w", err)
	}
	if c.DefaultRole == "" {
		return nil
	}
	if c.DefaultRole != RoleAdmin && c.DefaultRole != RoleViewer {
		return fmt.Errorf("oidc default role must be %s or %s", RoleAdmin, RoleViewer)
	}
	return nil
}

// Client is safe for concurrent use.
type Client struct {
	cfg Config

	mu         sync.Mutex
	disco      discovery
	discoUntil time.Time
	jwks       jwks
	jwksUntil  time.Time
}

type discovery struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
	Issuer                string `json:"issuer"`
}

type jwks struct {
	Keys []jwk `json:"keys"`
}

type jwk struct {
	Kty string   `json:"kty"`
	Use string   `json:"use"`
	Kid string   `json:"kid"`
	Alg string   `json:"alg"`
	N   string   `json:"n"`
	E   string   `json:"e"`
	X5C []string `json:"x5c"`
}

// Claims contains the identity fields Rookery cares about.
type Claims struct {
	Subject           string   `json:"sub"`
	Issuer            string   `json:"iss"`
	Audience          any      `json:"aud"`
	AuthorizedParty   string   `json:"azp"`
	Expiry            int64    `json:"exp"`
	NotBefore         int64    `json:"nbf"`
	IssuedAt          int64    `json:"iat"`
	Nonce             string   `json:"nonce"`
	Email             string   `json:"email"`
	PreferredUsername string   `json:"preferred_username"`
	Name              string   `json:"name"`
	Groups            []string `json:"groups"`
	Role              string   `json:"-"`
	Username          string   `json:"-"`
}

func New(cfg Config) (*Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if !cfg.Enabled() {
		return nil, nil
	}
	cfg.Issuer = strings.TrimRight(cfg.Issuer, "/")
	if len(cfg.Scopes) == 0 {
		cfg.Scopes = []string{"openid", "email", "profile"}
	}
	if cfg.ProviderName == "" {
		cfg.ProviderName = "SSO"
	}
	if cfg.DefaultRole == "" {
		cfg.DefaultRole = RoleViewer
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = http.DefaultClient
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Client{cfg: cfg}, nil
}

func (c *Client) ProviderName() string { return c.cfg.ProviderName }

func (c *Client) RedirectURL() string { return c.cfg.RedirectURL }

func (c *Client) AuthCodeURL(ctx context.Context, redirectURL, state, nonce string) (string, error) {
	if redirectURL == "" {
		redirectURL = c.cfg.RedirectURL
	}
	d, err := c.discovery(ctx)
	if err != nil {
		return "", err
	}
	v := url.Values{
		"client_id":     {c.cfg.ClientID},
		"redirect_uri":  {redirectURL},
		"response_type": {"code"},
		"scope":         {strings.Join(c.cfg.Scopes, " ")},
		"state":         {state},
		"nonce":         {nonce},
	}
	return d.AuthorizationEndpoint + "?" + v.Encode(), nil
}

func (c *Client) Exchange(ctx context.Context, code, redirectURL, nonce string) (Claims, error) {
	if redirectURL == "" {
		redirectURL = c.cfg.RedirectURL
	}
	d, err := c.discovery(ctx)
	if err != nil {
		return Claims{}, err
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURL},
		"client_id":     {c.cfg.ClientID},
		"client_secret": {c.cfg.ClientSecret},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return Claims{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	res, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return Claims{}, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		return Claims{}, fmt.Errorf("oidc token endpoint returned %s: %s", res.Status, strings.TrimSpace(string(body)))
	}
	var tok struct {
		IDToken string `json:"id_token"`
	}
	if err := json.NewDecoder(res.Body).Decode(&tok); err != nil {
		return Claims{}, err
	}
	if tok.IDToken == "" {
		return Claims{}, errors.New("oidc token response did not include an id_token")
	}
	claims, err := c.VerifyIDToken(ctx, tok.IDToken, nonce)
	if err != nil {
		return Claims{}, err
	}
	claims.Username = claims.username()
	claims.Role = c.roleFor(claims)
	return claims, nil
}

func (c *Client) VerifyIDToken(ctx context.Context, raw, nonce string) (Claims, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return Claims{}, errors.New("id_token must have three JWT parts")
	}
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Claims{}, fmt.Errorf("decode id_token header: %w", err)
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return Claims{}, fmt.Errorf("parse id_token header: %w", err)
	}
	if header.Alg != "RS256" {
		return Claims{}, fmt.Errorf("unsupported id_token alg %q", header.Alg)
	}
	key, err := c.key(ctx, header.Kid)
	if err != nil {
		return Claims{}, err
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return Claims{}, fmt.Errorf("decode id_token signature: %w", err)
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], sig); err != nil {
		return Claims{}, fmt.Errorf("verify id_token signature: %w", err)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Claims{}, fmt.Errorf("decode id_token payload: %w", err)
	}
	var claims Claims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return Claims{}, fmt.Errorf("parse id_token claims: %w", err)
	}
	if err := c.validateClaims(claims, nonce); err != nil {
		return Claims{}, err
	}
	return claims, nil
}

func (c *Client) validateClaims(claims Claims, nonce string) error {
	if !issuerEqual(claims.Issuer, c.cfg.Issuer) {
		return fmt.Errorf("id_token issuer %q does not match %q", claims.Issuer, c.cfg.Issuer)
	}
	if claims.Subject == "" {
		return errors.New("id_token has no subject")
	}
	if !audienceContains(claims.Audience, c.cfg.ClientID) {
		return errors.New("id_token audience does not include this client")
	}
	if audCount(claims.Audience) > 1 && claims.AuthorizedParty != c.cfg.ClientID {
		return errors.New("id_token authorized party does not match this client")
	}
	now := c.cfg.Now().Unix()
	if claims.Expiry == 0 || now > claims.Expiry+60 {
		return errors.New("id_token is expired")
	}
	if claims.NotBefore != 0 && now+60 < claims.NotBefore {
		return errors.New("id_token is not valid yet")
	}
	if nonce != "" && subtle.ConstantTimeCompare([]byte(claims.Nonce), []byte(nonce)) != 1 {
		return errors.New("id_token nonce mismatch")
	}
	return nil
}

func (c *Client) roleFor(claims Claims) string {
	matches := func(values, allowed []string) bool {
		for _, v := range values {
			for _, a := range allowed {
				if strings.EqualFold(strings.TrimSpace(v), strings.TrimSpace(a)) && strings.TrimSpace(a) != "" {
					return true
				}
			}
		}
		return false
	}
	if matches([]string{claims.Subject, claims.Email, claims.PreferredUsername}, c.cfg.AdminUsers) ||
		matches(claims.Groups, c.cfg.AdminGroups) {
		return RoleAdmin
	}
	return c.cfg.DefaultRole
}

func (c Claims) username() string {
	for _, v := range []string{c.PreferredUsername, c.Email, c.Name, c.Subject} {
		v = strings.TrimSpace(v)
		if v != "" {
			return v
		}
	}
	return "oidc-user"
}

func audienceContains(aud any, clientID string) bool {
	switch v := aud.(type) {
	case string:
		return v == clientID
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && s == clientID {
				return true
			}
		}
	}
	return false
}

func audCount(aud any) int {
	switch v := aud.(type) {
	case string:
		if v == "" {
			return 0
		}
		return 1
	case []any:
		return len(v)
	}
	return 0
}

func (c *Client) discovery(ctx context.Context) (discovery, error) {
	c.mu.Lock()
	if c.disco.AuthorizationEndpoint != "" && c.cfg.Now().Before(c.discoUntil) {
		d := c.disco
		c.mu.Unlock()
		return d, nil
	}
	c.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.Issuer+"/.well-known/openid-configuration", nil)
	if err != nil {
		return discovery{}, err
	}
	res, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return discovery{}, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return discovery{}, fmt.Errorf("oidc discovery returned %s", res.Status)
	}
	var d discovery
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&d); err != nil {
		return discovery{}, err
	}
	if !issuerEqual(d.Issuer, c.cfg.Issuer) {
		return discovery{}, fmt.Errorf("oidc discovery issuer %q does not match %q", d.Issuer, c.cfg.Issuer)
	}
	if d.AuthorizationEndpoint == "" || d.TokenEndpoint == "" || d.JWKSURI == "" {
		return discovery{}, errors.New("oidc discovery document is missing required endpoints")
	}
	c.mu.Lock()
	c.disco, c.discoUntil = d, c.cfg.Now().Add(time.Hour)
	c.mu.Unlock()
	return d, nil
}

func (c *Client) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	set, err := c.keys(ctx)
	if err != nil {
		return nil, err
	}
	for _, k := range set.Keys {
		if kid != "" && k.Kid != kid {
			continue
		}
		if k.Kty != "RSA" || (k.Use != "" && k.Use != "sig") || (k.Alg != "" && k.Alg != "RS256") {
			continue
		}
		return rsaKey(k)
	}
	if kid != "" {
		c.mu.Lock()
		c.jwksUntil = time.Time{}
		c.mu.Unlock()
		set, err = c.keys(ctx)
		if err != nil {
			return nil, err
		}
		for _, k := range set.Keys {
			if k.Kid == kid && k.Kty == "RSA" {
				return rsaKey(k)
			}
		}
	}
	return nil, fmt.Errorf("no matching RSA signing key for kid %q", kid)
}

func (c *Client) keys(ctx context.Context) (jwks, error) {
	c.mu.Lock()
	if len(c.jwks.Keys) > 0 && c.cfg.Now().Before(c.jwksUntil) {
		set := c.jwks
		c.mu.Unlock()
		return set, nil
	}
	c.mu.Unlock()

	d, err := c.discovery(ctx)
	if err != nil {
		return jwks{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.JWKSURI, nil)
	if err != nil {
		return jwks{}, err
	}
	res, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return jwks{}, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode > 299 {
		return jwks{}, fmt.Errorf("oidc jwks returned %s", res.Status)
	}
	var set jwks
	if err := json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&set); err != nil {
		return jwks{}, err
	}
	c.mu.Lock()
	c.jwks, c.jwksUntil = set, c.cfg.Now().Add(time.Hour)
	c.mu.Unlock()
	return set, nil
}

func rsaKey(k jwk) (*rsa.PublicKey, error) {
	if len(k.X5C) > 0 {
		der, err := base64.StdEncoding.DecodeString(k.X5C[0])
		if err != nil {
			return nil, err
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, err
		}
		pub, ok := cert.PublicKey.(*rsa.PublicKey)
		if !ok {
			return nil, errors.New("x5c certificate is not RSA")
		}
		return pub, nil
	}
	nb, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, err
	}
	eb, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, err
	}
	e := 0
	for _, b := range eb {
		e = e<<8 + int(b)
	}
	if e == 0 {
		return nil, errors.New("RSA exponent is zero")
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nb), E: e}, nil
}

func issuerEqual(a, b string) bool {
	return strings.TrimRight(a, "/") == strings.TrimRight(b, "/")
}

func RandomToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}
