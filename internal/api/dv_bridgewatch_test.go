package api

import (
	"context"
	"io"
	"log"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/dv/ctl"
	"github.com/artw1/aslcp/internal/dvpanel"
	"github.com/artw1/aslcp/internal/store"
)

// bwCtl is a control socket that remembers what it was asked and keeps a
// status like the engine does.
type bwCtl struct {
	mu     sync.Mutex
	st     ctl.Status
	calls  []string
	unlink []ctl.UnlinkArgs
	oldEng bool // refuses the Release field like an older engine
}

func (c *bwCtl) note(s string) { c.calls = append(c.calls, s) }
func (c *bwCtl) Status(context.Context) (ctl.Status, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.st, nil
}
func (c *bwCtl) Reload(context.Context) (ctl.Status, error) { return c.Status(context.Background()) }
func (c *bwCtl) Mode(context.Context, ctl.ModeArgs) (ctl.Status, error) {
	return c.Status(context.Background())
}
func (c *bwCtl) Tune(_ context.Context, a ctl.TuneArgs) (ctl.Status, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.note("tune " + a.Mode)
	t := a.Target
	c.st.Mode, c.st.Target = a.Mode, &t
	return c.st, nil
}
func (c *bwCtl) Unlink(_ context.Context, a ctl.UnlinkArgs) (ctl.Status, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if a.Release && c.oldEng {
		return ctl.Status{}, &ctl.Error{Code: ctl.CodeBadRequest, Msg: "bad args"}
	}
	c.unlink = append(c.unlink, a)
	c.note("unlink")
	c.st.Target = nil
	return c.st, nil
}
func (c *bwCtl) Subscribe(context.Context, func(ctl.Status), func(ctl.Event)) error { return nil }
func (c *bwCtl) Path() string                                                       { return "" }
func (c *bwCtl) count(s string) (n int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, x := range c.calls {
		if x == s {
			n++
		}
	}
	return
}

// bwResume is a resume tracker with a remembered tune per mode and a record
// of Forget.
type bwResume struct {
	mu      sync.Mutex
	tunes   map[string]string
	forgets []string
}

func (r *bwResume) Get() dvpanel.View {
	r.mu.Lock()
	defer r.mu.Unlock()
	v := dvpanel.View{}
	v.PerMode = map[string]dvpanel.Tune{}
	for m, t := range r.tunes {
		v.PerMode[m] = dvpanel.Tune{Tune: t}
	}
	return v
}
func (r *bwResume) SetResume(bool) error { return nil }
func (r *bwResume) Forget(m string) {
	r.mu.Lock()
	r.forgets = append(r.forgets, m)
	delete(r.tunes, m)
	r.mu.Unlock()
}
func (r *bwResume) RememberTune(m, t, _ string) {
	r.mu.Lock()
	r.tunes[m] = t
	r.mu.Unlock()
}

type bwRig struct {
	s  *Server
	c  *bwCtl
	r  *bwResume
	st *store.Store
	w  *dvBridgeWatch
	t0 time.Time
}

func newBWRig(t *testing.T, mode string, tg uint32, remembered string) *bwRig {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	c := &bwCtl{st: ctl.Status{Mode: mode, Started: time.Now(), Updated: time.Now()}}
	if tg != 0 {
		c.st.Target = &ctl.Target{TG: tg, Slot: 2}
	}
	res := &bwResume{tunes: map[string]string{}}
	if remembered != "" {
		res.tunes[mode] = remembered
	}
	s := &Server{d: Deps{Store: st, DVCtl: c, DVResume: res, DVEnabled: func() bool { return true }, Logger: log.New(io.Discard, "", 0)}}
	s.hub = newHub(func(int) {})
	s.dvs = newDVSnapper(s)
	s.dvs.setStatus(c.st)
	w := newDVBridgeWatch(s)
	return &bwRig{s: s, c: c, r: res, st: st, w: w, t0: time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)}
}

func (g *bwRig) step(sec int, linked, known bool) {
	g.w.step(context.Background(), g.t0.Add(time.Duration(sec)*time.Second), linked, known)
}

func (g *bwRig) audits(t *testing.T, prefix string) []store.Audit {
	t.Helper()
	l, err := g.st.ListAuditPage(store.AuditFilter{ActionPrefix: prefix})
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestBridgeReleaseAfterGrace(t *testing.T) {
	g := newBWRig(t, ctl.ModeBM, 91, "91")
	g.step(0, true, true)
	g.step(1, false, true) // lost
	g.step(5, false, true) // 4 s: still waiting
	if g.c.count("unlink") != 0 {
		t.Fatal("released inside the grace period")
	}
	g.step(6, false, true) // 5 s
	if g.c.count("unlink") != 1 || len(g.c.unlink) != 1 || !g.c.unlink[0].Release || g.c.unlink[0].Mode != ctl.ModeBM {
		t.Fatalf("%+v", g.c.unlink)
	}
	g.step(30, false, true) // once per loss
	if g.c.count("unlink") != 1 {
		t.Fatal("released twice")
	}
	if len(g.r.forgets) != 0 || g.r.tunes[ctl.ModeBM] != "91" {
		t.Fatalf("the remembered tune was touched: %v %v", g.r.forgets, g.r.tunes)
	}
	a := g.audits(t, "dv.bridge.released")
	if len(a) != 1 || a[0].Username != "system" || !strings.Contains(a[0].Detail, `"mode":"BM"`) || !strings.Contains(a[0].Detail, `"target":"91"`) {
		t.Fatalf("%+v", a)
	}
	if mode, _, tuned := g.s.dvTunedNow(); mode != ctl.ModeBM || tuned {
		t.Fatalf("mode kept and target cleared: %s %v", mode, tuned)
	}
}

func TestBridgeBlipInsideGraceDoesNothing(t *testing.T) {
	g := newBWRig(t, ctl.ModeBM, 91, "91")
	g.step(0, true, true)
	g.step(1, false, true)
	g.step(3, true, true) // an Asterisk restart brought the link back
	g.step(20, true, true)
	if len(g.c.calls) != 0 {
		t.Fatalf("%v", g.c.calls)
	}
	// a later real loss still counts from its own start
	g.step(21, false, true)
	g.step(24, false, true)
	if g.c.count("unlink") != 0 {
		t.Fatal("grace counted from the first blip")
	}
	g.step(26, false, true)
	if g.c.count("unlink") != 1 {
		t.Fatalf("%v", g.c.calls)
	}
}

func TestBridgeNeverLinkedNoAction(t *testing.T) {
	g := newBWRig(t, ctl.ModeBM, 91, "91")
	for i := 0; i < 20; i++ {
		g.step(i, false, true)
	}
	if len(g.c.calls) != 0 {
		t.Fatalf("%v", g.c.calls)
	}
	g.step(21, true, true) // linked for the first time: nothing to restore
	if len(g.c.calls) != 0 {
		t.Fatalf("%v", g.c.calls)
	}
}

func TestBridgeUnknownChangesNothing(t *testing.T) {
	g := newBWRig(t, ctl.ModeBM, 91, "91")
	g.step(0, true, true)
	g.step(1, false, false) // Asterisk down: unknown, not a loss
	g.step(30, false, false)
	if len(g.c.calls) != 0 {
		t.Fatalf("%v", g.c.calls)
	}
}

func TestBridgeNoTargetNoRelease(t *testing.T) {
	g := newBWRig(t, ctl.ModeBM, 0, "91")
	g.step(0, true, true)
	g.step(1, false, true)
	g.step(10, false, true)
	if len(g.c.calls) != 0 || len(g.audits(t, "dv.bridge")) != 0 {
		t.Fatalf("%v", g.c.calls)
	}
	// the echo test is not a target either
	g = newBWRig(t, ctl.ModeLoop, 9990, "")
	g.step(0, true, true)
	g.step(1, false, true)
	g.step(10, false, true)
	if len(g.c.calls) != 0 {
		t.Fatalf("%v", g.c.calls)
	}
}

func TestBridgeNoReleaseWhenDVDisabled(t *testing.T) {
	g := newBWRig(t, ctl.ModeBM, 91, "91")
	g.s.d.DVEnabled = func() bool { return false }
	g.step(0, true, true)
	g.step(1, false, true)
	g.step(10, false, true)
	if len(g.c.calls) != 0 {
		t.Fatalf("%v", g.c.calls)
	}
}

func TestBridgeOldEngineStillUnlinks(t *testing.T) {
	g := newBWRig(t, ctl.ModeBM, 91, "91")
	g.c.oldEng = true
	g.step(0, true, true)
	g.step(1, false, true)
	g.step(7, false, true)
	if len(g.c.unlink) != 1 || g.c.unlink[0].Release {
		t.Fatalf("%+v", g.c.unlink)
	}
}

func TestBridgeReleaseRemembersAnUnrememberedTune(t *testing.T) {
	g := newBWRig(t, ctl.ModeBM, 91, "")
	g.step(0, true, true)
	g.step(1, false, true)
	g.step(7, false, true)
	if g.r.tunes[ctl.ModeBM] != "91" {
		t.Fatalf("%v", g.r.tunes)
	}
}

func TestBridgeRestoreRetunesRemembered(t *testing.T) {
	g := newBWRig(t, ctl.ModeBM, 91, "91")
	g.step(0, true, true)
	g.step(1, false, true)
	g.step(7, false, true) // released
	g.step(8, true, true)  // linked again
	if g.c.count("tune BM") != 1 {
		t.Fatalf("%v", g.c.calls)
	}
	if tg := g.c.st.Target; tg == nil || tg.TG != 91 {
		t.Fatalf("%+v", g.c.st.Target)
	}
	a := g.audits(t, "dv.bridge.retuned")
	if len(a) != 1 || a[0].Username != "system" || !strings.Contains(a[0].Detail, `"tune":"91"`) {
		t.Fatalf("%+v", a)
	}
	g.step(9, true, true) // not again
	if g.c.count("tune BM") != 1 {
		t.Fatalf("%v", g.c.calls)
	}
}

func TestBridgeRestoreNeedsMemoryAndNoTarget(t *testing.T) {
	// the operator pressed Unlink: nothing is remembered
	g := newBWRig(t, ctl.ModeBM, 0, "")
	g.step(0, true, true)
	g.step(1, false, true)
	g.step(8, false, true)
	g.step(9, true, true)
	if len(g.c.calls) != 0 {
		t.Fatalf("%v", g.c.calls)
	}
	// somebody tuned meanwhile: the target stays
	g = newBWRig(t, ctl.ModeBM, 91, "91")
	g.step(0, true, true)
	g.step(1, false, true)
	g.step(8, false, true) // released
	g.c.st.Target = &ctl.Target{TG: 2001}
	g.s.dvs.setStatus(g.c.st)
	g.step(9, true, true)
	if g.c.count("tune BM") != 0 {
		t.Fatalf("%v", g.c.calls)
	}
}

func TestBridgeRestoreWaitsForTuneInFlight(t *testing.T) {
	g := newBWRig(t, ctl.ModeBM, 91, "91")
	g.step(0, true, true)
	g.step(1, false, true)
	g.step(8, false, true)
	g.s.dvTuning.Add(1)
	g.step(9, true, true)
	if g.c.count("tune BM") != 0 {
		t.Fatal("tuned while a tune was in flight")
	}
	g.s.dvTuning.Add(-1)
	g.step(10, true, true)
	if g.c.count("tune BM") != 1 {
		t.Fatalf("%v", g.c.calls)
	}
}
