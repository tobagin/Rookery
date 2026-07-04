package server

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"sort"

	"github.com/tobagin/rookery/internal/quadlet"
	"github.com/tobagin/rookery/internal/rhost"
)

// This file is the one place that knows whether an area's unit files live
// on this machine or across ssh; everything above it just says "read the
// unit in this area".

func areaReadFile(ctx context.Context, area Area, p string) ([]byte, error) {
	if !area.Remote() {
		return os.ReadFile(p)
	}
	return rhost.ReadFile(ctx, area.Scope.SSH, p)
}

func areaWriteFile(ctx context.Context, area Area, p string, data []byte) error {
	if !area.Remote() {
		return writeFileAtomic(p, data)
	}
	return rhost.WriteFileAtomic(ctx, area.Scope.SSH, p, data)
}

func areaRemove(ctx context.Context, area Area, p string) error {
	if !area.Remote() {
		return os.Remove(p)
	}
	return rhost.Remove(ctx, area.Scope.SSH, p)
}

func areaExists(ctx context.Context, area Area, p string) (bool, error) {
	if !area.Remote() {
		_, err := os.Stat(p)
		if err == nil {
			return true, nil
		}
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return rhost.Exists(ctx, area.Scope.SSH, p)
}

// discovered is one unit file found in an area, with its content already
// in hand (remote listing fetches contents in the same round trip; local
// reads are cheap).
type discovered struct {
	unit quadlet.UnitFile
	data []byte // nil when the file vanished between listing and reading
}

// discoverArea lists an area's units. Directory order encodes shadowing:
// the first directory containing a name wins.
func discoverArea(ctx context.Context, area Area) ([]discovered, error) {
	if !area.Remote() {
		units, err := quadlet.Discover(area.Dirs)
		if err != nil {
			return nil, err
		}
		out := make([]discovered, len(units))
		for i, u := range units {
			data, _ := os.ReadFile(u.Path)
			out[i] = discovered{unit: u, data: data}
		}
		return out, nil
	}

	seen := map[string]bool{}
	var out []discovered
	for _, dir := range area.Dirs {
		files, err := rhost.ReadDirFiles(ctx, area.Scope.SSH, dir)
		if err != nil {
			return nil, err
		}
		for _, p := range rhost.SortedNames(files) {
			name := path.Base(p)
			kind := quadlet.KindFromName(name)
			if kind == "" || seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, discovered{
				unit: quadlet.UnitFile{Name: name, Kind: kind, Path: p},
				data: files[p],
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].unit.Name < out[j].unit.Name })
	return out, nil
}

// areaValidate dry-runs content through the Quadlet generator of the host
// the unit will actually run on.
func (s *Server) areaValidate(ctx context.Context, area Area, name, content string) (quadlet.ValidationResult, error) {
	if area.Remote() {
		return rhost.ValidateRemote(ctx, area.Scope.SSH, !area.Scope.IsSystem(), name, content)
	}
	return s.validate(ctx, !area.Scope.IsSystem(), name, content)
}

// joinUnitPath keeps remote paths POSIX regardless of the local OS.
func joinUnitPath(area Area, dir, name string) string {
	if area.Remote() {
		return path.Join(dir, name)
	}
	return filepath.Join(dir, name)
}
