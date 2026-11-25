package api

import (
	"context"
	"io"
	"log"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/dv/ctl"
	"github.com/artw1/aslcp/internal/dvpanel"
	"github.com/artw1/aslcp/internal/store"
)

func TestBridgeLinkedElsewhere(t *testing.T) {
	locals := []string{"1997", "1998", "2001"}
	linked := func(set ...string) func(string) bool {
		return func(n string) bool {
			for _, x := range set {
				if x == n {
					return true
				}
			}
			return false
		}
	}
	cases := []struct {
		name         string
		set          []string
		node, target string
		want         bool
	}{
		{"only the dropped pair", []string{"1997"}, "1997", "2001", false},
		{"the same pair seen stale from the bridge side", []string{"1997"}, "2001", "1997", false},
		{"another local node is linked", []string{"1997", "1998"}, "1997", "2001", true},
		{"nothing linked", nil, "1997", "2001", false},
		{"the bridge dropped everything", []string{"1997", "1998"}, "2001", "", false},
		{"disconnect all of a local node", []string{"1997", "1998"}, "1997", "", true},
	}
	for _, c := range cases {
		if got := bridgeLinkedElsewhere(locals, linked(c.set...), "2001", c.node, c.target); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
}

// stubCtl is the control socket of the internal tests: it only answers
// Unlink and Tune, and refuses the Release field like an older engine when
// old is set.
type stubCtl struct {
	DVCtl
	mu     sync.Mutex
	old    bool
	unlink []ctl.UnlinkArgs
	tunes  []ctl.TuneArgs
}

func (c *stubCtl) Unlink(_ context.Context, a ctl.UnlinkArgs) (ctl.Status, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if a.Release && c.old {
		return ctl.Status{}, &ctl.Error{Code: ctl.CodeBadRequest, Msg: "bad args"}
	}
	c.unlink = append(c.unlink, a)
	return ctl.Status{Mode: a.Mode}, nil
}

func (c *stubCtl) Tune(_ context.Context, a ctl.TuneArgs) (ctl.Status, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tunes = append(c.tunes, a)
	t := a.Target
	return ctl.Status{Mode: a.Mode, Target: &t}, nil
}

type stubResume struct {
	mu      sync.Mutex
	tunes   map[string]string
	forgets []string
}

func (r *stubResume) Get() dvpanel.View {
	r.mu.Lock()
	defer r.mu.Unlock()
	v := dvpanel.View{PerMode: map[string]dvpanel.Tune{}}
	for m, t := range r.tunes {
		v.PerMode[m] = dvpanel.Tune{Tune: t}
	}
	return v
}
func (r *stubResume) SetResume(bool) error { return nil }
func (r *stubResume) Forget(m string) {
	r.mu.Lock()
	r.forgets = append(r.forgets, m)
	delete(r.tunes, m)
	r.mu.Unlock()
}
func (r *stubResume) RememberTune(m, t, _ string) {
	r.mu.Lock()
	r.tunes[m] = t
	r.mu.Unlock()
}

func newStubServer(t *testing.T, c *stubCtl, r *stubResume, tg uint32) *Server {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s := &Server{d: Deps{Store: st, DVCtl: c, DVResume: r, DVEnabled: func() bool { return true }, Logger: log.New(io.Discard, "", 0),
		DVBridgeNode: func(context.Context) (string, bool) { return "2001", true }}}
	s.hub = newHub(func(int) {})
	s.dvs = newDVSnapper(s)
	stt := ctl.Status{Mode: ctl.ModeBM, Started: time.Now(), Updated: time.Now()}
	if tg != 0 {
		stt.Target = &ctl.Target{TG: tg, Slot: 2}
	}
	s.dvs.setStatus(stt)
	return s
}

func TestBridgeReleaseOldEngineStillUnlinks(t *testing.T) {
	c := &stubCtl{old: true}
	r := &stubResume{tunes: map[string]string{ctl.ModeBM: "91"}}
	s := newStubServer(t, c, r, 91)
	s.dvLinkDropped(context.Background(), dvSystemUser, "1997", "2001")
	if len(c.unlink) != 1 || c.unlink[0].Release || c.unlink[0].Mode != ctl.ModeBM {
		t.Fatalf("%+v", c.unlink)
	}
	if len(r.forgets) != 0 || r.tunes[ctl.ModeBM] != "91" {
		t.Fatalf("the remembered tune was touched: %v %v", r.forgets, r.tunes)
	}
}

func TestBridgeReleaseNeverForgets(t *testing.T) {
	c := &stubCtl{}
	r := &stubResume{tunes: map[string]string{}}
	s := newStubServer(t, c, r, 91)
	s.dvLinkDropped(context.Background(), dvScheduleUser, "1997", "2001")
	if len(c.unlink) != 1 || !c.unlink[0].Release || len(r.forgets) != 0 || r.tunes[ctl.ModeBM] != "91" {
		t.Fatalf("%+v %v %v", c.unlink, r.forgets, r.tunes)
	}
	// the bridge node as the local node dropping its link counts too
	c.unlink = nil
	s.dvs.setStatus(ctl.Status{Mode: ctl.ModeBM, Target: &ctl.Target{TG: 91, Slot: 2}})
	s.dvLinkDropped(context.Background(), dvSystemUser, "2001", "1997")
	if len(c.unlink) != 1 {
		t.Fatalf("%+v", c.unlink)
	}
	// a link that is not the bridge node's
	c.unlink = nil
	s.dvs.setStatus(ctl.Status{Mode: ctl.ModeBM, Target: &ctl.Target{TG: 91, Slot: 2}})
	s.dvLinkDropped(context.Background(), dvSystemUser, "1997", "50241")
	if len(c.unlink) != 0 {
		t.Fatalf("%+v", c.unlink)
	}
}

func TestBridgeRestoreRules(t *testing.T) {
	ctx := context.Background()
	c := &stubCtl{}
	r := &stubResume{tunes: map[string]string{ctl.ModeBM: "91"}}
	s := newStubServer(t, c, r, 0)
	s.dvTuning.Add(1) // a tune in flight (a DV pin): leave it alone
	s.dvBridgeRestore(ctx, dvSystemUser)
	if len(c.tunes) != 0 {
		t.Fatal("tuned while a tune was in flight")
	}
	s.dvTuning.Add(-1)
	s.dvBridgeRestore(ctx, dvSystemUser)
	if len(c.tunes) != 1 || c.tunes[0].Target.TG != 91 {
		t.Fatalf("%+v", c.tunes)
	}
	// a target is set now: not again
	s.dvBridgeRestore(ctx, dvSystemUser)
	if len(c.tunes) != 1 {
		t.Fatalf("%+v", c.tunes)
	}
}
