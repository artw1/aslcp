package config

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"
)

// ini is a parsed INI file: the last value of each section and key (both
// lower case).
type ini map[string]map[string]string

// parseINI reads "[section]" headers and "key = value" lines. A ";" or "#"
// starts a comment (at the start of a line, or after a blank inside one).
// Keys before the first section belong to section "".
func parseINI(data []byte) (ini, error) {
	f := ini{}
	sec := ""
	sc := bufio.NewScanner(bytes.NewReader(data))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(stripComment(sc.Text()))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") {
			end := strings.IndexByte(line, ']')
			if end < 0 {
				return nil, fmt.Errorf("line %d: unterminated section header", n)
			}
			sec = strings.ToLower(strings.TrimSpace(line[1:end]))
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return nil, fmt.Errorf("line %d: expected key = value", n)
		}
		k = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(k, ">")))
		if k == "" {
			return nil, fmt.Errorf("line %d: empty key", n)
		}
		if f[sec] == nil {
			f[sec] = map[string]string{}
		}
		f[sec][k] = strings.TrimSpace(v)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("reading: %w", err)
	}
	return f, nil
}

// stripComment cuts a line at a comment: a ";" or "#" at the start or after
// a blank (so a URL fragment such as "a#b" stays).
func stripComment(line string) string {
	for i := 0; i < len(line); i++ {
		if line[i] != ';' && line[i] != '#' {
			continue
		}
		if i == 0 || line[i-1] == ' ' || line[i-1] == '\t' {
			return line[:i]
		}
	}
	return line
}

func (f ini) get(sec, key string) (string, bool) {
	v, ok := f[sec][key]
	return v, ok
}
