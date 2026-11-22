package api_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/configsvc"
	"github.com/artw1/aslcp/internal/dv/ctl"
	"github.com/artw1/aslcp/internal/dv/ctl/ctltest"
	"github.com/artw1/aslcp/internal/helper"
	"github.com/artw1/aslcp/internal/hostlists"
	"github.com/artw1/aslcp/internal/rpt"
)

// fakeEngine is the helper's digital modes part.
type fakeEngine struct {
	mu      sync.Mutex
	status  helper.DVStatus
	job     helper.DVJob
	enableE error
	rec     *helper.DVPrivateRecord
	calls   []string
	ident   helper.DVIdentityView
	nets    map[string]helper.DVNetworkView
	pw      map[string]string
	netErr  error
}

func (f *fakeEngine) note(s string) { f.mu.Lock(); f.calls = append(f.calls, s); f.mu.Unlock() }
func (f *fakeEngine) DVStatus(context.Context) (helper.DVStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status, nil
}
func (f *fakeEngine) DVJob(context.Context) (helper.DVJob, error) { return f.job, nil }
func (f *fakeEngine) DVEnable(context.Context) (helper.DVStatus, error) {
	f.note("enable")
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.enableE != nil {
		return helper.DVStatus{}, f.enableE
	}
	f.status.Job = "running"
	return f.status, nil
}
func (f *fakeEngine) DVDisable(context.Context) (helper.DVStatus, error) {
	f.note("disable")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status.Enabled = false
	return f.status, nil
}
func (f *fakeEngine) DVConfig(context.Context) (helper.DVConfigResult, error) {
	f.note("config")
	return helper.DVConfigResult{}, nil
}
func (f *fakeEngine) DVRestart(context.Context) (helper.DVStatus, error) {
	f.note("restart")
	return f.status, nil
}
func (f *fakeEngine) DVRestoreDiscard(context.Context) (helper.DVStatus, error) {
	f.note("discard")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status.RestorePending = helper.DVRestorePending{}
	return f.status, nil
}
func (f *fakeEngine) DVPrivateGet(context.Context) (helper.DVPrivateReply, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return helper.DVPrivateReply{Record: f.rec}, nil
}
func (f *fakeEngine) DVPrivateSet(_ context.Context, r helper.DVPrivateRecord) (helper.DVPrivateReply, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rec = &r
	return helper.DVPrivateReply{Record: f.rec}, nil
}
func (f *fakeEngine) DVPrivateClear(context.Context) (helper.DVPrivateReply, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rec = nil
	return helper.DVPrivateReply{}, nil
}

func dvNets(avail ...string) []ctl.NetStatus {
	var out []ctl.NetStatus
	for _, m := range []string{ctl.ModeLoop, ctl.ModeBM, ctl.ModeTGIF, ctl.ModeDMR, ctl.ModeYSF, ctl.ModeP25, ctl.ModeNXDN} {
		n := ctl.NetStatus{Mode: m, Enabled: true}
		for _, a := range avail {
			if a == m {
				n.Available = true
			}
		}
		out = append(out, n)
	}
	return out
}

// dvEnv is an env with digital modes enabled, a fake engine, a fake control
// socket and a host list manager loaded from synthetic fixtures.
func dvEnv(t *testing.T, avail ...string) (*env, *fakeEngine, *ctltest.Fake) {
	t.Helper()
	fe := &fakeEngine{status: helper.DVStatus{Supported: true, Enabled: true, MD380Installed: true, Active: true, MD380Active: true}}
	fc := ctltest.New(t)
	fc.SetStatus(ctl.Status{Version: "2.0.0-test", Started: time.Now().Add(-90 * time.Second), Mode: ctl.ModeLoop, Networks: dvNets(avail...),
		Vocoder: ctl.VocoderStatus{OK: true}})
	dir := t.TempDir()
	for _, f := range []string{"DMR_Hosts.txt", "YSF_Hosts.txt", "P25_Hosts.txt", "NXDN_Hosts.txt"} {
		b, err := os.ReadFile(filepath.Join("..", "hostlists", "testdata", f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	hl := hostlists.New(hostlists.Options{Dir: dir, Enabled: func() bool { return true }})
	hl.Load()
	e := newEnv(t, opts{dvengine: fe, dvctl: ctl.NewClient(fc.Path()), hostlists: hl, dvEnabled: func() bool { return true }})
	return e, fe, fc
}

type dvSnap struct {
	Installed, Running, Configured bool
	Bridge                         struct {
		Mode, Tune, Source string
		Slot               int
		Private            bool
	}
	Modes []struct {
		ID                 string
		Enabled, Available bool
		Unlink, Label      string
	}
	Services []struct {
		Unit   string
		Active bool
	}
	Vocoder *struct{ OK bool }
	Engine  *struct {
		Version string
		Uptime  int64
	}
	Caller *struct{ Call string }
	Heard  []struct{ Call, DMRID string }
}

func (e *env) dvSnapshot(role string) dvSnap {
	e.t.Helper()
	var sn dvSnap
	if c := e.do(role, "GET", "/api/dv/snapshot", nil, &sn); c != 200 {
		e.t.Fatalf("snapshot: %d", c)
	}
	return sn
}

func waitDV(t *testing.T, e *env, f func(dvSnap) bool) dvSnap {
	t.Helper()
	var sn dvSnap
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if sn = e.dvSnapshot("viewer"); f(sn) {
			return sn
		}
	}
	t.Fatalf("condition not met, last snapshot %+v", sn)
	return sn
}

func TestDVSnapshotDisabled(t *testing.T) {
	e := newEnv(t, opts{})
	sn := e.dvSnapshot("viewer")
	if sn.Installed || sn.Running || len(sn.Modes) != 0 {
		t.Fatalf("%+v", sn)
	}
}

func TestDVSnapshotLive(t *testing.T) {
	e, _, _ := dvEnv(t, ctl.ModeLoop)
	sn := waitDV(t, e, func(s dvSnap) bool { return s.Running })
	if !sn.Installed || sn.Bridge.Source != "aslcp-dv" || sn.Bridge.Mode != "LOOP" {
		t.Fatalf("%+v", sn)
	}
	if sn.Engine == nil || sn.Engine.Version != "2.0.0-test" || sn.Engine.Uptime < 80 || sn.Vocoder == nil || !sn.Vocoder.OK {
		t.Fatalf("%+v", sn)
	}
	avail := map[string]bool{}
	for _, m := range sn.Modes {
		avail[m.ID] = m.Available
		if m.ID == "BM" && m.Label != "BrandMeister" {
			t.Fatalf("label %q", m.Label)
		}
	}
	if !avail["LOOP"] || avail["BM"] || avail["YSF"] || len(sn.Modes) != 7 {
		t.Fatalf("modes %+v", sn.Modes)
	}
	if len(sn.Services) != 2 || sn.Services[0].Unit != "aslcp-dv" || !sn.Services[0].Active || sn.Services[1].Unit != "md380-emu" || !sn.Services[1].Active {
		t.Fatalf("services %+v", sn.Services)
	}
}

func TestDVCallers(t *testing.T) {
	e, _, fc := dvEnv(t, ctl.ModeLoop)
	waitDV(t, e, func(s dvSnap) bool { return s.Running })
	start := time.Now().Add(-5 * time.Second)
	talk := ctl.Talk{ID: 1, Dir: "rx", Mode: ctl.ModeLoop, Call: "N0CALL", DMRID: 1234567, TG: 9990, Slot: 2, Start: start}
	fc.Emit(ctl.Event{Type: ctl.EvTalkStart, At: start, Talk: &talk})
	waitDV(t, e, func(s dvSnap) bool { return s.Caller != nil && s.Caller.Call == "N0CALL" })
	talk.End = time.Now()
	fc.Emit(ctl.Event{Type: ctl.EvTalkEnd, At: talk.End, Talk: &talk})
	sn := waitDV(t, e, func(s dvSnap) bool { return s.Caller == nil && len(s.Heard) == 1 })
	if sn.Heard[0].Call != "N0CALL" || sn.Heard[0].DMRID != "1234567" {
		t.Fatalf("%+v", sn.Heard)
	}
}

func TestDVTuneLoop(t *testing.T) {
	e, _, fc := dvEnv(t, ctl.ModeLoop)
	waitDV(t, e, func(s dvSnap) bool { return s.Running })
	var r struct {
		Mode     string
		Switched bool
		Snapshot dvSnap
	}
	if c := e.do("op", "POST", "/api/dv/tune", map[string]string{"mode": "LOOP", "tune": "9990"}, &r); c != 200 || r.Mode != "LOOP" || !r.Snapshot.Running {
		t.Fatalf("%d %+v", c, r)
	}
	var found bool
	for _, q := range fc.Requests() {
		if q.Op != ctl.OpTune {
			continue
		}
		var a ctl.TuneArgs
		if err := json.Unmarshal(q.Args, &a); err != nil {
			t.Fatal(err)
		}
		found = a.Mode == "LOOP" && a.Target.TG == 9990 && a.Target.Private
	}
	if !found {
		t.Fatalf("no tune request: %+v", fc.Requests())
	}
	// the unlink value is an unlink
	if c := e.do("op", "POST", "/api/dv/tune", map[string]string{"mode": "LOOP", "tune": "disconnect"}, nil); c != 200 {
		t.Fatalf("unlink %d", c)
	}
	// a viewer may not tune
	expectErr(t, e, "viewer", "POST", "/api/dv/tune", map[string]string{"mode": "LOOP", "tune": "9990"}, 403, "forbidden")
}

func TestDVNotAvailable(t *testing.T) {
	e, _, _ := dvEnv(t, ctl.ModeLoop)
	waitDV(t, e, func(s dvSnap) bool { return s.Running })
	expectErr(t, e, "op", "POST", "/api/dv/mode", map[string]string{"mode": "BM"}, 409, "not_available")
	expectErr(t, e, "op", "POST", "/api/dv/tune", map[string]string{"mode": "BM", "tune": "91"}, 409, "not_available")
	expectErr(t, e, "op", "POST", "/api/dv/mode", map[string]string{"mode": "NOPE"}, 400, "bad_request")
}

func TestDVTuneYSFResolvesFromList(t *testing.T) {
	e, _, fc := dvEnv(t, ctl.ModeLoop, ctl.ModeYSF)
	waitDV(t, e, func(s dvSnap) bool { return s.Running })
	expectErr(t, e, "op", "POST", "/api/dv/tune", map[string]string{"mode": "YSF", "tune": "99999"}, 422, "unknown_target")
	if c := e.do("op", "POST", "/api/dv/tune", map[string]string{"mode": "YSF", "tune": "00001"}, nil); c != 200 {
		t.Fatalf("%d", c)
	}
	last := fc.Requests()[len(fc.Requests())-1]
	var a ctl.TuneArgs
	_ = json.Unmarshal(last.Args, &a)
	if last.Op != ctl.OpTune || a.Target.Host != "192.0.2.20" || a.Target.Port != 42000 {
		t.Fatalf("%+v %+v", last, a)
	}
}

func TestDVMode(t *testing.T) {
	e, _, fc := dvEnv(t, ctl.ModeLoop)
	waitDV(t, e, func(s dvSnap) bool { return s.Running })
	var r struct {
		Mode      string
		Confirmed bool
	}
	if c := e.do("op", "POST", "/api/dv/mode", map[string]string{"mode": "LOOP"}, &r); c != 200 || r.Mode != "LOOP" || !r.Confirmed {
		t.Fatalf("%d %+v", c, r)
	}
	if rq := fc.Requests(); rq[len(rq)-1].Op != ctl.OpMode {
		t.Fatalf("%+v", rq)
	}
}

func TestDVEngineDown(t *testing.T) {
	e, _, fc := dvEnv(t, ctl.ModeLoop)
	waitDV(t, e, func(s dvSnap) bool { return s.Running })
	fc.Close()
	waitDV(t, e, func(s dvSnap) bool { return !s.Running })
	expectErr(t, e, "op", "POST", "/api/dv/mode", map[string]string{"mode": "LOOP"}, 503, "engine_down")
}

func TestDVStatusEnableDisable(t *testing.T) {
	e, fe, _ := dvEnv(t, ctl.ModeLoop)
	var st map[string]any
	if c := e.do("admin", "GET", "/api/dv/status", nil, &st); c != 200 || st["supported"] != true || st["enabled"] != true || st["restorePending"] != false {
		t.Fatalf("%d %v", c, st)
	}
	if _, bad := st["legacyUnits"]; bad {
		t.Fatalf("%v", st)
	}
	expectErr(t, e, "op", "GET", "/api/dv/status", nil, 403, "forbidden")
	if c := e.do("admin", "POST", "/api/dv/enable", nil, &st); c != 202 || st["job"] != "running" {
		t.Fatalf("%d %v", c, st)
	}
	fe.mu.Lock()
	fe.enableE = helper.ErrBusy
	fe.mu.Unlock()
	expectErr(t, e, "admin", "POST", "/api/dv/enable", nil, 409, "busy")
	fe.mu.Lock()
	fe.enableE = helper.ErrDVUnsupported
	fe.mu.Unlock()
	expectErr(t, e, "admin", "POST", "/api/dv/enable", nil, 409, "not_supported")
	expectErr(t, e, "op", "POST", "/api/dv/disable", nil, 403, "forbidden")
	if c := e.do("admin", "POST", "/api/dv/disable", nil, &st); c != 202 || st["enabled"] != false {
		t.Fatalf("%d %v", c, st)
	}
}

func TestDVJob(t *testing.T) {
	e, fe, _ := dvEnv(t, ctl.ModeLoop)
	fe.job = helper.DVJob{Job: "running", Log: "installing md380-emu", At: 1}
	var j map[string]any
	if c := e.do("admin", "GET", "/api/dv/job", nil, &j); c != 200 || j["job"] != "running" || !strings.Contains(j["log"].(string), "md380-emu") {
		t.Fatalf("%d %v", c, j)
	}
}

func TestDVRestorePending(t *testing.T) {
	e, fe, _ := dvEnv(t, ctl.ModeLoop)
	fe.status.RestorePending = helper.DVRestorePending{Pending: true}
	var st map[string]any
	e.do("admin", "GET", "/api/dv/status", nil, &st)
	if st["restorePending"] != true {
		t.Fatalf("%v", st)
	}
	if c := e.do("admin", "DELETE", "/api/dv/restore-pending", nil, &st); c != 200 || st["restorePending"] != false {
		t.Fatalf("%d %v", c, st)
	}
}

func TestDVRemovedEndpoints(t *testing.T) {
	e, _, _ := dvEnv(t, ctl.ModeLoop)
	for _, r := range [][2]string{{"POST", "install"}, {"POST", "uninstall"}, {"PUT", "credentials"}, {"PUT", "modes"},
		{"PUT", "brandmeister"}, {"PUT", "brandmeister/enabled"}, {"POST", "stfu/install"}, {"GET", "dmr-status"}, {"POST", "restart"}} {
		if c := e.do("admin", r[0], "/api/dv/"+r[1], map[string]string{}, nil); c != 404 && c != 405 {
			t.Errorf("%s %s: %d", r[0], r[1], c)
		}
	}
}

func TestDVLists(t *testing.T) {
	e, _, _ := dvEnv(t, ctl.ModeLoop)
	var l struct {
		Mode  string
		Items []struct {
			Value, Name, Address string
			Port                 int
		}
		ETag string
	}
	code, _, h := e.raw("GET", "/api/dv/lists?mode=ysf", nil, e.ck["op"], nil)
	if code != 200 {
		t.Fatalf("%d", code)
	}
	if c := e.do("op", "GET", "/api/dv/lists?mode=ysf", nil, &l); c != 200 || l.Mode != "ysf" || len(l.Items) != 2 || l.Items[0].Name != "Test Room" || l.Items[0].Port != 42000 {
		t.Fatalf("%d %+v", c, l)
	}
	tag := h.Get("ETag")
	if tag == "" {
		t.Fatal("no ETag")
	}
	if c, _, _ := e.raw("GET", "/api/dv/lists?mode=ysf", nil, e.ck["op"], map[string]string{"If-None-Match": tag}); c != 304 {
		t.Fatalf("conditional: %d", c)
	}
	expectErr(t, e, "op", "GET", "/api/dv/lists?mode=bogus", nil, 400, "bad_request")
	expectErr(t, e, "viewer", "GET", "/api/dv/lists?mode=dmr", nil, 403, "forbidden")
}

func TestDVListsNone(t *testing.T) {
	fe := &fakeEngine{status: helper.DVStatus{Supported: true, Enabled: true}}
	fc := ctltest.New(t)
	hl := hostlists.New(hostlists.Options{Dir: t.TempDir(), Enabled: func() bool { return true }})
	hl.Load()
	e := newEnv(t, opts{dvengine: fe, dvctl: ctl.NewClient(fc.Path()), hostlists: hl, dvEnabled: func() bool { return true }})
	expectErr(t, e, "op", "GET", "/api/dv/lists?mode=ysf", nil, 404, "no_list")
}

func TestDVSetupReadOnly(t *testing.T) {
	e, fe, _ := dvEnv(t, ctl.ModeLoop)
	var v struct {
		BridgeNode struct {
			Node, Name string
			Owned      bool
			TxPort     int
			RxPort     int
		}
		Files []struct {
			ID                string
			Present, Writable bool
			SHA256            string
			History           int
		}
	}
	if c := e.do("admin", "GET", "/api/dv/setup", nil, &v); c != 200 {
		t.Fatalf("%d", c)
	}
	if v.BridgeNode.Name != "Digital modes linked node" || v.BridgeNode.RxPort != 34001 || v.BridgeNode.TxPort != 32001 || len(v.Files) != 2 || v.Files[0].ID != "rpt.conf" {
		t.Fatalf("%+v", v)
	}
	if c := e.do("admin", "PUT", "/api/dv/setup", map[string]string{}, nil); c != 404 && c != 405 {
		t.Fatalf("write: %d", c)
	}
	fe.mu.Lock()
	defer fe.mu.Unlock()
	if len(fe.calls) != 0 {
		t.Fatalf("setup called the helper: %v", fe.calls)
	}
}

func TestDVNodeName(t *testing.T) {
	e, _, _ := dvEnv(t, ctl.ModeLoop)
	var r struct{ Name string }
	if c := e.do("admin", "PUT", "/api/dv/private-node/name", map[string]string{"name": "Shack node"}, &r); c != 200 || r.Name != "Shack node" {
		t.Fatalf("%d %+v", c, r)
	}
	if c := e.do("admin", "PUT", "/api/dv/private-node/name", map[string]string{"name": ""}, &r); c != 200 || r.Name != "Digital modes linked node" {
		t.Fatalf("%d %+v", c, r)
	}
}

func TestDVErrUnavailableWithoutEngine(t *testing.T) {
	e := newEnv(t, opts{})
	expectErr(t, e, "admin", "GET", "/api/dv/status", nil, 503, "dv_unavailable")
}

func dvPrivEnv(t *testing.T) (*env, *fakeEngine, *ctltest.Fake) {
	t.Helper()
	fe := &fakeEngine{status: helper.DVStatus{Supported: true, Enabled: true}}
	fc := ctltest.New(t)
	e := newEnv(t, opts{dvengine: fe, dvctl: ctl.NewClient(fc.Path()), dvEnabled: func() bool { return true },
		configFn: func(c rpt.API) *configsvc.Service {
			s := configsvc.New(configsvc.NewDemoHelper(), c.(configsvc.AMI), rpt.New(c))
			s.HealthTimeout, s.HealthPoll = 2*time.Second, 20*time.Millisecond
			return s
		}})
	return e, fe, fc
}

func TestDVPrivateNodePlanApplyRemove(t *testing.T) {
	e, fe, fc := dvPrivEnv(t)
	var plan struct {
		Kind            string
		Node            string
		RxPort, TxPort  int
		RestartAsterisk bool
		Files           []struct {
			Path    string
			Changed bool
		}
	}
	body := map[string]any{"node": "1999", "dryRun": true}
	if c := e.do("admin", "PUT", "/api/dv/private-node", body, &plan); c != 200 || plan.Kind != "set" || plan.Node != "1999" || plan.RxPort != 34001 || plan.TxPort != 32001 || !plan.RestartAsterisk {
		t.Fatalf("%d %+v", c, plan)
	}
	if len(plan.Files) == 0 || plan.Files[0].Path != "rpt.conf" || !plan.Files[0].Changed {
		t.Fatalf("%+v", plan.Files)
	}
	fe.mu.Lock()
	if fe.rec != nil || len(fe.calls) != 0 {
		t.Fatalf("a dry run changed something: %v", fe.calls)
	}
	fe.mu.Unlock()
	// the restart must be confirmed
	expectErr(t, e, "admin", "PUT", "/api/dv/private-node", map[string]any{"node": "1999"}, 409, "restart_required")
	expectErr(t, e, "op", "PUT", "/api/dv/private-node", body, 403, "forbidden")
	expectErr(t, e, "admin", "PUT", "/api/dv/private-node", map[string]any{"node": "2500", "dryRun": true}, 400, "invalid_node")

	var res struct {
		Applied bool
		Steps   []struct {
			Name string
			OK   bool
		}
		Rollback struct{ Available bool }
	}
	if c := e.do("admin", "PUT", "/api/dv/private-node", map[string]any{"node": "1999", "restart": true}, &res); c != 200 || !res.Applied {
		t.Fatalf("%d %+v", c, res)
	}
	got := map[string]bool{}
	for _, s := range res.Steps {
		got[s.Name] = s.OK
	}
	for _, n := range []string{"asterisk_config", "engine_config", "asterisk_restart", "engine_reload", "health"} {
		if !got[n] {
			t.Errorf("step %s: %+v", n, res.Steps)
		}
	}
	if got["ini"] || got["bridge_restart"] || !res.Rollback.Available {
		t.Errorf("%+v", res)
	}
	fe.mu.Lock()
	rec := fe.rec
	fe.mu.Unlock()
	if rec == nil || rec.Node != "1999" || !rec.UsrpUntouched {
		t.Fatalf("record %+v", rec)
	}
	reloaded := false
	for _, q := range fc.Requests() {
		reloaded = reloaded || q.Op == ctl.OpReload
	}
	if !reloaded {
		t.Errorf("no reload: %+v", fc.Requests())
	}

	// the setup view now owns the node
	var v struct {
		BridgeNode struct {
			Node  string
			Owned bool
		}
	}
	if c := e.do("admin", "GET", "/api/dv/setup", nil, &v); c != 200 || v.BridgeNode.Node != "1999" || !v.BridgeNode.Owned {
		t.Fatalf("%d %+v", c, v)
	}
	// remove it again
	if c := e.do("admin", "DELETE", "/api/dv/private-node?restart=true", nil, &res); c != 200 || !res.Applied {
		t.Fatalf("remove %d %+v", c, res)
	}
	fe.mu.Lock()
	defer fe.mu.Unlock()
	if fe.rec != nil {
		t.Fatalf("record kept: %+v", fe.rec)
	}
}

func TestDVPrivateNodeRemoveNone(t *testing.T) {
	e, _, _ := dvPrivEnv(t)
	expectErr(t, e, "admin", "DELETE", "/api/dv/private-node?dryRun=true", nil, 409, "no_bridge_node")
	expectErr(t, e, "admin", "POST", "/api/dv/private-node/rollback", map[string]any{}, 404, "no_rollback")
	expectErr(t, e, "admin", "POST", "/api/dv/private-node/adopt", map[string]any{}, 409, "no_bridge_node")
}

// The web client reads the snapshot from the bare path; the status stays
// under /api/dv/status.
func TestDVBareGetIsSnapshot(t *testing.T) {
	e := newEnv(t, opts{})
	var bare, snap map[string]any
	if c := e.do("viewer", "GET", "/api/dv", nil, &bare); c != 200 {
		t.Fatalf("GET /api/dv: %d", c)
	}
	if c := e.do("viewer", "GET", "/api/dv/snapshot", nil, &snap); c != 200 {
		t.Fatalf("snapshot: %d", c)
	}
	for _, k := range []string{"installed", "running", "modes", "bridge", "heard"} {
		if _, ok := bare[k]; !ok {
			t.Errorf("bare reply lacks %q: %v", k, bare)
		}
	}
	if _, ok := bare["supported"]; ok {
		t.Error("bare reply is the status")
	}
}

// DVNetworkGet and DVNetworkSet keep the settings in memory; the password is
// held in pw and never in a view.
func (f *fakeEngine) DVNetworkGet(context.Context) (helper.DVNetworkReply, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.netReply(false), nil
}

func (f *fakeEngine) netReply(changed bool) helper.DVNetworkReply {
	r := helper.DVNetworkReply{Identity: f.ident, Rendered: true, Changed: changed}
	for _, m := range []string{"BM", "TGIF", "DMR", "YSF", "P25", "NXDN"} {
		if v, ok := f.nets[m]; ok {
			v.PasswordSet = f.pw[m] != ""
			r.Networks = append(r.Networks, v)
		}
	}
	return r
}

func (f *fakeEngine) DVNetworkSet(_ context.Context, q helper.DVNetworkRequest) (helper.DVNetworkReply, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, "network:"+q.Mode)
	if f.netErr != nil {
		return helper.DVNetworkReply{}, f.netErr
	}
	if q.Identity != nil {
		f.ident = *q.Identity
	}
	if q.Mode != "" {
		if f.nets == nil {
			f.nets, f.pw = map[string]helper.DVNetworkView{}, map[string]string{}
		}
		v := f.nets[q.Mode]
		v.Mode = q.Mode
		if q.Enabled != nil {
			v.Enabled = *q.Enabled
		}
		if q.Address != nil {
			v.Address = *q.Address
		}
		if q.Port != nil {
			v.Port = *q.Port
		}
		if q.Password != nil && *q.Password != "" {
			f.pw[q.Mode] = *q.Password
		}
		if q.ClearPassword {
			delete(f.pw, q.Mode)
		}
		if q.Options != nil {
			v.Options = q.Options
		}
		f.nets[q.Mode] = v
	}
	return f.netReply(true), nil
}
