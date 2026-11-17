package astconf

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Sentinel errors returned (wrapped) by LoadTree and Effective.
var (
	// ErrIncludeCycle is returned when a file includes itself, directly or
	// indirectly.
	ErrIncludeCycle = errors.New("astconf: include cycle")
	// ErrInheritanceCycle is returned when sections inherit from each other.
	ErrInheritanceCycle = errors.New("astconf: inheritance cycle")
	// ErrMissingParent is returned when a section names an undefined parent.
	ErrMissingParent = errors.New("astconf: missing parent section")
	// ErrNoSection is returned by Effective for an unknown section name.
	ErrNoSection = errors.New("astconf: no such section")
)

const maxIncludeDepth = 64

// FS abstracts file access so the resolver can run on a directory, a test
// fixture or an in-memory tree. Glob returns paths in the same namespace as
// its pattern, so that results can be passed to ReadFile.
type FS interface {
	ReadFile(path string) ([]byte, error)
	Glob(pattern string) ([]string, error)
}

// OSFS is an FS backed by the operating system. Relative paths are resolved
// against Root (the current directory if Root is empty); absolute paths are
// used as they are.
type OSFS struct{ Root string }

// NewOSFS returns an OSFS rooted at dir.
func NewOSFS(dir string) *OSFS { return &OSFS{Root: dir} }

func (o *OSFS) abs(p string) string {
	if filepath.IsAbs(p) || o.Root == "" {
		return filepath.FromSlash(p)
	}
	return filepath.Join(o.Root, filepath.FromSlash(p))
}

// ReadFile reads the named file.
func (o *OSFS) ReadFile(p string) ([]byte, error) { return os.ReadFile(o.abs(p)) }

// Glob expands pattern. Relative patterns yield relative (slash-separated)
// results.
func (o *OSFS) Glob(pattern string) ([]string, error) {
	matches, err := filepath.Glob(o.abs(pattern))
	if err != nil {
		return nil, err
	}
	if filepath.IsAbs(pattern) || o.Root == "" {
		for i, m := range matches {
			matches[i] = filepath.ToSlash(m)
		}
		return matches, nil
	}
	for i, m := range matches {
		rel, err := filepath.Rel(o.Root, m)
		if err != nil {
			return nil, err
		}
		matches[i] = filepath.ToSlash(rel)
	}
	return matches, nil
}

// Tree is a configuration file with all of its includes spliced in.
type Tree struct {
	// Root is the configuration root against which relative include paths
	// are resolved.
	Root string
	// Main is the logical name of the top-level file.
	Main string
	// Files holds every parsed file by logical name (relative to Root when
	// possible).
	Files map[string]*File
	// Sections is the logical view: one element per section header in
	// include-expanded order. Names may repeat.
	Sections []*LogicalSection
	// Includes records every #include/#tryinclude encountered.
	Includes []IncludeRef
	// Execs records #exec directives; they are never executed.
	Execs []ExecRef
}

// LogicalSection is one section occurrence in the spliced view. Its entries
// may come from several files, because an #include inside the section
// continues it with the included file's leading lines.
type LogicalSection struct {
	Name     string
	Template bool
	Parents  []string
	Append   bool
	// File and Line locate the header ("" and 0 for the preamble).
	File string
	Line int
	// Entries are the key/value lines in order, with provenance.
	Entries []ResolvedEntry
}

// ResolvedEntry is a key/value line with its provenance.
type ResolvedEntry struct {
	Key, Op, Value string
	// File and Line locate the entry itself.
	File string
	Line int
	// Section is the name of the section the entry is defined in, which
	// differs from the resolved section when inherited; SectionFile and
	// SectionLine locate that section's header.
	Section     string
	SectionFile string
	SectionLine int
	// Inherited is true when the entry came from a parent section.
	Inherited bool
	// Entry is the underlying syntax node.
	Entry *Entry
}

// IncludeRef records an include directive.
type IncludeRef struct {
	File     string   // file containing the directive
	Line     int      // its line number
	Pattern  string   // path as written
	Try      bool     // #tryinclude
	Resolved []string // logical names of the files actually included
}

// ExecRef records an #exec directive.
type ExecRef struct {
	File    string
	Line    int
	Command string
}

type loader struct {
	fs    FS
	tree  *Tree
	cur   *LogicalSection
	stack []string
}

// LoadTree parses file and follows #include and #tryinclude directives in
// order, splicing included content in place (so lines after an include,
// before the next header, continue whichever section is current at the end
// of the included text). Relative paths are resolved against root; globs are
// expanded alphabetically. A missing #include target is an error, a missing
// #tryinclude target is skipped, and include cycles are reported with
// ErrIncludeCycle. #exec lines are recorded in Tree.Execs and never run.
func LoadTree(fsys FS, root, file string) (*Tree, error) {
	t := &Tree{Root: root, Files: map[string]*File{}}
	l := &loader{fs: fsys, tree: t}
	name := l.logical(file)
	t.Main = name
	if err := l.load(name, 0); err != nil {
		var fe *fileError
		if errors.As(err, &fe) {
			return nil, fmt.Errorf("astconf: load %s: %w", name, fe.err)
		}
		return nil, err
	}
	return t, nil
}

// logical maps a path as written to a logical name: relative to the root when
// possible, absolute otherwise.
func (l *loader) logical(p string) string {
	p = path.Clean(filepath.ToSlash(p))
	if root := strings.TrimSuffix(path.Clean(filepath.ToSlash(l.tree.Root)), "/"); path.IsAbs(p) && root != "" && root != "." {
		if rel, ok := strings.CutPrefix(p, root+"/"); ok {
			return rel
		}
	}
	return p
}

func (l *loader) physical(name string) string {
	if path.IsAbs(name) || l.tree.Root == "" {
		return name
	}
	return path.Join(filepath.ToSlash(l.tree.Root), name)
}

func (l *loader) load(name string, depth int) error {
	for i, s := range l.stack {
		if s == name {
			return fmt.Errorf("%w: %s", ErrIncludeCycle, strings.Join(append(l.stack[i:], name), " -> "))
		}
	}
	if depth > maxIncludeDepth {
		return fmt.Errorf("astconf: include depth exceeds %d at %s", maxIncludeDepth, name)
	}
	data, err := l.fs.ReadFile(l.physical(name))
	if err != nil {
		return &fileError{name: name, err: err}
	}
	f, err := Parse(name, data)
	if err != nil {
		return err
	}
	if _, ok := l.tree.Files[name]; !ok {
		l.tree.Files[name] = f
	}
	l.stack = append(l.stack, name)
	defer func() { l.stack = l.stack[:len(l.stack)-1] }()

	for _, line := range f.Lines() {
		switch line.Kind {
		case KindSection:
			l.cur = &LogicalSection{Name: line.Name, Template: line.Template,
				Parents: line.Parents, Append: line.Append, File: name, Line: line.Num}
			l.tree.Sections = append(l.tree.Sections, l.cur)
		case KindKeyValue:
			if l.cur == nil {
				l.cur = &LogicalSection{}
				l.tree.Sections = append(l.tree.Sections, l.cur)
			}
			l.cur.Entries = append(l.cur.Entries, ResolvedEntry{
				Key: line.Key, Op: line.Op, Value: line.Value,
				File: name, Line: line.Num,
				Section: l.cur.Name, SectionFile: l.cur.File, SectionLine: l.cur.Line,
				Entry: line,
			})
		case KindDirective:
			if line.Directive == "#exec" {
				l.tree.Execs = append(l.tree.Execs, ExecRef{File: name, Line: line.Num, Command: line.Path})
				continue
			}
			if err := l.include(name, line, depth); err != nil {
				return err
			}
		}
	}
	return nil
}

func hasMeta(p string) bool { return strings.ContainsAny(p, "*?[") }

func (l *loader) include(from string, line *Line, depth int) error {
	try := line.Directive == "#tryinclude"
	ref := IncludeRef{File: from, Line: line.Num, Pattern: line.Path, Try: try}
	fail := func(err error) error {
		return fmt.Errorf("%s:%d: %s %q: %w", from, line.Num, line.Directive, line.Path, err)
	}
	var names []string
	if hasMeta(line.Path) {
		matches, err := l.fs.Glob(l.physical(l.logical(line.Path)))
		if err != nil {
			return fail(err)
		}
		for _, m := range matches {
			names = append(names, l.logical(m))
		}
		sort.Strings(names)
	} else {
		names = []string{l.logical(line.Path)}
	}
	for _, n := range names {
		err := l.load(n, depth+1)
		var fe *fileError
		switch {
		case err == nil:
			ref.Resolved = append(ref.Resolved, n)
		case !errors.As(err, &fe) || fe.name != n:
			return err // already annotated by a deeper include, or a cycle
		case try && errors.Is(fe.err, fs.ErrNotExist):
			// #tryinclude of a missing file is skipped.
		default:
			return fail(fe.err)
		}
	}
	l.tree.Includes = append(l.tree.Includes, ref)
	return nil
}

// fileError reports that the file called name could not be read. include
// turns it into a positioned error (or skips it for #tryinclude).
type fileError struct {
	name string
	err  error
}

func (e *fileError) Error() string { return e.name + ": " + e.err.Error() }
func (e *fileError) Unwrap() error { return e.err }

// Lookup returns every logical section called name, in order.
func (t *Tree) Lookup(name string) []*LogicalSection {
	var out []*LogicalSection
	for _, s := range t.Sections {
		if s.Name == name {
			out = append(out, s)
		}
	}
	return out
}

// Effective returns the effective entries of the named section. Parents are
// applied in the order listed (later parents override earlier ones), then the
// section's own entries override: a key present in an overriding level
// replaces every inherited occurrence of that key, while repeated keys
// within one level accumulate in order. Sections that share a name are
// combined in order (their parent lists are concatenated without
// duplicates). Keys compare case-insensitively.
//
// Cycles yield an error wrapping ErrInheritanceCycle, undefined parents an
// error wrapping ErrMissingParent, and an unknown name ErrNoSection.
func Effective(t *Tree, name string) ([]ResolvedEntry, error) {
	return effective(t, name, nil)
}

func effective(t *Tree, name string, stack []string) ([]ResolvedEntry, error) {
	for i, s := range stack {
		if s == name {
			return nil, fmt.Errorf("%w: %s", ErrInheritanceCycle, strings.Join(append(stack[i:], name), " -> "))
		}
	}
	secs := t.Lookup(name)
	if len(secs) == 0 {
		return nil, fmt.Errorf("%w: %q", ErrNoSection, name)
	}
	stack = append(stack, name)
	var parents []string
	seen := map[string]bool{}
	var own []ResolvedEntry
	for _, s := range secs {
		for _, p := range s.Parents {
			if !seen[p] {
				seen[p] = true
				parents = append(parents, p)
			}
		}
		own = append(own, s.Entries...)
	}
	var result []ResolvedEntry
	for _, p := range parents {
		if len(t.Lookup(p)) == 0 {
			return nil, fmt.Errorf("%w: section %q inherits from %q", ErrMissingParent, name, p)
		}
		inh, err := effective(t, p, stack)
		if err != nil {
			return nil, err
		}
		result = overlay(result, inh)
	}
	for i := range result {
		result[i].Inherited = true
	}
	for i := range own {
		own[i].Inherited = false
	}
	return overlay(result, own), nil
}

// overlay returns base with every key defined in over removed, followed by
// over.
func overlay(base, over []ResolvedEntry) []ResolvedEntry {
	keys := map[string]bool{}
	for _, e := range over {
		keys[strings.ToLower(e.Key)] = true
	}
	out := make([]ResolvedEntry, 0, len(base)+len(over))
	for _, e := range base {
		if !keys[strings.ToLower(e.Key)] {
			out = append(out, e)
		}
	}
	return append(out, over...)
}
