// Package server exposes the HTTP API and embedded web UI. It holds no
// state of its own: every request re-reads unit files from disk and unit
// state from systemd, so the browser and `ssh + systemctl` always agree.
package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/tobagin/rookery/internal/gitstore"
	"github.com/tobagin/rookery/internal/gpu"
	"github.com/tobagin/rookery/internal/oidc"
	"github.com/tobagin/rookery/internal/podman"
	"github.com/tobagin/rookery/internal/quadlet"
	"github.com/tobagin/rookery/internal/registry"
	"github.com/tobagin/rookery/internal/rhost"
	"github.com/tobagin/rookery/internal/systemd"
	"github.com/tobagin/rookery/internal/userstore"
	"github.com/tobagin/rookery/web"
)

// Systemd is the slice of systemd.Manager the handlers use; tests provide
// a fake.
type Systemd interface {
	Start(ctx context.Context, scope systemd.Scope, unit string) error
	Stop(ctx context.Context, scope systemd.Scope, unit string) error
	Restart(ctx context.Context, scope systemd.Scope, unit string) error
	Enable(ctx context.Context, scope systemd.Scope, unit string) error
	Disable(ctx context.Context, scope systemd.Scope, unit string) error
	DaemonReload(ctx context.Context, scope systemd.Scope) error
	Status(ctx context.Context, scope systemd.Scope, units []string) ([]systemd.UnitStatus, error)
}

// ValidateFunc validates a candidate unit file; quadlet.Validate in
// production.
type ValidateFunc func(ctx context.Context, userScope bool, fileName, content string) (quadlet.ValidationResult, error)

// Podman is the slice of the Podman API the server uses; podman.Client in
// production, a fake in tests. A nil Podman disables the dependent
// features (host panel, import, update checks).
type Podman interface {
	Info(ctx context.Context) (*podman.Info, error)
	Containers(ctx context.Context) ([]podman.ContainerSummary, error)
	Stats(ctx context.Context) ([]podman.ContainerStats, error)
	InspectContainer(ctx context.Context, nameOrID string) ([]byte, error)
	StopContainer(ctx context.Context, nameOrID string) error
	ImageDigests(ctx context.Context, ref string) ([]string, error)
	PullImage(ctx context.Context, ref string) error
}

// Area is one set of Quadlet directories managed under one systemd scope:
// the system, or a single user. Its label is the scope path segment in the
// API ("system" or the username).
type Area struct {
	Label string
	// NodeID groups multiple areas that belong to the same physical host.
	// Empty means local areas group as "local" and remote areas group by
	// their SSH target, preserving the original one-alias-one-node behavior.
	NodeID string
	Scope  systemd.Scope
	// Dirs is the Quadlet search path; Dirs[0] is primary — new units are
	// created there, and units found elsewhere are read-only.
	Dirs []string
	// Git, when set, records every save/delete in the primary dir's
	// repository and serves history/rollback. Local areas only.
	Git *gitstore.Store
}

// Remote reports whether this area's files and systemd live on another
// host, reached over ssh (Scope.SSH carries the target).
func (a Area) Remote() bool { return a.Scope.IsRemote() }

// Options configures a Server; zero-value fields get safe defaults.
type Options struct {
	Areas    []Area
	Systemd  Systemd
	Validate ValidateFunc
	Podman   Podman // nil disables the Podman panel, import, and updates
	Version  string
	Password string // legacy single admin password; empty defers to Users
	// Users is the on-disk account store; an empty store (plus no legacy
	// Password) triggers the first-run setup wizard. nil disables accounts.
	Users *userstore.Store
	// DisablePasswordLogin turns off /api/login for local accounts and the
	// legacy single password. It is intended for OIDC-only deployments.
	DisablePasswordLogin bool
	// OIDC enables browser SSO alongside local accounts.
	OIDC *oidc.Client
	// SessionTTL is the idle timeout for login sessions (sliding); zero
	// means 24h.
	SessionTTL time.Duration
	// Settings describes effective deployment/auth/runtime settings for the
	// admin settings API.
	Settings []SettingGroup
	SELinux  func() bool // nil -> detect on the host
	// GPUs enumerates host GPUs; nil -> gpu.Detect. Injectable for tests.
	GPUs func(ctx context.Context) []gpu.Device
	// ResolveDigest fetches an image tag's current registry digest;
	// nil -> a real registry client. Injectable for tests.
	ResolveDigest func(ctx context.Context, image string) (string, error)
	// RemoteDigests / RemotePull / RemoteGPUs run the remote-host halves of
	// update checks and GPU inventory; nil -> rhost over ssh. Injectable
	// for tests.
	RemoteDigests func(ctx context.Context, target string, userSession bool, image string) ([]string, error)
	RemotePull    func(ctx context.Context, target string, userSession bool, image string) error
	RemoteGPUs    func(ctx context.Context, target string) []gpu.Device
	AlertTest     func(ctx context.Context) error
}

// Server routes API and UI requests.
type Server struct {
	areasMu       sync.RWMutex
	areas         []Area
	sysd          Systemd
	validate      ValidateFunc
	pod           Podman
	resolve       func(ctx context.Context, image string) (string, error)
	remoteDigests func(ctx context.Context, target string, userSession bool, image string) ([]string, error)
	remotePull    func(ctx context.Context, target string, userSession bool, image string) error
	remoteGPUs    func(ctx context.Context, target string) []gpu.Device
	alertTest     func(ctx context.Context) error
	version       string
	password      string
	users         *userstore.Store
	noPassword    bool
	oidc          *oidc.Client
	oidcStates    *oidcStates
	selinux       func() bool
	gpus          func(ctx context.Context) []gpu.Device
	sess          *sessions
	settings      []SettingGroup
	mux           *http.ServeMux
}

// New builds the Server and its routes.
func New(opts Options) *Server {
	s := &Server{
		areas:         opts.Areas,
		sysd:          opts.Systemd,
		validate:      opts.Validate,
		pod:           opts.Podman,
		version:       opts.Version,
		password:      opts.Password,
		users:         opts.Users,
		noPassword:    opts.DisablePasswordLogin,
		oidc:          opts.OIDC,
		oidcStates:    newOIDCStates(),
		selinux:       opts.SELinux,
		gpus:          opts.GPUs,
		resolve:       opts.ResolveDigest,
		remoteDigests: opts.RemoteDigests,
		remotePull:    opts.RemotePull,
		remoteGPUs:    opts.RemoteGPUs,
		alertTest:     opts.AlertTest,
		sess:          newSessions(opts.SessionTTL),
		settings:      opts.Settings,
		mux:           http.NewServeMux(),
	}
	if s.validate == nil {
		s.validate = quadlet.Validate
	}
	if s.selinux == nil {
		s.selinux = quadlet.SELinuxEnforcing
	}
	if s.gpus == nil {
		s.gpus = gpu.Detect
	}
	if s.resolve == nil {
		s.resolve = registry.NewClient().ResolveDigest
	}
	if s.remoteDigests == nil {
		s.remoteDigests = rhost.ImageDigests
	}
	if s.remotePull == nil {
		s.remotePull = rhost.PullImage
	}
	if s.remoteGPUs == nil {
		s.remoteGPUs = func(ctx context.Context, target string) []gpu.Device {
			out, err := rhost.Run(ctx, target, gpu.RemoteProbeScript, nil)
			if err != nil && out == "" {
				return nil
			}
			return gpu.ParseRemoteProbe(out)
		}
	}
	if s.users != nil {
		s.sess.useDB(s.users.DB())
	}
	s.mux.HandleFunc("GET /api/auth", s.handleAuthStatus)
	s.mux.HandleFunc("GET /metrics", s.handleMetrics)
	s.mux.HandleFunc("POST /api/login", s.handleLogin)
	s.mux.HandleFunc("POST /api/onboarding", s.handleOnboarding)
	s.mux.HandleFunc("POST /api/me/password", s.handleChangeMyPassword)
	s.mux.HandleFunc("GET /api/oidc/login", s.handleOIDCLogin)
	s.mux.HandleFunc("GET /api/oidc/callback", s.handleOIDCCallback)
	s.mux.HandleFunc("POST /api/logout", s.handleLogout)
	s.mux.HandleFunc("POST /api/share", s.handleShare)
	s.mux.HandleFunc("GET /api/setup", s.handleSetup)
	s.mux.HandleFunc("POST /api/setup", s.handleSetup)
	s.mux.HandleFunc("GET /api/users", s.handleListUsers)
	s.mux.HandleFunc("POST /api/users", s.handleCreateUser)
	s.mux.HandleFunc("PATCH /api/users/{name}", s.handlePatchUser)
	s.mux.HandleFunc("DELETE /api/users/{name}", s.handleDeleteUser)
	s.mux.HandleFunc("POST /api/users/{name}/password", s.handleSetUserPassword)
	s.mux.HandleFunc("GET /api/tokens", s.handleListTokens)
	s.mux.HandleFunc("POST /api/tokens", s.handleCreateToken)
	s.mux.HandleFunc("DELETE /api/tokens/{name}", s.handleDeleteToken)
	s.mux.HandleFunc("GET /api/settings", s.handleGetSettings)
	s.mux.HandleFunc("PUT /api/settings", s.handlePutSettings)
	s.mux.HandleFunc("POST /api/alerts/test", s.handleAlertTest)
	s.mux.HandleFunc("GET /api/audit", s.handleAuditEvents)
	s.mux.HandleFunc("GET /api/backup", s.handleBackup)
	s.mux.HandleFunc("POST /api/restore", s.handleRestore)
	s.mux.HandleFunc("GET /api/license", s.handleLicense)
	s.mux.HandleFunc("GET /api/nodes", s.handleNodes)
	s.mux.HandleFunc("POST /api/nodes", s.handleAddNode)
	s.mux.HandleFunc("DELETE /api/nodes/{id}", s.handleDeleteNode)
	s.mux.HandleFunc("GET /api/groups", s.handleNodeGroups)
	s.mux.HandleFunc("PATCH /api/nodes/{id}/labels", s.handleNodeLabels)
	s.mux.HandleFunc("GET /api/policies", s.handlePolicies)
	s.mux.HandleFunc("POST /api/policies/waivers", s.handleWaivePolicy)
	s.mux.HandleFunc("DELETE /api/policies/waivers/{key}", s.handleDeletePolicyWaiver)
	s.mux.HandleFunc("POST /api/convert", s.handleConvert)
	s.mux.HandleFunc("GET /api/import/containers", s.handleImportContainers)
	s.mux.HandleFunc("POST /api/import/containers/{id}/stop", s.handleStopImportContainer)
	s.mux.HandleFunc("GET /api/units", s.handleListUnits)
	s.mux.HandleFunc("GET /api/stats", s.handleStats)
	s.mux.HandleFunc("GET /api/units/{scope}/{name}", s.handleGetUnit)
	s.mux.HandleFunc("PUT /api/units/{scope}/{name}", s.handlePutUnit)
	s.mux.HandleFunc("DELETE /api/units/{scope}/{name}", s.handleDeleteUnit)
	s.mux.HandleFunc("POST /api/units/bulk-action", s.handleBulkAction)
	s.mux.HandleFunc("POST /api/units/{scope}/{name}/action", s.handleAction)
	s.mux.HandleFunc("GET /api/units/{scope}/{name}/logs", s.handleLogs)
	s.mux.HandleFunc("GET /api/units/{scope}/{name}/history", s.handleHistory)
	s.mux.HandleFunc("GET /api/units/{scope}/{name}/history/{commit}", s.handleHistoryShow)
	s.mux.HandleFunc("POST /api/units/{scope}/{name}/rollback", s.handleRollback)
	s.mux.HandleFunc("POST /api/validate", s.handleValidate)
	s.mux.HandleFunc("GET /api/host", s.handleHost)
	s.mux.HandleFunc("GET /api/gpus", s.handleGPUs)
	s.mux.HandleFunc("GET /api/updates", s.handleUpdates)
	s.mux.HandleFunc("POST /api/updates/apply", s.handleApplyUpdates)
	s.mux.HandleFunc("POST /api/units/{scope}/{name}/update", s.handleUpdateUnit)
	s.mux.HandleFunc("GET /api/secrets", s.handleListSecrets)
	s.mux.HandleFunc("POST /api/secrets", s.handleCreateSecret)
	s.mux.HandleFunc("DELETE /api/secrets/{name}", s.handleDeleteSecret)
	s.mux.HandleFunc("GET /api/images/stale", s.handleStaleImages)
	s.mux.HandleFunc("POST /api/images/prune", s.handlePruneImages)
	s.mux.Handle("GET /", http.FileServerFS(web.Files))
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// A share link's first visit carries ?share=; persist it as a cookie so
	// the SPA's API calls inherit the access.
	if tok := r.URL.Query().Get("share"); tok != "" && s.authConfigured() && s.shareValid(tok) {
		http.SetCookie(w, &http.Cookie{
			Name: shareCookie, Value: tok, Path: "/",
			HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil,
		})
	}
	if s.authRequired(r) {
		sess, loggedIn := s.session(r)
		switch {
		case loggedIn && sess.role == userstore.RoleAdmin:
			if s.users != nil && s.users.NeedsOnboarding(sess.user) &&
				r.URL.Path != "/api/auth" && r.URL.Path != "/api/onboarding" && r.URL.Path != "/api/logout" {
				httpError(w, http.StatusForbidden, "complete first-login setup before using Rookery")
				return
			}
			// full access
		case loggedIn: // viewer account
			if r.URL.Path == "/api/me/password" && r.Method == http.MethodPost {
				// Any real account may rotate its own password; share links cannot.
			} else if !readOnlyAllowed(r) {
				httpError(w, http.StatusForbidden, "your account is view-only")
				return
			}
		case s.shareAccess(r):
			if !readOnlyAllowed(r) {
				httpError(w, http.StatusForbidden, "this is a read-only share link")
				return
			}
		default:
			httpError(w, http.StatusUnauthorized, "authentication required")
			return
		}
	}
	s.mux.ServeHTTP(w, r)
}

func (s *Server) area(label string) (Area, bool) {
	s.areasMu.RLock()
	defer s.areasMu.RUnlock()
	for _, a := range s.areas {
		if a.Label == label {
			return a, true
		}
	}
	return Area{}, false
}

func (s *Server) areasSnapshot() []Area {
	s.areasMu.RLock()
	defer s.areasMu.RUnlock()
	out := make([]Area, len(s.areas))
	copy(out, s.areas)
	return out
}

func (s *Server) SetAlertTest(fn func(ctx context.Context) error) {
	s.alertTest = fn
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("write response: %v", err)
	}
}

func httpError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
