// Command rookery serves the Quadlet-native web UI for a Podman host.
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/user"
	"strings"

	"github.com/tobagin/rookery/internal/podman"
	"github.com/tobagin/rookery/internal/quadlet"
	"github.com/tobagin/rookery/internal/server"
	"github.com/tobagin/rookery/internal/systemd"
)

// version is stamped by the build (see Makefile).
var version = "dev"

func main() {
	listen := flag.String("listen", envOr("ROOKERY_LISTEN", "127.0.0.1:7878"), "address to listen on")
	users := flag.String("users", envOr("ROOKERY_USERS", ""), "comma-separated users whose rootless quadlets to manage (rootful only)")
	passwordFile := flag.String("password-file", envOr("ROOKERY_PASSWORD_FILE", ""), "file containing the admin password (or set ROOKERY_PASSWORD)")
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
	if password == "" && !isLoopback(*listen) {
		log.Printf("WARNING: no admin password configured while listening on %s — anyone who can reach this port controls your containers. Set ROOKERY_PASSWORD or -password-file.", *listen)
	}

	areas, err := detectAreas(*users)
	if err != nil {
		log.Fatal(err)
	}

	srv := server.New(server.Options{
		Areas:    areas,
		Systemd:  systemd.NewManager(),
		Podman:   podman.New(podman.SocketPath()),
		Version:  version,
		Password: password,
	})

	labels := make([]string, len(areas))
	for i, a := range areas {
		labels[i] = a.Label
	}
	log.Printf("rookery %s listening on http://%s (scopes: %s)", version, *listen, strings.Join(labels, ", "))
	log.Fatal(http.ListenAndServe(*listen, srv))
}

// detectAreas picks which Quadlet trees this instance manages: rootful
// manages the system tree plus any -users sessions; rootless manages only
// the invoking user's own tree.
func detectAreas(usersFlag string) ([]server.Area, error) {
	if os.Geteuid() == 0 {
		areas := []server.Area{{Label: "system", Scope: systemd.Scope{}, Dirs: quadlet.SystemDirs()}}
		for _, name := range strings.Split(usersFlag, ",") {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
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

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
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
