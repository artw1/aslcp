// Package astconf is a lossless parser, writer and resolver for Asterisk
// configuration files (rpt.conf, iax.conf, modules.conf, extensions.conf, ...).
//
// The parser builds a concrete syntax tree in which every line keeps its exact
// raw bytes, including the original line ending. Serializing an unmodified
// File therefore reproduces the input byte for byte. Edits rewrite only the
// lines they touch.
//
// Values are opaque strings: they are never split on ',' or '|', never
// unquoted and never unescaped. In particular an escaped semicolon (`\;`) is
// kept verbatim, backslash included.
//
// Includes and template inheritance are handled by the resolver (see
// LoadTree and Effective).
package astconf

import (
	"bytes"
	"strings"
)

// Kind classifies a Line.
type Kind int

// Line kinds.
const (
	// KindBlank is an empty or whitespace-only line.
	KindBlank Kind = iota
	// KindComment is a ';' comment line, or a '#' line that is neither a
	// directive nor a key/value pair.
	KindComment
	// KindBlockComment is any line that opens, closes or lies inside a
	// ";--" ... "--;" block comment.
	KindBlockComment
	// KindSection is a section header such as "[name](!,parent)".
	KindSection
	// KindDirective is #include, #tryinclude or #exec.
	KindDirective
	// KindKeyValue is "key = value" or "key => value".
	KindKeyValue
	// KindUnknown is any other non-empty line (for example a stray
	// continuation line). It is preserved verbatim.
	KindUnknown
)

func (k Kind) String() string {
	switch k {
	case KindBlank:
		return "blank"
	case KindComment:
		return "comment"
	case KindBlockComment:
		return "blockcomment"
	case KindSection:
		return "section"
	case KindDirective:
		return "directive"
	case KindKeyValue:
		return "keyvalue"
	}
	return "unknown"
}

// Line is one physical line of a configuration file.
//
// Raw always holds the exact bytes of the line including its line ending and
// is what File.Bytes emits. The other fields are a parsed view; which of them
// are meaningful depends on Kind.
type Line struct {
	// Kind classifies the line.
	Kind Kind
	// Num is the 1-based line number. It is renumbered after every edit.
	Num int
	// Raw is the exact line text including the line ending ("\n", "\r\n" or
	// nothing for an unterminated final line).
	Raw string

	// Key, Op, Value and Comment describe a KindKeyValue line. Op is "="
	// or "=>"; the whitespace around it is kept in the unexported layout
	// fields. Value excludes the trailing comment and the whitespace
	// before it. Comment includes the leading ';' (empty if none).
	Key, Op, Value string
	// Comment is the trailing comment (with its leading ';') of a
	// key/value, section or directive line, or the whole text of a
	// comment line.
	Comment string

	// Name, Template, Parents and Append describe a KindSection line.
	// Template is true for "(!...)", Append for "(+)".
	Name     string
	Template bool
	Parents  []string
	Append   bool

	// Directive ("#include", "#tryinclude", "#exec", lower-cased) and Path
	// describe a KindDirective line. For #exec Path is the whole command.
	Directive string
	Path      string

	// layout of a key/value line: lead+Key+sep+Value+gap+Comment+eol == Raw.
	lead, sep, gap, eol string
}

// Entry is a key/value line. It is an alias of Line so that provenance
// (Num) and layout are shared.
type Entry = Line

var directives = [...]string{"#include", "#tryinclude", "#exec"}

const wsChars = " \t\r\v\f"

// isBlockOpen reports whether s starts a ";--" block comment. Asterisk does
// not treat ";---" as one.
func isBlockOpen(s string) bool {
	return len(s) >= 3 && s[:3] == ";--" && (len(s) == 3 || s[3] != '-')
}

// scanComments finds where the code part of text ends and tracks block
// comment nesting. depth is the nesting depth at the start of the line. When
// the line starts inside a block comment codeEnd is 0.
func scanComments(s string, depth int) (codeEnd, newDepth int) {
	codeEnd = len(s)
	set := false
	if depth > 0 {
		codeEnd, set = 0, true
	}
	for i := 0; i < len(s); {
		if depth > 0 {
			switch {
			case strings.HasPrefix(s[i:], "--;"):
				depth--
				i += 3
			case isBlockOpen(s[i:]):
				depth++
				i += 3
			default:
				i++
			}
			continue
		}
		c := s[i]
		if c == '\\' && i+1 < len(s) && s[i+1] == ';' {
			i += 2
			continue
		}
		if c != ';' {
			i++
			continue
		}
		if !set {
			codeEnd, set = i, true
		}
		if isBlockOpen(s[i:]) {
			depth = 1
			i += 3
			continue
		}
		break // plain comment: rest of line is ignored
	}
	return codeEnd, depth
}

// parseLine classifies one line (without its line ending).
func parseLine(text, eol string, depth int) (*Line, int) {
	l := &Line{Raw: text + eol, eol: eol}
	codeEnd, nd := scanComments(text, depth)
	if depth > 0 {
		l.Kind, l.Comment = KindBlockComment, text
		return l, nd
	}
	code, comment := text[:codeEnd], text[codeEnd:]
	trimmed := strings.TrimRight(code, wsChars)
	gap := code[len(trimmed):]
	body := strings.TrimLeft(trimmed, wsChars)
	lead := trimmed[:len(trimmed)-len(body)]
	l.Comment = comment

	if body == "" {
		switch {
		case comment == "" && lead == "" && gap == "":
			l.Kind = KindBlank
		case comment == "":
			l.Kind = KindBlank
		case isBlockOpen(comment):
			l.Kind = KindBlockComment
		default:
			l.Kind = KindComment
		}
		return l, nd
	}

	if body[0] == '#' {
		if parseDirective(l, body) {
			return l, nd
		}
		if !strings.Contains(body, "=") {
			l.Kind, l.Comment = KindComment, text
			return l, nd
		}
	}
	if body[0] == '[' {
		if parseSection(l, body) {
			return l, nd
		}
		l.Kind = KindUnknown
		return l, nd
	}
	if parseKeyValue(l, body, lead, gap) {
		return l, nd
	}
	l.Kind = KindUnknown
	return l, nd
}

func parseDirective(l *Line, body string) bool {
	for _, d := range directives {
		if len(body) < len(d) || !strings.EqualFold(body[:len(d)], d) {
			continue
		}
		rest := body[len(d):]
		if rest != "" && !strings.ContainsRune(wsChars, rune(rest[0])) {
			continue
		}
		rest = strings.TrimSpace(rest)
		l.Kind, l.Directive = KindDirective, d
		l.Path = rest
		if d != "#exec" && rest != "" {
			l.Path = parseIncludePath(rest)
		}
		return true
	}
	return false
}

func parseIncludePath(rest string) string {
	if q := rest[0]; q == '"' || q == '<' {
		end := byte('"')
		if q == '<' {
			end = '>'
		}
		if i := strings.IndexByte(rest[1:], end); i >= 0 {
			return rest[1 : 1+i]
		}
		return rest[1:]
	}
	if i := strings.IndexAny(rest, wsChars); i >= 0 {
		return rest[:i]
	}
	return rest
}

func parseSection(l *Line, body string) bool {
	closeIdx := strings.IndexByte(body, ']')
	if closeIdx < 0 {
		return false
	}
	l.Kind = KindSection
	l.Name = body[1:closeIdx]
	rest := strings.TrimSpace(body[closeIdx+1:])
	if !strings.HasPrefix(rest, "(") {
		return true
	}
	end := strings.IndexByte(rest, ')')
	if end < 0 {
		end = len(rest)
	}
	for i, opt := range strings.Split(rest[1:end], ",") {
		opt = strings.TrimSpace(opt)
		switch {
		case opt == "":
		case opt == "!" && i == 0:
			l.Template = true
		case opt == "+" && i == 0:
			l.Append = true
		default:
			l.Parents = append(l.Parents, opt)
		}
	}
	return true
}

func parseKeyValue(l *Line, body, lead, gap string) bool {
	i := strings.IndexByte(body, '=')
	if i < 0 {
		return false
	}
	key := strings.TrimRight(body[:i], wsChars)
	if key == "" {
		return false
	}
	op := "="
	if i+1 < len(body) && body[i+1] == '>' {
		op = "=>"
	}
	value := strings.TrimLeft(body[i+len(op):], wsChars)
	l.Kind = KindKeyValue
	l.Key, l.Op, l.Value = key, op, value
	l.lead, l.gap = lead, gap
	l.sep = body[len(key) : len(body)-len(value)]
	return true
}

// File is a parsed configuration file.
type File struct {
	// Name is the name given to Parse (informational only).
	Name     string
	sections []*Section // sections[0] is always the preamble
	eol      string     // dominant line ending, used for new lines
}

// Section is a run of lines starting at a header (or the preamble, whose
// Header is nil and Name is ""). The same name may occur several times in
// one file.
type Section struct {
	// Name is the section name ("" for the preamble).
	Name string
	// Template is true for "[name](!...)".
	Template bool
	// Parents lists inherited sections in the order written.
	Parents []string
	// Append is true for "[name](+)".
	Append bool
	// Header is the header line, nil for the preamble.
	Header *Line

	file  *File
	lines []*Line // includes Header as the first element when non-nil
}

// Parse parses data. It never fails on odd content: anything that is not
// understood is kept as KindUnknown. The error return is reserved for
// I/O-level problems and is currently always nil.
func Parse(name string, data []byte) (*File, error) {
	f := &File{Name: name, eol: "\n"}
	cur := &Section{file: f}
	f.sections = []*Section{cur}
	depth, crlf, lf := 0, 0, 0
	for pos := 0; pos < len(data); {
		var text, eol string
		if i := bytes.IndexByte(data[pos:], '\n'); i < 0 {
			text = string(data[pos:])
			pos = len(data)
		} else {
			text = string(data[pos : pos+i])
			eol = "\n"
			pos += i + 1
			if strings.HasSuffix(text, "\r") {
				text, eol = text[:len(text)-1], "\r\n"
				crlf++
			} else {
				lf++
			}
		}
		var l *Line
		l, depth = parseLine(text, eol, depth)
		if l.Kind == KindSection {
			cur = &Section{Name: l.Name, Template: l.Template, Parents: l.Parents, Append: l.Append, Header: l, file: f}
			f.sections = append(f.sections, cur)
		}
		cur.lines = append(cur.lines, l)
	}
	if crlf > lf {
		f.eol = "\r\n"
	}
	f.renumber()
	return f, nil
}

func (f *File) renumber() {
	n := 0
	for _, s := range f.sections {
		for _, l := range s.lines {
			n++
			l.Num = n
		}
	}
}

// Bytes serializes the file. For an unmodified file the result is
// byte-identical to the parsed input.
func (f *File) Bytes() []byte {
	var b bytes.Buffer
	for _, s := range f.sections {
		for _, l := range s.lines {
			b.WriteString(l.Raw)
		}
	}
	return b.Bytes()
}

// Lines returns every line of the file in order.
func (f *File) Lines() []*Line {
	var out []*Line
	for _, s := range f.sections {
		out = append(out, s.lines...)
	}
	return out
}

// Preamble returns the pseudo-section holding lines before the first header.
func (f *File) Preamble() *Section { return f.sections[0] }

// Sections returns all sections in file order. The preamble is included
// (first, with Name "") only if it contains lines.
func (f *File) Sections() []*Section {
	if len(f.sections[0].lines) == 0 {
		return append([]*Section(nil), f.sections[1:]...)
	}
	return append([]*Section(nil), f.sections...)
}

// Section returns every section called name, in file order. Asterisk allows
// a name to be reopened, so there may be several. Name "" returns the
// preamble if it is non-empty.
func (f *File) Section(name string) []*Section {
	var out []*Section
	for _, s := range f.Sections() {
		if s.Name == name {
			out = append(out, s)
		}
	}
	return out
}

// Lines returns the section's lines, header first (if any).
func (s *Section) Lines() []*Line { return append([]*Line(nil), s.lines...) }

// Entries returns the key/value lines of the section in order.
func (s *Section) Entries() []*Entry {
	var out []*Entry
	for _, l := range s.lines {
		if l.Kind == KindKeyValue {
			out = append(out, l)
		}
	}
	return out
}

// Directives returns the #include/#tryinclude/#exec lines of the section.
func (s *Section) Directives() []*Line {
	var out []*Line
	for _, l := range s.lines {
		if l.Kind == KindDirective {
			out = append(out, l)
		}
	}
	return out
}

// Get returns all entries with the given key, in order. Keys compare
// case-insensitively, as in Asterisk.
func (s *Section) Get(key string) []*Entry {
	var out []*Entry
	for _, e := range s.Entries() {
		if strings.EqualFold(e.Key, key) {
			out = append(out, e)
		}
	}
	return out
}
