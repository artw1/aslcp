package api_test

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/api"
	"github.com/artw1/aslcp/internal/dvswitch"
	"github.com/artw1/aslcp/internal/helper"
)

// ctlDV is a DVSwitch with a bridge that reacts to tunes and mode switches.
type ctlDV struct {
	fakeDV
	mu      sync.Mutex
	info    helper.DVSwitchInfo
	modes   helper.DVModesView
	tunes   []helper.DVTune
	noConf  bool // a switch does not show in ABInfo (the "not confirmed" path)
	active  string
	infoErr error
}

func newCtlDV() *ctlDV {
	return &ctlDV{
		info:   helper.DVSwitchInfo{Found: true, Mode: "DMR", TG: "91", Slot: "2", Mute: "OFF", Updated: "2026-11-18T10:00:00Z", GatewayID: "1234567"},
		modes:  helper.DVModesView{DMR: true, YSF: true},
		active: "active",
	}
}

func (f *ctlDV) DVSwitchStatus(context.Context) (helper.DVSwitchStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return helper.DVSwitchStatus{Installed: true, Supported: true, Version: "1.7.0",
		Install: helper.DVSwitchJob{Job: "idle", Log: "apt secret-ish log line"},
		Units: []helper.DVSwitchUnit{
			{Name: "analog_bridge", Loaded: true, Active: f.active},
			{Name: "mmdvm_bridge", Loaded: true, Active: "active"},
			{Name: "netcheck", Loaded: true, Active: "active"},
			{Name: "ysfgateway", Loaded: true, Active: "inactive"},
			{Name: "p25gateway", Loaded: true, Active: "active"},
		}}, nil
}

func (f *ctlDV) DVSwitchSetup(context.Context) (helper.DVSwitchSetup, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return helper.DVSwitchSetup{Identity: helper.DVIdentityView{Callsign: "N0CALL", DMRID: "1234567"}, Modes: f.modes, Files: []helper.DVFileView{}}, nil
}

func (f *ctlDV) DVSwitchInfo(context.Context) (helper.DVSwitchInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.info, f.infoErr
}

func (f *ctlDV) DVSwitchLists(_ context.Context, mode string) (helper.DVList, error) {
	if mode != "ysf" {
		return helper.DVList{}, helper.ErrDVNoList
	}
	return helper.DVList{Mode: mode, Items: []helper.DVListItem{
		{Value: "12345", Name: "Example room", Address: "192.0.2.10", Port: 42000}}}, nil
}

func (f *ctlDV) DVSwitchTune(_ context.Context, r helper.DVSwitchRequest) (helper.DVTuneResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := *r.Tune
	f.tunes = append(f.tunes, t)
	res := helper.DVTuneResult{Mode: t.Mode, Confirmed: !f.noConf}
	if helper.DVNormMode(f.info.Mode) != t.Mode {
		res.Switched = true
		if !f.noConf {
			f.info.Mode = t.Mode
		}
	}
	if !t.ModeOnly && !f.noConf {
		v := t.Tune
		if t.Mode == "YSF" && t.Tune == "12345" {
			v = "192.0.2.10:42000"
		}
		f.info.Tune = v
		if t.Mode == "DMR" {
			f.info.TG = v
		}
	}
	return res, nil
}

func (f *ctlDV) set(fn func(*ctlDV)) {
	f.mu.Lock()
	fn(f)
	f.mu.Unlock()
}

func (f *ctlDV) lastTunes() []helper.DVTune {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]helper.DVTune(nil), f.tunes...)
}

func installed() bool { return true }

func ctlEnv(t *testing.T, f *ctlDV) *env {
	return newEnv(t, opts{dvswitch: f, dvInstalled: installed,
		dvNode: func(context.Context) (string, bool) { return "1999", true }})
}

func TestDVSnapshotRolesAndContent(t *testing.T) {
	f := newCtlDV()
	f.info = helper.DVSwitchInfo{Found: true, Mode: "YSFN", Tune: "192.0.2.10:42000", Slot: "2", Mute: "ON", Updated: "2026-11-18T10:00:00Z"}
	e := ctlEnv(t, f)
	expectErr(t, e, "", "GET", "/api/dvswitch/snapshot", nil, 401, "unauthorized")
	for _, role := range []string{"viewer", "op", "admin"} {
		code, b, _ := e.raw("GET", "/api/dvswitch/snapshot", nil, e.ck[role], nil)
		if code != 200 {
			t.Fatalf("%s: %d %s", role, code, b)
		}
		for _, leak := range []string{"N0CALL", "1234567", "apt secret", "jobLog", "\"log\""} {
			if strings.Contains(string(b), leak) {
				t.Errorf("%s: snapshot leaks %q: %s", role, leak, b)
			}
		}
	}
	var sn api.DVSnapshot
	if c := e.do("viewer", "GET", "/api/dvswitch/snapshot", nil, &sn); c != 200 {
		t.Fatal(c)
	}
	if !sn.Installed || !sn.Running || !sn.Configured {
		t.Errorf("flags: %+v", sn)
	}
	b := sn.Bridge
	if b.Mode != "YSF" || b.Tune != "192.0.2.10:42000" || b.TargetName != "Example room" || b.Source != "abinfo" || b.Slot != 2 || !b.Muted {
		t.Errorf("bridge: %+v", b)
	}
	if len(sn.Modes) != 4 || sn.Modes[0] != (api.DVModeView{ID: "DMR", Enabled: true, Unlink: "4000"}) ||
		sn.Modes[1].Unlink != "disconnect" || sn.Modes[2].Enabled || sn.Modes[3].Unlink != "9999" {
		t.Errorf("modes: %+v", sn.Modes)
	}
	// ysfgateway is enabled (YSF) but down; p25gateway is not enabled; netcheck is not shown
	want := []api.DVServiceView{{Unit: "analog_bridge", Active: true}, {Unit: "mmdvm_bridge", Active: true}, {Unit: "ysfgateway", Active: false}}
	if len(sn.Services) != len(want) {
		t.Fatalf("services: %+v", sn.Services)
	}
	for i := range want {
		if sn.Services[i] != want[i] {
			t.Errorf("service %d: %+v", i, sn.Services[i])
		}
	}
	if !sn.BridgeNode.Present || sn.BridgeNode.Node != "1999" {
		t.Errorf("bridge node: %+v", sn.BridgeNode)
	}
}

func TestDVSnapshotStates(t *testing.T) {
	// no DVSwitch on the node: 200 with installed false, the helper is not asked
	f := newCtlDV()
	e := newEnv(t, opts{dvswitch: f})
	var sn api.DVSnapshot
	if c := e.do("viewer", "GET", "/api/dvswitch/snapshot", nil, &sn); c != 200 || sn.Installed || sn.Configured || sn.Bridge.Source != "none" || len(sn.Modes) != 0 {
		t.Fatalf("%d %+v", c, sn)
	}
	// helper not wired at all
	e = newEnv(t, opts{})
	if c := e.do("viewer", "GET", "/api/dvswitch/snapshot", nil, &sn); c != 200 || sn.Installed {
		t.Fatalf("%d %+v", c, sn)
	}
	// analog_bridge down: installed but not configured, mode unknown, no bridge node
	f = newCtlDV()
	f.active, f.info = "inactive", helper.DVSwitchInfo{}
	e = newEnv(t, opts{dvswitch: f, dvInstalled: installed})
	if c := e.do("viewer", "GET", "/api/dvswitch/snapshot", nil, &sn); c != 200 || !sn.Installed || sn.Running || sn.Configured || sn.Bridge.Source != "none" || sn.Bridge.Mode != "" || sn.BridgeNode.Present {
		t.Fatalf("%d %+v", c, sn)
	}
}

func TestDVSnapshotTuneOfAnotherModeIsNotShown(t *testing.T) {
	f := newCtlDV()
	f.info = helper.DVSwitchInfo{Found: true, Mode: "YSFN", Tune: "12345"}
	e := ctlEnv(t, f)
	var sn api.DVSnapshot
	e.do("viewer", "GET", "/api/dvswitch/snapshot", nil, &sn)
	if sn.Bridge.Tune != "12345" {
		t.Fatalf("%+v", sn.Bridge)
	}
	// the mode changes, last_tune stays: it belongs to YSF, not to P25
	f.set(func(f *ctlDV) { f.info.Mode = "P25" })
	time.Sleep(450 * time.Millisecond) // past the coalescing of snapshots
	e.do("viewer", "GET", "/api/dvswitch/snapshot", nil, &sn)
	if sn.Bridge.Mode != "P25" || sn.Bridge.Tune != "" {
		t.Fatalf("%+v", sn.Bridge)
	}
}

func TestDVModeRolesAndBehaviour(t *testing.T) {
	f := newCtlDV()
	e := ctlEnv(t, f)
	body := map[string]any{"mode": "YSF"}
	expectErr(t, e, "", "POST", "/api/dvswitch/mode", body, 401, "unauthorized")
	expectErr(t, e, "viewer", "POST", "/api/dvswitch/mode", body, 403, "forbidden")
	if len(f.lastTunes()) != 0 {
		t.Fatal("a viewer reached the helper")
	}
	// CSRF header is required
	if c, _, _ := e.raw("POST", "/api/dvswitch/mode", body, e.ck["op"], map[string]string{"X-ASLCP": ""}); c != 403 || len(f.lastTunes()) != 0 {
		t.Fatalf("csrf: %d", c)
	}
	var rep struct {
		Mode      string
		Switched  bool
		Confirmed bool
		Snapshot  api.DVSnapshot
	}
	if c := e.do("op", "POST", "/api/dvswitch/mode", body, &rep); c != 200 || !rep.Confirmed || !rep.Switched || rep.Snapshot.Bridge.Mode != "YSF" {
		t.Fatalf("%d %+v", c, rep)
	}
	if tn := f.lastTunes(); len(tn) != 1 || tn[0] != (helper.DVTune{Mode: "YSF", ModeOnly: true}) {
		t.Fatalf("tunes %+v", tn)
	}
	// lower case is accepted; admin may switch too
	if c := e.do("admin", "POST", "/api/dvswitch/mode", map[string]any{"mode": "dmr"}, &rep); c != 200 || rep.Snapshot.Bridge.Mode != "DMR" {
		t.Fatalf("%d %+v", c, rep)
	}
	expectErr(t, e, "op", "POST", "/api/dvswitch/mode", map[string]any{"mode": "P25"}, 409, "mode_disabled")
	expectErr(t, e, "op", "POST", "/api/dvswitch/mode", map[string]any{"mode": "DSTAR"}, 400, "bad_request")
	expectErr(t, e, "op", "POST", "/api/dvswitch/mode", map[string]any{}, 400, "bad_request")
	if n := len(f.lastTunes()); n != 2 {
		t.Fatalf("a refused request reached the helper: %d", n)
	}
	aud, _ := e.st.ListAudit(50)
	got := map[string]int{}
	for _, a := range aud {
		got[a.Action]++
	}
	if got["dvswitch.mode"] != 2 || got["dvswitch.mode.refused"] != 1 {
		t.Errorf("audit: %v", got)
	}
	expectErr(t, newEnv(t, opts{}), "op", "POST", "/api/dvswitch/mode", body, 503, "dvswitch_unavailable")
}

func TestDVModeNotConfirmed(t *testing.T) {
	f := newCtlDV()
	f.noConf = true
	e := ctlEnv(t, f)
	var rep struct {
		Confirmed bool
		Snapshot  api.DVSnapshot
	}
	start := time.Now()
	if c := e.do("op", "POST", "/api/dvswitch/mode", map[string]any{"mode": "YSF"}, &rep); c != 200 || rep.Confirmed || rep.Snapshot.Bridge.Mode != "DMR" {
		t.Fatalf("%d %+v", c, rep)
	}
	if d := time.Since(start); d < time.Second || d > 4*time.Second {
		t.Errorf("waited %v for the confirmation", d)
	}
}

func TestDVUnlink(t *testing.T) {
	for _, c := range []struct{ abMode, mode, value string }{
		{"DMR", "DMR", "4000"}, {"YSFN", "YSF", "disconnect"}, {"YSFW", "YSF", "disconnect"}, {"P25", "P25", "9999"}, {"NXDN", "NXDN", "9999"},
	} {
		f := newCtlDV()
		f.info.Mode = c.abMode
		e := ctlEnv(t, f)
		expectErr(t, e, "", "POST", "/api/dvswitch/unlink", map[string]any{}, 401, "unauthorized")
		expectErr(t, e, "viewer", "POST", "/api/dvswitch/unlink", map[string]any{}, 403, "forbidden")
		if len(f.lastTunes()) != 0 {
			t.Fatal("a viewer reached the helper")
		}
		var rep struct {
			Mode      string
			Confirmed bool
			Snapshot  api.DVSnapshot
		}
		role := map[bool]string{true: "op", false: "admin"}[c.abMode != "NXDN"]
		if code := e.do(role, "POST", "/api/dvswitch/unlink", map[string]any{}, &rep); code != 200 || rep.Mode != c.mode {
			t.Fatalf("%s: %d %+v", c.abMode, code, rep)
		}
		if tn := f.lastTunes(); len(tn) != 1 || tn[0] != (helper.DVTune{Mode: c.mode, Tune: c.value}) {
			t.Errorf("%s: %+v", c.abMode, tn)
		}
		aud, _ := e.st.ListAudit(50)
		n := 0
		for _, a := range aud {
			if a.Action == "dvswitch.unlink" && strings.Contains(a.Detail, c.value) {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%s: audit %d", c.abMode, n)
		}
	}
	// an empty body is fine, a bridge without ABInfo or in an unknown mode is not
	f := newCtlDV()
	e := ctlEnv(t, f)
	if c, _, _ := e.raw("POST", "/api/dvswitch/unlink", nil, e.ck["op"], nil); c != 200 {
		t.Fatalf("empty body: %d", c)
	}
	f.set(func(f *ctlDV) { f.info = helper.DVSwitchInfo{} })
	expectErr(t, e, "op", "POST", "/api/dvswitch/unlink", map[string]any{}, 409, "no_info")
	f.set(func(f *ctlDV) { f.info = helper.DVSwitchInfo{Found: true, Mode: "DSTAR"} })
	expectErr(t, e, "op", "POST", "/api/dvswitch/unlink", map[string]any{}, 409, "mode_unknown")
	expectErr(t, newEnv(t, opts{}), "op", "POST", "/api/dvswitch/unlink", map[string]any{}, 503, "dvswitch_unavailable")
}

func TestDVTuneReturnsSnapshot(t *testing.T) {
	f := newCtlDV()
	e := ctlEnv(t, f)
	var rep struct {
		Mode      string
		Confirmed bool
		Snapshot  api.DVSnapshot
	}
	if c := e.do("op", "POST", "/api/dvswitch/tune", map[string]any{"mode": "YSF", "tune": "12345"}, &rep); c != 200 || !rep.Confirmed || rep.Mode != "YSF" {
		t.Fatalf("%d %+v", c, rep)
	}
	if b := rep.Snapshot.Bridge; b.Mode != "YSF" || b.Tune != "192.0.2.10:42000" || b.TargetName != "Example room" {
		t.Fatalf("%+v", b)
	}
}

func TestDVFavoritesRoles(t *testing.T) {
	e := ctlEnv(t, newCtlDV())
	for _, ep := range [][2]string{{"GET", "/api/dvswitch/favorites"}, {"POST", "/api/dvswitch/favorites"}, {"PUT", "/api/dvswitch/favorites/1"}, {"DELETE", "/api/dvswitch/favorites/1"}} {
		expectErr(t, e, "", ep[0], ep[1], map[string]any{"mode": "YSF", "value": "12345", "name": "x"}, 401, "unauthorized")
	}
	good := map[string]any{"mode": "YSF", "value": "12345", "name": "Example room"}
	expectErr(t, e, "viewer", "POST", "/api/dvswitch/favorites", good, 403, "forbidden")
	expectErr(t, e, "viewer", "PUT", "/api/dvswitch/favorites/1", map[string]any{"name": "y"}, 403, "forbidden")
	expectErr(t, e, "viewer", "DELETE", "/api/dvswitch/favorites/1", nil, 403, "forbidden")
	if c, _, _ := e.raw("POST", "/api/dvswitch/favorites", good, e.ck["op"], map[string]string{"X-ASLCP": ""}); c != 403 {
		t.Fatalf("csrf: %d", c)
	}
	var l []map[string]any
	if c := e.do("viewer", "GET", "/api/dvswitch/favorites", nil, &l); c != 200 || len(l) != 0 {
		t.Fatalf("%d %v", c, l)
	}
	var f1, f2 struct {
		ID          int64
		Mode, Value string
		Name        string
		Sort        int
	}
	if c := e.do("op", "POST", "/api/dvswitch/favorites", good, &f1); c != 201 || f1.ID == 0 || f1.Mode != "YSF" || f1.Sort != 0 {
		t.Fatalf("%d %+v", c, f1)
	}
	if c := e.do("admin", "POST", "/api/dvswitch/favorites", map[string]any{"mode": "ysf", "value": "192.0.2.10:42000", "name": "  Direct  "}, &f2); c != 201 || f2.Mode != "YSF" || f2.Name != "Direct" || f2.Sort != 1 {
		t.Fatalf("%d %+v", c, f2)
	}
	expectErr(t, e, "op", "POST", "/api/dvswitch/favorites", good, 409, "duplicate")
	// the same value in another mode is not a duplicate
	if c := e.do("op", "POST", "/api/dvswitch/favorites", map[string]any{"mode": "P25", "value": "12345", "name": "TG"}, nil); c != 201 {
		t.Fatal(c)
	}
	for name, b := range map[string]map[string]any{
		"bad mode":      {"mode": "DSTAR", "value": "1", "name": "x"},
		"bad dmr":       {"mode": "DMR", "value": "abc", "name": "x"},
		"option-like":   {"mode": "DMR", "value": "-9", "name": "x"},
		"tlv smuggling": {"mode": "YSF", "value": "a=b", "name": "x"},
		"empty name":    {"mode": "DMR", "value": "91", "name": "  "},
		"long name":     {"mode": "DMR", "value": "91", "name": strings.Repeat("a", 41)},
		"control":       {"mode": "DMR", "value": "91", "name": "a\nb"},
	} {
		if c, code := e.errCode("op", "POST", "/api/dvswitch/favorites", b); c != 400 || code != "bad_request" {
			t.Errorf("%s: %d %s", name, c, code)
		}
	}
	if c := e.do("op", "POST", "/api/dvswitch/favorites", map[string]any{"mode": "DMR", "value": "91", "name": strings.Repeat("é", 40)}, nil); c != 201 {
		t.Errorf("40 runes: %d", c)
	}
	// rename and re-sort
	var up struct {
		Name string
		Sort int
	}
	if c := e.do("op", "PUT", "/api/dvswitch/favorites/"+strconv.FormatInt(f1.ID, 10), map[string]any{"name": "Renamed", "sort": 7}, &up); c != 200 || up.Name != "Renamed" || up.Sort != 7 {
		t.Fatalf("%d %+v", c, up)
	}
	expectErr(t, e, "op", "PUT", "/api/dvswitch/favorites/"+strconv.FormatInt(f1.ID, 10), map[string]any{"name": ""}, 400, "bad_request")
	expectErr(t, e, "op", "PUT", "/api/dvswitch/favorites/9999", map[string]any{"name": "x"}, 404, "not_found")
	expectErr(t, e, "op", "PUT", "/api/dvswitch/favorites/abc", map[string]any{"name": "x"}, 400, "bad_request")
	if c := e.do("op", "DELETE", "/api/dvswitch/favorites/"+strconv.FormatInt(f2.ID, 10), nil, nil); c != 204 {
		t.Fatal(c)
	}
	expectErr(t, e, "admin", "DELETE", "/api/dvswitch/favorites/"+strconv.FormatInt(f2.ID, 10), nil, 404, "not_found")
	if c := e.do("viewer", "GET", "/api/dvswitch/favorites", nil, &l); c != 200 || len(l) != 3 {
		t.Fatalf("%d %v", c, l)
	}
	aud, _ := e.st.ListAudit(100)
	got := map[string]int{}
	for _, a := range aud {
		got[a.Action]++
	}
	if got["dvswitch.favorite.add"] != 4 || got["dvswitch.favorite.update"] != 1 || got["dvswitch.favorite.delete"] != 1 {
		t.Errorf("audit: %v", got)
	}
}

func TestDVFavoritesCap(t *testing.T) {
	e := ctlEnv(t, newCtlDV())
	for i := 1; i <= 50; i++ {
		if c := e.do("op", "POST", "/api/dvswitch/favorites", map[string]any{"mode": "NXDN", "value": strconv.Itoa(i), "name": "TG " + strconv.Itoa(i)}, nil); c != 201 {
			t.Fatalf("%d: %d", i, c)
		}
	}
	expectErr(t, e, "op", "POST", "/api/dvswitch/favorites", map[string]any{"mode": "NXDN", "value": "51", "name": "one more"}, 409, "limit")
	if c := e.do("op", "POST", "/api/dvswitch/favorites", map[string]any{"mode": "P25", "value": "51", "name": "other mode"}, nil); c != 201 {
		t.Fatalf("another mode: %d", c)
	}
}

func TestDVTargetNameFromFavorite(t *testing.T) {
	f := newCtlDV()
	f.info.TG = "91"
	e := ctlEnv(t, f)
	e.do("op", "POST", "/api/dvswitch/favorites", map[string]any{"mode": "DMR", "value": "91", "name": "World wide"}, nil)
	time.Sleep(450 * time.Millisecond)
	var sn api.DVSnapshot
	e.do("viewer", "GET", "/api/dvswitch/snapshot", nil, &sn)
	if sn.Bridge.Tune != "91" || sn.Bridge.TargetName != "World wide" {
		t.Fatalf("%+v", sn.Bridge)
	}
}

// memSettings is the tracker's settings store.
type memSettings struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *memSettings) GetSetting(k string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if v, ok := s.m[k]; ok {
		return v, nil
	}
	return "", errors.New("not found")
}

func (s *memSettings) SetSetting(k, v string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]string{}
	}
	s.m[k] = v
	return nil
}

func dvEvents(s *sseStream, n int, d time.Duration) []api.DVSnapshot {
	var out []api.DVSnapshot
	end := time.After(d)
	for len(out) < n {
		select {
		case ev := <-s.events:
			if ev.name == "dvswitch" {
				var sn api.DVSnapshot
				if err := json.Unmarshal([]byte(ev.data), &sn); err != nil {
					s.t.Fatal(err)
				}
				out = append(out, sn)
			}
		case <-end:
			return out
		}
	}
	return out
}

func TestDVSnapshotEventsFromPoll(t *testing.T) {
	f := newCtlDV()
	tr := &dvswitch.Tracker{Src: f, Store: &memSettings{}}
	e := newEnv(t, opts{dvswitch: f, dvresume: tr, dvInstalled: installed})
	s := e.stream("viewer")
	// the tracker polls faster while a client is connected
	if tr.Viewers == nil || tr.Viewers() != 1 {
		t.Fatal("viewers hook missing or wrong")
	}
	ctx := context.Background()
	tr.Poll(ctx) // first snapshot: published
	if ev := dvEvents(s, 1, 3*time.Second); len(ev) != 1 || ev[0].Bridge.Mode != "DMR" || ev[0].Bridge.Tune != "91" {
		t.Fatalf("first: %+v", ev)
	}
	// nothing changed, only the file time: no event
	f.set(func(f *ctlDV) { f.info.Updated = "2026-11-18T10:05:00Z" })
	tr.Poll(ctx)
	tr.Poll(ctx)
	if ev := dvEvents(s, 1, 500*time.Millisecond); len(ev) != 0 {
		t.Fatalf("unchanged state was published: %+v", ev)
	}
	// somebody else retunes the bridge: one event
	f.set(func(f *ctlDV) { f.info.TG = "93" })
	tr.Poll(ctx)
	tr.Poll(ctx)
	ev := dvEvents(s, 2, time.Second)
	if len(ev) != 1 || ev[0].Bridge.Tune != "93" {
		t.Fatalf("change: %+v", ev)
	}
	// a service goes down
	f.set(func(f *ctlDV) { f.active = "inactive" })
	time.Sleep(16 * time.Second / 10 * 10) // past the status cache
	tr.Poll(ctx)
	ev = dvEvents(s, 1, 2*time.Second)
	if len(ev) != 1 || ev[0].Running || ev[0].Configured {
		t.Fatalf("service down: %+v", ev)
	}
}

func TestDVOwnCommandPublishesAndRereads(t *testing.T) {
	f := newCtlDV()
	e := ctlEnv(t, f)
	s := e.stream("viewer")
	if c := e.do("op", "POST", "/api/dvswitch/tune", map[string]any{"mode": "DMR", "tune": "93"}, nil); c != 200 {
		t.Fatal(c)
	}
	ev := dvEvents(s, 1, 2*time.Second)
	if len(ev) != 1 || ev[0].Bridge.Tune != "93" {
		t.Fatalf("%+v", ev)
	}
	// the bridge settles a moment later: the re-reads at 300 ms and 1.5 s pick it up
	f.set(func(f *ctlDV) { f.info.Slot = "1" })
	ev = dvEvents(s, 1, 3*time.Second)
	if len(ev) != 1 || ev[0].Bridge.Slot != 1 {
		t.Fatalf("re-read: %+v", ev)
	}
}
