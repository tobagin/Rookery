package quadlet

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// SystemDirs returns the search path for rootful Quadlet units. The first
// directory is the primary one: new units are written there. Later
// directories are read-only sources (distro/runtime provided).
func SystemDirs() []string {
	return []string{
		"/etc/containers/systemd",
		"/run/containers/systemd",
		"/usr/share/containers/systemd",
	}
}

// UserDirs returns the search path for a user's rootless Quadlet units.
func UserDirs(home string) []string {
	return []string{
		filepath.Join(home, ".config", "containers", "systemd"),
	}
}

// Discover lists Quadlet unit files across dirs. When the same file name
// appears in several directories, the earliest directory wins, mirroring
// how the primary (writable) dir shadows read-only ones. Missing
// directories are skipped silently.
func Discover(dirs []string) ([]UnitFile, error) {
	seen := map[string]bool{}
	var units []UnitFile
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			name := e.Name()
			kind := KindFromName(name)
			if kind == "" || seen[name] {
				continue
			}
			seen[name] = true
			units = append(units, UnitFile{Name: name, Kind: kind, Path: filepath.Join(dir, name)})
		}
	}
	sort.Slice(units, func(i, j int) bool { return units[i].Name < units[j].Name })
	return units, nil
}
