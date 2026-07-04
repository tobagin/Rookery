package convert

import (
	"fmt"
	"strings"
)

// runState carries the few values that influence the whole unit rather than
// mapping to a single key.
type runState struct {
	name string
}

type flagSpec struct {
	takesValue bool
	handle     func(b *builder, st *runState, val string)
}

// containerKey maps a flag straight onto a [Container] key.
func containerKey(key string) flagSpec {
	return flagSpec{true, func(b *builder, _ *runState, v string) {
		b.add("Container", key, quoteIfNeeded(v))
	}}
}

// containerBool sets a [Container] boolean key.
func containerBool(key string) flagSpec {
	return flagSpec{false, func(b *builder, _ *runState, _ string) {
		b.add("Container", key, "true")
	}}
}

// dropped ignores flags whose job systemd/Quadlet takes over.
func dropped(reason string) flagSpec {
	return flagSpec{false, func(b *builder, _ *runState, _ string) {
		if reason != "" {
			b.warnf("%s", reason)
		}
	}}
}

func passthroughValue(flag string) flagSpec {
	return flagSpec{true, func(b *builder, _ *runState, v string) {
		b.add("Container", "PodmanArgs", flag+"="+quoteIfNeeded(v))
	}}
}

var longFlags = map[string]flagSpec{
	"name": {true, func(b *builder, st *runState, v string) {
		st.name = v
		b.add("Container", "ContainerName", v)
	}},
	"publish":     containerKey("PublishPort"),
	"expose":      containerKey("ExposeHostPort"),
	"volume":      containerKey("Volume"),
	"mount":       containerKey("Mount"),
	"env":         containerKey("Environment"),
	"env-file":    containerKey("EnvironmentFile"),
	"env-host":    containerBool("EnvironmentHost"),
	"network":     containerKey("Network"),
	"net":         containerKey("Network"),
	"user":        containerKey("User"),
	"userns":      containerKey("UserNS"),
	"device":      containerKey("AddDevice"),
	"cap-add":     containerKey("AddCapability"),
	"cap-drop":    containerKey("DropCapability"),
	"label":       containerKey("Label"),
	"hostname":    containerKey("HostName"),
	"ip":          containerKey("IP"),
	"ip6":         containerKey("IP6"),
	"dns":         containerKey("DNS"),
	"dns-search":  containerKey("DNSSearch"),
	"dns-option":  containerKey("DNSOption"),
	"entrypoint":  containerKey("Entrypoint"),
	"workdir":     containerKey("WorkingDir"),
	"add-host":    containerKey("AddHost"),
	"group-add":   containerKey("GroupAdd"),
	"tmpfs":       containerKey("Tmpfs"),
	"shm-size":    containerKey("ShmSize"),
	"pull":        containerKey("Pull"),
	"secret":      containerKey("Secret"),
	"sysctl":      containerKey("Sysctl"),
	"ulimit":      containerKey("Ulimit"),
	"pids-limit":  containerKey("PidsLimit"),
	"log-driver":  containerKey("LogDriver"),
	"log-opt":     containerKey("LogOpt"),
	"stop-signal": containerKey("StopSignal"),
	"stop-timeout": {true, func(b *builder, _ *runState, v string) {
		b.add("Container", "StopTimeout", v)
	}},
	"health-cmd":          containerKey("HealthCmd"),
	"health-interval":     containerKey("HealthInterval"),
	"health-retries":      containerKey("HealthRetries"),
	"health-timeout":      containerKey("HealthTimeout"),
	"health-start-period": containerKey("HealthStartPeriod"),
	"health-on-failure":   containerKey("HealthOnFailure"),
	"no-healthcheck":      containerBool("NoHealthcheck"),
	"read-only":           containerBool("ReadOnly"),
	"init":                containerBool("RunInit"),
	"restart": {true, func(b *builder, _ *runState, v string) {
		switch {
		case v == "always":
			b.add("Service", "Restart", "always")
		case v == "unless-stopped":
			b.add("Service", "Restart", "always")
			b.warnf("--restart unless-stopped has no systemd equivalent; mapped to Restart=always")
		case v == "on-failure" || strings.HasPrefix(v, "on-failure:"):
			b.add("Service", "Restart", "on-failure")
			if n, ok := strings.CutPrefix(v, "on-failure:"); ok {
				b.add("Service", "StartLimitBurst", n)
			}
		case v == "no", v == "never":
			// systemd default
		default:
			b.warnf("unknown --restart policy %q ignored", v)
		}
	}},
	"gpus": {true, func(b *builder, _ *runState, v string) {
		if v == "all" {
			b.add("Container", "AddDevice", "nvidia.com/gpu=all")
		} else {
			b.add("Container", "AddDevice", "nvidia.com/gpu="+v)
		}
		b.warnf("--gpus mapped to CDI device nvidia.com/gpu=%s; requires nvidia-container-toolkit with CDI specs generated", v)
	}},
	"security-opt": {true, func(b *builder, _ *runState, v string) {
		switch v {
		case "label=disable", "label:disable":
			b.add("Container", "SecurityLabelDisable", "true")
		case "no-new-privileges":
			b.add("Container", "NoNewPrivileges", "true")
		default:
			b.add("Container", "PodmanArgs", "--security-opt="+quoteIfNeeded(v))
		}
	}},
	"privileged": {false, func(b *builder, _ *runState, _ string) {
		b.add("Container", "PodmanArgs", "--privileged")
		b.warnf("--privileged passed through as PodmanArgs; consider granting specific capabilities/devices instead")
	}},
	"pod": {true, func(b *builder, _ *runState, v string) {
		b.add("Container", "Pod", v+".pod")
		b.warnf("--pod %s mapped to Pod=%s.pod — create a matching %s.pod unit for it", v, v, v)
	}},
	"memory":      passthroughValue("--memory"),
	"memory-swap": passthroughValue("--memory-swap"),
	"cpus":        passthroughValue("--cpus"),
	"cpu-shares":  passthroughValue("--cpu-shares"),
	"detach":      dropped(""),
	"rm":          dropped(""),
	"interactive": dropped("-i/--interactive dropped: quadlet services are not interactive"),
	"tty":         dropped(""),
	"replace":     dropped(""),
}

var shortFlags = map[byte]string{
	'p': "publish",
	'v': "volume",
	'e': "env",
	'u': "user",
	'w': "workdir",
	'l': "label",
	'm': "memory",
	'h': "hostname",
	'd': "detach",
	'i': "interactive",
	't': "tty",
	'q': "detach", // -q has no unit-file meaning; treat as droppable bool
}

// FromRunCommand converts a `podman run ...` (or `docker run ...`) command
// line into a .container unit.
func FromRunCommand(cmd string) (GeneratedUnit, error) {
	words, err := splitWords(cmd)
	if err != nil {
		return GeneratedUnit{}, err
	}
	for len(words) > 0 && (words[0] == "sudo" || words[0] == "podman" || words[0] == "docker" || strings.HasSuffix(words[0], "/podman")) {
		words = words[1:]
	}
	if len(words) == 0 || words[0] != "run" {
		return GeneratedUnit{}, fmt.Errorf("expected a `podman run ...` command")
	}
	words = words[1:]

	b := newBuilder()
	st := &runState{}
	var image string
	var args []string

	for i := 0; i < len(words); i++ {
		w := words[i]
		if image != "" {
			args = append(args, w)
			continue
		}
		switch {
		case w == "--":
			// end of flags; next word is the image
		case strings.HasPrefix(w, "--"):
			flagName, val, hasVal := strings.Cut(w[2:], "=")
			spec, known := longFlags[flagName]
			if !known {
				if hasVal {
					b.add("Container", "PodmanArgs", "--"+flagName+"="+quoteIfNeeded(val))
					b.warnf("unrecognized flag --%s passed through as PodmanArgs", flagName)
				} else {
					b.add("Container", "PodmanArgs", "--"+flagName)
					b.warnf("unrecognized flag --%s passed through as PodmanArgs; if it takes a value, use --%s=value form and re-convert", flagName, flagName)
				}
				continue
			}
			if spec.takesValue && !hasVal {
				if i+1 >= len(words) {
					return GeneratedUnit{}, fmt.Errorf("flag --%s is missing its value", flagName)
				}
				i++
				val = words[i]
			}
			spec.handle(b, st, val)
		case strings.HasPrefix(w, "-") && len(w) > 1:
			body := w[1:]
			for j := 0; j < len(body); j++ {
				longName, known := shortFlags[body[j]]
				if !known {
					b.warnf("unrecognized short flag -%c ignored", body[j])
					continue
				}
				spec := longFlags[longName]
				if spec.takesValue {
					val := body[j+1:]
					if val == "" {
						if i+1 >= len(words) {
							return GeneratedUnit{}, fmt.Errorf("flag -%c is missing its value", body[j])
						}
						i++
						val = words[i]
					}
					spec.handle(b, st, val)
					break
				}
				spec.handle(b, st, "")
			}
		default:
			image = w
		}
	}
	if image == "" {
		return GeneratedUnit{}, fmt.Errorf("no image found in command")
	}

	b.addFirst("Container", "Image", image)
	if len(args) > 0 {
		b.add("Container", "Exec", shellJoin(args))
	}
	unitBase := st.name
	if unitBase == "" {
		unitBase = imageBaseName(image)
	}
	unitBase = sanitizeName(unitBase)
	b.add("Unit", "Description", unitBase+" (imported from podman run)")
	b.add("Install", "WantedBy", "default.target")

	return GeneratedUnit{
		Name:     unitBase + ".container",
		Content:  b.render(),
		Warnings: b.warnings,
	}, nil
}
