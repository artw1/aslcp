package astconf

import (
	"strings"
)

// opStyle is how an operator is spaced: " = ", "=", " => " ...
type opStyle struct {
	op            string
	before, after bool
}

func (o opStyle) sep() string {
	s := o.op
	if o.before {
		s = " " + s
	}
	if o.after {
		s += " "
	}
	return s
}

func styleOf(e *Entry) opStyle {
	st := opStyle{op: e.Op}
	if e.sep != "" {
		st.before = strings.ContainsRune(wsChars, rune(e.sep[0]))
		st.after = strings.ContainsRune(wsChars, rune(e.sep[len(e.sep)-1]))
	}
	if e.Value == "" {
		st.after = true
	}
	return st
}

// dominant returns the most frequent style (first seen wins ties).
func dominant(entries []*Entry) (opStyle, bool) {
	counts := map[opStyle]int{}
	var order []opStyle
	for _, e := range entries {
		st := styleOf(e)
		if counts[st] == 0 {
			order = append(order, st)
		}
		counts[st]++
	}
	var best opStyle
	n := 0
	for _, st := range order {
		if counts[st] > n {
			best, n = st, counts[st]
		}
	}
	return best, n > 0
}

func (s *Section) style() opStyle {
	if st, ok := dominant(s.Entries()); ok {
		return st
	}
	var all []*Entry
	for _, sec := range s.file.sections {
		all = append(all, sec.Entries()...)
	}
	if st, ok := dominant(all); ok {
		return st
	}
	return opStyle{op: "=", before: true, after: true}
}

func stripEOL(s string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(s)
}

func (e *Entry) render() string {
	return e.lead + e.Key + e.sep + e.Value + e.gap + e.Comment + e.eol
}

// SetValue replaces the entry's value, preserving the key, the operator and
// its spacing, the trailing comment and, when the new value fits, the column
// at which the trailing comment starts (when the padding is spaces only).
// CR and LF characters in v are dropped. v is taken literally: an unescaped
// ';' would turn the rest into a comment when the file is parsed again, so
// callers should write `\;` for a literal semicolon. It is a no-op on lines
// that are not key/value lines.
func (e *Entry) SetValue(v string) {
	if e.Kind != KindKeyValue {
		return
	}
	v = stripEOL(v)
	if e.Value == "" && v != "" && e.sep != "" && !strings.ContainsRune(wsChars, rune(e.sep[len(e.sep)-1])) {
		e.sep += " "
	}
	if e.Comment != "" && e.gap != "" && strings.Trim(e.gap, " ") == "" {
		total := len(e.Value) + len(e.gap)
		if len(v)+1 <= total {
			e.gap = strings.Repeat(" ", total-len(v))
		} else {
			e.gap = " "
		}
	} else if e.Comment != "" && e.gap == "" && v != "" {
		e.gap = " "
	}
	e.Value = v
	e.Raw = e.render()
}

// insertIndex is where new entries go: right after the last content line
// (key/value, directive or unknown), i.e. before trailing blank and comment
// lines.
func (s *Section) insertIndex() int {
	last := -1
	for i, l := range s.lines {
		switch l.Kind {
		case KindKeyValue, KindDirective, KindUnknown:
			last = i
		}
	}
	if last >= 0 {
		return last + 1
	}
	if s.Header != nil {
		return 1
	}
	idx := 0
	for i, l := range s.lines {
		if l.Kind != KindBlank {
			idx = i + 1
		}
	}
	return idx
}

func (s *Section) insert(idx int, l *Line) {
	if idx > 0 {
		if prev := s.lines[idx-1]; prev.eol == "" {
			prev.eol = s.file.eol
			prev.Raw += prev.eol
		}
	}
	s.lines = append(s.lines, nil)
	copy(s.lines[idx+1:], s.lines[idx:])
	s.lines[idx] = l
	s.file.renumber()
}

func (s *Section) newEntry(key, op, value string, st opStyle) *Entry {
	key = strings.TrimSpace(stripEOL(key))
	value = stripEOL(value)
	if op = strings.TrimSpace(op); op != "" {
		st.op = op
	}
	l, _ := parseLine(key+st.sep()+value, s.file.eol, 0)
	if l.Kind != KindKeyValue || l.Key != key || l.Op != st.op {
		return nil
	}
	return l
}

// Set updates the last occurrence of key, or appends a new entry after the
// section's last entry (before trailing blank/comment lines) if the key is
// absent. New entries use the section's dominant operator style. It returns
// the entry, or nil if key/value cannot be represented as a key/value line
// (for instance an empty key).
func (s *Section) Set(key, value string) *Entry {
	if es := s.Get(key); len(es) > 0 {
		e := es[len(es)-1]
		e.SetValue(value)
		return e
	}
	return s.Add(key, "", value)
}

// Add always appends a new entry, which allows repeated keys. op is "=" or
// "=>" (surrounding spaces are ignored); an empty op uses the section's
// dominant operator. Spacing follows the section's dominant style. It
// returns nil if key/value cannot be represented as a key/value line.
func (s *Section) Add(key, op, value string) *Entry {
	e := s.newEntry(key, op, value, s.style())
	if e == nil {
		return nil
	}
	s.insert(s.insertIndex(), e)
	return e
}

// Delete removes the entry's line from the section and reports whether it
// was found.
func (s *Section) Delete(e *Entry) bool {
	for i, l := range s.lines {
		if l == e {
			s.lines = append(s.lines[:i], s.lines[i+1:]...)
			s.file.renumber()
			return true
		}
	}
	return false
}

// AddSection appends a new section at the end of the file, preceded by a
// blank line (unless the file is empty or already ends in one). parents may
// be empty.
func (f *File) AddSection(name string, template bool, parents []string) *Section {
	hdr := "[" + stripEOL(name) + "]"
	var opts []string
	if template {
		opts = append(opts, "!")
	}
	opts = append(opts, parents...)
	if len(opts) > 0 {
		hdr += "(" + stripEOL(strings.Join(opts, ",")) + ")"
	}
	last := f.sections[len(f.sections)-1]
	if len(f.Lines()) > 0 {
		if n := len(last.lines); n > 0 {
			if prev := last.lines[n-1]; prev.Kind != KindBlank {
				last.insert(n, &Line{Kind: KindBlank, Raw: f.eol, eol: f.eol})
			} else if prev.eol == "" {
				prev.eol = f.eol
				prev.Raw += f.eol
			}
		}
	}
	l, _ := parseLine(hdr, f.eol, 0)
	s := &Section{Name: l.Name, Template: l.Template, Parents: l.Parents, Header: l, file: f, lines: []*Line{l}}
	f.sections = append(f.sections, s)
	f.renumber()
	return s
}

// SetKey renames the entry's key in place, keeping the operator, the value,
// the trailing comment and the column of the value (when the padding after
// the key is whitespace). It is a no-op on lines that are not key/value lines.
func (e *Entry) SetKey(k string) {
	if e.Kind != KindKeyValue {
		return
	}
	k = strings.TrimSpace(stripEOL(k))
	if k == "" || k == e.Key {
		return
	}
	if d := len(e.Key) - len(k); d != 0 && e.sep != "" && strings.ContainsRune(wsChars, rune(e.sep[0])) {
		if d > 0 {
			e.sep = strings.Repeat(" ", d) + e.sep
		} else if n := len(e.sep) - len(strings.TrimLeft(e.sep, wsChars)); n > -d && strings.Trim(e.sep[:n], " ") == "" {
			e.sep = e.sep[-d:]
		}
	}
	e.Key = k
	e.Raw = e.render()
}

// RemoveSection deletes the section with all its lines. With dropBlank, when
// it was the last section, the blank line before it goes with it (the one
// AddSection put there).
func (f *File) RemoveSection(s *Section, dropBlank bool) bool {
	for i, x := range f.sections {
		if x != s || i == 0 {
			continue
		}
		last := i == len(f.sections)-1
		f.sections = append(f.sections[:i], f.sections[i+1:]...)
		if last && dropBlank && i > 0 {
			p := f.sections[i-1]
			if n := len(p.lines); n > 0 && p.lines[n-1].Kind == KindBlank {
				p.lines = p.lines[:n-1]
			}
		}
		f.renumber()
		return true
	}
	return false
}

// Rename changes the section's name in its header line, keeping the
// options, the trailing comment and the line ending untouched. It is a no-op
// for the preamble, for an empty or unchanged name and for a name that holds
// a line break or ']'.
func (s *Section) Rename(name string) {
	name = strings.TrimSpace(name)
	if s.Header == nil || name == "" || name == s.Name || strings.ContainsAny(name, "\r\n]") {
		return
	}
	old := "[" + s.Name + "]"
	i := strings.Index(s.Header.Raw, old)
	if i < 0 {
		return
	}
	s.Header.Raw = s.Header.Raw[:i] + "[" + name + "]" + s.Header.Raw[i+len(old):]
	s.Header.Name = name
	s.Name = name
}
