package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/dv/ctl"
	"github.com/artw1/aslcp/internal/dv/ctl/ctltest"
	"github.com/artw1/aslcp/internal/dvpanel"
	"github.com/artw1/aslcp/internal/store"
)

// bridgeNode is the bridge node of these tests (the local node is 1997).
const bridgeNode = "1999"

type bridgeRig struct {
	e  *env
	fc *ctltest.Fake
	tr *dvpanel.Tracker
}

// newBridgeRig is an env with digital modes on, the bridge node 1999, the
// engine in mode BM with TG tg tuned (0: no target) and remembered as its tune
// ("" for none). The local node starts linked to the bridge node only.
func newBridgeRig(t *testing.T, tg uint32, remembered string, mut func(*opts)) *bridgeRig {
	t.Helper()
	return newBridgeRigMode(t, ctl.ModeBM, tg, remembered, mut)
}

func newBridgeRigMode(t *testing.T, mode string, tg uint32, remembered string, mut func(*opts)) *bridgeRig {
	t.Helper()
	fc := ctltest.New(t)
	setEngine := func(tg uint32) {
		st := ctl.Status{Version: "2.0.0-test", Started: time.Now().Add(-90 * time.Second), Mode: mode, Networks: dvNets(ctl.ModeLoop, ctl.ModeBM),
			Vocoder: ctl.VocoderStatus{OK: true}}
		if tg != 0 {
			st.Target = &ctl.Target{TG: tg, Slot: 2}
		}
		fc.SetStatus(st)
	}
	setEngine(tg)
	tr := &dvpanel.Tracker{Store: &memSettings{}}
	if remembered != "" {
		tr.RememberTune(ctl.ModeBM, remembered, "")
	}
	o := opts{dvengine: &fakeEngine{}, dvctl: ctl.NewClient(fc.Path()), dvresume: tr, dvEnabled: func() bool { return true },
		dvNode: func(context.Context) (string, bool) { return bridgeNode, true }}
	if mut != nil {
		mut(&o)
	}
	e := newEnv(t, o)
	e.srv.SetDVRetuneDelay(0)
	waitDV(t, e, func(s dvSnap) bool {
		if tg == 0 || mode != ctl.ModeBM {
			return s.Running && s.Bridge.Mode == mode
		}
		return s.Running && s.Bridge.Tune != ""
	})
	e.setLinks(bridgeNode)
	return &bridgeRig{e: e, fc: fc, tr: tr}
}

func (g *bridgeRig) ops(op string) (out []ctl.Request) {
	for _, q := range g.fc.Requests() {
		if q.Op == op {
			out = append(out, q)
		}
	}
	return
}

// waitOps waits until n requests of op were seen.
func (g *bridgeRig) waitOps(op string, n int) []ctl.Request {
	g.e.t.Helper()
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		if l := g.ops(op); len(l) >= n {
			return l
		}
	}
	g.e.t.Fatalf("no %s request #%d: %v", op, n, g.fc.Requests())
	return nil
}

// quiet checks, a little later, that op was not requested.
func (g *bridgeRig) never(op string) {
	g.e.t.Helper()
	time.Sleep(150 * time.Millisecond)
	if l := g.ops(op); len(l) != 0 {
		g.e.t.Fatalf("unexpected %s: %v", op, l)
	}
}

func (g *bridgeRig) audits(prefix string) []store.Audit {
	g.e.t.Helper()
	l, err := g.e.st.ListAuditPage(store.AuditFilter{ActionPrefix: prefix})
	if err != nil {
		g.e.t.Fatal(err)
	}
	return l
}

func (g *bridgeRig) disconnect(target string) {
	g.e.t.Helper()
	if c := g.e.do("op", "POST", "/api/nodes/"+localNode+"/disconnect", map[string]any{"target": target}, nil); c != 202 {
		g.e.t.Fatalf("disconnect %s: %d", target, c)
	}
}

func (g *bridgeRig) connect(body map[string]any) {
	g.e.t.Helper()
	if c := g.e.do("op", "POST", "/api/nodes/"+localNode+"/connect", body, nil); c != 202 {
		g.e.t.Fatalf("connect %v: %d", body, c)
	}
}

func (g *bridgeRig) checkReleased(actor string) {
	g.e.t.Helper()
	u := g.waitOps(ctl.OpUnlink, 1)
	if len(u) != 1 {
		g.e.t.Fatalf("unlinks: %v", u)
	}
	var a ctl.UnlinkArgs
	if err := json.Unmarshal(u[0].Args, &a); err != nil || !a.Release || a.Mode != ctl.ModeBM {
		g.e.t.Fatalf("unlink args %s (%v)", u[0].Args, err)
	}
	au := g.audits("dv.bridge.released")
	if len(au) != 1 || au[0].Username != actor || !strings.Contains(au[0].Detail, `"mode":"BM"`) || !strings.Contains(au[0].Detail, `"target":"91"`) {
		g.e.t.Fatalf("audit: %+v", au)
	}
	// the tune is remembered, never forgotten
	if v := g.tr.Get(); v.PerMode[ctl.ModeBM].Tune != "91" {
		g.e.t.Fatalf("remembered tune lost: %+v", v)
	}
}

func TestBridgeReleasedOnNodeDisconnect(t *testing.T) {
	g := newBridgeRig(t, 91, "91", nil)
	g.disconnect(bridgeNode)
	g.checkReleased("op")
}

func TestBridgeReleaseRemembersAnUnrememberedTune(t *testing.T) {
	g := newBridgeRig(t, 91, "", nil)
	g.disconnect(bridgeNode)
	g.checkReleased("op")
}

func TestBridgeOtherLinkDropKeepsTarget(t *testing.T) {
	g := newBridgeRig(t, 91, "91", nil)
	g.e.setLinks(bridgeNode, "50241")
	g.disconnect("50241")
	g.never(ctl.OpUnlink)
}

func TestBridgeNoTargetNoRelease(t *testing.T) {
	g := newBridgeRig(t, 0, "91", nil)
	g.disconnect(bridgeNode)
	g.never(ctl.OpUnlink)
	if len(g.audits("dv.bridge")) != 0 {
		t.Fatal("audited")
	}
}

func TestBridgeEchoTestIsNoTarget(t *testing.T) {
	g := newBridgeRigMode(t, ctl.ModeLoop, 9990, "", nil)
	g.disconnect(bridgeNode)
	g.never(ctl.OpUnlink)
}

func TestBridgeNoReleaseWhenDVDisabled(t *testing.T) {
	g := newBridgeRigDisabled(t)
	g.disconnect(bridgeNode)
	g.never(ctl.OpUnlink)
}

// newBridgeRigDisabled is a rig whose engine switch is off.
func newBridgeRigDisabled(t *testing.T) *bridgeRig {
	t.Helper()
	fc := ctltest.New(t)
	fc.SetStatus(ctl.Status{Version: "2.0.0-test", Started: time.Now(), Mode: ctl.ModeBM, Target: &ctl.Target{TG: 91, Slot: 2},
		Networks: dvNets(ctl.ModeLoop, ctl.ModeBM), Vocoder: ctl.VocoderStatus{OK: true}})
	tr := &dvpanel.Tracker{Store: &memSettings{}}
	e := newEnv(t, opts{dvctl: ctl.NewClient(fc.Path()), dvresume: tr, dvEnabled: func() bool { return false },
		dvNode: func(context.Context) (string, bool) { return bridgeNode, true }})
	e.setLinks(bridgeNode)
	return &bridgeRig{e: e, fc: fc, tr: tr}
}

func TestBridgeExclusiveConnectDropsBridge(t *testing.T) {
	g := newBridgeRig(t, 91, "91", nil)
	g.e.setLinks(bridgeNode, "50241")
	g.connect(map[string]any{"target": "2000", "exclusive": true})
	g.checkReleased("op")
}

func TestBridgeExclusiveConnectKeepsBridgeAsTarget(t *testing.T) {
	g := newBridgeRig(t, 91, "91", nil)
	g.e.setLinks(bridgeNode, "50241")
	g.connect(map[string]any{"target": bridgeNode, "exclusive": true})
	g.never(ctl.OpUnlink)
}

func TestBridgeDisconnectAllReleases(t *testing.T) {
	g := newBridgeRig(t, 91, "91", nil)
	g.e.setLinks(bridgeNode, "50241")
	if c := g.e.do("op", "POST", "/api/nodes/"+localNode+"/disconnect-all", nil, nil); c != 202 {
		t.Fatal(c)
	}
	g.checkReleased("op")
}

func TestBridgeDisconnectAllWithoutBridgeLink(t *testing.T) {
	g := newBridgeRig(t, 91, "91", nil)
	g.e.setLinks("50241")
	if c := g.e.do("op", "POST", "/api/nodes/"+localNode+"/disconnect-all", nil, nil); c != 202 {
		t.Fatal(c)
	}
	g.never(ctl.OpUnlink)
}

func TestBridgeScheduleDisconnectReleases(t *testing.T) {
	g := newBridgeRig(t, 91, "91", func(o *opts) { o.schedules = true })
	e := g.e
	body := schedBody(map[string]any{"action": "disconnect", "target": bridgeNode, "end": "", "start": "19:00"})
	if c := e.do("op", "POST", "/api/schedules", body, nil); c != 201 {
		t.Fatal(c)
	}
	ny, _ := time.LoadLocation("America/New_York")
	e.clk.set(time.Date(2026, 11, 17, 19, 0, 5, 0, ny))
	e.eng.RunOnce(context.Background())
	g.checkReleased("schedule")
}

func TestBridgeRetunedOnBridgeConnect(t *testing.T) {
	g := newBridgeRig(t, 0, "91", nil)
	g.e.setLinks("50241")
	g.connect(map[string]any{"target": bridgeNode})
	tu := g.waitOps(ctl.OpTune, 1)
	var a ctl.TuneArgs
	if err := json.Unmarshal(tu[0].Args, &a); err != nil || a.Mode != ctl.ModeBM || a.Target.TG != 91 {
		t.Fatalf("%s %v", tu[0].Args, err)
	}
	for end := time.Now().Add(3 * time.Second); len(g.audits("dv.bridge.retuned")) == 0 && time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
	}
	au := g.audits("dv.bridge.retuned")
	if len(au) != 1 || au[0].Username != "op" || !strings.Contains(au[0].Detail, `"tune":"91"`) {
		t.Fatalf("%+v", au)
	}
	if len(g.ops(ctl.OpUnlink)) != 0 {
		t.Fatal("unlinked")
	}
}

func TestBridgeNoRetuneWithoutMemoryOrWithTarget(t *testing.T) {
	// the operator pressed Unlink: nothing is remembered
	g := newBridgeRig(t, 0, "", nil)
	g.e.setLinks("50241")
	g.connect(map[string]any{"target": bridgeNode})
	g.never(ctl.OpTune)
	// something is tuned already
	g = newBridgeRig(t, 2001, "91", nil)
	g.e.setLinks("50241")
	g.connect(map[string]any{"target": bridgeNode})
	g.never(ctl.OpTune)
	// a link that is not the bridge node
	g = newBridgeRig(t, 0, "91", nil)
	g.connect(map[string]any{"target": "2000"})
	g.never(ctl.OpTune)
}
