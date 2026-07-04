package convert

import (
	"fmt"
	"strings"
)

// splitWords tokenizes a shell command the way a POSIX shell would split it:
// single quotes are literal, double quotes allow \" escapes, backslash
// escapes the next character, and a backslash-newline is a line
// continuation (pasted multi-line podman run commands are the common case).
func splitWords(s string) ([]string, error) {
	var words []string
	var cur strings.Builder
	inWord := false
	flush := func() {
		if inWord {
			words = append(words, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == '\'':
			j := strings.IndexByte(s[i+1:], '\'')
			if j < 0 {
				return nil, fmt.Errorf("unclosed single quote")
			}
			cur.WriteString(s[i+1 : i+1+j])
			inWord = true
			i += j + 2
		case c == '"':
			i++
			for i < len(s) && s[i] != '"' {
				if s[i] == '\\' && i+1 < len(s) && strings.IndexByte("\"\\$`", s[i+1]) >= 0 {
					i++
				}
				cur.WriteByte(s[i])
				i++
			}
			if i >= len(s) {
				return nil, fmt.Errorf("unclosed double quote")
			}
			inWord = true
			i++
		case c == '\\':
			if i+1 >= len(s) {
				return nil, fmt.Errorf("trailing backslash")
			}
			if s[i+1] == '\n' {
				i += 2 // line continuation
				continue
			}
			cur.WriteByte(s[i+1])
			inWord = true
			i += 2
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			flush()
			i++
		default:
			cur.WriteByte(c)
			inWord = true
			i++
		}
	}
	flush()
	return words, nil
}
