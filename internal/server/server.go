// Package server exposes the HTTP API and embedded web UI. It holds no
// state of its own: every request re-reads unit files from disk and unit
// state from systemd, so the browser and `ssh + systemctl` always agree.
package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

	"github.com/tobagin/rookery/internal/gitstore"
	"github.com/tobagin/rookery/internal/gpu"
	"github.com/tobagin/rookery/internal/podman"
	"github.com/tobagin/rookery/internal/quadlet"
	"github.com/tobagin/rookery/internal/registry"
	"github.com/tobagin/rookery/internal/systemd"
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
	InspectContainer(ctx context.Context, nameOrID string) ([]byte, error)
	ImageDigests(ctx context.Context, ref string) ([]string, error)
	PullImage(ctx context.Context, ref string) error
}

// Area is one set of Quadlet directories managed under one systemd scope:
// the system, or a single user. Its label is the scope path segment in the
// API ("system" or the username).
type Area struct {
	Label string
	Scope systemd.Scope
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
	Password string      // empty disables authentication
	SELinux  func() bool // nil -> detect on the host
	// GPUs enumerates host GPUs; nil -> gpu.Detect. Injectable for tests.
	GPUs func(ctx context.Context) []gpu.Device
	// ResolveDigest fetches an image tag's current registry digest;
	// nil -> a real registry client. Injectable for tests.
	ResolveDigest func(ctx context.Context, image string) (string, error)
}

// Server routes API and UI requests.
type Server struct {
	areas    []Area
	sysd     Systemd
	validate ValidateFunc
	pod      Podman
	resolve  func(ctx context.Context, image string) (string, error)
	version  string
	password string
	selinux  func() bool
	gpus     func(ctx context.Context) []gpu.Device
	sess     *sessions
	mux      *http.ServeMux
}

// New builds the Server and its routes.
func New(opts Options) *Server {
	s := &Server{
		areas:    opts.Areas,
		sysd:     opts.Systemd,
		validate: opts.Validate,
		pod:      opts.Podman,
		version:  opts.Version,
		password: opts.Password,
		selinux:  opts.SELinux,
		gpus:     opts.GPUs,
		resolve:  opts.ResolveDigest,
		sess:     newSessions(),
		mux:      http.NewServeMux(),
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
	s.mux.HandleFunc("GET /api/auth", s.handleAuthStatus)
	s.mux.HandleFunc("POST /api/login", s.handleLogin)
	s.mux.HandleFunc("POST /api/logout", s.handleLogout)
	s.mux.HandleFunc("POST /api/convert", s.handleConvert)
	s.mux.HandleFunc("GET /api/import/containers", s.handleImportContainers)
	s.mux.HandleFunc("GET /api/units", s.handleListUnits)
	s.mux.HandleFunc("GET /api/units/{scope}/{name}", s.handleGetUnit)
	s.mux.HandleFunc("PUT /api/units/{scope}/{name}", s.handlePutUnit)
	s.mux.HandleFunc("DELETE /api/units/{scope}/{name}", s.handleDeleteUnit)
	s.mux.HandleFunc("POST /api/units/{scope}/{name}/action", s.handleAction)
	s.mux.HandleFunc("GET /api/units/{scope}/{name}/logs", s.handleLogs)
	s.mux.HandleFunc("GET /api/units/{scope}/{name}/history", s.handleHistory)
	s.mux.HandleFunc("GET /api/units/{scope}/{name}/history/{commit}", s.handleHistoryShow)
	s.mux.HandleFunc("POST /api/units/{scope}/{name}/rollback", s.handleRollback)
	s.mux.HandleFunc("POST /api/validate", s.handleValidate)
	s.mux.HandleFunc("GET /api/host", s.handleHost)
	s.mux.HandleFunc("GET /api/gpus", s.handleGPUs)
	s.mux.HandleFunc("GET /api/updates", s.handleUpdates)
	s.mux.HandleFunc("POST /api/units/{scope}/{name}/update", s.handleUpdateUnit)
	s.mux.Handle("GET /", http.FileServerFS(web.Files))
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.authRequired(r) && !s.authenticated(r) {
		httpError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	s.mux.ServeHTTP(w, r)
}

func (s *Server) area(label string) (Area, bool) {
	for _, a := range s.areas {
		if a.Label == label {
			return a, true
		}
	}
	return Area{}, false
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
