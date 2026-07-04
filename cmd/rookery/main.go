// Command rookery serves the Quadlet-native web UI for a Podman host.
package main

import (
	"flag"
	"fmt"
	"log"
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
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("rookery", version)
		return
	}

	areas, err := detectAreas(*users)
	if err != nil {
		log.Fatal(err)
	}

	srv := server.New(server.Options{
		Areas:   areas,
		Systemd: systemd.NewManager(),
		Podman:  podman.New(podman.SocketPath()),
		Version: version,
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
