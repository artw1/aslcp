// Package ini is a lossless, line-oriented editor for the ini files of the
// previous digital voice stack (Analog_Bridge.ini, MMDVM_Bridge.ini, the gateway
// inis and the USRP ini). It changes
// values only: comments, blank lines, ordering, key spacing, inline comments
// and line endings stay byte for byte.
//
// Syntax handled (the union of what those tools read):
//   - "[name]" starts a section (trailing text after "]" is kept);
//   - a line whose first non-blank character is ';' or '#' is a comment;
//   - "key = value" or "key=value"; the text between the key and the value,
//     and between the value and an inline comment, is kept as written;
//   - an inline comment starts at whitespace followed by ';' (as Python's
//     configparser with inline_comment_prefixes=(';',), which the stack's helper scripts use).
//
// Sections and keys are matched case-sensitively and exactly.
package ini

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
)

type kind int

const (
	kBlank kind = iota
	kComment
	kSection
	kKV
	kOther // not understood: kept as is
)

type line struct {
	raw  string // the line without its end-of-line
	eol  string // "\n", "\r\n" or "" (last line without a newline)
	kind kind

	section string // kSection: its name; kKV: the section the line is in
	key     string

	// kKV: raw[:vs] is the key and separator, raw[vs:ve] the value and
	// raw[ve:] the whitespace and inline comment after it.
	vs, ve int
}

// File is a parsed ini file.
type File struct {
	bom   string
	lines []line
}

// Parse never fails: a line it does not understand is kept as is.
func Parse(data []byte) *File {
	f := &File{}
	s := string(data)
	if strings.HasPrefix(s, "\xef\xbb\xbf") {
		f.bom, s = "\xef\xbb\xbf", s[3:]
	}
	section := ""
	for len(s) > 0 {
		var raw, eol string
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			raw, s, eol = s[:i], s[i+1:], "\n"
			if strings.HasSuffix(raw, "\r") {
				raw, eol = raw[:len(raw)-1], "\r\n"
			}
		} else {
			raw, s = s, ""
		}
		l := line{raw: raw, eol: eol}
		t := strings.TrimLeft(raw, " \t")
		switch {
		case t == "":
			l.kind = kBlank
		case t[0] == ';' || t[0] == '#':
			l.kind = kComment
		case t[0] == '[':
			if j := strings.IndexByte(t, ']'); j > 0 {
				l.kind, l.section = kSection, t[1:j]
				section = l.section
			} else {
				l.kind = kOther
			}
		default:
			if k, vs, ve, ok := splitKV(raw); ok {
				l.kind, l.key, l.vs, l.ve, l.section = kKV, k, vs, ve, section
			} else {
				l.kind = kOther
			}
		}
		if l.kind != kKV {
			l.section = section
		}
		f.lines = append(f.lines, l)
	}
	return f
}

// splitKV splits "key <ws> = <ws> value <ws> ; comment".
func splitKV(raw string) (key string, vs, ve int, ok bool) {
	eq := strings.IndexByte(raw, '=')
	if eq <= 0 {
		return "", 0, 0, false
	}
	key = strings.TrimSpace(raw[:eq])
	if key == "" || strings.ContainsAny(key, " \t") {
		return "", 0, 0, false
	}
	vs = eq + 1
	for vs < len(raw) && (raw[vs] == ' ' || raw[vs] == '\t') {
		vs++
	}
	end := len(raw)
	// Inline comment: whitespace followed by ';'.
	for i := vs; i < len(raw); i++ {
		if raw[i] == ';' && i > vs && (raw[i-1] == ' ' || raw[i-1] == '\t') {
			end = i
			break
		}
	}
	// The value stops before the whitespace that precedes the comment (or
	// the end of the line).
	ve = end
	for ve > vs && (raw[ve-1] == ' ' || raw[ve-1] == '\t') {
		ve--
	}
	return key, vs, ve, true
}

// Bytes returns the file content. Parse(b).Bytes() == b for any input.
func (f *File) Bytes() []byte {
	var b bytes.Buffer
	b.WriteString(f.bom)
	for _, l := range f.lines {
		b.WriteString(l.raw)
		b.WriteString(l.eol)
	}
	return b.Bytes()
}

// HasSection reports whether a "[name]" line exists.
func (f *File) HasSection(name string) bool {
	for _, l := range f.lines {
		if l.kind == kSection && l.section == name {
			return true
		}
	}
	return false
}

// Get returns the value of the first key in the section (inline comment and
// surrounding blanks removed).
func (f *File) Get(section, key string) (string, bool) {
	for _, l := range f.lines {
		if l.kind == kKV && l.section == section && l.key == key {
			return l.raw[l.vs:l.ve], true
		}
	}
	return "", false
}

// GetFold is Get with the key compared case-insensitively (section still
// exact), like Python's configparser, which the stack's scripts read the
// USRP ini with. Not for the MMDVM-family files, whose parsers are case-sensitive.
func (f *File) GetFold(section, key string) (string, bool) {
	for _, l := range f.lines {
		if l.kind == kKV && l.section == section && strings.EqualFold(l.key, key) {
			return l.raw[l.vs:l.ve], true
		}
	}
	return "", false
}

// Sections returns the section names in file order (a repeated name once).
func (f *File) Sections() []string {
	var out []string
	seen := map[string]bool{}
	for _, l := range f.lines {
		if l.kind == kSection && !seen[l.section] {
			seen[l.section] = true
			out = append(out, l.section)
		}
	}
	return out
}

// KeyFold returns the spelling of the first key in the section that matches
// key case-insensitively, or key itself when there is none. A writer of a
// case-insensitive file (the USRP ini) passes it to Set so that it replaces
// the existing line instead of adding a second spelling.
func (f *File) KeyFold(section, key string) string {
	for _, l := range f.lines {
		if l.kind == kKV && l.section == section && strings.EqualFold(l.key, key) {
			return l.key
		}
	}
	return key
}

// Errors of Set.
var (
	ErrNoSection = errors.New("section not found")
	ErrBadValue  = errors.New("value cannot be written to an ini file")
)

// CheckValue refuses values that could not be read back as written: control
// characters (a line break would inject lines), leading or trailing blanks and
// an inline-comment start (blank followed by ';').
func CheckValue(v string) error {
	for _, r := range v {
		if r < 0x20 || r == 0x7f {
			return ErrBadValue
		}
	}
	if v != strings.TrimSpace(v) || strings.Contains(v, " ;") || strings.Contains(v, "\t;") || strings.HasPrefix(v, ";") {
		return ErrBadValue
	}
	return nil
}

// Set replaces the value of the first matching key. When the key is missing
// from an existing section it is added: below a commented example of the same
// key ("# Startup=..."), else after the last key of the section, in the
// section's spacing style. A missing section is ErrNoSection (the file is
// never restructured). It reports whether the file changed.
func (f *File) Set(section, key, value string) (bool, error) {
	if err := CheckValue(value); err != nil {
		return false, err
	}
	if key == "" || strings.ContainsAny(key, "=[] \t\r\n;#") {
		return false, fmt.Errorf("invalid key")
	}
	if !f.HasSection(section) {
		return false, ErrNoSection
	}
	for i := range f.lines {
		l := &f.lines[i]
		if l.kind != kKV || l.section != section || l.key != key {
			continue
		}
		if l.raw[l.vs:l.ve] == value {
			return false, nil
		}
		l.raw = l.raw[:l.vs] + value + l.raw[l.ve:]
		l.ve = l.vs + len(value)
		return true, nil
	}
	f.insert(section, key, value)
	return true, nil
}

func (f *File) insert(section, key, value string) {
	first, last, lastKV, example := -1, -1, -1, -1
	for i, l := range f.lines {
		if l.kind == kSection && l.section == section && first < 0 {
			first = i
		}
		if first >= 0 {
			if i > first && l.kind == kSection {
				break
			}
			last = i
			if l.kind == kKV {
				lastKV = i
			}
			if l.kind == kComment && example < 0 && isExample(l.raw, key) {
				example = i
			}
		}
	}
	at := lastKV
	if example >= 0 {
		at = example
	}
	if at < 0 {
		at = first
	}
	// Style: copy the separator of the nearest key line of the section.
	sep := "="
	for i := first + 1; i <= last; i++ {
		if l := f.lines[i]; l.kind == kKV && l.section == section {
			if j := strings.IndexByte(l.raw, '='); j > 0 {
				k := strings.TrimRight(l.raw[:j], " \t")
				sep = l.raw[len(k):l.vs]
				if sep == "" {
					sep = "="
				}
			}
			break
		}
	}
	nl := line{kind: kKV, section: section, key: key}
	nl.raw = key + sep + value
	nl.vs, nl.ve = len(key)+len(sep), len(nl.raw)
	anchor := &f.lines[at]
	nl.eol = anchor.eol
	if nl.eol == "" { // anchor was the last line without a newline
		anchor.eol, nl.eol = "\n", ""
	}
	f.lines = append(f.lines, line{})
	copy(f.lines[at+2:], f.lines[at+1:])
	f.lines[at+1] = nl
}

// isExample: a comment line like "# Startup=x" or ";Startup = x".
func isExample(raw, key string) bool {
	t := strings.TrimLeft(raw, " \t")
	t = strings.TrimLeft(t[1:], " \t#;")
	if !strings.HasPrefix(t, key) {
		return false
	}
	rest := strings.TrimLeft(t[len(key):], " \t")
	return strings.HasPrefix(rest, "=")
}
