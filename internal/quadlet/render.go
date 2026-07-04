package quadlet

import (
	"fmt"
	"strings"
)

// String renders the file back to systemd unit syntax, one blank line
// between sections.
func (f *File) String() string {
	var b strings.Builder
	for i, s := range f.Sections {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "[%s]\n", s.Name)
		for _, e := range s.Entries {
			fmt.Fprintf(&b, "%s=%s\n", e.Key, e.Value)
		}
	}
	return b.String()
}
