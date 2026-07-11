// Command rookery serves the Quadlet-native web UI for a Podman host.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
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

	api "github.com/rookerylabs/rookery-agent-api"
	"github.com/rookerylabs/rookery/internal/agent"
	"github.com/rookerylabs/rookery/internal/alert"
	"github.com/rookerylabs/rookery/internal/appdb"
	"github.com/rookerylabs/rookery/internal/gitstore"
	"github.com/rookerylabs/rookery/internal/oidc"
	"github.com/rookerylabs/rookery/internal/podman"
	"github.com/rookerylabs/rookery/internal/quadlet"
	"github.com/rookerylabs/rookery/internal/rhost"
	"github.com/rookerylabs/rookery/internal/server"
	"github.com/rookerylabs/rookery/internal/systemd"
	"github.com/rookerylabs/rookery/internal/userstore"
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
	agents := flag.String("agents", envOr("ROOKERY_AGENTS", ""),
		`comma-separated rookery-agents to manage, as alias=url (e.g. "pi=http://10.87.0.5:7666"); the agent serves every scope on its host`)
	agentToken := flag.String("agent-token", envOr("ROOKERY_AGENT_TOKEN", ""),
		"shared bearer token presented to rookery-agents")
	alerts := flag.String("alerts", envOr("ROOKERY_ALERTS", ""),
		`comma-separated failure-alert destinations: ntfy://host/topic, telegram://BOT_TOKEN@CHAT_ID, or an http(s) webhook URL`)
	alertInterval := flag.Duration("alert-interval", envDurationOr("ROOKERY_ALERT_INTERVAL", 30*time.Second),
		"failure-alert polling interval")
	alertCooldown := flag.Duration("alert-cooldown", envDurationOr("ROOKERY_ALERT_COOLDOWN", 0),
		"minimum time between repeated failure alerts for the same unit; 0 disables suppression")
	oidcIssuer := flag.String("oidc-issuer", envOr("ROOKERY_OIDC_ISSUER", ""), "OIDC issuer URL for SSO")
	oidcClientID := flag.String("oidc-client-id", envOr("ROOKERY_OIDC_CLIENT_ID", ""), "OIDC client ID")
	oidcClientSecret := flag.String("oidc-client-secret", envOr("ROOKERY_OIDC_CLIENT_SECRET", ""), "OIDC client secret")
	oidcRedirectURL := flag.String("oidc-redirect-url", envOr("ROOKERY_OIDC_REDIRECT_URL", ""), "public OIDC callback URL (default derives from request)")
	oidcName := flag.String("oidc-name", envOr("ROOKERY_OIDC_NAME", "SSO"), "label shown on the SSO sign-in button")
	oidcAdmins := flag.String("oidc-admins", envOr("ROOKERY_OIDC_ADMINS", ""), "comma-separated OIDC sub/email/preferred_username values that get admin role")
	oidcAdminGroups := flag.String("oidc-admin-groups", envOr("ROOKERY_OIDC_ADMIN_GROUPS", ""), "comma-separated OIDC groups that get admin role")
	oidcDefaultRole := flag.String("oidc-default-role", envOr("ROOKERY_OIDC_DEFAULT_ROLE", "viewer"), "role for other OIDC users: viewer or admin")
	dataDir := flag.String("data-dir", envOr("ROOKERY_DATA_DIR", ""),
		"directory for rookery's own files (rookery.db); default /etc/rookery rootful, ~/.config/rookery rootless")
	sessionTTL := flag.Duration("session-ttl", envDurationOr("ROOKERY_SESSION_TTL", 24*time.Hour),
		"idle timeout for login sessions (sliding)")
	shareTTL := flag.Duration("share-ttl", envDurationOr("ROOKERY_SHARE_TTL", 7*24*time.Hour),
		"lifetime of read-only share links")
	auditRetention := flag.Duration("audit-retention", envDurationOr("ROOKERY_AUDIT_RETENTION", 0),
		"prune audit events older than this on startup; 0 keeps everything")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	flagSet := visitedFlags()

	if *showVersion {
		fmt.Println("rookery", version)
		return
	}

	password, err := loadPassword(*passwordFile)
	if err != nil {
		log.Fatal(err)
	}
	effectiveDataDir := resolveDataDir(*dataDir)
	accounts, err := userstore.Open(filepath.Join(effectiveDataDir, "users.json"))
	if err != nil {
		log.Fatal(err)
	}
	dbSettings, err := appdb.GetSettings(accounts.DB())
	if err != nil {
		log.Fatal(err)
	}
	if *auditRetention > 0 {
		if n, err := appdb.DeleteAuditEventsBefore(accounts.DB(), time.Now().Add(-*auditRetention)); err != nil {
			log.Printf("audit retention prune failed: %v", err)
		} else if n > 0 {
			log.Printf("pruned %d audit events older than %s", n, *auditRetention)
		}
	}
	dbApplied := applyDBSettings(dbSettings, flagSet, map[string]string{
		"managedUsers":         "users",
		"remotes":              "remotes",
		"alerts":               "alerts",
		"oidcIssuer":           "oidc-issuer",
		"oidcClientID":         "oidc-client-id",
		"oidcRedirectURL":      "oidc-redirect-url",
		"oidcName":             "oidc-name",
		"oidcAdmins":           "oidc-admins",
		"oidcAdminGroups":      "oidc-admin-groups",
		"oidcDefaultRole":      "oidc-default-role",
		"disablePasswordLogin": "disable-password-login",
		"gitTracking":          "git",
		"sessionTTL":           "session-ttl",
	}, map[string]string{
		"managedUsers":         "ROOKERY_USERS",
		"remotes":              "ROOKERY_REMOTES",
		"alerts":               "ROOKERY_ALERTS",
		"oidcIssuer":           "ROOKERY_OIDC_ISSUER",
		"oidcClientID":         "ROOKERY_OIDC_CLIENT_ID",
		"oidcRedirectURL":      "ROOKERY_OIDC_REDIRECT_URL",
		"oidcName":             "ROOKERY_OIDC_NAME",
		"oidcAdmins":           "ROOKERY_OIDC_ADMINS",
		"oidcAdminGroups":      "ROOKERY_OIDC_ADMIN_GROUPS",
		"oidcDefaultRole":      "ROOKERY_OIDC_DEFAULT_ROLE",
		"disablePasswordLogin": "ROOKERY_DISABLE_PASSWORD_LOGIN",
		"gitTracking":          "ROOKERY_GIT",
		"sessionTTL":           "ROOKERY_SESSION_TTL",
	}, map[string]any{
		"managedUsers": users, "remotes": remotes, "alerts": alerts,
		"oidcIssuer": oidcIssuer, "oidcClientID": oidcClientID, "oidcRedirectURL": oidcRedirectURL,
		"oidcName": oidcName, "oidcAdmins": oidcAdmins, "oidcAdminGroups": oidcAdminGroups, "oidcDefaultRole": oidcDefaultRole,
		"disablePasswordLogin": disablePasswordLogin, "gitTracking": gitFlag, "sessionTTL": sessionTTL,
	})
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
	if created, generated, bootstrapPassword, err := bootstrapInitialAdmin(accounts, password, *disablePasswordLogin); err != nil {
		log.Fatal(err)
	} else if created {
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
	agentAreasList, err := agentAreas(*agents, *agentToken)
	if err != nil {
		log.Fatal(err)
	}
	areas = append(areas, agentAreasList...)
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
		ShareTTL:             *shareTTL,
		Settings:             buildSettings(effectiveDataDir, accounts.Path(), *listen, *users, *remotes, *alerts, *gitFlag, *sessionTTL, *disablePasswordLogin, *oidcIssuer, *oidcClientID, *oidcRedirectURL, *oidcName, *oidcAdmins, *oidcAdminGroups, *oidcDefaultRole, flagSet, dbApplied),
	})

	if *alerts != "" {
		notifier, err := alert.Parse(*alerts)
		if err != nil {
			log.Fatal(err)
		}
		sendAlert := func(title, msg string) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			notifier.Send(ctx, title, msg)
		}
		srv.SetAlertTest(func(ctx context.Context) error {
			notifier.Send(ctx, "Rookery: test notification", "Alert delivery is configured.")
			return nil
		})
		go srv.WatchFailuresWithCooldown(context.Background(), *alertInterval, *alertCooldown, sendAlert)
		log.Printf("failure alerts enabled (%s, interval %s, cooldown %s)", *alerts, *alertInterval, *alertCooldown)
	}

	labels := make([]string, len(areas))
	for i, a := range areas {
		labels[i] = a.Label
	}
	log.Printf("rookery %s listening on http://%s (scopes: %s)", version, *listen, strings.Join(labels, ", "))
	if !isLoopback(*listen) {
		log.Printf("WARNING: %s is not loopback and Rookery speaks plain HTTP — put a TLS reverse proxy in front before exposing it", *listen)
	}
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
			uid, _ := strconv.Atoi(u.Uid)
			areas = append(areas, server.Area{
				Label: u.Username,
				Scope: systemd.Scope{User: u.Username},
				Dirs:  quadlet.UserDirs(u.HomeDir),
				UID:   uid,
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
	uid, _ := strconv.Atoi(u.Uid)
	return []server.Area{{
		Label: u.Username,
		Scope: systemd.Scope{User: u.Username},
		Dirs:  quadlet.UserDirs(u.HomeDir),
		UID:   uid,
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

func visitedFlags() map[string]bool {
	out := map[string]bool{}
	flag.Visit(func(f *flag.Flag) { out[f.Name] = true })
	return out
}

func applyDBSettings(settings []appdb.Setting, flagSet map[string]bool, flagNames, envNames map[string]string, targets map[string]any) map[string]bool {
	byKey := map[string]json.RawMessage{}
	for _, s := range settings {
		if s.Source == "db" && !s.Locked {
			byKey[s.Key] = s.Value
		}
	}
	applied := map[string]bool{}
	for key, target := range targets {
		if flagSet[flagNames[key]] || os.Getenv(envNames[key]) != "" {
			continue
		}
		raw, ok := byKey[key]
		if !ok {
			continue
		}
		switch p := target.(type) {
		case *string:
			var v string
			if json.Unmarshal(raw, &v) == nil {
				*p = v
				applied[key] = true
			}
		case *bool:
			var v bool
			if json.Unmarshal(raw, &v) == nil {
				*p = v
				applied[key] = true
			}
		case *time.Duration:
			var v string
			if json.Unmarshal(raw, &v) == nil {
				if d, err := time.ParseDuration(v); err == nil {
					*p = d
					applied[key] = true
				}
			}
		}
	}
	return applied
}

func buildSettings(dataDir, dbPath, listen, users, remotes, alerts string, gitTracking bool, sessionTTL time.Duration, disablePasswordLogin bool, oidcIssuer, oidcClientID, oidcRedirectURL, oidcName, oidcAdmins, oidcAdminGroups, oidcDefaultRole string, flagSet map[string]bool, dbApplied map[string]bool) []server.SettingGroup {
	source := func(key, flagName, envName string) (string, bool) {
		switch {
		case flagSet[flagName]:
			return "flag", true
		case os.Getenv(envName) != "":
			return "env", true
		case dbApplied[key]:
			return "db", false
		default:
			return "default", false
		}
	}
	item := func(key, label string, value any, flagName, envName string, editable bool) server.SettingItem {
		src, locked := source(key, flagName, envName)
		return server.SettingItem{
			Key:             key,
			Label:           label,
			Value:           value,
			Source:          src,
			Locked:          locked,
			Editable:        editable,
			RestartRequired: editable,
		}
	}
	runtime := func(key, label string, value any, src string) server.SettingItem {
		return server.SettingItem{Key: key, Label: label, Value: value, Source: src, Locked: true, Editable: false}
	}
	return []server.SettingGroup{
		{Name: "Authentication", Items: []server.SettingItem{
			item("disablePasswordLogin", "Password login disabled", disablePasswordLogin, "disable-password-login", "ROOKERY_DISABLE_PASSWORD_LOGIN", true),
			item("oidcIssuer", "OIDC issuer", oidcIssuer, "oidc-issuer", "ROOKERY_OIDC_ISSUER", true),
			item("oidcClientID", "OIDC client ID", oidcClientID, "oidc-client-id", "ROOKERY_OIDC_CLIENT_ID", true),
			item("oidcRedirectURL", "OIDC redirect URL", oidcRedirectURL, "oidc-redirect-url", "ROOKERY_OIDC_REDIRECT_URL", true),
			item("oidcName", "OIDC provider name", oidcName, "oidc-name", "ROOKERY_OIDC_NAME", true),
			item("oidcAdmins", "OIDC admin users", oidcAdmins, "oidc-admins", "ROOKERY_OIDC_ADMINS", true),
			item("oidcAdminGroups", "OIDC admin groups", oidcAdminGroups, "oidc-admin-groups", "ROOKERY_OIDC_ADMIN_GROUPS", true),
			item("oidcDefaultRole", "OIDC default role", oidcDefaultRole, "oidc-default-role", "ROOKERY_OIDC_DEFAULT_ROLE", true),
		}},
		{Name: "Deployment", Items: []server.SettingItem{
			runtime("listen", "Listen address", listen, "runtime"),
			runtime("dataDir", "Data directory", dataDir, "runtime"),
			runtime("dbPath", "Database path", dbPath, "runtime"),
			item("managedUsers", "Managed local users", users, "users", "ROOKERY_USERS", true),
			item("remotes", "Remote hosts", remotes, "remotes", "ROOKERY_REMOTES", true),
			item("gitTracking", "Git tracking default", gitTracking, "git", "ROOKERY_GIT", true),
			item("alerts", "Failure alerts", alerts, "alerts", "ROOKERY_ALERTS", true),
			item("sessionTTL", "Session TTL", sessionTTL.String(), "session-ttl", "ROOKERY_SESSION_TTL", true),
		}},
		{Name: "About", Items: []server.SettingItem{
			runtime("version", "Version", version, "build"),
		}},
	}
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

func bootstrapInitialAdmin(accounts *userstore.Store, password string, disablePasswordLogin bool) (created, generated bool, bootstrapPassword string, err error) {
	if accounts == nil || !accounts.Empty() || disablePasswordLogin {
		return false, false, "", nil
	}
	bootstrapPassword = password
	if bootstrapPassword == "" {
		bootstrapPassword, err = temporaryPassword()
		if err != nil {
			return false, false, "", err
		}
		generated = true
	}
	err = accounts.CreateWithProfile(userstore.User{
		Name:               "admin",
		Email:              "", // required at first-login onboarding via MustSetEmail
		Role:               userstore.RoleAdmin,
		MustChangePassword: generated,
		MustSetEmail:       true,
	}, bootstrapPassword)
	if err != nil {
		return false, false, "", err
	}
	return true, generated, bootstrapPassword, nil
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
// Aliases may be grouped as node.scope=target, which keeps rootful and
// rootless connections to the same host under one fleet node.
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
		nodeID := alias
		if node, scope, ok := strings.Cut(alias, "."); ok && node != "" && groupedRemoteScope(scope) {
			nodeID = node
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		uid, home, remoteUser, err := rhost.Probe(ctx, target)
		cancel()
		if err != nil {
			log.Printf("WARNING: remote %s (%s) unreachable, skipping: %v", alias, target, err)
			continue
		}
		area := server.Area{Label: alias, NodeID: nodeID}
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

// agentAreas builds an area per rookery-agent. It probes each agent's
// /v1/info to learn whether it serves a user or the system scope — the
// analogue of remoteAreas' ssh Probe, but over the agent's own HTTP API, so
// there is no ssh account or key to arrange.
func agentAreas(spec, token string) ([]server.Area, error) {
	var areas []server.Area
	for _, entry := range strings.Split(spec, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		alias, url, ok := strings.Cut(entry, "=")
		if !ok || alias == "" || url == "" {
			return nil, fmt.Errorf("-agents: entry %q must be alias=url", entry)
		}
		if token == "" {
			return nil, fmt.Errorf("-agents: %s needs a token (set -agent-token or ROOKERY_AGENT_TOKEN)", alias)
		}
		cli := agent.New(url, token)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		info, err := cli.Scopes(ctx)
		cancel()
		if err != nil {
			log.Printf("WARNING: agent %s (%s) unreachable, skipping: %v", alias, url, err)
			continue
		}
		// One agent serves every scope on its host; register an area per
		// scope, all grouped under the host's node (NodeID = alias). The label
		// is alias.<scopeID> (e.g. pi.system, pi.tobagin) so the API path
		// segment stays unique and readable.
		for _, sc := range info.Scopes {
			area := server.Area{
				Label:      alias + "." + sc.ID,
				NodeID:     alias,
				Agent:      cli,
				AgentScope: sc.ID,
			}
			if !sc.System {
				area.Scope = systemd.Scope{User: sc.User}
			}
			areas = append(areas, area)
		}
		log.Printf("agent %s: %s (%d scopes: %s)", alias, url, len(info.Scopes), scopeLabels(info.Scopes))
	}
	return areas, nil
}

func scopeLabels(scopes []api.Scope) string {
	out := make([]string, len(scopes))
	for i, s := range scopes {
		out[i] = s.ID
	}
	return strings.Join(out, " ")
}

func groupedRemoteScope(scope string) bool {
	switch scope {
	case "root", "rootful", "user", "rootless":
		return true
	}
	return false
}

// attachGit opens (or with force, initializes) a git repository in each
// local area's primary directory. Directories that already are
// repositories get history tracking even without -git; plain directories
// are left alone unless the flag asks for them. Remote areas get history
// only when the directory is already a repository over there — Rookery
// never git-inits someone else's host.
func attachGit(areas []server.Area, force bool) {
	for i := range areas {
		if areas[i].ViaAgent() {
			continue // agent areas have no local dir and keep no git
		}
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
