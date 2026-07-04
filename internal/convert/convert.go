// Package convert turns existing container definitions — `podman run`
// commands, compose files, running containers — into Quadlet unit files.
// This is the migration path: it aims for a faithful, reviewable draft that
// the editor then validates with the host generator; anything it cannot map
// cleanly becomes an explicit warning, never a silent drop.
package convert

import (
	"fmt"
	"strings"

	"github.com/tobagin/rookery/internal/quadlet"
)

// GeneratedUnit is one proposed Quadlet file. Warnings list everything the
// converter had to guess about or leave behind.
type GeneratedUnit struct {
	Name     string   `json:"name"`
	Content  string   `json:"content"`
	Warnings []string `json:"warnings"`
}

// builder accumulates entries per section and renders them in canonical
// order.
type builder struct {
	sections map[string]*quadlet.Section
	warnings []string
}

func newBuilder() *builder {
	return &builder{sections: map[string]*quadlet.Section{}}
}

func (b *builder) section(name string) *quadlet.Section {
	s := b.sections[name]
	if s == nil {
		s = &quadlet.Section{Name: name}
		b.sections[name] = s
	}
	return s
}

func (b *builder) add(section, key, value string) {
	s := b.section(section)
	s.Entries = append(s.Entries, quadlet.Entry{Key: key, Value: value})
}

func (b *builder) addFirst(section, key, value string) {
	s := b.section(section)
	s.Entries = append([]quadlet.Entry{{Key: key, Value: value}}, s.Entries...)
}

func (b *builder) warnf(format string, args ...any) {
	b.warnings = append(b.warnings, fmt.Sprintf(format, args...))
}

var sectionOrder = []string{"Unit", "Container", "Pod", "Kube", "Network", "Volume", "Image", "Build", "Service", "Install"}

func (b *builder) render() string {
	f := &quadlet.File{}
	for _, name := range sectionOrder {
		if s, ok := b.sections[name]; ok && len(s.Entries) > 0 {
			f.Sections = append(f.Sections, s)
		}
	}
	return f.String()
}

// sanitizeName reduces an arbitrary string to a safe systemd unit base name.
func sanitizeName(s string) string {
	var out strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			out.WriteRune(r)
		default:
			out.WriteRune('-')
		}
	}
	name := strings.Trim(out.String(), "-.")
	if name == "" {
		name = "imported"
	}
	return name
}

// imageBaseName extracts a unit-name candidate from an image reference:
// docker.io/jellyfin/jellyfin:latest -> jellyfin.
func imageBaseName(image string) string {
	base := image
	if i := strings.LastIndex(base, "@"); i >= 0 {
		base = base[:i]
	}
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	if i := strings.LastIndex(base, ":"); i >= 0 {
		base = base[:i]
	}
	return sanitizeName(base)
}

// quoteIfNeeded wraps a value in double quotes when systemd would otherwise
// split it on whitespace.
func quoteIfNeeded(v string) string {
	if strings.ContainsAny(v, " \t") && !strings.HasPrefix(v, `"`) {
		return `"` + strings.ReplaceAll(v, `"`, `\"`) + `"`
	}
	return v
}

// shellJoin renders command arguments back into one Exec= line.
func shellJoin(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		if a == "" || strings.ContainsAny(a, " \t\"'") {
			parts[i] = `"` + strings.ReplaceAll(a, `"`, `\"`) + `"`
		} else {
			parts[i] = a
		}
	}
	return strings.Join(parts, " ")
}
