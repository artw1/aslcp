package astconf

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tmpTree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func values(es []ResolvedEntry) string {
	var p []string
	for _, e := range es {
		p = append(p, e.Key+"="+e.Value)
	}
	return strings.Join(p, ",")
}

func TestIncludeSplice(t *testing.T) {
	dir := tmpTree(t, map[string]string{
		"main.conf":    "[general]\na=1\n#include inc/one.conf\nc=3\n[other]\nz=26\n#tryinclude missing.conf\n#tryinclude \"inc/*.conf\"\n",
		"inc/one.conf": "b=2\n[sub]\ns=1\n",
		"inc/two.conf": "t=2\n",
		"inc/a.conf":   "first=1\n",
	})
	tr, err := LoadTree(NewOSFS(dir), "", "main.conf")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range tr.Sections {
		names = append(names, s.Name)
	}
	if got := strings.Join(names, ","); got != "general,sub,other,sub" {
		t.Fatalf("sections %s", got)
	}
	g := tr.Lookup("general")[0]
	if got := values(g.Entries); got != "a=1,b=2" {
		t.Errorf("general %s", got)
	}
	// c=3 follows the include and so continues the section current at the
	// end of the included text, which is [sub].
	if got := values(tr.Lookup("sub")[0].Entries); got != "s=1,c=3" {
		t.Errorf("sub %s", got)
	}
	if got := values(tr.Lookup("other")[0].Entries); got != "z=26,first=1,b=2" {
		t.Errorf("other %s", got)
	}
	// Glob order a.conf, one.conf, two.conf: one.conf reopens [sub], then t=2 continues it.
	if got := values(tr.Lookup("sub")[1].Entries); got != "s=1,t=2" {
		t.Errorf("sub#2 %s", got)
	}
	var inc []string
	for _, r := range tr.Includes {
		inc = append(inc, strings.Join(r.Resolved, "|"))
	}
	if got := strings.Join(inc, ";"); got != "inc/one.conf;;inc/a.conf|inc/one.conf|inc/two.conf" {
		t.Errorf("includes %q", got)
	}
}

func TestProvenanceThroughInclude(t *testing.T) {
	dir := tmpTree(t, map[string]string{
		"main.conf":  "; c\n[general]\na=1\n#include sub/x.conf\n",
		"sub/x.conf": "\n\nb=2\n",
	})
	tr, err := LoadTree(NewOSFS(dir), "", "main.conf")
	if err != nil {
		t.Fatal(err)
	}
	es, err := Effective(tr, "general")
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 2 {
		t.Fatal(es)
	}
	if es[0].File != "main.conf" || es[0].Line != 3 || es[0].SectionLine != 2 {
		t.Errorf("%+v", es[0])
	}
	if es[1].File != "sub/x.conf" || es[1].Line != 3 || es[1].SectionFile != "main.conf" || es[1].Section != "general" {
		t.Errorf("%+v", es[1])
	}
	if es[1].Entry == nil || es[1].Entry.Value != "2" {
		t.Error("entry node missing")
	}
}

func TestPreambleEntries(t *testing.T) {
	dir := tmpTree(t, map[string]string{"m.conf": "x=1\n[a]\ny=2\n"})
	tr, err := LoadTree(NewOSFS(dir), "", "m.conf")
	if err != nil {
		t.Fatal(err)
	}
	es, err := Effective(tr, "")
	if err != nil || values(es) != "x=1" {
		t.Errorf("%v %v", es, err)
	}
}

func TestIncludeErrors(t *testing.T) {
	dir := tmpTree(t, map[string]string{
		"missing.conf": "[a]\n#include nothere.conf\n",
		"cycle1.conf":  "[a]\n#include cycle2.conf\n",
		"cycle2.conf":  "#tryinclude cycle1.conf\n",
		"self.conf":    "#include self.conf\n",
		"deep.conf":    "#include missing.conf\n",
		"ok.conf":      "#include \"g/*.conf\"\n#tryinclude none.conf\n",
		"exec.conf":    "[a]\n#exec /bin/false\nk=1\n",
	})
	fsys := NewOSFS(dir)
	_, err := LoadTree(fsys, "", "missing.conf")
	if err == nil || !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), "missing.conf:2") {
		t.Errorf("missing: %v", err)
	}
	_, err = LoadTree(fsys, "", "deep.conf")
	if err == nil || !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), "missing.conf:2") {
		t.Errorf("deep: %v", err)
	}
	for _, f := range []string{"cycle1.conf", "self.conf"} {
		_, err = LoadTree(fsys, "", f)
		if !errors.Is(err, ErrIncludeCycle) {
			t.Errorf("%s: %v", f, err)
		}
	}
	// Glob without matches is not an error.
	if _, err = LoadTree(fsys, "", "ok.conf"); err != nil {
		t.Errorf("ok: %v", err)
	}
	if _, err = LoadTree(fsys, "", "nonexistent.conf"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("main missing: %v", err)
	}
	tr, err := LoadTree(fsys, "", "exec.conf")
	if err != nil || len(tr.Execs) != 1 || tr.Execs[0].Command != "/bin/false" || tr.Execs[0].Line != 2 {
		t.Errorf("exec: %v %+v", err, tr)
	}
}

func TestRootAndAbsolute(t *testing.T) {
	dir := tmpTree(t, map[string]string{
		"rpt.conf":   "[a]\n#include " + "ABS" + "\n#include rel.conf\n",
		"rel.conf":   "r=1\n",
		"other/x.cf": "abs=1\n",
	})
	abs := filepath.Join(dir, "other", "x.cf")
	data, _ := os.ReadFile(filepath.Join(dir, "rpt.conf"))
	os.WriteFile(filepath.Join(dir, "rpt.conf"), []byte(strings.Replace(string(data), "ABS", abs, 1)), 0o644)
	// OS paths with an explicit root and an empty-root OSFS.
	tr, err := LoadTree(&OSFS{}, dir, "rpt.conf")
	if err != nil {
		t.Fatal(err)
	}
	es, _ := Effective(tr, "a")
	if values(es) != "abs=1,r=1" {
		t.Errorf("%s", values(es))
	}
	if es[0].File != "other/x.cf" || es[1].File != "rel.conf" {
		t.Errorf("files %q %q", es[0].File, es[1].File)
	}
}

func TestInheritance(t *testing.T) {
	dir := tmpTree(t, map[string]string{
		"m.conf": `[base](!)
a = base
b = base
rep = 1
rep = 2
[mixin](!)
b = mixin
c = mixin
[node](base,mixin)
c = node
d = node
d = node2
[child](node)
a = child
rep = 3
rep = 4
`,
	})
	tr, err := LoadTree(NewOSFS(dir), "", "m.conf")
	if err != nil {
		t.Fatal(err)
	}
	es, err := Effective(tr, "node")
	if err != nil {
		t.Fatal(err)
	}
	// base: a,b,rep,rep; mixin overrides b (later parent wins) -> a,rep,rep,b(mixin),c(mixin);
	// node overrides c.
	if got := values(es); got != "a=base,rep=1,rep=2,b=mixin,c=node,d=node,d=node2" {
		t.Errorf("node: %s", got)
	}
	es, err = Effective(tr, "child")
	if err != nil {
		t.Fatal(err)
	}
	if got := values(es); got != "rep=3,rep=4,b=mixin,c=node,d=node,d=node2,a=child" && got != "b=mixin,c=node,d=node,d=node2,a=child,rep=3,rep=4" {
		t.Errorf("child: %s", got)
	}
	byKey := map[string]ResolvedEntry{}
	for _, e := range es {
		byKey[e.Key+"="+e.Value] = e
	}
	if e := byKey["b=mixin"]; e.Section != "mixin" || e.Line != 7 || !e.Inherited || e.SectionLine != 6 {
		t.Errorf("b provenance %+v", e)
	}
	if e := byKey["a=child"]; e.Section != "child" || e.Inherited || e.Line != 14 {
		t.Errorf("a provenance %+v", e)
	}
	if e := byKey["d=node2"]; e.Section != "node" || e.Line != 12 || e.File != "m.conf" {
		t.Errorf("d provenance %+v", e)
	}
}

func TestReopenedSectionsCombine(t *testing.T) {
	dir := tmpTree(t, map[string]string{
		"m.conf": "[t](!)\nx=t\ny=t\n[s]\na=1\n[o]\n[s](t)\na=2\nx=s\n",
	})
	tr, _ := LoadTree(NewOSFS(dir), "", "m.conf")
	es, err := Effective(tr, "s")
	if err != nil {
		t.Fatal(err)
	}
	if got := values(es); got != "y=t,a=1,a=2,x=s" {
		t.Errorf("%s", got)
	}
}

func TestInheritanceErrors(t *testing.T) {
	dir := tmpTree(t, map[string]string{
		"m.conf": "[a](b)\nk=1\n[b](c)\n[c](a)\n[orphan](nope)\n[self](self)\n",
	})
	tr, err := LoadTree(NewOSFS(dir), "", "m.conf")
	if err != nil {
		t.Fatal(err)
	}
	_, err = Effective(tr, "a")
	if !errors.Is(err, ErrInheritanceCycle) || !strings.Contains(err.Error(), "a -> b -> c -> a") {
		t.Errorf("cycle: %v", err)
	}
	_, err = Effective(tr, "self")
	if !errors.Is(err, ErrInheritanceCycle) {
		t.Errorf("self: %v", err)
	}
	_, err = Effective(tr, "orphan")
	if !errors.Is(err, ErrMissingParent) || !strings.Contains(err.Error(), `"nope"`) || !strings.Contains(err.Error(), `"orphan"`) {
		t.Errorf("missing: %v", err)
	}
	if _, err = Effective(tr, "zzz"); !errors.Is(err, ErrNoSection) {
		t.Errorf("nosection: %v", err)
	}
}

func TestCorpusTree(t *testing.T) {
	root, _ := filepath.Abs("testdata/corpus/asl3-dist")
	tr, err := LoadTree(NewOSFS(root), "", "rpt.conf")
	if err != nil {
		t.Fatal(err)
	}
	es, err := Effective(tr, "1999")
	if err != nil {
		t.Fatal(err)
	}
	if len(es) < 10 {
		t.Errorf("only %d entries", len(es))
	}
	var haveInherited bool
	for _, e := range es {
		if e.Inherited && e.Section == "node-main" && e.File == "rpt.conf" {
			haveInherited = true
		}
	}
	if !haveInherited {
		t.Error("no entry inherited from node-main")
	}
	if len(tr.Files) < 2 {
		t.Errorf("expected includes to be loaded, files=%d", len(tr.Files))
	}
	// All other corpus files resolve cleanly too.
	for _, f := range []string{"iax.conf", "extensions.conf", "modules.conf", "simpleusb.conf", "usbradio.conf", "voter.conf", "echolink.conf", "asterisk.conf"} {
		if _, err := LoadTree(NewOSFS(root), "", f); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
}
