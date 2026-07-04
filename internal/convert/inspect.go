package convert

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// inspectData is the slice of `podman inspect` output the importer maps.
type inspectData struct {
	Name      string `json:"Name"`
	ImageName string `json:"ImageName"`
	Config    struct {
		Image      string            `json:"Image"`
		Env        []string          `json:"Env"`
		Cmd        []string          `json:"Cmd"`
		Entrypoint any               `json:"Entrypoint"` // string in older podman, []string in newer
		WorkingDir string            `json:"WorkingDir"`
		User       string            `json:"User"`
		Labels     map[string]string `json:"Labels"`
	} `json:"Config"`
	HostConfig struct {
		PortBindings map[string][]struct {
			HostIP   string `json:"HostIp"`
			HostPort string `json:"HostPort"`
		} `json:"PortBindings"`
		RestartPolicy struct {
			Name string `json:"Name"`
		} `json:"RestartPolicy"`
		Devices []struct {
			PathOnHost      string `json:"PathOnHost"`
			PathInContainer string `json:"PathInContainer"`
		} `json:"Devices"`
		CapAdd      []string `json:"CapAdd"`
		CapDrop     []string `json:"CapDrop"`
		SecurityOpt []string `json:"SecurityOpt"`
	} `json:"HostConfig"`
	Mounts []struct {
		Type        string   `json:"Type"`
		Name        string   `json:"Name"`
		Source      string   `json:"Source"`
		Destination string   `json:"Destination"`
		RW          bool     `json:"RW"`
		Options     []string `json:"Options"`
	} `json:"Mounts"`
	NetworkSettings struct {
		Networks map[string]json.RawMessage `json:"Networks"`
	} `json:"NetworkSettings"`
}

// Env vars every container gets from the image/runtime; carrying them into
// the unit would freeze incidental defaults.
var boringEnv = map[string]bool{"PATH": true, "TERM": true, "HOSTNAME": true, "HOME": true, "container": true}

// boringLabel filters image metadata labels that would just be noise in a
// unit file.
func boringLabel(k string) bool {
	return k == "PODMAN_SYSTEMD_UNIT" ||
		k == "maintainer" ||
		strings.HasPrefix(k, "org.opencontainers.image.") ||
		strings.HasPrefix(k, "io.buildah.")
}

// FromInspect converts one container's `podman inspect` JSON into a
// .container unit — the "import my running containers" migration path.
func FromInspect(raw []byte) (GeneratedUnit, error) {
	// The CLI wraps inspect output in an array; the REST endpoint doesn't.
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "[") {
		var list []json.RawMessage
		if err := json.Unmarshal(raw, &list); err != nil || len(list) == 0 {
			return GeneratedUnit{}, fmt.Errorf("unexpected inspect JSON: %v", err)
		}
		raw = list[0]
	}
	var in inspectData
	if err := json.Unmarshal(raw, &in); err != nil {
		return GeneratedUnit{}, fmt.Errorf("parse inspect JSON: %w", err)
	}

	image := in.ImageName
	if image == "" {
		image = in.Config.Image
	}
	if image == "" {
		return GeneratedUnit{}, fmt.Errorf("inspect data has no image name")
	}
	name := strings.TrimPrefix(in.Name, "/")
	if name == "" {
		name = imageBaseName(image)
	}

	b := newBuilder()
	base := sanitizeName(name)
	b.add("Unit", "Description", base+" (imported from container)")
	b.add("Container", "Image", image)
	b.add("Container", "ContainerName", name)

	ports := make([]string, 0, len(in.HostConfig.PortBindings))
	for spec, binds := range in.HostConfig.PortBindings {
		ctrPort := strings.TrimSuffix(spec, "/tcp")
		proto := ""
		if strings.HasSuffix(spec, "/udp") {
			ctrPort = strings.TrimSuffix(spec, "/udp")
			proto = "/udp"
		}
		for _, bind := range binds {
			p := bind.HostPort + ":" + ctrPort + proto
			if bind.HostIP != "" && bind.HostIP != "0.0.0.0" {
				p = bind.HostIP + ":" + p
			}
			ports = append(ports, p)
		}
	}
	sort.Strings(ports)
	for _, p := range ports {
		b.add("Container", "PublishPort", p)
	}

	for _, m := range in.Mounts {
		var val string
		switch m.Type {
		case "volume":
			val = m.Name + ":" + m.Destination
		case "bind":
			val = m.Source + ":" + m.Destination
		default:
			b.warnf("mount of type %q at %s was not converted", m.Type, m.Destination)
			continue
		}
		var opts []string
		if !m.RW {
			opts = append(opts, "ro")
		}
		for _, o := range m.Options {
			if o == "z" || o == "Z" {
				opts = append(opts, o)
			}
		}
		if len(opts) > 0 {
			val += ":" + strings.Join(opts, ",")
		}
		b.add("Container", "Volume", val)
	}

	skippedEnv := []string{}
	for _, kv := range in.Config.Env {
		key, _, _ := strings.Cut(kv, "=")
		if boringEnv[key] {
			skippedEnv = append(skippedEnv, key)
			continue
		}
		b.add("Container", "Environment", quoteIfNeeded(kv))
	}
	if len(skippedEnv) > 0 {
		sort.Strings(skippedEnv)
		b.warnf("runtime-default env vars skipped: %s", strings.Join(skippedEnv, ", "))
	}

	for _, k := range sortedKeys(in.Config.Labels) {
		if boringLabel(k) {
			continue
		}
		b.add("Container", "Label", quoteIfNeeded(k+"="+in.Config.Labels[k]))
	}
	if in.Config.User != "" {
		b.add("Container", "User", in.Config.User)
	}
	if in.Config.WorkingDir != "" && in.Config.WorkingDir != "/" {
		b.add("Container", "WorkingDir", in.Config.WorkingDir)
	}
	switch ep := in.Config.Entrypoint.(type) {
	case string:
		if ep != "" {
			b.add("Container", "Entrypoint", ep)
		}
	case []any:
		if parts := asStrings(ep); len(parts) > 0 {
			b.add("Container", "Entrypoint", shellJoin(parts))
		}
	}
	if len(in.Config.Cmd) > 0 {
		b.add("Container", "Exec", shellJoin(in.Config.Cmd))
	}
	b.warnf("Exec= carries the container's full command line, which usually duplicates the image default — remove it unless you overrode the command")

	for _, d := range in.HostConfig.Devices {
		val := d.PathOnHost
		if d.PathInContainer != "" && d.PathInContainer != d.PathOnHost {
			val += ":" + d.PathInContainer
		}
		b.add("Container", "AddDevice", val)
	}
	for _, c := range in.HostConfig.CapAdd {
		b.add("Container", "AddCapability", c)
	}
	for _, c := range in.HostConfig.CapDrop {
		b.add("Container", "DropCapability", c)
	}
	for _, s := range in.HostConfig.SecurityOpt {
		if s == "label=disable" || s == "label:disable" {
			b.add("Container", "SecurityLabelDisable", "true")
		}
	}

	networks := sortedKeys(in.NetworkSettings.Networks)
	for _, n := range networks {
		if n == "podman" || n == "bridge" {
			continue // default network needs no declaration
		}
		b.add("Container", "Network", n)
		b.warnf("network %q must already exist on the host, or be defined as a %s.network unit (then use Network=%s.network)", n, n, n)
	}

	switch in.HostConfig.RestartPolicy.Name {
	case "always":
		b.add("Service", "Restart", "always")
	case "unless-stopped":
		b.add("Service", "Restart", "always")
		b.warnf("restart policy unless-stopped mapped to Restart=always")
	case "on-failure":
		b.add("Service", "Restart", "on-failure")
	}
	b.add("Install", "WantedBy", "default.target")

	return GeneratedUnit{Name: base + ".container", Content: b.render(), Warnings: b.warnings}, nil
}
