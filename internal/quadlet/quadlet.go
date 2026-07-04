// Package quadlet models Podman Quadlet unit files on disk: discovery,
// parsing, and validation. Files on disk are the single source of truth;
// this package never caches state anywhere else.
package quadlet

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Kind is the Quadlet unit type, derived from the file extension.
type Kind string

const (
	KindContainer Kind = "container"
	KindPod       Kind = "pod"
	KindNetwork   Kind = "network"
	KindVolume    Kind = "volume"
	KindKube      Kind = "kube"
	KindImage     Kind = "image"
	KindBuild     Kind = "build"
)

// Kinds lists every Quadlet unit type Rookery manages.
var Kinds = []Kind{KindContainer, KindPod, KindNetwork, KindVolume, KindKube, KindImage, KindBuild}

// KindFromName returns the Kind for a Quadlet file name, or "" if the
// extension is not a Quadlet type.
func KindFromName(name string) Kind {
	ext := strings.TrimPrefix(filepath.Ext(name), ".")
	for _, k := range Kinds {
		if string(k) == ext {
			return k
		}
	}
	return ""
}

// UnitFile is a Quadlet unit file found on disk.
type UnitFile struct {
	Name string // file name, e.g. "jellyfin.container"
	Kind Kind
	Path string // absolute path on disk
}

// ServiceName maps a Quadlet file name to the systemd service unit the
// generator produces for it (per podman-systemd.unit(5)).
func ServiceName(fileName string) (string, error) {
	kind := KindFromName(fileName)
	base := strings.TrimSuffix(fileName, filepath.Ext(fileName))
	switch kind {
	case KindContainer, KindKube:
		return base + ".service", nil
	case KindPod:
		return base + "-pod.service", nil
	case KindVolume:
		return base + "-volume.service", nil
	case KindNetwork:
		return base + "-network.service", nil
	case KindImage:
		return base + "-image.service", nil
	case KindBuild:
		return base + "-build.service", nil
	}
	return "", fmt.Errorf("not a quadlet file name: %q", fileName)
}

// CheckName rejects unit names that are empty, contain path separators, or
// don't carry a Quadlet extension. It guards every API path that touches
// the filesystem.
func CheckName(name string) error {
	if name == "" {
		return fmt.Errorf("empty unit name")
	}
	if name != filepath.Base(name) || strings.ContainsAny(name, "/\\") {
		return fmt.Errorf("invalid unit name %q", name)
	}
	if strings.HasPrefix(name, ".") {
		return fmt.Errorf("unit name %q may not start with a dot", name)
	}
	if KindFromName(name) == "" {
		return fmt.Errorf("unit name %q does not have a quadlet extension", name)
	}
	return nil
}
