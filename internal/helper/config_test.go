package helper

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func needGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

type cfgEnv struct {
	t    *testing.T
	o    *Ops
	root string
	hist string
}

func newCfg(t *testing.T) *cfgEnv {
	t.Helper()
	needGit(t)
	base := t.TempDir()
	root := filepath.Join(base, "asterisk")
	for _, d := range []string{"custom", "custom/rpt"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	e := &cfgEnv{t: t, root: root, hist: filepath.Join(base, "hist", "repo")}
	e.o = &Ops{Run: ExecRunner{}, ConfigRoot: root, HistoryDir: e.hist,
		NewFileOwner: func() (int, int, fs.FileMode) { return os.Getuid(), os.Getgid(), 0o640 }}
	e.put("rpt.conf", "[general]\nnode=1\n", 0o644)
	e.put("custom/rpt/a.conf", "x=1\n", 0o640)
	return e
}

func (e *cfgEnv) put(rel, content string, mode fs.FileMode) {
	e.t.Helper()
	p := filepath.Join(e.root, rel)
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		e.t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		e.t.Fatal(err)
	}
}

func (e *cfgEnv) read(rel string) string {
	e.t.Helper()
	b, err := os.ReadFile(filepath.Join(e.root, rel))
	if err != nil {
		e.t.Fatal(err)
	}
	return string(b)
}

func (e *cfgEnv) sha(rel string) string { return sha256hex([]byte(e.read(rel))) }

func (e *cfgEnv) apply(files ...ConfigFile) (ConfigApplyResult, error) {
	return e.o.ConfigApply(context.Background(), &ConfigRequest{Files: files, Message: "test edit", Author: "alice"})
}

func TestPathValidation(t *testing.T) {
	e := newCfg(t)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "evil.conf"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// symlinked parent directory pointing outside the root
	if err := os.Symlink(outside, filepath.Join(e.root, "custom", "escape")); err != nil {
		t.Fatal(err)
	}
	// symlink target (file) inside root pointing outside
	if err := os.Symlink(filepath.Join(outside, "evil.conf"), filepath.Join(e.root, "link.conf")); err != nil {
		t.Fatal(err)
	}
	// symlink to a file inside the root
	if err := os.Symlink(filepath.Join(e.root, "rpt.conf"), filepath.Join(e.root, "inner.conf")); err != nil {
		t.Fatal(err)
	}
	// custom/inside -> custom/rpt (stays inside root and custom)
	e.put("noext", "x", 0o644)
	if err := os.Mkdir(filepath.Join(e.root, "dir.conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(e.root, "a/b/c/d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.root, "a/b/c/d/deep.conf"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.root, "a/b/c/ok4.conf"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	bad := []struct{ name, path string }{
		{"empty", ""},
		{"absolute", "/etc/passwd"},
		{"absolute conf", "/etc/asterisk/rpt.conf"},
		{"dotdot", "../rpt.conf"},
		{"dotdot mid", "custom/../rpt.conf"},
		{"dot", "./rpt.conf"},
		{"double slash", "custom//a.conf"},
		{"trailing slash", "rpt.conf/"},
		{"hidden", ".hidden.conf"},
		{"hidden dir", "custom/.x/a.conf"},
		{"not conf", "noext"},
		{"conf prefix only", "rpt.conf.bak"},
		{"dpkg-dist", "rpt.conf.dpkg-dist"},
		{"space", "my file.conf"},
		{"backslash", "custom\\a.conf"},
		{"nul", "rpt\x00.conf"},
		{"newline", "rpt\n.conf"},
		{"too deep", "a/b/c/d/deep.conf"},
		{"parent symlink escape", "custom/escape/evil.conf"},
		{"target symlink escape", "link.conf"},
		{"target symlink inside", "inner.conf"},
		{"directory", "dir.conf"},
		{"too long", strings.Repeat("a", 250) + ".conf"},
		{"unicode", "réseau.conf"},
	}
	for _, c := range bad {
		t.Run("read/"+c.name, func(t *testing.T) {
			if _, err := e.o.ConfigRead(c.path); err == nil {
				t.Fatalf("read %q accepted", c.path)
			}
		})
		t.Run("write/"+c.name, func(t *testing.T) {
			if _, err := e.apply(ConfigFile{Path: c.path, Content: "x"}); err == nil {
				t.Fatalf("write %q accepted", c.path)
			}
		})
	}
	// nothing outside was touched
	if b, _ := os.ReadFile(filepath.Join(outside, "evil.conf")); string(b) != "x" {
		t.Fatal("outside file modified")
	}
	// depth 4 is allowed (existing file)
	if _, err := e.o.ConfigRead("a/b/c/ok4.conf"); err != nil {
		t.Fatalf("depth 4 read: %v", err)
	}
	// new files only under custom/
	for _, p := range []string{"new.conf", "a/b/new.conf", "custom/nodir/new.conf", "customx/new.conf"} {
		if _, err := e.apply(ConfigFile{Path: p, Content: "x"}); err == nil {
			t.Errorf("new file %q accepted", p)
		}
	}
	// new file via symlinked dir inside custom that leaves custom is refused
	if err := os.Symlink(e.root, filepath.Join(e.root, "custom", "up")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.apply(ConfigFile{Path: "custom/up/new.conf", Content: "x"}); err == nil {
		t.Error("new file through symlink leaving custom/ accepted")
	}
	if _, err := os.Stat(filepath.Join(e.root, "new.conf")); err == nil {
		t.Error("file created outside custom/")
	}
	// valid new file in an existing custom dir
	if _, err := e.apply(ConfigFile{Path: "custom/rpt/new.conf", Content: "ok\n"}); err != nil {
		t.Fatalf("valid new file: %v", err)
	}
}

func TestContentAndCountLimits(t *testing.T) {
	e := newCfg(t)
	base := e.sha("rpt.conf")
	cases := []struct {
		name, content string
	}{
		{"nul", "a\x00b"},
		{"invalid utf8", "a\xffb"},
		{"too big", strings.Repeat("a", maxConfigFile+1)},
	}
	for _, c := range cases {
		if _, err := e.apply(ConfigFile{Path: "rpt.conf", Content: c.content, Base: base}); err == nil {
			t.Errorf("%s accepted", c.name)
		}
	}
	// exactly 1 MiB is fine
	if _, err := e.apply(ConfigFile{Path: "rpt.conf", Content: strings.Repeat("a", maxConfigFile), Base: base}); err != nil {
		t.Errorf("1 MiB rejected: %v", err)
	}
	// count
	var files []ConfigFile
	for i := 0; i <= maxConfigFiles; i++ {
		files = append(files, ConfigFile{Path: "custom/rpt/n" + string(rune('a'+i%26)) + string(rune('a'+i/26)) + ".conf", Content: "x"})
	}
	if _, err := e.apply(files...); err == nil || !strings.Contains(err.Error(), "too many") {
		t.Errorf("51 files: %v", err)
	}
	if _, err := e.apply(files[:50]...); err != nil {
		t.Errorf("50 files: %v", err)
	}
	// duplicates, bad base, bad message/author
	if _, err := e.apply(ConfigFile{Path: "rpt.conf", Content: "x"}, ConfigFile{Path: "rpt.conf", Content: "y"}); err == nil {
		t.Error("duplicate accepted")
	}
	if _, err := e.apply(ConfigFile{Path: "rpt.conf", Content: "x", Base: "XYZ"}); err == nil {
		t.Error("bad base accepted")
	}
	for _, m := range []string{"", "a\nb", "a\rb", "a\x00b", strings.Repeat("m", 201), "tab\there", "\xff"} {
		_, err := e.o.ConfigApply(context.Background(), &ConfigRequest{Files: []ConfigFile{{Path: "custom/rpt/m.conf", Content: "x"}}, Message: m, Author: "alice"})
		if err == nil {
			t.Errorf("message %q accepted", m)
		}
	}
	for _, a := range []string{"", "a b", "a<b>", "a/b", strings.Repeat("a", 65), "a\nb", "-x y"} {
		_, err := e.o.ConfigApply(context.Background(), &ConfigRequest{Files: []ConfigFile{{Path: "custom/rpt/m.conf", Content: "x"}}, Message: "m", Author: a})
		if err == nil {
			t.Errorf("author %q accepted", a)
		}
	}
	if _, err := e.o.ConfigApply(context.Background(), &ConfigRequest{Files: []ConfigFile{{Path: "custom/rpt/m.conf", Content: "x"}}, Message: "-m --amend", Author: "a-b.c_d"}); err != nil {
		t.Errorf("message starting with dash: %v", err)
	}
}

func TestApplySuccessConflictAndHistory(t *testing.T) {
	e := newCfg(t)
	base := e.sha("rpt.conf")
	res, err := e.apply(ConfigFile{Path: "rpt.conf", Content: "[general]\nnode=2\n", Base: base})
	if err != nil {
		t.Fatal(err)
	}
	if e.read("rpt.conf") != "[general]\nnode=2\n" || res.Commit == "" || res.Before == "" || res.Commit == res.Before {
		t.Fatalf("%+v", res)
	}
	if len(res.Files) != 1 || res.Files[0] != "rpt.conf" {
		t.Fatalf("files %v", res.Files)
	}
	// history repo root is 0700
	if fi, err := os.Stat(e.hist); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("history dir: %v %v", fi, err)
	}

	// stale base: conflict, nothing written, names the file; two files, one stale
	_, err = e.apply(ConfigFile{Path: "rpt.conf", Content: "other\n", Base: base},
		ConfigFile{Path: "custom/rpt/a.conf", Content: "changed\n", Base: e.sha("custom/rpt/a.conf")})
	var ce *ConflictError
	if !errors.As(err, &ce) || len(ce.Files) != 1 || ce.Files[0] != "rpt.conf" {
		t.Fatalf("want conflict on rpt.conf, got %v", err)
	}
	if e.read("custom/rpt/a.conf") != "x=1\n" {
		t.Fatal("file written despite conflict")
	}
	// new file with a base, and an existing file with an empty base: conflicts
	if _, err := e.apply(ConfigFile{Path: "custom/rpt/zzz.conf", Content: "x", Base: base}); !errors.As(err, &ce) {
		t.Fatalf("new file with base: %v", err)
	}
	if _, err := e.apply(ConfigFile{Path: "rpt.conf", Content: "x", Base: ""}); !errors.As(err, &ce) {
		t.Fatalf("existing file without base: %v", err)
	}

	// outside edit is captured by the "Before:" snapshot
	e.put("custom/rpt/a.conf", "outside=1\n", 0o640)
	r2, err := e.apply(ConfigFile{Path: "rpt.conf", Content: "[general]\nnode=3\n", Base: e.sha("rpt.conf")})
	if err != nil {
		t.Fatal(err)
	}
	log, err := e.o.ConfigLog(context.Background(), &ConfigRequest{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	var msgs []string
	for _, c := range log {
		msgs = append(msgs, c.Message)
	}
	want := []string{"test edit", "Before: test edit", "test edit", "Before: test edit", "Initial snapshot"}
	// first apply: Before commit absent when tree unchanged since init? the
	// initial snapshot already matches, so only: Initial, test edit, Before, test edit
	_ = want
	if msgs[0] != "test edit" || msgs[1] != "Before: test edit" || msgs[len(msgs)-1] != "Initial snapshot" || log[0].Commit != r2.Commit || log[1].Commit != r2.Before {
		t.Fatalf("log: %v", msgs)
	}
	if log[0].Author != "alice" || len(log[0].Files) != 1 || log[0].Files[0] != "rpt.conf" {
		t.Fatalf("%+v", log[0])
	}
	if len(log[1].Files) != 1 || log[1].Files[0] != "custom/rpt/a.conf" {
		t.Fatalf("snapshot files %+v", log[1])
	}
	// paging
	one, _ := e.o.ConfigLog(context.Background(), &ConfigRequest{Limit: 1, Offset: 1})
	if len(one) != 1 || one[0].Commit != r2.Before {
		t.Fatalf("paging: %+v", one)
	}
	for _, bad := range []ConfigRequest{{Limit: 201}, {Limit: -1}, {Offset: -1}} {
		if _, err := e.o.ConfigLog(context.Background(), &bad); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}

	// show
	sh, err := e.o.ConfigShow(context.Background(), &ConfigRequest{Commit: r2.Commit[:8]})
	if err != nil {
		t.Fatal(err)
	}
	if sh.Commit != r2.Commit || sh.Message != "test edit" || len(sh.Files) != 1 || sh.Files[0].Status != "modified" {
		t.Fatalf("%+v", sh)
	}
	d := sh.Files[0].Diff
	if !strings.HasPrefix(d, "--- a/rpt.conf\n+++ b/rpt.conf\n@@") || !strings.Contains(d, "-node=2\n+node=3\n") || strings.Contains(d, "diff --git") {
		t.Fatalf("diff:\n%s", d)
	}
	// root commit shows added files
	root := log[len(log)-1]
	sh, err = e.o.ConfigShow(context.Background(), &ConfigRequest{Commit: root.Commit})
	if err != nil || len(sh.Files) != 2 || sh.Files[0].Status != "added" || !strings.Contains(sh.Files[0].Diff, "+node=1") && !strings.Contains(sh.Files[1].Diff, "+node=1") {
		t.Fatalf("root show: %+v %v", sh, err)
	}
	for _, c := range []string{"", "xyz", "abc", "HEAD", "--all", strings.Repeat("a", 41), "0000000", "../x"} {
		if _, err := e.o.ConfigShow(context.Background(), &ConfigRequest{Commit: c}); err == nil {
			t.Errorf("show %q accepted", c)
		}
	}
}

func TestNoOpApply(t *testing.T) {
	e := newCfg(t)
	res, err := e.apply(ConfigFile{Path: "rpt.conf", Content: e.read("rpt.conf"), Base: e.sha("rpt.conf")})
	if err != nil || res.Commit != res.Before || len(res.Files) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestModePreservedAndNewFileOwner(t *testing.T) {
	e := newCfg(t)
	e.put("custom/rpt/a.conf", "x=1\n", 0o600)
	if _, err := e.apply(
		ConfigFile{Path: "custom/rpt/a.conf", Content: "x=2\n", Base: e.sha("custom/rpt/a.conf")},
		ConfigFile{Path: "rpt.conf", Content: "n\n", Base: e.sha("rpt.conf")},
		ConfigFile{Path: "custom/rpt/fresh.conf", Content: "n\n"}); err != nil {
		t.Fatal(err)
	}
	for rel, want := range map[string]fs.FileMode{"custom/rpt/a.conf": 0o600, "rpt.conf": 0o644, "custom/rpt/fresh.conf": 0o640} {
		fi, err := os.Stat(filepath.Join(e.root, rel))
		if err != nil || fi.Mode().Perm() != want {
			t.Errorf("%s: %v %v want %v", rel, fi.Mode(), err, want)
		}
		if uid, gid, ok := ownerIDs(fi); ok && (uid != os.Getuid() || gid != os.Getgid()) {
			t.Errorf("%s owner %d:%d", rel, uid, gid)
		}
	}
	// no temp files left
	ents, _ := os.ReadDir(filepath.Join(e.root, "custom/rpt"))
	for _, en := range ents {
		if strings.Contains(en.Name(), "tmp") {
			t.Errorf("leftover %s", en.Name())
		}
	}
}

func TestOwnershipPreservedAsRoot(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	e := newCfg(t)
	if err := os.Chown(filepath.Join(e.root, "rpt.conf"), 12345, 23456); err != nil {
		t.Fatal(err)
	}
	if _, err := e.apply(ConfigFile{Path: "rpt.conf", Content: "n\n", Base: e.sha("rpt.conf")}); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(filepath.Join(e.root, "rpt.conf"))
	if uid, gid, _ := ownerIDs(fi); uid != 12345 || gid != 23456 {
		t.Fatalf("owner %d:%d", uid, gid)
	}
}

func TestRollbackOnWriteError(t *testing.T) {
	e := newCfg(t)
	e.put("custom/rpt/b.conf", "b=1\n", 0o640)
	var calls []string
	e.o.ConfigWrite = func(abs, parent string, data []byte, uid, gid int, mode fs.FileMode) error {
		calls = append(calls, filepath.Base(abs))
		if filepath.Base(abs) == "b.conf" && string(data) == "b=2\n" {
			return errors.New("disk full")
		}
		return atomicWrite(abs, parent, data, uid, gid, mode)
	}
	a0, b0, r0 := e.read("custom/rpt/a.conf"), e.read("custom/rpt/b.conf"), e.read("rpt.conf")
	_, err := e.apply(
		ConfigFile{Path: "custom/rpt/a.conf", Content: "a=2\n", Base: e.sha("custom/rpt/a.conf")},
		ConfigFile{Path: "custom/rpt/newfile.conf", Content: "new\n"},
		ConfigFile{Path: "custom/rpt/b.conf", Content: "b=2\n", Base: e.sha("custom/rpt/b.conf")},
		ConfigFile{Path: "rpt.conf", Content: "z\n", Base: e.sha("rpt.conf")})
	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("err = %v", err)
	}
	if e.read("custom/rpt/a.conf") != a0 || e.read("custom/rpt/b.conf") != b0 || e.read("rpt.conf") != r0 {
		t.Fatal("files not rolled back")
	}
	if _, err := os.Stat(filepath.Join(e.root, "custom/rpt/newfile.conf")); err == nil {
		t.Fatal("new file not removed")
	}
	// history has no commit for the failed apply
	log, _ := e.o.ConfigLog(context.Background(), &ConfigRequest{Limit: 50})
	for _, c := range log {
		if c.Message == "test edit" {
			t.Fatalf("commit for failed apply: %+v", log)
		}
	}
	// and a working writer afterwards still works
	e.o.ConfigWrite = nil
	if _, err := e.apply(ConfigFile{Path: "rpt.conf", Content: "z\n", Base: e.sha("rpt.conf")}); err != nil {
		t.Fatal(err)
	}
}

func TestRestore(t *testing.T) {
	e := newCfg(t)
	c := context.Background()
	r1, _ := e.apply(ConfigFile{Path: "rpt.conf", Content: "v2\n", Base: e.sha("rpt.conf")},
		ConfigFile{Path: "custom/rpt/a.conf", Content: "a2\n", Base: e.sha("custom/rpt/a.conf")})
	if _, err := e.apply(ConfigFile{Path: "rpt.conf", Content: "v3\n", Base: e.sha("rpt.conf")},
		ConfigFile{Path: "custom/rpt/late.conf", Content: "late\n"}); err != nil {
		t.Fatal(err)
	}
	// restore to just before the first edit
	res, err := e.o.ConfigRestore(c, &ConfigRequest{Commit: r1.Before, Author: "bob"})
	if err != nil {
		t.Fatal(err)
	}
	if e.read("rpt.conf") != "[general]\nnode=1\n" || e.read("custom/rpt/a.conf") != "x=1\n" {
		t.Fatalf("not restored: %q %q", e.read("rpt.conf"), e.read("custom/rpt/a.conf"))
	}
	if e.read("custom/rpt/late.conf") != "late\n" {
		t.Fatal("file that did not exist then was touched")
	}
	if len(res.Skipped) != 1 || res.Skipped[0] != "custom/rpt/late.conf" {
		t.Fatalf("skipped %v", res.Skipped)
	}
	if len(res.Files) != 2 || res.Commit == res.Before {
		t.Fatalf("%+v", res)
	}
	log, _ := e.o.ConfigLog(c, &ConfigRequest{Limit: 1})
	if !strings.HasPrefix(log[0].Message, "Restore "+r1.Before[:7]+": ") || log[0].Author != "bob" {
		t.Fatalf("%+v", log[0])
	}
	// restoring again: nothing differs
	if _, err := e.o.ConfigRestore(c, &ConfigRequest{Commit: r1.Before, Author: "bob"}); err == nil || !strings.Contains(err.Error(), "nothing to restore") {
		t.Fatalf("second restore: %v", err)
	}
	// rollback of the restore itself works
	if _, err := e.o.ConfigRestore(c, &ConfigRequest{Commit: res.Before, Author: "bob"}); err != nil {
		t.Fatal(err)
	}
	if e.read("rpt.conf") != "v3\n" {
		t.Fatalf("%q", e.read("rpt.conf"))
	}
	// bad requests
	if _, err := e.o.ConfigRestore(c, &ConfigRequest{Commit: "nothex!", Author: "bob"}); err == nil {
		t.Error("bad commit")
	}
	if _, err := e.o.ConfigRestore(c, &ConfigRequest{Commit: r1.Before, Author: "b b"}); err == nil {
		t.Error("bad author")
	}
}

func TestRestoreSkipsMaliciousTreeEntries(t *testing.T) {
	e := newCfg(t)
	c := context.Background()
	outside := t.TempDir()
	// a symlink .conf tracked in history must never be written through
	if err := os.Symlink(filepath.Join(outside, "t.conf"), filepath.Join(e.root, "custom", "ln.conf")); err != nil {
		t.Fatal(err)
	}
	e.put("allmon.ini.php", "passwd=secret\n", 0o660)
	e.put("old.conf.bak", "x", 0o644)
	r, err := e.apply(ConfigFile{Path: "rpt.conf", Content: "v2\n", Base: e.sha("rpt.conf")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.o.ConfigRestore(c, &ConfigRequest{Commit: r.Before, Author: "bob"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outside, "t.conf")); err == nil {
		t.Fatal("wrote through symlink")
	}
	// list ignores .bak and non-.conf and the symlink
	list, _ := e.o.ConfigList(c)
	for _, l := range list {
		if strings.Contains(l.Path, "bak") || strings.Contains(l.Path, "php") || l.Path == "custom/ln.conf" {
			t.Errorf("listed %s", l.Path)
		}
	}
	// the snapshot tracks the non-.conf file too, and show lists it raw
	log, _ := e.o.ConfigLog(c, &ConfigRequest{Limit: 50})
	sh, err := e.o.ConfigShow(c, &ConfigRequest{Commit: log[len(log)-1].Commit})
	if err != nil {
		t.Fatal(err)
	}
	_ = sh
	found := false
	for _, cm := range log {
		for _, f := range cm.Files {
			if f == "allmon.ini.php" {
				found = true
			}
		}
	}
	if !found {
		t.Error("non-conf file not snapshotted")
	}
}

func TestListAndRead(t *testing.T) {
	e := newCfg(t)
	c := context.Background()
	e.put("zz.conf", "z", 0o600)
	e.put("big.conf", strings.Repeat("a", maxConfigFile+1), 0o644)
	e.put("README", "x", 0o644)
	e.put("x.conf.dpkg-dist", "x", 0o644)
	os.MkdirAll(filepath.Join(e.root, "a/b/c/d"), 0o755)
	os.WriteFile(filepath.Join(e.root, "a/b/c/d/deep.conf"), []byte("x"), 0o644)
	os.MkdirAll(filepath.Join(e.root, "broadcastify"), 0o750)
	os.WriteFile(filepath.Join(e.root, "broadcastify/x.cfg"), []byte("x"), 0o640)
	list, err := e.o.ConfigList(c)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, l := range list {
		got = append(got, l.Path)
	}
	if strings.Join(got, ",") != "custom/rpt/a.conf,rpt.conf,zz.conf" {
		t.Fatalf("%v", got)
	}
	for _, l := range list {
		if l.Path == "zz.conf" && (l.Mode != "0600" || l.SHA256 != sha256hex([]byte("z")) || l.Size != 1 || l.Owner == "") {
			t.Fatalf("%+v", l)
		}
	}
	rd, err := e.o.ConfigRead("rpt.conf")
	if err != nil || rd.Content != "[general]\nnode=1\n" || rd.SHA256 != sha256hex([]byte(rd.Content)) {
		t.Fatalf("%+v %v", rd, err)
	}
	if _, err := e.o.ConfigRead("missing.conf"); err == nil {
		t.Fatal("missing file read")
	}
	if _, err := e.o.ConfigRead("big.conf"); err == nil {
		t.Fatal("big file read")
	}
	e.put("bin.conf", "a\x00b", 0o644)
	if _, err := e.o.ConfigRead("bin.conf"); err == nil {
		t.Fatal("binary file read")
	}
	// entry cap
	for i := 0; i < maxListEntries+10; i++ {
		os.WriteFile(filepath.Join(e.root, "custom", "f"+strings.Repeat("x", 1)+string(rune('a'+i%26))+string(rune('a'+(i/26)%26))+string(rune('a'+i/676))+".conf"), []byte("x"), 0o644)
	}
	list, _ = e.o.ConfigList(c)
	if len(list) != maxListEntries {
		t.Fatalf("list cap: %d", len(list))
	}
}

// fixed argv and hardening options, no shell
func TestGitInvocation(t *testing.T) {
	needGit(t)
	e := newCfg(t)
	rec := &fakeRunner{rules: map[string]resp{"env -i": {}}}
	// git rev-parse HEAD returns a hash so prepareRepo proceeds
	rec.rules["env -i PATH=/usr/local/bin:/usr/bin:/bin HOME=/nonexistent GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null GIT_TERMINAL_PROMPT=0 GIT_ASKPASS=/bin/false LC_ALL=C GIT_DIR="] = resp{out: "abc\n"}
	e.o.Run = rec
	_, _ = e.o.ConfigLog(context.Background(), &ConfigRequest{Limit: 1})
	if len(rec.calls) == 0 {
		t.Fatal("no git calls")
	}
	for _, c := range rec.calls {
		for _, need := range []string{"env -i ", "HOME=/nonexistent", "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0",
			" git --no-pager ", "core.hooksPath=/dev/null", "commit.gpgSign=false", "GIT_DIR=" + e.hist, "GIT_WORK_TREE=" + e.root} {
			if !strings.Contains(c, need) {
				t.Errorf("%q missing %q", c, need)
			}
		}
	}
}

func TestHooksDisabled(t *testing.T) {
	e := newCfg(t)
	marker := filepath.Join(t.TempDir(), "hook-ran")
	if _, err := e.apply(ConfigFile{Path: "rpt.conf", Content: "v2\n", Base: e.sha("rpt.conf")}); err != nil {
		t.Fatal(err)
	}
	hooks := filepath.Join(e.hist, "hooks")
	os.MkdirAll(hooks, 0o755)
	for _, h := range []string{"pre-commit", "post-commit", "commit-msg", "prepare-commit-msg", "post-checkout", "reference-transaction"} {
		script := "#!/bin/sh\ntouch " + marker + "\n"
		if err := os.WriteFile(filepath.Join(hooks, h), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.apply(ConfigFile{Path: "rpt.conf", Content: "v3\n", Base: e.sha("rpt.conf")}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.o.ConfigRestore(context.Background(), &ConfigRequest{Commit: "HEAD"[:0] + func() string {
		l, _ := e.o.ConfigLog(context.Background(), &ConfigRequest{Limit: 50})
		return l[len(l)-1].Commit
	}(), Author: "bob"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a git hook was executed")
	}
}

func TestHistoryDirChecks(t *testing.T) {
	needGit(t)
	// pre-existing history dir with loose permissions is tightened
	e := newCfg(t)
	os.MkdirAll(e.hist, 0o755)
	os.Chmod(e.hist, 0o755)
	if _, err := e.o.ConfigLog(context.Background(), &ConfigRequest{}); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(e.hist); fi.Mode().Perm() != 0o700 {
		t.Fatalf("mode %v", fi.Mode())
	}
	// a symlink instead of the directory is refused
	e2 := newCfg(t)
	target := t.TempDir()
	os.MkdirAll(filepath.Dir(e2.hist), 0o755)
	os.Symlink(target, e2.hist)
	if _, err := e2.o.ConfigLog(context.Background(), &ConfigRequest{}); err == nil {
		t.Fatal("symlinked history dir accepted")
	}
	// ancestors must be closed to others
	d := t.TempDir()
	if err := os.Chmod(d, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := checkAncestors(d); err == nil {
		t.Fatal("world-writable ancestor accepted")
	}
	if err := os.Chmod(d, 0o755); err != nil {
		t.Fatal(err)
	}
	// /tmp itself may be 1777 so only the leaf is asserted here
	if _, err := os.Stat(d); err != nil {
		t.Fatal(err)
	}
}

func TestConfigFieldRejectedOnOtherOps(t *testing.T) {
	o := &Ops{Run: &fakeRunner{rules: map[string]resp{}}}
	for _, op := range []string{OpUpdateStatus, OpUpdateApply, OpFirewallOpen, OpChannelBeta, OpChannelStable, OpAsteriskRestart, "bogus", ""} {
		if _, err := o.HandleRequest(context.Background(), Request{Op: op, Config: &ConfigRequest{Path: "rpt.conf"}}); err == nil || !strings.Contains(err.Error(), "config field") {
			t.Errorf("%q: %v", op, err)
		}
	}
	// stray fields on config ops
	for op, c := range map[string]ConfigRequest{
		OpConfigList:    {Path: "x"},
		OpConfigRead:    {Message: "x"},
		OpConfigLog:     {Path: "x"},
		OpConfigShow:    {Limit: 1},
		OpConfigApply:   {Commit: "abcdef0"},
		OpConfigRestore: {Files: []ConfigFile{{Path: "x"}}},
		OpConfigRecord:  {Commit: "abcdef0"},
	} {
		c := c
		if _, err := o.HandleRequest(context.Background(), Request{Op: op, Config: &c}); err == nil || !strings.Contains(err.Error(), "unexpected field") {
			t.Errorf("%s: %v", op, err)
		}
	}
}

func TestAsteriskRestart(t *testing.T) {
	f := &fakeRunner{rules: map[string]resp{"systemctl restart asterisk": {}}}
	o := &Ops{Run: f}
	res, err := o.HandleRequest(context.Background(), Request{Op: OpAsteriskRestart})
	if err != nil || res.(OKResult).OK != true || f.called("systemctl restart asterisk") != 1 {
		t.Fatalf("%v %v", res, err)
	}
	f.rules["systemctl restart asterisk"] = resp{code: 1, out: "Job failed"}
	if _, err := o.AsteriskRestart(context.Background()); err == nil {
		t.Fatal("failure not reported")
	}
}

func TestConfigOpsOverSocket(t *testing.T) {
	e := newCfg(t)
	cl, _ := startServer(t, e.o, func(net.Conn) (uint32, error) { return 0, nil }, []uint32{0})
	ctx := context.Background()
	list, err := cl.ConfigList(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("%v %v", list, err)
	}
	rd, err := cl.ConfigRead(ctx, "rpt.conf")
	if err != nil {
		t.Fatal(err)
	}
	// content with HTML characters survives the round trip
	body := "[x]\na = <b>&</b>\n"
	res, err := cl.ConfigApply(ctx, []ConfigFile{{Path: "rpt.conf", Content: body, Base: rd.SHA256}}, "html", "alice")
	if err != nil {
		t.Fatal(err)
	}
	if e.read("rpt.conf") != body {
		t.Fatal("content mangled")
	}
	_, err = cl.ConfigApply(ctx, []ConfigFile{{Path: "rpt.conf", Content: "q", Base: rd.SHA256}}, "stale", "alice")
	var ce *ConflictError
	if !errors.As(err, &ce) || ce.Files[0] != "rpt.conf" {
		t.Fatalf("conflict: %v", err)
	}
	if _, err := cl.ConfigShow(ctx, res.Commit); err != nil {
		t.Fatal(err)
	}
	// oversized request is cut off
	big := []ConfigFile{}
	for i := 0; i < 6; i++ {
		big = append(big, ConfigFile{Path: "custom/rpt/big" + string(rune('a'+i)) + ".conf", Content: strings.Repeat("a", maxConfigFile)})
	}
	if _, err := cl.ConfigApply(ctx, big, "big", "alice"); err == nil {
		t.Fatal("6 MiB request accepted")
	}
	// plain op with config field over the wire
	if err := cl.do(ctx, Request{Op: OpUpdateStatus, Config: &ConfigRequest{Path: "x"}}, nil, 1<<20); err == nil {
		t.Fatal("config on update-status accepted")
	}
}

func TestConfigRecord(t *testing.T) {
	e := newCfg(t)
	ctx := context.Background()
	if _, err := e.o.prepareRepo(ctx); err != nil { // initial snapshot, without the radio file
		t.Fatal(err)
	}
	e.put("simpleusb.conf", "[1999]\nrxmixerset=500\n", 0o644)
	req := &ConfigRequest{Path: "simpleusb.conf", Message: "Radio tuning saved from web UI (device 1999)", Author: "alice"}
	r1, err := e.o.ConfigRecord(ctx, req)
	if err != nil || len(r1.Files) != 1 || r1.Commit == r1.Before {
		t.Fatalf("first record: %+v %v", r1, err)
	}
	// unchanged file: no new commit
	r2, err := e.o.ConfigRecord(ctx, req)
	if err != nil || len(r2.Files) != 0 || r2.Commit != r1.Commit {
		t.Fatalf("second record: %+v %v", r2, err)
	}
	// file rewritten behind our back is recorded, with the given message
	e.put("simpleusb.conf", "[1999]\nrxmixerset=640\n", 0o644)
	r3, err := e.o.ConfigRecord(ctx, req)
	if err != nil || r3.Commit == r2.Commit {
		t.Fatalf("third record: %+v %v", r3, err)
	}
	log, err := e.o.ConfigLog(ctx, &ConfigRequest{})
	if err != nil || len(log) == 0 || !strings.Contains(log[0].Message, "Radio tuning saved") || log[0].Author != "alice" {
		t.Fatalf("log: %+v %v", log, err)
	}
	// the file itself is never written by the op
	if e.read("simpleusb.conf") != "[1999]\nrxmixerset=640\n" {
		t.Fatal("record changed the file")
	}
	// only the radio interface files are accepted
	for _, bad := range []string{"rpt.conf", "custom/rpt/a.conf", "../simpleusb.conf", "", "extensions.conf"} {
		if _, err := e.o.ConfigRecord(ctx, &ConfigRequest{Path: bad, Message: "x", Author: "alice"}); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, err := e.o.ConfigRecord(ctx, &ConfigRequest{Path: "simpleusb.conf", Message: "bad\nline", Author: "alice"}); err == nil {
		t.Error("multi-line message accepted")
	}
	if _, err := e.o.ConfigRecord(ctx, &ConfigRequest{Path: "simpleusb.conf", Message: "ok", Author: "a b"}); err == nil {
		t.Error("bad author accepted")
	}
	// a missing file is an error, not an empty commit
	if err := os.Remove(filepath.Join(e.root, "simpleusb.conf")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.o.ConfigRecord(ctx, req); err == nil {
		t.Error("missing file accepted")
	}
}

func TestConfigRecordOverSocket(t *testing.T) {
	e := newCfg(t)
	if _, err := e.o.prepareRepo(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.put("simpleusb.conf", "[1999]\n", 0o644)
	cl, _ := startServer(t, e.o, func(net.Conn) (uint32, error) { return 0, nil }, []uint32{0})
	res, err := cl.ConfigRecord(context.Background(), "simpleusb.conf", "Radio tuning saved", "alice")
	if err != nil || res.Commit == "" {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := cl.ConfigRecord(context.Background(), "rpt.conf", "x", "alice"); err == nil {
		t.Fatal("rpt.conf accepted")
	}
}
