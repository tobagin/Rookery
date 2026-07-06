// Command rookery serves the Quadlet-native web UI for a Podman host.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tobagin/rookery/internal/alert"
	"github.com/tobagin/rookery/internal/gitstore"
	"github.com/tobagin/rookery/internal/oidc"
	"github.com/tobagin/rookery/internal/podman"
	"github.com/tobagin/rookery/internal/quadlet"
	"github.com/tobagin/rookery/internal/rhost"
	"github.com/tobagin/rookery/internal/server"
	"github.com/tobagin/rookery/internal/systemd"
	"github.com/tobagin/rookery/internal/userstore"
)

// version is stamped by the build (see Makefile).
var version = "dev"

func main() {
	// 7665 spells ROOK on a phone keypad; 7878 collided with Radarr.
	listen := flag.String("listen", envOr("ROOKERY_LISTEN", "127.0.0.1:7665"), "address to listen on")
	users := flag.String("users", envOr("ROOKERY_USERS", ""), `comma-separated users whose rootless quadlets to manage (rootful only); empty auto-discovers users with a ~/.config/containers/systemd tree, "none" disables`)
	passwordFile := flag.String("password-file", envOr("ROOKERY_PASSWORD_FILE", ""), "file containing the admin password (or set ROOKERY_PASSWORD)")
	disablePasswordLogin := flag.Bool("disable-password-login", envBoolOr("ROOKERY_DISABLE_PASSWORD_LOGIN", false), "disable username/password login; requires OIDC")
	gitFlag := flag.Bool("git", envOr("ROOKERY_GIT", "") == "1" || envOr("ROOKERY_GIT", "") == "true",
		"track unit directories in git: commit on save, history, rollback (auto-enabled for dirs that are already repositories)")
	remotes := flag.String("remotes", envOr("ROOKERY_REMOTES", ""),
		`comma-separated remote hosts to manage over ssh, as alias=user@host (e.g. "nas=root@nas.local,media=deploy@media.lan")`)
	alerts := flag.String("alerts", envOr("ROOKERY_ALERTS", ""),
		`comma-separated failure-alert destinations: ntfy://host/topic, telegram://BOT_TOKEN@CHAT_ID, or an http(s) webhook URL`)
	oidcIssuer := flag.String("oidc-issuer", envOr("ROOKERY_OIDC_ISSUER", ""), "OIDC issuer URL for SSO")
	oidcClientID := flag.String("oidc-client-id", envOr("ROOKERY_OIDC_CLIENT_ID", ""), "OIDC client ID")
	oidcClientSecret := flag.String("oidc-client-secret", envOr("ROOKERY_OIDC_CLIENT_SECRET", ""), "OIDC client secret")
	oidcRedirectURL := flag.String("oidc-redirect-url", envOr("ROOKERY_OIDC_REDIRECT_URL", ""), "public OIDC callback URL (default derives from request)")
	oidcName := flag.String("oidc-name", envOr("ROOKERY_OIDC_NAME", "SSO"), "label shown on the SSO sign-in button")
	oidcAdmins := flag.String("oidc-admins", envOr("ROOKERY_OIDC_ADMINS", ""), "comma-separated OIDC sub/email/preferred_username values that get admin role")
	oidcAdminGroups := flag.String("oidc-admin-groups", envOr("ROOKERY_OIDC_ADMIN_GROUPS", ""), "comma-separated OIDC groups that get admin role")
	oidcDefaultRole := flag.String("oidc-default-role", envOr("ROOKERY_OIDC_DEFAULT_ROLE", "viewer"), "role for other OIDC users: viewer or admin")
	dataDir := flag.String("data-dir", envOr("ROOKERY_DATA_DIR", ""),
		"directory for rookery's own files (users.json); default /etc/rookery rootful, ~/.config/rookery rootless")
	sessionTTL := flag.Duration("session-ttl", envDurationOr("ROOKERY_SESSION_TTL", 24*time.Hour),
		"idle timeout for login sessions (sliding)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("rookery", version)
		return
	}

	password, err := loadPassword(*passwordFile)
	if err != nil {
		log.Fatal(err)
	}
	accounts, err := userstore.Open(filepath.Join(resolveDataDir(*dataDir), "users.json"))
	if err != nil {
		log.Fatal(err)
	}
	oidcClient, err := oidc.New(oidc.Config{
		Issuer:       *oidcIssuer,
		ClientID:     *oidcClientID,
		ClientSecret: *oidcClientSecret,
		RedirectURL:  *oidcRedirectURL,
		ProviderName: *oidcName,
		DefaultRole:  *oidcDefaultRole,
		AdminUsers:   splitList(*oidcAdmins),
		AdminGroups:  splitList(*oidcAdminGroups),
	})
	if err != nil {
		log.Fatal(err)
	}
	if oidcClient != nil {
		log.Printf("OIDC SSO enabled for issuer %s", *oidcIssuer)
	}
	if *disablePasswordLogin && oidcClient == nil {
		log.Fatal("-disable-password-login requires OIDC to be configured")
	}
	if accounts.Empty() && oidcClient == nil && !*disablePasswordLogin {
		bootstrapPassword := password
		generated := false
		if bootstrapPassword == "" {
			bootstrapPassword, err = temporaryPassword()
			if err != nil {
				log.Fatal(err)
			}
			generated = true
		}
		if err := accounts.CreateWithProfile(userstore.User{
			Name:               "admin",
			Email:              "admin@example.com",
			Role:               userstore.RoleAdmin,
			MustChangePassword: generated,
			MustSetEmail:       true,
		}, bootstrapPassword); err != nil {
			log.Fatal(err)
		}
		if generated {
			log.Printf("created initial admin account: username admin, temporary password %q", bootstrapPassword)
			log.Printf("sign in at http://%s and change the email/password before using Rookery", *listen)
		} else {
			log.Printf("created initial admin account from configured password: username admin")
			log.Printf("sign in at http://%s and set the admin email before using Rookery", *listen)
		}
		password = ""
	}

	areas, err := detectAreas(*users)
	if err != nil {
		log.Fatal(err)
	}
	remoteAreasList, err := remoteAreas(*remotes)
	if err != nil {
		log.Fatal(err)
	}
	areas = append(areas, remoteAreasList...)
	attachGit(areas, *gitFlag)

	srv := server.New(server.Options{
		Areas:                areas,
		Systemd:              systemd.NewManager(),
		Podman:               podman.New(podman.SocketPath()),
		Version:              version,
		Password:             password,
		Users:                accounts,
		DisablePasswordLogin: *disablePasswordLogin,
		OIDC:                 oidcClient,
		SessionTTL:           *sessionTTL,
	})

	if *alerts != "" {
		notifier, err := alert.Parse(*alerts)
		if err != nil {
			log.Fatal(err)
		}
		go srv.WatchFailures(context.Background(), 30*time.Second, func(title, msg string) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			notifier.Send(ctx, title, msg)
		})
		log.Printf("failure alerts enabled (%s)", *alerts)
	}

	labels := make([]string, len(areas))
	for i, a := range areas {
		labels[i] = a.Label
	}
	log.Printf("rookery %s listening on http://%s (scopes: %s)", version, *listen, strings.Join(labels, ", "))
	log.Fatal(http.ListenAndServe(*listen, srv))
}

// detectAreas picks which Quadlet trees this instance manages: rootful
// manages the system tree plus rootless user sessions — those named by
// -users, or, when the flag is empty, every user with an existing
// ~/.config/containers/systemd tree ("none" disables). Rootless manages
// only the invoking user's own tree.
func detectAreas(usersFlag string) ([]server.Area, error) {
	if os.Geteuid() == 0 {
		areas := []server.Area{{Label: "system", Scope: systemd.Scope{}, Dirs: quadlet.SystemDirs()}}
		names := splitList(usersFlag)
		if usersFlag == "" {
			names = discoverQuadletUsers("/etc/passwd")
			if len(names) > 0 {
				log.Printf("auto-discovered rootless quadlet users: %s (pass -users to override, -users none to disable)", strings.Join(names, ", "))
			}
		} else if usersFlag == "none" {
			names = nil
		}
		for _, name := range names {
			u, err := user.Lookup(name)
			if err != nil {
				return nil, fmt.Errorf("-users: %w", err)
			}
			areas = append(areas, server.Area{
				Label: u.Username,
				Scope: systemd.Scope{User: u.Username},
				Dirs:  quadlet.UserDirs(u.HomeDir),
			})
		}
		return areas, nil
	}
	u, err := user.Current()
	if err != nil {
		return nil, err
	}
	if usersFlag != "" && usersFlag != u.Username {
		return nil, fmt.Errorf("-users requires running as root")
	}
	return []server.Area{{
		Label: u.Username,
		Scope: systemd.Scope{User: u.Username},
		Dirs:  quadlet.UserDirs(u.HomeDir),
	}}, nil
}

func splitList(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// discoverQuadletUsers scans passwd for human accounts (uid >= 1000, not
// nobody) whose ~/.config/containers/systemd directory exists. NSS-only
// users (LDAP etc.) are not seen — name them with -users instead.
func discoverQuadletUsers(passwdPath string) []string {
	data, err := os.ReadFile(passwdPath)
	if err != nil {
		return nil
	}
	var names []string
	for _, line := range strings.Split(string(data), "\n") {
		f := strings.Split(line, ":")
		if len(f) < 6 {
			continue
		}
		uid, err := strconv.Atoi(f[2])
		if err != nil || uid < 1000 || uid == 65534 {
			continue
		}
		if st, err := os.Stat(filepath.Join(f[5], ".config", "containers", "systemd")); err == nil && st.IsDir() {
			names = append(names, f[0])
		}
	}
	return names
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envDurationOr(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
		log.Printf("WARNING: %s=%q is not a duration; using %s", key, os.Getenv(key), fallback)
	}
	return fallback
}

func envBoolOr(key string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "":
		return fallback
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		log.Printf("WARNING: %s=%q is not a boolean; using %t", key, os.Getenv(key), fallback)
		return fallback
	}
}

func temporaryPassword() (string, error) {
	buf := make([]byte, 18)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// resolveDataDir picks where rookery's own files (users.json) live.
func resolveDataDir(flagVal string) string {
	if flagVal != "" {
		return flagVal
	}
	if os.Geteuid() == 0 {
		return "/etc/rookery"
	}
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "rookery")
	}
	return ".rookery"
}

// remoteAreas probes each alias=user@host entry over ssh and builds an
// area for it: the system Quadlet tree when the ssh account is root, the
// account's own rootless tree otherwise. An unreachable host is skipped
// with a warning so one dead box doesn't take the whole UI down at boot.
func remoteAreas(spec string) ([]server.Area, error) {
	var areas []server.Area
	for _, entry := range strings.Split(spec, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		alias, target, ok := strings.Cut(entry, "=")
		if !ok || alias == "" || target == "" {
			return nil, fmt.Errorf("-remotes: entry %q must be alias=user@host", entry)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		uid, home, remoteUser, err := rhost.Probe(ctx, target)
		cancel()
		if err != nil {
			log.Printf("WARNING: remote %s (%s) unreachable, skipping: %v", alias, target, err)
			continue
		}
		area := server.Area{Label: alias}
		if uid == 0 {
			area.Scope = systemd.Scope{SSH: target}
			area.Dirs = quadlet.SystemDirs()
		} else {
			area.Scope = systemd.Scope{User: remoteUser, SSH: target}
			area.Dirs = quadlet.UserDirs(home)
		}
		log.Printf("remote %s: %s (uid %d, %s scope)", alias, target, uid, area.Scope)
		areas = append(areas, area)
	}
	return areas, nil
}

// attachGit opens (or with force, initializes) a git repository in each
// local area's primary directory. Directories that already are
// repositories get history tracking even without -git; plain directories
// are left alone unless the flag asks for them. Remote areas get history
// only when the directory is already a repository over there — Rookery
// never git-inits someone else's host.
func attachGit(areas []server.Area, force bool) {
	for i := range areas {
		var store *gitstore.Store
		var err error
		if areas[i].Remote() {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			store, err = gitstore.OpenRemote(ctx, areas[i].Scope.SSH, areas[i].Dirs[0])
			cancel()
		} else {
			store, err = gitstore.Open(areas[i].Dirs[0], force)
		}
		switch {
		case err == nil:
			areas[i].Git = store
			log.Printf("git history enabled for %s (%s)", areas[i].Label, areas[i].Dirs[0])
		case errors.Is(err, gitstore.ErrNotRepo):
			// not tracked, not requested — fine
		default:
			log.Printf("WARNING: git history unavailable for %s: %v", areas[i].Label, err)
		}
	}
}

// loadPassword prefers an explicit password file over the environment.
func loadPassword(file string) (string, error) {
	if file != "" {
		data, err := os.ReadFile(file)
		if err != nil {
			return "", fmt.Errorf("-password-file: %w", err)
		}
		return strings.TrimSpace(string(data)), nil
	}
	return os.Getenv("ROOKERY_PASSWORD"), nil
}

func isLoopback(listen string) bool {
	host, _, err := net.SplitHostPort(listen)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
