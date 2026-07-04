package convert

import (
	"fmt"
	"sort"
	"strings"
)

// FromCompose converts a compose file into a set of Quadlet units: one
// .container per service, plus .volume/.network units for top-level
// declarations. Service references to declared volumes/networks are
// rewritten to their <name>.volume / <name>.network unit form so Quadlet
// wires up the dependencies.
func FromCompose(src string) ([]GeneratedUnit, error) {
	root, err := parseYAML(src)
	if err != nil {
		return nil, err
	}
	doc, ok := root.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("compose file must be a YAML mapping at the top level")
	}
	services, ok := doc["services"].(map[string]any)
	if !ok || len(services) == 0 {
		return nil, fmt.Errorf("no services found in compose file")
	}

	declaredVolumes := topLevelNames(doc["volumes"])
	declaredNetworks := topLevelNames(doc["networks"])

	var units []GeneratedUnit
	for _, name := range sortedKeys(services) {
		svc, ok := services[name].(map[string]any)
		if !ok {
			units = append(units, GeneratedUnit{
				Name:     sanitizeName(name) + ".container",
				Warnings: []string{fmt.Sprintf("service %q skipped: not a mapping", name)},
			})
			continue
		}
		units = append(units, composeService(name, svc, services, declaredVolumes, declaredNetworks))
	}
	for _, v := range sortedKeys(declaredVolumes) {
		units = append(units, GeneratedUnit{Name: sanitizeName(v) + ".volume", Content: "[Volume]\n"})
	}
	for _, n := range sortedKeys(declaredNetworks) {
		units = append(units, GeneratedUnit{Name: sanitizeName(n) + ".network", Content: "[Network]\n"})
	}
	return units, nil
}

func topLevelNames(v any) map[string]any {
	m, _ := v.(map[string]any)
	if m == nil {
		m = map[string]any{}
	}
	return m
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// asStrings normalizes a YAML value that compose allows as scalar-or-list.
func asStrings(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []any:
		var out []string
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func composeService(name string, svc, allServices map[string]any, volumes, networks map[string]any) GeneratedUnit {
	b := newBuilder()
	base := sanitizeName(name)
	b.add("Unit", "Description", base+" (imported from compose)")

	image, _ := svc["image"].(string)
	if _, hasBuild := svc["build"]; hasBuild {
		b.warnf("build: is not converted — build the image separately (or use a .build unit) and set Image=")
	}
	if image == "" {
		image = "IMAGE-REQUIRED"
		b.warnf("service has no image:; fill in Image= before saving")
	}
	b.add("Container", "Image", image)

	if cn, ok := svc["container_name"].(string); ok {
		b.add("Container", "ContainerName", cn)
	}
	for _, p := range asStrings(svc["ports"]) {
		b.add("Container", "PublishPort", p)
	}
	if l, ok := svc["ports"].([]any); ok {
		for _, e := range l {
			if _, isMap := e.(map[string]any); isMap {
				b.warnf("long-syntax ports entry skipped; add a PublishPort= line manually")
			}
		}
	}
	composeVolumes(b, svc["volumes"], volumes)
	composeEnvironment(b, svc["environment"])
	for _, f := range asStrings(svc["env_file"]) {
		b.add("Container", "EnvironmentFile", f)
	}
	for _, n := range asStrings(svc["networks"]) {
		if _, declared := networks[n]; declared {
			b.add("Container", "Network", sanitizeName(n)+".network")
		} else {
			b.add("Container", "Network", n)
			b.warnf("network %q is not declared in this compose file; ensure it exists on the host", n)
		}
	}
	if m, ok := svc["networks"].(map[string]any); ok {
		for _, n := range sortedKeys(m) {
			if _, declared := networks[n]; declared {
				b.add("Container", "Network", sanitizeName(n)+".network")
			} else {
				b.add("Container", "Network", n)
			}
			if m[n] != nil {
				b.warnf("per-network options for %q (aliases, static IPs) were not converted", n)
			}
		}
	}
	composeSimpleKeys(b, svc)
	composeDependsOn(b, svc["depends_on"], allServices)
	composeHealthcheck(b, svc["healthcheck"])

	if r, ok := svc["restart"].(string); ok {
		switch r {
		case "always", "unless-stopped":
			b.add("Service", "Restart", "always")
			if r == "unless-stopped" {
				b.warnf("restart: unless-stopped mapped to Restart=always")
			}
		case "on-failure":
			b.add("Service", "Restart", "on-failure")
		}
	}
	b.add("Install", "WantedBy", "default.target")

	for key := range svc {
		switch key {
		case "image", "build", "container_name", "ports", "volumes", "environment",
			"env_file", "networks", "restart", "depends_on", "healthcheck",
			"command", "entrypoint", "user", "working_dir", "hostname", "privileged",
			"cap_add", "cap_drop", "devices", "read_only", "shm_size", "tmpfs",
			"labels", "dns", "extra_hosts", "security_opt", "stop_grace_period",
			"init", "pull_policy", "expose", "sysctls":
		default:
			b.warnf("compose key %q was not converted", key)
		}
	}

	return GeneratedUnit{Name: base + ".container", Content: b.render(), Warnings: b.warnings}
}

func composeVolumes(b *builder, v any, declared map[string]any) {
	list, _ := v.([]any)
	for _, e := range list {
		switch t := e.(type) {
		case string:
			src, rest, ok := strings.Cut(t, ":")
			if !ok {
				b.add("Container", "Volume", t) // anonymous volume
				continue
			}
			if _, isDeclared := declared[src]; isDeclared {
				src = sanitizeName(src) + ".volume"
			} else if !strings.HasPrefix(src, "/") && !strings.HasPrefix(src, ".") && !strings.HasPrefix(src, "~") {
				b.warnf("volume %q is not declared in this compose file; Podman will treat it as a named volume", src)
			}
			b.add("Container", "Volume", src+":"+rest)
		case map[string]any:
			typ, _ := t["type"].(string)
			source, _ := t["source"].(string)
			target, _ := t["target"].(string)
			if target == "" {
				b.warnf("long-syntax volume entry without target skipped")
				continue
			}
			if typ == "volume" {
				if _, isDeclared := declared[source]; isDeclared {
					source = sanitizeName(source) + ".volume"
				}
			}
			val := source + ":" + target
			if ro, _ := t["read_only"].(string); ro == "true" {
				val += ":ro"
			}
			b.add("Container", "Volume", val)
		}
	}
}

func composeEnvironment(b *builder, v any) {
	switch t := v.(type) {
	case map[string]any:
		for _, k := range sortedKeys(t) {
			val, _ := t[k].(string)
			b.add("Container", "Environment", quoteIfNeeded(k+"="+val))
		}
	case []any:
		for _, e := range t {
			if s, ok := e.(string); ok {
				b.add("Container", "Environment", quoteIfNeeded(s))
			}
		}
	}
}

// composeSimpleKeys handles scalar/list keys that map one-to-one.
func composeSimpleKeys(b *builder, svc map[string]any) {
	if c, ok := svc["command"]; ok {
		switch t := c.(type) {
		case string:
			b.add("Container", "Exec", t)
		case []any:
			b.add("Container", "Exec", shellJoin(asStrings(t)))
		}
	}
	if e, ok := svc["entrypoint"]; ok {
		switch t := e.(type) {
		case string:
			b.add("Container", "Entrypoint", t)
		case []any:
			b.add("Container", "Entrypoint", shellJoin(asStrings(t)))
		}
	}
	if u, ok := svc["user"].(string); ok {
		b.add("Container", "User", u)
	}
	if w, ok := svc["working_dir"].(string); ok {
		b.add("Container", "WorkingDir", w)
	}
	if h, ok := svc["hostname"].(string); ok {
		b.add("Container", "HostName", h)
	}
	if p, ok := svc["privileged"].(string); ok && p == "true" {
		b.add("Container", "PodmanArgs", "--privileged")
		b.warnf("privileged: true passed through as PodmanArgs")
	}
	for _, c := range asStrings(svc["cap_add"]) {
		b.add("Container", "AddCapability", c)
	}
	for _, c := range asStrings(svc["cap_drop"]) {
		b.add("Container", "DropCapability", c)
	}
	for _, d := range asStrings(svc["devices"]) {
		b.add("Container", "AddDevice", d)
	}
	if ro, ok := svc["read_only"].(string); ok && ro == "true" {
		b.add("Container", "ReadOnly", "true")
	}
	if s, ok := svc["shm_size"].(string); ok {
		b.add("Container", "ShmSize", s)
	}
	for _, tm := range asStrings(svc["tmpfs"]) {
		b.add("Container", "Tmpfs", tm)
	}
	switch labels := svc["labels"].(type) {
	case map[string]any:
		for _, k := range sortedKeys(labels) {
			val, _ := labels[k].(string)
			b.add("Container", "Label", quoteIfNeeded(k+"="+val))
		}
	case []any:
		for _, l := range asStrings(labels) {
			b.add("Container", "Label", quoteIfNeeded(l))
		}
	}
	for _, d := range asStrings(svc["dns"]) {
		b.add("Container", "DNS", d)
	}
	for _, h := range asStrings(svc["extra_hosts"]) {
		b.add("Container", "AddHost", h)
	}
	for _, s := range asStrings(svc["security_opt"]) {
		if s == "label=disable" || s == "label:disable" {
			b.add("Container", "SecurityLabelDisable", "true")
		} else {
			b.add("Container", "PodmanArgs", "--security-opt="+quoteIfNeeded(s))
		}
	}
	if g, ok := svc["stop_grace_period"].(string); ok {
		b.warnf("stop_grace_period %q not converted; set StopTimeout= (seconds) if needed", g)
	}
	if i, ok := svc["init"].(string); ok && i == "true" {
		b.add("Container", "RunInit", "true")
	}
	if p, ok := svc["pull_policy"].(string); ok {
		b.add("Container", "Pull", p)
	}
	for _, e := range asStrings(svc["expose"]) {
		b.add("Container", "ExposeHostPort", e)
	}
	if sysctls, ok := svc["sysctls"].(map[string]any); ok {
		for _, k := range sortedKeys(sysctls) {
			val, _ := sysctls[k].(string)
			b.add("Container", "Sysctl", k+"="+val)
		}
	}
}

func composeDependsOn(b *builder, v any, allServices map[string]any) {
	var deps []string
	switch t := v.(type) {
	case []any:
		deps = asStrings(t)
	case map[string]any:
		deps = sortedKeys(t)
	}
	for _, d := range deps {
		if _, ok := allServices[d]; !ok {
			b.warnf("depends_on %q does not match a service in this file", d)
			continue
		}
		service := sanitizeName(d) + ".service"
		b.add("Unit", "Wants", service)
		b.add("Unit", "After", service)
	}
}

func composeHealthcheck(b *builder, v any) {
	hc, ok := v.(map[string]any)
	if !ok {
		return
	}
	if disable, _ := hc["disable"].(string); disable == "true" {
		b.add("Container", "NoHealthcheck", "true")
		return
	}
	switch test := hc["test"].(type) {
	case string:
		b.add("Container", "HealthCmd", test)
	case []any:
		parts := asStrings(test)
		if len(parts) > 0 && (parts[0] == "CMD" || parts[0] == "CMD-SHELL") {
			parts = parts[1:]
		}
		if len(parts) > 0 {
			b.add("Container", "HealthCmd", shellJoin(parts))
		}
	}
	for _, k := range [][2]string{
		{"interval", "HealthInterval"}, {"timeout", "HealthTimeout"},
		{"retries", "HealthRetries"}, {"start_period", "HealthStartPeriod"},
	} {
		if val, ok := hc[k[0]].(string); ok {
			b.add("Container", k[1], val)
		}
	}
}
