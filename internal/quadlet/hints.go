package quadlet

import (
	"fmt"
	"os"
	"strings"
)

// SELinuxEnforcing reports whether the host is running SELinux in enforcing
// mode — the situation where an unlabeled bind mount will get EACCES.
func SELinuxEnforcing() bool {
	data, err := os.ReadFile("/sys/fs/selinux/enforce")
	return err == nil && strings.TrimSpace(string(data)) == "1"
}

// GPURefs lists the GPU attachments a parsed unit declares: CDI devices
// (nvidia.com/gpu=…), DRI/KFD device nodes, and raw --gpus passthroughs.
func GPURefs(f *File) []string {
	var refs []string
	for _, section := range []string{"Container", "Pod"} {
		for _, d := range f.All(section, "AddDevice") {
			dev := strings.TrimPrefix(d, "-") // quadlet allows a leading "-" for optional devices
			switch {
			case strings.HasPrefix(dev, "nvidia.com/gpu"),
				strings.HasPrefix(dev, "amd.com/gpu"),
				strings.HasPrefix(dev, "/dev/dri"),
				strings.HasPrefix(dev, "/dev/kfd"),
				strings.HasPrefix(dev, "/dev/nvidia"):
				refs = append(refs, dev)
			}
		}
		for _, args := range f.All(section, "PodmanArgs") {
			if strings.Contains(args, "--gpus") {
				refs = append(refs, strings.TrimSpace(args))
			}
		}
	}
	return refs
}

// VolumeHints inspects a unit file body and flags bind mounts that carry no
// SELinux relabel option. Callers should only surface these on enforcing
// hosts. A content that doesn't parse yields no hints — the validator
// reports parse problems.
func VolumeHints(content string) []string {
	f, err := Parse([]byte(content))
	if err != nil {
		return nil
	}
	var hints []string
	for _, section := range []string{"Container", "Pod"} {
		for _, v := range f.All(section, "Volume") {
			parts := strings.Split(v, ":")
			if len(parts) < 2 {
				continue // anonymous or named volume without options
			}
			src := parts[0]
			// Only host-path bind mounts need relabeling; named volumes
			// (including *.volume references) and specifiers don't.
			if !strings.HasPrefix(src, "/") && !strings.HasPrefix(src, ".") && !strings.HasPrefix(src, "~") {
				continue
			}
			relabeled := false
			if len(parts) >= 3 {
				for _, opt := range strings.Split(parts[len(parts)-1], ",") {
					if opt == "z" || opt == "Z" {
						relabeled = true
					}
				}
			}
			if !relabeled {
				hints = append(hints, fmt.Sprintf(
					"SELinux: bind mount %q has no relabel option — the container will likely get 'permission denied'. Append :Z (private to this container) or :z (shared between containers), e.g. Volume=%s:Z", v, v))
			}
		}
	}
	return hints
}
