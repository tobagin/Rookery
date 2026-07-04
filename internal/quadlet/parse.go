package quadlet

import (
	"fmt"
	"strings"
)

// File is a parsed systemd-style unit file. Order and repeated keys are
// preserved: Quadlet relies on repeated keys (Volume=, PublishPort=, ...).
type File struct {
	Sections []*Section
}

// Section is one [Name] block with its entries in file order.
type Section struct {
	Name    string
	Entries []Entry
}

// Entry is a single Key=Value line.
type Entry struct {
	Key   string
	Value string
}

// Parse reads systemd unit syntax: [Section] headers, Key=Value entries,
// '#'/';' comment lines, and trailing-backslash line continuations.
func Parse(data []byte) (*File, error) {
	f := &File{}
	var cur *Section
	lines := strings.Split(string(data), "\n")
	for i := 0; i < len(lines); i++ {
		lineNo := i + 1
		line := strings.TrimSpace(lines[i])
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		for strings.HasSuffix(line, "\\") && i+1 < len(lines) {
			i++
			line = strings.TrimSpace(strings.TrimSuffix(line, "\\")) + " " + strings.TrimSpace(lines[i])
		}
		if strings.HasPrefix(line, "[") {
			if !strings.HasSuffix(line, "]") || len(line) < 3 {
				return nil, fmt.Errorf("line %d: malformed section header %q", lineNo, line)
			}
			cur = &Section{Name: line[1 : len(line)-1]}
			f.Sections = append(f.Sections, cur)
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: expected key=value, got %q", lineNo, line)
		}
		if cur == nil {
			return nil, fmt.Errorf("line %d: entry %q outside of any section", lineNo, line)
		}
		cur.Entries = append(cur.Entries, Entry{Key: strings.TrimSpace(key), Value: strings.TrimSpace(value)})
	}
	return f, nil
}

// Get returns the last value of key in section (systemd semantics: last
// assignment wins) and whether it was present.
func (f *File) Get(section, key string) (string, bool) {
	var val string
	found := false
	for _, v := range f.All(section, key) {
		val, found = v, true
	}
	return val, found
}

// All returns every value assigned to key in section, in file order.
func (f *File) All(section, key string) []string {
	var out []string
	for _, s := range f.Sections {
		if s.Name != section {
			continue
		}
		for _, e := range s.Entries {
			if e.Key == key {
				out = append(out, e.Value)
			}
		}
	}
	return out
}
