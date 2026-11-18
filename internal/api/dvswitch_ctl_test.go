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
	atoiTG  bool // a non-DMR tune leaves its leading digits in digital.tg
	noConf  bool // a switch does not show in ABInfo (the "not confirmed" path)
	active  string
	infoErr error
	left    []string // the unlinks sent before a switch: "mode>value"
	bm      bool     // BrandMeister configured
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
	return helper.DVSwitchSetup{Identity: helper.DVIdentityView{Callsign: "N0CALL", DMRID: "1234567"}, Modes: f.modes, Files: []helper.DVFileView{},
		BrandMeister: helper.DVBMView{Configured: f.bm}}, nil
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
	if cur := helper.DVPanelMode(f.info); cur != t.Mode {
		res.Switched = true
		// like the helper: a linked mode is unlinked before the switch
		lt := f.info.Tune
		if lt == "" && helper.DVDMRFamily(cur) {
			lt = f.info.TG
		}
		if cur != "" && lt != "" && !helper.DVUnlinkValue(cur, lt) {
			res.Left = cur
			f.left = append(f.left, cur+">"+helper.DVUnlinkTune(cur))
		}
		if !f.noConf {
			// BM is ambe_mode DMR on STFU's ports
			f.info.Mode, f.info.Panel = t.Mode, t.Mode
			if t.Mode == "BM" {
				f.info.Mode = "DMR"
			}
		}
	}
	if !t.ModeOnly && !f.noConf {
		v := t.Tune
		if t.Mode == "YSF" && t.Tune == "12345" {
			v = "192.0.2.10:42000"
		}
		f.info.Tune = v
		if helper.DVDMRFamily(t.Mode) {
			f.info.TG = v
		} else if f.atoiTG {
			// Analog_Bridge reads txTg= as a number: "192.0.2.10:42000" gives 192
			n := 0
			for n < len(v) && v[n] >= '0' && v[n] <= '9' {
				n++
			}
			if n > 0 {
				f.info.TG = v[:n]
			}
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
	if len(sn.Modes) != 5 || sn.Modes[0] != (api.DVModeView{ID: "BM", Enabled: false, Unlink: "4000"}) || sn.Modes[1] != (api.DVModeView{ID: "DMR", Enabled: true, Unlink: "4000"}) ||
		sn.Modes[2].Unlink != "disconnect" || sn.Modes[3].Enabled || sn.Modes[4].Unlink != "9999" {
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
	// no remembered YSF target: YSF's unlink value goes with the switch
	if tn := f.lastTunes(); len(tn) != 1 || tn[0] != (helper.DVTune{Mode: "YSF", Tune: "disconnect"}) {
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

// The scenario of a real node: DMR on TG 3100, switch to YSF, link a
// reflector shown as host:port, switch back. The DMR tune sent is 3100, never
// the reflector's leading digits.
func TestDVModeSwitchBackSendsRememberedTune(t *testing.T) {
	f := newCtlDV()
	f.atoiTG = true
	f.info.Tune = "" // fresh DMR session
	f.info.TG = "9"
	tr := &dvswitch.Tracker{Src: f, Store: &memSettings{}}
	e := newEnv(t, opts{dvswitch: f, dvInstalled: installed, dvresume: tr,
		dvNode: func(context.Context) (string, bool) { return "1999", true }})
	ctx := context.Background()
	var rep struct{ Confirmed bool }
	post := func(path string, body map[string]any) {
		t.Helper()
		if c := e.do("op", "POST", path, body, &rep); c != 200 {
			t.Fatalf("%s: %d", path, c)
		}
		tr.Poll(ctx)
		tr.Poll(ctx)
	}
	post("/api/dvswitch/tune", map[string]any{"mode": "DMR", "tune": "3100"})
	post("/api/dvswitch/mode", map[string]any{"mode": "YSF"})
	post("/api/dvswitch/tune", map[string]any{"mode": "YSF", "tune": "192.0.2.10:42000"})
	if f.info.TG != "192" {
		t.Fatalf("the fake does not model the leftover: %q", f.info.TG)
	}
	n := len(f.lastTunes())
	post("/api/dvswitch/mode", map[string]any{"mode": "DMR"})
	sent := f.lastTunes()[n:]
	if len(sent) != 1 || sent[0] != (helper.DVTune{Mode: "DMR", Tune: "3100"}) {
		t.Fatalf("sent on switch back: %+v", sent)
	}
	for _, s := range f.lastTunes() {
		if s.Mode == "DMR" && (s.Tune == "192" || s.Tune == "76") {
			t.Fatalf("a reflector prefix was sent as a TG: %+v", s)
		}
	}
	v := tr.Get()
	if v.PerMode["DMR"].Tune != "3100" || v.PerMode["YSF"].Tune != "192.0.2.10:42000" {
		t.Fatalf("%+v", v)
	}
	if f.info.TG != "3100" {
		t.Fatalf("bridge TG %q", f.info.TG)
	}
	// and back to YSF: the reflector again
	n = len(f.lastTunes())
	post("/api/dvswitch/mode", map[string]any{"mode": "YSF"})
	if sent := f.lastTunes()[n:]; len(sent) != 1 || sent[0] != (helper.DVTune{Mode: "YSF", Tune: "192.0.2.10:42000"}) {
		t.Fatalf("%+v", sent)
	}
}

// Disconnect DMR, switch to YSF and back: DMR stays disconnected (no tune is
// sent on the way back). Tuning DMR again is remembered as usual.
func TestDVModeSwitchBackAfterDisconnectStaysDisconnected(t *testing.T) {
	f := newCtlDV()
	f.atoiTG = true
	f.info.Tune = ""
	f.info.TG = "9"
	tr := &dvswitch.Tracker{Src: f, Store: &memSettings{}}
	e := newEnv(t, opts{dvswitch: f, dvInstalled: installed, dvresume: tr,
		dvNode: func(context.Context) (string, bool) { return "1999", true }})
	ctx := context.Background()
	var rep struct{ Confirmed bool }
	post := func(path string, body map[string]any) {
		t.Helper()
		if c := e.do("op", "POST", path, body, &rep); c != 200 {
			t.Fatalf("%s: %d", path, c)
		}
		tr.Poll(ctx)
		tr.Poll(ctx)
	}
	post("/api/dvswitch/tune", map[string]any{"mode": "DMR", "tune": "3100"})
	post("/api/dvswitch/unlink", map[string]any{})
	if got := tr.Get().PerMode["DMR"].Tune; got != "" {
		t.Fatalf("the disconnect was not remembered: %q", got)
	}
	post("/api/dvswitch/mode", map[string]any{"mode": "YSF"})
	n := len(f.lastTunes())
	post("/api/dvswitch/mode", map[string]any{"mode": "DMR"})
	// only DMR's unlink value goes with the switch
	if sent := f.lastTunes()[n:]; len(sent) != 1 || sent[0] != (helper.DVTune{Mode: "DMR", Tune: "4000"}) {
		t.Fatalf("switching back re-tuned DMR: %+v", sent)
	}
	// a new connection is remembered again
	post("/api/dvswitch/tune", map[string]any{"mode": "DMR", "tune": "3100"})
	if got := tr.Get().PerMode["DMR"].Tune; got != "3100" {
		t.Fatalf("a new tune was not remembered: %q", got)
	}
}

// trackedCtl is a ctlDV with a tracker and a post helper that polls twice
// after each request.
func trackedCtl(t *testing.T, f *ctlDV) (*env, *dvswitch.Tracker, func(string, map[string]any) map[string]any) {
	t.Helper()
	tr := &dvswitch.Tracker{Src: f, Store: &memSettings{}}
	e := newEnv(t, opts{dvswitch: f, dvInstalled: installed, dvresume: tr,
		dvNode: func(context.Context) (string, bool) { return "1999", true }})
	ctx := context.Background()
	tr.Poll(ctx)
	tr.Poll(ctx)
	post := func(path string, body map[string]any) map[string]any {
		t.Helper()
		var rep map[string]any
		if c := e.do("op", "POST", path, body, &rep); c != 200 {
			t.Fatalf("%s %v: %d", path, body, c)
		}
		tr.Poll(ctx)
		tr.Poll(ctx)
		return rep
	}
	return e, tr, post
}

func (f *ctlDV) lastLeft() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.left
	f.left = nil
	return out
}

// Leaving a linked mode unlinks it first; its remembered tune stays, so
// switching back re-tunes it.
func TestDVModeSwitchUnlinksTheModeLeft(t *testing.T) {
	f := newCtlDV()
	f.bm = true
	f.info = helper.DVSwitchInfo{Found: true, Mode: "DMR", Panel: "BM", TG: "91", Tune: "91", Slot: "2"}
	e, tr, post := trackedCtl(t, f)
	if got := tr.Get().PerMode["BM"].Tune; got != "91" {
		t.Fatalf("BM not remembered: %q", got)
	}

	// BM on 91 to DMR: BM's 4000 first, DMR unlinked (nothing remembered)
	n := len(f.lastTunes())
	rep := post("/api/dvswitch/mode", map[string]any{"mode": "DMR"})
	if l := f.lastLeft(); len(l) != 1 || l[0] != "BM>4000" || rep["left"] != "BM" {
		t.Fatalf("left %v, reply %v", l, rep["left"])
	}
	if sent := f.lastTunes()[n:]; len(sent) != 1 || sent[0] != (helper.DVTune{Mode: "DMR", Tune: "4000"}) {
		t.Fatalf("%+v", sent)
	}
	if got := tr.Get().PerMode["BM"].Tune; got != "91" {
		t.Fatalf("BM's tune forgotten: %q", got)
	}
	// and back: BM re-tunes 91; DMR was unlinked, nothing to leave
	n = len(f.lastTunes())
	post("/api/dvswitch/mode", map[string]any{"mode": "BM"})
	if sent := f.lastTunes()[n:]; len(sent) != 1 || sent[0] != (helper.DVTune{Mode: "BM", Tune: "91"}) {
		t.Fatalf("%+v", sent)
	}
	if l := f.lastLeft(); len(l) != 0 {
		t.Fatalf("left %v", l)
	}
	aud, _ := e.st.ListAudit(50)
	found := false
	for _, a := range aud {
		found = found || a.Action == "dvswitch.mode" && strings.Contains(a.Detail, `"left":"BM"`)
	}
	if !found {
		t.Error("audit lacks the mode left")
	}

	// YSF linked to DMR: YSF disconnects first, comes back on the way back
	post("/api/dvswitch/mode", map[string]any{"mode": "YSF"})
	post("/api/dvswitch/tune", map[string]any{"mode": "YSF", "tune": "12345"})
	f.lastLeft()
	post("/api/dvswitch/mode", map[string]any{"mode": "DMR"})
	if l := f.lastLeft(); len(l) != 1 || l[0] != "YSF>disconnect" {
		t.Fatalf("left %v", l)
	}
	if got := tr.Get().PerMode["YSF"].Tune; got != "192.0.2.10:42000" {
		t.Fatalf("YSF's tune forgotten: %q", got)
	}
	n = len(f.lastTunes())
	post("/api/dvswitch/mode", map[string]any{"mode": "YSF"})
	if sent := f.lastTunes()[n:]; len(sent) != 1 || sent[0] != (helper.DVTune{Mode: "YSF", Tune: "192.0.2.10:42000"}) {
		t.Fatalf("%+v", sent)
	}
}

// Regression ("DMR linked to 76"): a mode with nothing remembered gets its
// unlink value on a switch, and the panel never shows another mode's value
// as its talkgroup.
func TestDVModeSwitchNeverShowsAnotherModesValue(t *testing.T) {
	for _, c := range []struct{ from, to string }{
		{"DMR", "DMR"}, {"BM", "BM"}, {"BM", "DMR"}, {"DMR", "BM"},
	} {
		t.Run(c.from+"-YSF-"+c.to, func(t *testing.T) {
			f := newCtlDV()
			f.bm, f.atoiTG = true, true
			f.info = helper.DVSwitchInfo{Found: true, Mode: "DMR", Panel: c.from, TG: "4000", Slot: "2"}
			e, tr, post := trackedCtl(t, f)
			// to YSF (no remembered target: disconnect), then a reflector:
			// Analog_Bridge reads its address as talkgroup 192
			post("/api/dvswitch/mode", map[string]any{"mode": "YSF"})
			post("/api/dvswitch/tune", map[string]any{"mode": "YSF", "tune": "12345"})
			var sn api.DVSnapshot
			e.do("viewer", "GET", "/api/dvswitch/snapshot", nil, &sn)
			if f.info.TG != "192" || sn.Bridge.Mode != "YSF" {
				t.Fatalf("setup: %+v %+v", f.info, sn.Bridge)
			}
			n := len(f.lastTunes())
			rep := post("/api/dvswitch/mode", map[string]any{"mode": c.to})
			if sent := f.lastTunes()[n:]; len(sent) != 1 || sent[0] != (helper.DVTune{Mode: c.to, Tune: "4000"}) {
				t.Fatalf("%+v", sent)
			}
			b, _ := json.Marshal(rep)
			if strings.Contains(string(b), `"tune":"192"`) {
				t.Fatalf("reply shows YSF's value: %s", b)
			}
			time.Sleep(450 * time.Millisecond) // past the coalescing of snapshots
			e.do("viewer", "GET", "/api/dvswitch/snapshot", nil, &sn)
			if sn.Bridge.Mode != c.to || sn.Bridge.Tune == "192" || sn.Bridge.Tune == "192.0.2.10:42000" {
				t.Fatalf("%+v", sn.Bridge)
			}
			if got := tr.Get().PerMode[c.to].Tune; got != "" {
				t.Fatalf("%s remembered %q", c.to, got)
			}
		})
	}
}

// The display alone: ABInfo in DMR still holding a YSF reflector as last
// tune and its number in the talkgroup does not read as DMR talkgroup 192.
func TestDVSnapshotDMRIgnoresYSFLeftovers(t *testing.T) {
	f := newCtlDV()
	f.info = helper.DVSwitchInfo{Found: true, Mode: "YSFN", Tune: "192.0.2.10:42000", TG: "192"}
	e := ctlEnv(t, f)
	var sn api.DVSnapshot
	e.do("viewer", "GET", "/api/dvswitch/snapshot", nil, &sn)
	f.set(func(f *ctlDV) { f.info.Mode = "DMR" })
	time.Sleep(450 * time.Millisecond)
	e.do("viewer", "GET", "/api/dvswitch/snapshot", nil, &sn)
	if sn.Bridge.Mode != "DMR" || sn.Bridge.Tune != "" {
		t.Fatalf("%+v", sn.Bridge)
	}
}

func TestDVModeBMNeedsConfiguration(t *testing.T) {
	f := newCtlDV()
	e := ctlEnv(t, f)
	expectErr(t, e, "op", "POST", "/api/dvswitch/mode", map[string]any{"mode": "BM"}, 409, "mode_disabled")
	f.set(func(f *ctlDV) { f.bm = true })
	var rep struct{ Snapshot api.DVSnapshot }
	if c := e.do("op", "POST", "/api/dvswitch/mode", map[string]any{"mode": "bm"}, &rep); c != 200 || rep.Snapshot.Bridge.Mode != "BM" {
		t.Fatalf("%d %+v", c, rep.Snapshot.Bridge)
	}
	if tn := f.lastTunes(); len(tn) != 1 || tn[0] != (helper.DVTune{Mode: "BM", Tune: "4000"}) {
		t.Fatalf("%+v", tn)
	}
}
