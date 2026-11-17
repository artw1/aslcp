package astconf

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func firstDiff(a, b []byte) (line int, la, lb string) {
	as, bs := strings.SplitAfter(string(a), "\n"), strings.SplitAfter(string(b), "\n")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var x, y string
		if i < len(as) {
			x = as[i]
		}
		if i < len(bs) {
			y = bs[i]
		}
		if x != y {
			return i + 1, x, y
		}
	}
	return 0, "", ""
}

func checkRoundTrip(t *testing.T, name string, data []byte) {
	t.Helper()
	f, err := Parse(name, data)
	if err != nil {
		t.Errorf("%s: parse: %v", name, err)
		return
	}
	if got := f.Bytes(); !bytes.Equal(got, data) {
		n, want, have := firstDiff(data, got)
		t.Errorf("%s: round-trip mismatch at line %d:\n want %q\n  got %q", name, n, want, have)
	}
}

func walk(t *testing.T, dir string, skipREADME bool) {
	t.Helper()
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || (skipREADME && d.Name() == "README.md") {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > 2<<20 {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) && !skipREADME {
			return nil // binary
		}
		checkRoundTrip(t, p, data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestGoldenRoundTrip(t *testing.T) {
	walk(t, "testdata/corpus", true)
	walk(t, "testdata/fixtures", true)
	if dir := os.Getenv("ASTCONF_EXTRA_CORPUS"); dir != "" {
		walk(t, dir, false)
	}
}

func load(t *testing.T, p string) *File {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", p))
	if err != nil {
		t.Fatal(err)
	}
	f, err := Parse(p, data)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func one(t *testing.T, f *File, name string) *Section {
	t.Helper()
	s := f.Section(name)
	if len(s) != 1 {
		t.Fatalf("section %q: got %d, want 1", name, len(s))
	}
	return s[0]
}

func TestCorpusRpt(t *testing.T) {
	f := load(t, "corpus/asl3-dist/rpt.conf")
	nm := one(t, f, "node-main")
	if !nm.Template || len(nm.Parents) != 0 {
		t.Errorf("node-main: %+v", nm)
	}
	n := one(t, f, "1999")
	if n.Template || len(n.Parents) != 1 || n.Parents[0] != "node-main" {
		t.Errorf("1999: %+v", n)
	}
	fn := one(t, f, "functions")
	if len(fn.Parents) != 1 || fn.Parents[0] != "functions-main" {
		t.Errorf("functions: %+v", fn.Parents)
	}
	var paths []string
	for _, s := range f.Sections() {
		for _, d := range s.Directives() {
			paths = append(paths, d.Directive+" "+d.Path)
		}
	}
	want := "#tryinclude custom/rpt.conf,#tryinclude custom/rpt/*.conf"
	if strings.Join(paths, ",") != want {
		t.Errorf("includes = %v", paths)
	}
	if len(one(t, f, "functions-main").Entries()) == 0 {
		t.Error("functions-main has no entries")
	}
}

func TestCorpusMultiParent(t *testing.T) {
	f := load(t, "corpus/asl3-dist/custom/rpt/SHARI.conf")
	s := one(t, f, "events-shari-pixx")
	if !s.Template || len(s.Parents) != 1 || s.Parents[0] != "events-shari" {
		t.Errorf("got %+v", s)
	}
	g := load(t, "corpus/asl3-dist/custom/simpleusb/HotSpotRadios.conf")
	s = one(t, g, "hotspotradio-pihat")
	if !s.Template || len(s.Parents) != 1 {
		t.Errorf("got %+v", s)
	}
}

func TestCorpusModules(t *testing.T) {
	f := load(t, "corpus/asl3-dist/modules.conf")
	var req, load, noload int
	for _, s := range f.Sections() {
		req += len(s.Get("require"))
		load += len(s.Get("load"))
		noload += len(s.Get("noload"))
	}
	if req < 1 || load < 5 || noload < 1 {
		t.Errorf("require=%d load=%d noload=%d", req, load, noload)
	}
	e := f.Section("modules")[0].Get("require")[0]
	if e.Value != "app_rpt.so" || e.Comment == "" {
		t.Errorf("require entry: %+v", e)
	}
}

func TestCorpusIax(t *testing.T) {
	f := load(t, "corpus/asl3-dist/iax.conf")
	g := one(t, f, "general")
	if n := len(g.Get("allow")); n < 3 {
		t.Errorf("allow count %d", n)
	}
	var vals []string
	for _, e := range g.Get("allow") {
		vals = append(vals, e.Value)
	}
	if vals[0] != "ulaw" {
		t.Errorf("allow order %v", vals)
	}
	if !one(t, f, "iaxrpt").Template {
		t.Error("iaxrpt not template")
	}
}

func TestCorpusExtensions(t *testing.T) {
	f := load(t, "corpus/asl3-dist/extensions.conf")
	found := false
	for _, s := range f.Sections() {
		for _, e := range s.Get("exten") {
			found = true
			if e.Op != "=>" {
				t.Fatalf("exten op %q", e.Op)
			}
		}
	}
	if !found {
		t.Error("no exten entries")
	}
}

func TestFixtureCRLF(t *testing.T) {
	f := load(t, "fixtures/crlf.conf")
	if f.eol != "\r\n" {
		t.Error("eol not CRLF")
	}
	s := one(t, f, "general")
	s.Set("new", "1")
	out := string(f.Bytes())
	if strings.Count(out, "\r\n") != strings.Count(out, "\n") {
		t.Errorf("mixed endings: %q", out)
	}
	if e := s.Get("key")[0]; e.Value != "value" || e.Comment != "; c" {
		t.Errorf("%+v", e)
	}
}

func TestFixtureNoTrailingNewline(t *testing.T) {
	f := load(t, "fixtures/no-trailing-newline.conf")
	s := one(t, f, "general")
	if e := s.Get("last")[0]; e.Value != "noeol" || e.eol != "" {
		t.Errorf("%+v", e)
	}
	s.Set("added", "x")
	want := "[general]\nkey = value\nlast = noeol\nadded = x\n"
	if got := string(f.Bytes()); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestFixtureBlockComment(t *testing.T) {
	f := load(t, "fixtures/blockcomment.conf")
	a := one(t, f, "a")
	var keys []string
	for _, e := range a.Entries() {
		keys = append(keys, e.Key+"="+e.Value)
	}
	if got := strings.Join(keys, ","); got != "k1=v1,k2=v2,k3=v3,k4=v4" {
		t.Errorf("entries %s", got)
	}
	if len(f.Preamble().Entries()) != 0 {
		t.Error("key leaked out of block comment in preamble")
	}
	if f.Section("lines") != nil {
		t.Error("section inside block comment parsed")
	}
	if k := a.Get("k4")[0]; k.Comment != ";-- one-liner --;" {
		t.Errorf("k4 comment %q", k.Comment)
	}
	var bc int
	for _, l := range f.Lines() {
		if l.Kind == KindBlockComment {
			bc++
		}
	}
	if bc != 9 {
		t.Errorf("block comment lines = %d", bc)
	}
}

func TestFixtureEscapedSemicolon(t *testing.T) {
	f := load(t, "fixtures/escaped-semicolon.conf")
	s := one(t, f, "x")
	m := s.Get("msg")[0]
	if m.Value != `hello\; world` || m.Comment != "; real comment" {
		t.Errorf("%+v", m)
	}
	e := s.Get("expr")[0]
	if e.Value != `${EXTEN},1|2 "quoted" ${FOO(a,b)}` || e.Comment != ";" {
		t.Errorf("%q %q", e.Value, e.Comment)
	}
	if v := s.Get("plain")[0].Value; v != "a,b|c" {
		t.Errorf("plain %q", v)
	}
}

func TestFixtureAlignment(t *testing.T) {
	f := load(t, "fixtures/alignment.conf")
	s := one(t, f, "x")
	o := s.Get("other")[0]
	if o.Value != "5" || o.Op != "=" || o.gap != "\t\t" || o.sep != "\t=\t" {
		t.Errorf("%+v", o)
	}
	o.SetValue("77")
	if !strings.Contains(string(f.Bytes()), "other\t=\t77\t\t; tabbed\n") {
		t.Errorf("%q", f.Bytes())
	}
}

func TestFixtureKeys(t *testing.T) {
	f := load(t, "fixtures/keys.conf")
	s := one(t, f, "functions")
	want := [][3]string{
		{"cop,62,GPIO4:1", "=", "c|t|RPT_RXKEYED"},
		{"70", "=", "ilink,5"},
		{"*7", "=", "ilink,6"},
		{"#1", "=", "hash key"},
		{"exten", "=>", "s,1,Answer"},
	}
	es := s.Entries()
	if len(es) != len(want) {
		t.Fatalf("got %d entries", len(es))
	}
	for i, w := range want {
		if es[i].Key != w[0] || es[i].Op != w[1] || es[i].Value != w[2] {
			t.Errorf("entry %d: %q %q %q", i, es[i].Key, es[i].Op, es[i].Value)
		}
	}
}

func TestFixtureModulesMixedOps(t *testing.T) {
	f := load(t, "fixtures/modules.conf")
	s := one(t, f, "modules")
	nl := s.Get("noload")[0]
	if nl.Op != "=" || nl.sep != "  = " {
		t.Errorf("%+v", nl)
	}
	if l := s.Get("load")[0]; l.Op != "=>" || l.Value != "x.so" {
		t.Errorf("%+v", l)
	}
	if a := s.Get("autoload")[0]; a.sep != "=" {
		t.Errorf("autoload sep %q", a.sep)
	}
}

func TestFixtureUnknown(t *testing.T) {
	f := load(t, "fixtures/unknown.conf")
	var kinds []Kind
	for _, l := range f.Lines() {
		kinds = append(kinds, l.Kind)
	}
	want := []Kind{KindSection, KindKeyValue, KindUnknown, KindBlank, KindUnknown, KindUnknown}
	if len(kinds) != len(want) {
		t.Fatalf("kinds %v", kinds)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Errorf("line %d: %v want %v", i+1, kinds[i], want[i])
		}
	}
	// Unknown lines count as content when appending.
	s := one(t, f, "nodes")
	s.Set("new", "1")
	if lines := strings.Split(string(f.Bytes()), "\n"); lines[6] != "new = 1" {
		t.Errorf("lines %q", lines)
	}
}

func TestFixtureInclude(t *testing.T) {
	f := load(t, "fixtures/include.conf")
	ds := one(t, f, "general").Directives()
	type d struct{ dir, path, comment string }
	want := []d{
		{"#tryinclude", "custom/x.conf", "; comment"},
		{"#include", "custom/y.conf", ""},
		{"#include", "z.conf", ""},
		{"#exec", "/bin/true arg", "; run"},
	}
	if len(ds) != len(want) {
		t.Fatalf("got %d directives", len(ds))
	}
	for i, w := range want {
		if ds[i].Directive != w.dir || ds[i].Path != w.path || ds[i].Comment != w.comment {
			t.Errorf("%d: %+v", i, ds[i])
		}
	}
}

func TestFixtureSections(t *testing.T) {
	f := load(t, "fixtures/sections.conf")
	pre := f.Preamble()
	if pre.Name != "" || len(pre.Get("orphan")) != 1 {
		t.Errorf("preamble %+v", pre)
	}
	if n := f.Sections()[0]; n != pre {
		t.Error("preamble not first")
	}
	nm := one(t, f, "node-main")
	if !nm.Template || nm.Header.Comment != "; template comment" || nm.Header.Num != 4 {
		t.Errorf("%+v", nm.Header)
	}
	ev := one(t, f, "events")
	if !ev.Template || strings.Join(ev.Parents, ",") != "x,y" {
		t.Errorf("%+v", ev)
	}
	ch := one(t, f, "child")
	if ch.Template || strings.Join(ch.Parents, ",") != "node-main,events" || ch.Header.Comment != "; trailing" {
		t.Errorf("%+v", ch)
	}
	dups := f.Section("dup")
	if len(dups) != 2 || dups[0].Get("k")[0].Value != "1" || dups[1].Get("k")[0].Value != "2" {
		t.Errorf("dups %v", dups)
	}
	if g := one(t, f, "general"); !g.Append {
		t.Error("append flag")
	}
}

// diffLines returns indexes (0-based) of lines that differ, assuming equal count.
func lineDiff(a, b string) (changed, added, removed int) {
	as, bs := strings.SplitAfter(a, "\n"), strings.SplitAfter(b, "\n")
	// longest common subsequence length by simple DP
	n, m := len(as), len(bs)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if as[i] == bs[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else {
				dp[i][j] = max(dp[i+1][j], dp[i][j+1])
			}
		}
	}
	return 0, m - dp[0][0], n - dp[0][0]
}

func TestSetValueKeepsComment(t *testing.T) {
	f, _ := Parse("t", []byte("[a]\nhangtime = 1000          ; comment\nx = 1\n"))
	e := f.Section("a")[0].Get("hangtime")[0]
	e.SetValue("500")
	want := "[a]\nhangtime = 500           ; comment\nx = 1\n"
	if got := string(f.Bytes()); got != want {
		t.Errorf("got %q want %q", got, want)
	}
	if strings.Index(want, "; comment") != strings.Index("[a]\nhangtime = 1000          ; comment\n", "; comment") {
		t.Error("test setup: comment column differs")
	}
	// Longer than the gap: comment shifts but a space remains.
	e.SetValue("123456789012345")
	if !strings.Contains(string(f.Bytes()), "hangtime = 123456789012345 ; comment\n") {
		t.Errorf("got %q", f.Bytes())
	}
	// Empty value and no comment.
	f2, _ := Parse("t", []byte("[a]\nk =\n"))
	f2.Section("a")[0].Get("k")[0].SetValue("v")
	if got := string(f2.Bytes()); got != "[a]\nk = v\n" {
		t.Errorf("got %q", got)
	}
	// CR/LF in values are dropped.
	f2.Section("a")[0].Get("k")[0].SetValue("a\nb")
	if got := string(f2.Bytes()); got != "[a]\nk = ab\n" {
		t.Errorf("got %q", got)
	}
}

func TestSetAppendPlacement(t *testing.T) {
	src := "[a]\nx = 1\ny = 2\n\n; trailing comment\n[b]\nz => 3\n"
	f, _ := Parse("t", []byte(src))
	f.Section("a")[0].Set("w", "9")
	want := "[a]\nx = 1\ny = 2\nw = 9\n\n; trailing comment\n[b]\nz => 3\n"
	if got := string(f.Bytes()); got != want {
		t.Errorf("got %q want %q", got, want)
	}
	// Dominant operator style, per section.
	f.Section("b")[0].Set("q", "4")
	if got := string(f.Bytes()); !strings.HasSuffix(got, "z => 3\nq => 4\n") {
		t.Errorf("got %q", got)
	}
	// Update of existing key hits the last occurrence.
	f2, _ := Parse("t", []byte("[a]\nallow=ulaw\nallow=gsm\n"))
	f2.Section("a")[0].Set("allow", "g722")
	if got := string(f2.Bytes()); got != "[a]\nallow=ulaw\nallow=g722\n" {
		t.Errorf("got %q", got)
	}
	f2.Section("a")[0].Set("new", "1")
	if got := string(f2.Bytes()); !strings.HasSuffix(got, "new=1\n") {
		t.Errorf("tight style not kept: %q", got)
	}
	// Empty section: after header.
	f3, _ := Parse("t", []byte("[a]\n\n[b]\n"))
	f3.Section("a")[0].Set("k", "v")
	if got := string(f3.Bytes()); got != "[a]\nk = v\n\n[b]\n" {
		t.Errorf("got %q", got)
	}
	// Invalid key.
	if f3.Section("a")[0].Set("", "v") != nil {
		t.Error("empty key accepted")
	}
}

func TestAddDeleteAddSection(t *testing.T) {
	src := "[a]\nallow = ulaw\n; note\nallow = gsm\n\n[b]\nk = v\n"
	f, _ := Parse("t", []byte(src))
	a := f.Section("a")[0]
	e := a.Add("allow", "=", "g722")
	if e == nil || e.Num != 5 {
		t.Fatalf("added %+v", e)
	}
	if got := len(a.Get("allow")); got != 3 {
		t.Errorf("allow count %d", got)
	}
	if !a.Delete(a.Get("allow")[0]) {
		t.Fatal("delete failed")
	}
	if a.Delete(e) != true || a.Delete(e) != false {
		t.Error("delete semantics")
	}
	f.AddSection("c", false, []string{"p1", "p2"})
	f.AddSection("t", true, nil)
	f.AddSection("m", true, []string{"x"})
	want := "[a]\n; note\nallow = gsm\n\n[b]\nk = v\n\n[c](p1,p2)\n\n[t](!)\n\n[m](!,x)\n"
	if got := string(f.Bytes()); got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
	c := f.Section("c")[0]
	if strings.Join(c.Parents, ",") != "p1,p2" || !f.Section("t")[0].Template {
		t.Error("section metadata")
	}
	c.Set("k", "v")
	if !strings.Contains(string(f.Bytes()), "[c](p1,p2)\nk = v\n") {
		t.Errorf("%q", f.Bytes())
	}
	// line numbers are consistent after edits
	for i, l := range f.Lines() {
		if l.Num != i+1 {
			t.Fatalf("line %d has Num %d", i+1, l.Num)
		}
	}
	// AddSection on empty file and on a file without trailing newline.
	g, _ := Parse("t", nil)
	g.AddSection("x", false, nil)
	if string(g.Bytes()) != "[x]\n" {
		t.Errorf("%q", g.Bytes())
	}
	h, _ := Parse("t", []byte("[a]\r\nk=1"))
	h.AddSection("b", false, nil)
	if got := string(h.Bytes()); got != "[a]\r\nk=1\r\n\r\n[b]\r\n" {
		t.Errorf("%q", got)
	}
}

func TestEditsOnlyTouchIntendedLines(t *testing.T) {
	data, err := os.ReadFile("testdata/corpus/asl3-dist/rpt.conf")
	if err != nil {
		t.Fatal(err)
	}
	f, _ := Parse("rpt.conf", data)
	before := string(f.Bytes())
	n := f.Section("1999")[0]
	n.Set("hangtime", "1234")
	n.Add("custom", "=", "x")
	f.AddSection("zzz", false, nil)
	_, added, removed := lineDiff(before, string(f.Bytes()))
	existing := len(n.Get("hangtime"))
	if existing == 0 {
		t.Fatal("hangtime missing")
	}
	// hangtime may be an update (1 changed = 1 removed + 1 added) or an append.
	if removed > 1 || added > 6 {
		t.Errorf("too many lines changed: +%d -%d", added, removed)
	}
	// Reparse is consistent.
	g, _ := Parse("x", f.Bytes())
	if got := g.Section("1999")[0].Get("custom"); len(got) != 1 || got[0].Value != "x" {
		t.Errorf("reparse: %v", got)
	}
	if !bytes.Equal(g.Bytes(), f.Bytes()) {
		t.Error("reparse not identical")
	}
	// Untouched sections keep raw bytes: compare line by line before the edit point.
	bl, al := strings.SplitAfter(before, "\n"), strings.SplitAfter(string(f.Bytes()), "\n")
	hdr := n.Header.Num
	for i := 0; i < hdr-1; i++ {
		if bl[i] != al[i] {
			t.Fatalf("line %d changed: %q -> %q", i+1, bl[i], al[i])
		}
	}
}

func FuzzRoundTrip(f *testing.F) {
	entries, _ := filepath.Glob("testdata/fixtures/*.conf")
	for _, p := range entries {
		if b, err := os.ReadFile(p); err == nil {
			f.Add(b)
		}
	}
	f.Add([]byte(""))
	f.Add([]byte(";--"))
	f.Add([]byte("[a](!,b\r\nk=>v ;--\r"))
	f.Fuzz(func(t *testing.T, data []byte) {
		fl, err := Parse("fuzz", data)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(fl.Bytes(), data) {
			t.Fatalf("round trip mismatch")
		}
		// Exercise edits: must not panic.
		for _, s := range fl.Sections() {
			s.Set("fuzzkey", "v")
			if es := s.Entries(); len(es) > 0 {
				es[0].SetValue("x")
				s.Delete(es[0])
			}
		}
		fl.AddSection("z", true, []string{"p"})
		_ = fl.Bytes()
	})
}
