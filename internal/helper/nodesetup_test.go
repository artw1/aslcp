package helper

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupEnv(t *testing.T) (*cfgEnv, *fakeRunner, *Client) {
	e := newCfg(t)
	e.put("savenode.conf", "# saved node\nNODE=1999\nPASSWORD=12345\nENABLE=0\nOTHER=\"keep me\"\n", 0o640)
	allmon := filepath.Join(filepath.Dir(e.root), "allmon3")
	if err := os.MkdirAll(allmon, 0o755); err != nil {
		t.Fatal(err)
	}
	ini := "; allmon\n[1999]\nip=127.0.0.1\nuser=admin\npass=old-pass ; c\n\n[other]\nx=1\n"
	if err := os.WriteFile(filepath.Join(allmon, "allmon3.ini"), []byte(ini), 0o640); err != nil {
		t.Fatal(err)
	}
	e.o.AllmonDir = allmon
	f := &fakeRunner{rules: map[string]resp{"systemctl try-restart": {}}}
	e.o.Run = &execGit{f}
	c, _ := startServer(t, e.o, PeerUID, []uint32{0, uint32(os.Getuid())})
	return e, f, c
}

// execGit sends git to the real runner and everything else to the fake.
type execGit struct{ f *fakeRunner }

func (x *execGit) Run(ctx context.Context, argv ...string) (string, int, error) {
	if argv[0] == "git" || argv[0] == "env" {
		return ExecRunner{}.Run(ctx, argv...)
	}
	return x.f.Run(ctx, argv...)
}

func TestSavenodeSetSocket(t *testing.T) {
	e, _, c := setupEnv(t)
	on := true
	res, err := c.SavenodeSet(context.Background(), NodeSetupRequest{Node: "1997", Password: "example-pass", Enable: &on, Author: "alice"})
	if err != nil || !res.Changed || !res.Recorded {
		t.Fatalf("%+v %v", res, err)
	}
	want := "# saved node\nNODE=1997\nPASSWORD=example-pass\nENABLE=1\nOTHER=\"keep me\"\n"
	if got := e.read("savenode.conf"); got != want {
		t.Fatalf("got %q", got)
	}
	if res, err = c.SavenodeSet(context.Background(), NodeSetupRequest{Node: "1997", Password: "example-pass", Enable: &on, Author: "alice"}); err != nil || res.Changed {
		t.Fatalf("second run should be a no-op: %+v %v", res, err)
	}
	// missing keys are appended
	e.put("savenode.conf", "NODE=1999", 0o640)
	if _, err = c.SavenodeSet(context.Background(), NodeSetupRequest{Node: "1998", Password: "p", Enable: &on, Author: "alice"}); err != nil {
		t.Fatal(err)
	}
	if got := e.read("savenode.conf"); got != "NODE=1998\nPASSWORD=p\nENABLE=1\n" {
		t.Fatalf("got %q", got)
	}
	// missing file is skipped, not created
	os.Remove(filepath.Join(e.root, "savenode.conf"))
	if res, err = c.SavenodeSet(context.Background(), NodeSetupRequest{Node: "1998", Password: "p", Enable: &on, Author: "alice"}); err != nil || res.Skipped != SetupSkipNoFile {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(e.root, "savenode.conf")); err == nil {
		t.Fatal("must not create the file")
	}
}

func TestSavenodeSetRefusals(t *testing.T) {
	e, _, c := setupEnv(t)
	on := true
	before := e.read("savenode.conf")
	bad := []NodeSetupRequest{
		{Node: "0", Password: "p", Enable: &on, Author: "a"},
		{Node: "01999", Password: "p", Enable: &on, Author: "a"},
		{Node: "12345678", Password: "p", Enable: &on, Author: "a"},
		{Node: "1999", Password: "p;rm -rf /", Enable: &on, Author: "a"},
		{Node: "1999", Password: "a b", Enable: &on, Author: "a"},
		{Node: "1999", Password: "$(id)", Enable: &on, Author: "a"},
		{Node: "1999", Password: "~x", Enable: &on, Author: "a"},
		{Node: "1999", Password: "p\nENABLE=1", Enable: &on, Author: "a"},
		{Node: "1999", Password: "", Enable: &on, Author: "a"},
		{Node: "1999", Password: "p", Author: "a"},
		{Node: "1999", Password: "p", Enable: &on, Author: "bad author"},
	}
	for i, r := range bad {
		if _, err := c.SavenodeSet(context.Background(), r); err == nil {
			t.Errorf("%d: expected a refusal: %+v", i, r)
		}
	}
	if e.read("savenode.conf") != before {
		t.Fatal("file changed by a refused request")
	}
}

func TestSavenodeSetRefusesSymlink(t *testing.T) {
	e, _, c := setupEnv(t)
	on := true
	p := filepath.Join(e.root, "savenode.conf")
	os.Remove(p)
	if err := os.Symlink("rpt.conf", p); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SavenodeSet(context.Background(), NodeSetupRequest{Node: "1998", Password: "p", Enable: &on, Author: "a"}); err == nil {
		t.Fatal("symlink must be refused")
	}
	if !strings.HasPrefix(e.read("rpt.conf"), "[general]") {
		t.Fatal("rpt.conf touched")
	}
}

func TestAllmonNodeSetSocket(t *testing.T) {
	e, f, c := setupEnv(t)
	ini := filepath.Join(e.o.AllmonDir, "allmon3.ini")
	res, err := c.AllmonNodeSet(context.Background(), NodeSetupRequest{OldNode: "1999", Node: "1997", AMIPass: "example-pass"})
	if err != nil || !res.Changed || !res.Restarted || f.called("systemctl try-restart allmon3") != 1 {
		t.Fatalf("%+v %v %v", res, err, f.calls)
	}
	b, _ := os.ReadFile(ini)
	want := "; allmon\n[1997]\nip=127.0.0.1\nuser=admin\npass=example-pass ; c\n\n[other]\nx=1\n"
	if string(b) != want {
		t.Fatalf("got %q", b)
	}
	if fi, _ := os.Stat(ini); fi.Mode().Perm() != fs.FileMode(0o640) {
		t.Fatalf("mode %v", fi.Mode())
	}
	// repeating is a no-op
	if res, err = c.AllmonNodeSet(context.Background(), NodeSetupRequest{OldNode: "1999", Node: "1997", AMIPass: "example-pass"}); err != nil || res.Changed {
		t.Fatalf("%+v %v", res, err)
	}
	// unknown node: refused, file untouched
	if _, err = c.AllmonNodeSet(context.Background(), NodeSetupRequest{OldNode: "1998", Node: "1996"}); err == nil {
		t.Fatal("expected refusal")
	}
	for _, r := range []NodeSetupRequest{{OldNode: "x", Node: "1997"}, {OldNode: "1997", Node: "1997", AMIPass: "a;b"}, {OldNode: "1997", Node: "1997", AMIPass: "a\nb"}} {
		if _, err = c.AllmonNodeSet(context.Background(), r); err == nil {
			t.Errorf("expected refusal: %+v", r)
		}
	}
	if b2, _ := os.ReadFile(ini); string(b2) != want {
		t.Fatal("file changed by refused request")
	}
	// missing file is skipped
	os.Remove(ini)
	if res, err = c.AllmonNodeSet(context.Background(), NodeSetupRequest{OldNode: "1997", Node: "1996"}); err != nil || res.Skipped != SetupSkipNoFile {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestNodeSetupOpsNeedSysField(t *testing.T) {
	_, _, c := setupEnv(t)
	var v NodeSetupResult
	if err := c.sys(context.Background(), OpSavenodeSet, nil, &v, 1<<20); err == nil {
		t.Fatal("request without fields must be refused")
	}
}
