package convert

import (
	"fmt"
	"strings"
)

// miniyaml is a deliberately small block-style YAML reader — enough for the
// compose files people actually write (nested maps, lists, quoted scalars,
// simple flow lists), kept in-tree so Rookery stays dependency-free.
// Anchors, aliases, multi-line scalars, and flow maps are rejected with a
// clear error instead of being mis-read. Scalars are returned as strings.

type yamlLine struct {
	indent int
	text   string
	no     int
}

type yamlParser struct {
	lines []yamlLine
	pos   int
}

func parseYAML(src string) (any, error) {
	var lines []yamlLine
	for no, raw := range strings.Split(src, "\n") {
		line := strings.TrimRight(raw, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || trimmed == "---" || trimmed == "..." {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if strings.HasPrefix(strings.TrimLeft(line, " "), "\t") || strings.HasPrefix(line, "\t") {
			return nil, fmt.Errorf("yaml line %d: tab indentation is not allowed", no+1)
		}
		text := stripComment(trimmed)
		if text == "" {
			continue
		}
		if strings.ContainsAny(text, "&*") && (strings.HasPrefix(text, "&") || strings.Contains(text, " &") || strings.Contains(text, " *")) {
			return nil, fmt.Errorf("yaml line %d: anchors/aliases are not supported by the built-in parser", no+1)
		}
		lines = append(lines, yamlLine{indent: indent, text: text, no: no + 1})
	}
	p := &yamlParser{lines: lines}
	v, err := p.block(0)
	if err != nil {
		return nil, err
	}
	if p.pos < len(p.lines) {
		return nil, fmt.Errorf("yaml line %d: unexpected indentation", p.lines[p.pos].no)
	}
	return v, nil
}

// stripComment removes a trailing " #comment" that is not inside quotes.
func stripComment(s string) string {
	inS, inD := false, false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\'':
			if !inD {
				inS = !inS
			}
		case '"':
			if !inS {
				inD = !inD
			}
		case '#':
			if !inS && !inD && i > 0 && (s[i-1] == ' ' || s[i-1] == '\t') {
				return strings.TrimSpace(s[:i])
			}
		}
	}
	return s
}

func (p *yamlParser) block(minIndent int) (any, error) {
	if p.pos >= len(p.lines) || p.lines[p.pos].indent < minIndent {
		return nil, nil
	}
	ind := p.lines[p.pos].indent
	if isListItem(p.lines[p.pos].text) {
		return p.list(ind)
	}
	return p.mapping(ind)
}

func isListItem(text string) bool {
	return text == "-" || strings.HasPrefix(text, "- ")
}

func (p *yamlParser) mapping(ind int) (map[string]any, error) {
	m := map[string]any{}
	for p.pos < len(p.lines) {
		l := p.lines[p.pos]
		if l.indent < ind || isListItem(l.text) {
			break
		}
		if l.indent > ind {
			return nil, fmt.Errorf("yaml line %d: unexpected indentation", l.no)
		}
		key, rest, ok := splitKeyValue(l.text)
		if !ok {
			return nil, fmt.Errorf("yaml line %d: expected `key:` or `key: value`, got %q", l.no, l.text)
		}
		p.pos++
		val, err := p.valueFor(rest, ind, l.no)
		if err != nil {
			return nil, err
		}
		m[key] = val
	}
	return m, nil
}

// valueFor resolves what follows a "key:" — an inline scalar, a nested
// block, a same-indent list, or null.
func (p *yamlParser) valueFor(rest string, ind, lineNo int) (any, error) {
	if rest != "" {
		if rest == "|" || rest == ">" || strings.HasPrefix(rest, "|") || strings.HasPrefix(rest, ">") {
			return nil, fmt.Errorf("yaml line %d: multi-line scalars (| and >) are not supported by the built-in parser", lineNo)
		}
		return parseScalar(rest, lineNo)
	}
	if p.pos < len(p.lines) {
		next := p.lines[p.pos]
		if next.indent > ind {
			return p.block(ind + 1)
		}
		if next.indent == ind && isListItem(next.text) {
			return p.list(ind)
		}
	}
	return nil, nil
}

func (p *yamlParser) list(ind int) ([]any, error) {
	out := []any{}
	for p.pos < len(p.lines) {
		l := p.lines[p.pos]
		if l.indent != ind || !isListItem(l.text) {
			break
		}
		rest := strings.TrimSpace(strings.TrimPrefix(l.text, "-"))
		restCol := ind + 2
		p.pos++
		if rest == "" {
			child, err := p.block(ind + 1)
			if err != nil {
				return nil, err
			}
			out = append(out, child)
			continue
		}
		if key, val, ok := splitKeyValue(rest); ok {
			// "- key: ..." opens a map whose further keys sit at restCol.
			m := map[string]any{}
			v, err := p.valueFor(val, restCol, l.no)
			if err != nil {
				return nil, err
			}
			m[key] = v
			for p.pos < len(p.lines) && p.lines[p.pos].indent == restCol && !isListItem(p.lines[p.pos].text) {
				row := p.lines[p.pos]
				k2, v2raw, ok := splitKeyValue(row.text)
				if !ok {
					return nil, fmt.Errorf("yaml line %d: expected `key: value`, got %q", row.no, row.text)
				}
				p.pos++
				v2, err := p.valueFor(v2raw, restCol, row.no)
				if err != nil {
					return nil, err
				}
				m[k2] = v2
			}
			out = append(out, m)
			continue
		}
		v, err := parseScalar(rest, l.no)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// splitKeyValue recognizes "key:" / "key: value" mapping rows. Quoted
// scalars and values whose colon has no following space (e.g. "80:80") are
// not mapping rows.
func splitKeyValue(text string) (key, value string, ok bool) {
	if strings.HasPrefix(text, `"`) || strings.HasPrefix(text, "'") {
		return "", "", false
	}
	if i := strings.Index(text, ": "); i >= 0 {
		return unquote(strings.TrimSpace(text[:i])), strings.TrimSpace(text[i+2:]), true
	}
	if strings.HasSuffix(text, ":") {
		return unquote(strings.TrimSpace(text[:len(text)-1])), "", true
	}
	return "", "", false
}

func parseScalar(s string, lineNo int) (any, error) {
	if strings.HasPrefix(s, "[") {
		if !strings.HasSuffix(s, "]") {
			return nil, fmt.Errorf("yaml line %d: unclosed flow list", lineNo)
		}
		inner := strings.TrimSpace(s[1 : len(s)-1])
		if inner == "" {
			return []any{}, nil
		}
		var items []any
		for _, part := range splitFlowItems(inner) {
			items = append(items, unquote(strings.TrimSpace(part)))
		}
		return items, nil
	}
	if strings.HasPrefix(s, "{") {
		return nil, fmt.Errorf("yaml line %d: flow maps ({...}) are not supported by the built-in parser", lineNo)
	}
	if s == "~" || s == "null" {
		return nil, nil
	}
	return unquote(s), nil
}

// splitFlowItems splits "a, b, 'c,d'" on commas outside quotes.
func splitFlowItems(s string) []string {
	var parts []string
	depth := 0
	inS, inD := false, false
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\'':
			if !inD {
				inS = !inS
			}
		case '"':
			if !inS {
				inD = !inD
			}
		case '[':
			depth++
		case ']':
			depth--
		case ',':
			if !inS && !inD && depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, s[start:])
	return parts
}

func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			body := s[1 : len(s)-1]
			if s[0] == '"' {
				body = strings.ReplaceAll(body, `\"`, `"`)
				body = strings.ReplaceAll(body, `\\`, `\`)
			}
			return body
		}
	}
	return s
}
