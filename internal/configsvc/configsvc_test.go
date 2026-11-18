package configsvc

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/ami"
	"github.com/artw1/aslcp/internal/helper"
	"github.com/artw1/aslcp/internal/rpt"
)

func TestUnifiedDiff(t *testing.T) {
	if UnifiedDiff("a.conf", "x\n", "x\n", false) != "" {
		t.Fatal("equal texts must give empty diff")
	}
	d := UnifiedDiff("a.conf", "a\nb\nc\n", "a\nB\nc\n", false)
	for _, w := range []string{"--- a/a.conf", "+++ b/a.conf", "-b", "+B", " a", " c"} {
		if !strings.Contains(d, w+"\n") && !strings.Contains(d, w) {
			t.Fatalf("missing %q in\n%s", w, d)
		}
	}
	// no newline at end of file
	d = UnifiedDiff("a.conf", "a\nb", "a\nb\n", false)
	if !strings.Contains(d, "No newline at end of file") {
		t.Fatalf("no EOL marker:\n%s", d)
	}
	// two far-apart changes give two hunks
	var o, n []string
	for i := 0; i < 40; i++ {
		o = append(o, "line")
		n = append(n, "line")
	}
	n[2], n[35] = "X", "Y"
	d = UnifiedDiff("a.conf", strings.Join(o, "\n")+"\n", strings.Join(n, "\n")+"\n", false)
	if strings.Count(d, "\n@@ ")+strings.Count(d[:3], "@@") < 2 {
		t.Fatalf("want 2 hunks:\n%s", d)
	}
	// new file
	d = UnifiedDiff("custom/n.conf", "", "[a]\n", true)
	if !strings.HasPrefix(d, "--- /dev/null\n+++ b/custom/n.conf\n") || !strings.Contains(d, "+[a]") {
		t.Fatalf("new file:\n%s", d)
	}
}

func TestMask(t *testing.T) {
	cases := map[string]string{
		"secret = hunter2":                        "secret = " + Masked,
		"passwd=abc ; note":                       "passwd=" + Masked + " ; note",
		"md5secret = 0123":                        "md5secret = " + Masked,
		"register => 1998:pw@host.invalid":        "register => " + Masked + "@host.invalid",
		"register => 1998:p@ss@host.invalid:4569": "register => " + Masked + "@host.invalid:4569",
		"duplex = 1":                              "duplex = 1",
		"secretive = x":                           "secretive = x",
		"Password=example-pass":                   "Password=" + Masked,
		"RemotePassword = example-pass ; c":       "RemotePassword = " + Masked + " ; c",
		"dmr_password=example-pass":               "dmr_password=" + Masked,
		"  PasswordHint = x":                      "  PasswordHint = " + Masked,
		"passport = x":                            "passport = x",
	}
	for in, want := range cases {
		if got := MaskLine(in); got != want {
			t.Errorf("%q -> %q want %q", in, got, want)
		}
	}
	d := "--- a/m\n+++ b/m\n@@ -1 +1 @@\n-secret = old\n+secret = new\n"
	if m := MaskDiff(d); strings.Contains(m, "old") || strings.Contains(m, "new") || !strings.Contains(m, "+++ b/m") {
		t.Fatalf("%s", m)
	}
}

func TestPlanReload(t *testing.T) {
	steps, restart := PlanReload([]string{"rpt.conf", "custom/rpt/x.conf", "iax.conf"})
	if restart || len(steps) != 2 || steps[0].Command == steps[1].Command {
		t.Fatalf("%+v %v", steps, restart)
	}
	if _, r := PlanReload([]string{"unknown.conf"}); !r {
		t.Fatal("unknown file must need a restart")
	}
	if s, r := PlanReload(nil); len(s) != 0 || r {
		t.Fatal("nothing changed")
	}
}

type fakeAMI struct {
	up   bool
	cmds []string
	fail int // commands failing with an error before succeeding
}

func (f *fakeAMI) State() ami.State { return ami.State{Connected: f.up} }
func (f *fakeAMI) Command(_ context.Context, c string) ([]string, error) {
	f.cmds = append(f.cmds, c)
	if f.fail > 0 {
		f.fail--
		return nil, errors.New("not connected")
	}
	return []string{"ok"}, nil
}

type fakeNodes struct{ bad bool }

func (fakeNodes) LocalNodes(context.Context) ([]string, error) { return []string{"1999"}, nil }
func (n fakeNodes) XStat(context.Context, string) (*rpt.XStat, error) {
	if n.bad {
		return nil, errors.New("no answer")
	}
	return &rpt.XStat{}, nil
}

func newSvc(a *fakeAMI, n Nodes) *Service {
	s := New(NewDemoHelper(), a, n)
	s.HealthTimeout, s.HealthPoll = 200*time.Millisecond, 5*time.Millisecond
	return s
}

func TestApplyHealthAndReload(t *testing.T) {
	a := &fakeAMI{up: true, fail: 2}
	s := newSvc(a, fakeNodes{})
	c, _ := s.Read(context.Background(), "rpt.conf")
	files := []File{{Path: "rpt.conf", Content: c.Content + "; x\n", Base: c.SHA256}}
	res, err := s.Apply(context.Background(), "admin", files, "", true)
	if err != nil {
		t.Fatal(err)
	}
	// first reload fails (fail=2 consumes the reload and one health probe), health retries until OK
	if len(res.Reload) != 1 || res.Reload[0].OK || res.Health == nil || !res.Health.OK {
		t.Fatalf("%+v", res)
	}
	// no reload: nil health
	c, _ = s.Read(context.Background(), "rpt.conf")
	res, err = s.Apply(context.Background(), "admin", []File{{Path: "rpt.conf", Content: c.Content + "; y\n", Base: c.SHA256}}, "m", false)
	if err != nil || res.Health != nil || res.Reload != nil {
		t.Fatalf("%+v %v", res, err)
	}
	// unhealthy node
	bad := newSvc(&fakeAMI{up: true}, fakeNodes{bad: true})
	c, _ = bad.Read(context.Background(), "rpt.conf")
	res, _ = bad.Apply(context.Background(), "a", []File{{Path: "rpt.conf", Content: c.Content + "; z\n", Base: c.SHA256}}, "", true)
	if res.Health == nil || res.Health.OK {
		t.Fatalf("%+v", res.Health)
	}
}

func TestApplyErrors(t *testing.T) {
	s := newSvc(&fakeAMI{up: true}, fakeNodes{})
	ctx := context.Background()
	var se *SyntaxErrors
	if _, err := s.Apply(ctx, "a", []File{{Path: "rpt.conf", Content: "garbage\n"}}, "", false); !errors.As(err, &se) {
		t.Fatalf("%v", err)
	}
	var ie *InvalidError
	if _, err := s.Apply(ctx, "a", []File{{Path: "../x.conf", Content: ""}}, "", false); !errors.As(err, &ie) {
		t.Fatalf("%v", err)
	}
	if _, err := s.Apply(ctx, "a", nil, "", false); !errors.As(err, &ie) {
		t.Fatalf("%v", err)
	}
	var ce *helper.ConflictError
	if _, err := s.Apply(ctx, "a", []File{{Path: "rpt.conf", Content: "[a]\n", Base: "deadbeef"}}, "", false); !errors.As(err, &ce) {
		t.Fatalf("%v", err)
	}
	var ne *NotFoundError
	if _, err := s.Show(ctx, "0000000", false); !errors.As(err, &ne) {
		t.Fatalf("%v", err)
	}
}

func TestPreviewNewFile(t *testing.T) {
	s := newSvc(&fakeAMI{up: true}, fakeNodes{})
	p, err := s.Preview(context.Background(), []File{
		{Path: "custom/rpt/new.conf", Content: "[a]\nb = 1\n"},
		{Path: "evil.conf", Content: "[a]\n"},
		{Path: "iax.conf", Content: "[general\n"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Files[0].OK || !p.Files[0].Changed || p.Files[1].OK || p.Files[2].OK || p.Files[2].Error.Line != 1 {
		t.Fatalf("%+v", p.Files)
	}
}
