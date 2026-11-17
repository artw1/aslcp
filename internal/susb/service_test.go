package susb

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

const rptConf = `[general]
[node-main](!)
rxchannel = SimpleUSB/placeholder

[1999](node-main)
rxchannel = SimpleUSB/1999
callsign = N0CALL

[1998]
rxchannel = SimpleUSB/1998

[1997]
rxchannel = dahdi/1
`

const simpleConf = `[general]
[node](!)
carrierfrom = usbinvert
ctcssfrom = no
rxboost = no
preemphasis = yes

[1999](node)
rxmixerset = 650
txmixaset = 500
txmixbset = 0
deemphasis = yes
plfilter = yes
invertptt = no
rxondelay = 0
txoffdelay = 0

[1998](node)
rxmixerset = 400
`

type env struct {
	t *testing.T
	f *fakeRadio
	c *fakeConfig
	r *fakeRecorder
	s *Service
}

func newEnv(t *testing.T, devs ...string) *env {
	t.Helper()
	if len(devs) == 0 {
		devs = []string{"1999", "1998"}
	}
	e := &env{t: t, f: newFake(devs...), c: &fakeConfig{files: map[string]string{"rpt.conf": rptConf, "simpleusb.conf": simpleConf}}, r: &fakeRecorder{}}
	e.s = New(e.f)
	e.s.Config, e.s.Recorder = e.c, e.r
	e.s.EchoIdle = 0
	e.s.MeterMinGap = 0
	return e
}

func ctx() context.Context { return context.Background() }

func TestDevicesMapNodes(t *testing.T) {
	e := newEnv(t)
	d, err := e.s.Devices(ctx())
	if err != nil || len(d) != 2 {
		t.Fatalf("%v %v", d, err)
	}
	// 1999 inherits nothing for rxchannel (its own line wins over the template);
	// 1998 is a plain section.
	if d[0].Name != "1999" || d[0].Node != "1999" || d[1].Name != "1998" || d[1].Node != "1998" {
		t.Fatalf("%+v", d)
	}
	if d[0].USBPath != "1-1.1" || d[0].Card != 1 {
		t.Fatalf("%+v", d[0])
	}
	// the list is cached
	n := len(e.f.commands())
	if _, err := e.s.Devices(ctx()); err != nil || len(e.f.commands()) != n {
		t.Fatal("not cached")
	}
}

func TestDevicesNoRptConfAndNoDevices(t *testing.T) {
	e := newEnv(t)
	delete(e.c.files, "rpt.conf")
	d, err := e.s.Devices(ctx())
	if err != nil || len(d) != 2 || d[0].Node != "" {
		t.Fatalf("%+v %v", d, err)
	}
	e2 := newEnv(t)
	e2.f.failCmd["susb tune menu-support 1"] = []string{""}
	if d, err := e2.s.Devices(ctx()); err != nil || len(d) != 0 {
		t.Fatalf("%v %v", d, err)
	}
	e3 := newEnv(t)
	e3.f.failCmd["susb tune menu-support 1"] = []string{"No such command 'susb tune menu-support 1'"}
	if _, err := e3.s.Devices(ctx()); code(err) != CodeNoDriver {
		t.Fatal(err)
	}
}

func TestReadSetsActiveVerifiesAndRestores(t *testing.T) {
	e := newEnv(t)
	e.f.active = "1998"
	st, err := e.s.Read(ctx(), "1999")
	if err != nil {
		t.Fatal(err)
	}
	if st.Device != "1999" || st.Node != "1999" || st.Params.RxVoice != 650 || st.Identity.Device != "1999" {
		t.Fatalf("%+v", st)
	}
	cmds := e.f.commands()
	// device list, labels, then: query active, set active, identity, params, restore
	tail := cmds[len(cmds)-5:]
	want := []string{"susb active", "susb active 1999", "susb tune menu-support 2", "susb tune menu-support 0+4", "susb active 1998"}
	for i := range want {
		if tail[i] != want[i] {
			t.Fatalf("commands: %v", tail)
		}
	}
	if e.f.active != "1998" {
		t.Fatal("previous device not restored")
	}
	if len(st.Dirty) != 0 {
		t.Fatalf("dirty: %v", st.Dirty)
	}
}

func TestReadUnknownAndMissingDevice(t *testing.T) {
	e := newEnv(t)
	for _, bad := range []string{"nope", "1999;x", "", strings.Repeat("a", 40), "../x"} {
		if _, err := e.s.Read(ctx(), bad); code(err) != CodeUnknownDevice {
			t.Errorf("%q: %v", bad, err)
		}
	}
	e.f.dev["1999"].notFound = true
	if _, err := e.s.Read(ctx(), "1999"); code(err) != CodeDeviceMissing {
		t.Fatal(err)
	}
}

func TestSetActiveMismatchAborts(t *testing.T) {
	e := newEnv(t)
	// someone else's reply names another device
	e.f.failCmd["susb active 1999"] = []string{"Active (command) Simple USB Radio device set to [1998]"}
	v := 100
	if _, err := e.s.Apply(ctx(), "1999", Patch{RxVoice: &v}); code(err) != CodeUnexpected {
		t.Fatal(err)
	}
	for _, c := range e.f.commands() {
		if strings.Contains(c, "menu-support c") {
			t.Fatal("a command ran on the wrong device")
		}
	}
}

func TestBaselineFromFileShowsUnsavedChanges(t *testing.T) {
	e := newEnv(t)
	e.f.dev["1999"].rx = 700 // changed behind ASLCP's back, file says 650
	e.f.dev["1999"].boost = true
	st, err := e.s.Read(ctx(), "1999")
	if err != nil {
		t.Fatal(err)
	}
	if st.Saved.RxVoice != 650 || st.Params.RxVoice != 700 {
		t.Fatalf("%+v", st)
	}
	if len(st.Dirty) != 2 || st.Dirty[0] != "rxVoice" || st.Dirty[1] != "rxBoost" {
		t.Fatalf("dirty: %v", st.Dirty)
	}
}

func TestApplyAndRevert(t *testing.T) {
	e := newEnv(t)
	if _, err := e.s.Read(ctx(), "1999"); err != nil {
		t.Fatal(err)
	}
	rx, txa, boost, car, rxon := 720, 450, true, "usb", 100
	res, err := e.s.Apply(ctx(), "1999", Patch{RxVoice: &rx, TxA: &txa, RxBoost: &boost, CarrierFrom: &car, RxOnDelayMs: &rxon})
	if err != nil || len(res.Errors) != 0 || len(res.Applied) != 5 {
		t.Fatalf("%+v %v", res, err)
	}
	d := e.f.dev["1999"]
	if d.rx != 720 || d.txa != 450 || !d.boost || d.carrier != 3 || d.rxon != 5 {
		t.Fatalf("%+v", d)
	}
	if res.Params.RxVoice != 720 || res.Params.RxOnDelayMs != 100 || len(res.Dirty) != 5 {
		t.Fatalf("%+v", res)
	}
	// the other device was not touched
	if e.f.dev["1998"].rx != 650 {
		t.Fatal("wrong device changed")
	}
	rev, err := e.s.Revert(ctx(), "1999")
	if err != nil || len(rev.Errors) != 0 {
		t.Fatalf("%+v %v", rev, err)
	}
	if d.rx != 650 || d.txa != 500 || d.boost || d.carrier != 4 || d.rxon != 0 || len(rev.Dirty) != 0 {
		t.Fatalf("%+v %v", d, rev.Dirty)
	}
}

func TestApplyValidationRunsNothing(t *testing.T) {
	e := newEnv(t)
	ok, bad := 500, 1000
	n := len(e.f.commands())
	if _, err := e.s.Apply(ctx(), "1999", Patch{TxA: &ok, RxVoice: &bad}); code(err) != CodeInvalid {
		t.Fatal(err)
	}
	if _, err := e.s.Apply(ctx(), "1999", Patch{}); code(err) != CodeInvalid {
		t.Fatal(err)
	}
	if len(e.f.commands()) != n {
		t.Fatal("commands were sent for an invalid request")
	}
}

func TestApplyReportsFieldErrors(t *testing.T) {
	e := newEnv(t)
	e.f.failCmd["susb tune menu-support c600"] = []string{"Channel 1999: Entry Error, Rx Channel Level setting not changed"}
	rx, txa := 600, 410
	res, err := e.s.Apply(ctx(), "1999", Patch{RxVoice: &rx, TxA: &txa})
	if err != nil || len(res.Errors) != 1 || res.Errors[0].Field != "rxVoice" || len(res.Applied) != 1 || res.Applied[0].Field != "txA" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestRevertWithoutBaseline(t *testing.T) {
	e := newEnv(t)
	if _, err := e.s.Revert(ctx(), "1999"); code(err) != CodeNoBaseline {
		t.Fatal(err)
	}
}

func TestMeterCachesAndServesBusy(t *testing.T) {
	e := newEnv(t)
	e.s.MeterMinGap = time.Hour
	m, err := e.s.Meter(ctx(), "1999")
	if err != nil || m.Peak != -76.3 {
		t.Fatalf("%+v %v", m, err)
	}
	n := len(e.f.commands())
	if _, err := e.s.Meter(ctx(), "1999"); err != nil || len(e.f.commands()) != n {
		t.Fatal("not served from the cache")
	}
	// while another operation holds the interface the last reading is served
	e.s.MeterMinGap = 0
	e.s.mu.Lock()
	m, err = e.s.Meter(ctx(), "1999")
	e.s.mu.Unlock()
	if err != nil || !m.Stale {
		t.Fatalf("%+v %v", m, err)
	}
	// and with nothing cached it is a clean error
	e.s.mu.Lock()
	_, err = e.s.Meter(ctx(), "1998")
	e.s.mu.Unlock()
	if code(err) != CodeUnavailable {
		t.Fatal(err)
	}
}

func TestMeterUnexpectedOutput(t *testing.T) {
	e := newEnv(t)
	e.f.failCmd["susb tune menu-support Y"] = []string{"Tx not keyed"}
	if _, err := e.s.Meter(ctx(), "1999"); code(err) != CodeUnexpected {
		t.Fatal(err)
	}
}

func TestEchoTimeoutAndOff(t *testing.T) {
	e := newEnv(t)
	e.s.EchoTimeout = 1500 * time.Millisecond
	es, err := e.s.SetEcho(ctx(), "1999", true)
	if err != nil || !es.On || es.ExpiresAt.IsZero() || !e.f.echoOn("1999") {
		t.Fatalf("%+v %v", es, err)
	}
	deadline := time.Now().Add(6 * time.Second)
	for e.f.echoOn("1999") && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	on := e.f.echoOn("1999")
	if on || e.s.EchoStatus("1999").On {
		t.Fatal("echo mode not switched off by the timeout")
	}
	// explicit off
	if _, err := e.s.SetEcho(ctx(), "1999", true); err != nil {
		t.Fatal(err)
	}
	if es, err := e.s.SetEcho(ctx(), "1999", false); err != nil || es.On || e.f.echoOn("1999") {
		t.Fatalf("%+v %v", es, err)
	}
}

func TestEchoOffWhenPageGone(t *testing.T) {
	e := newEnv(t)
	e.s.EchoIdle = 1200 * time.Millisecond
	if _, err := e.s.SetEcho(ctx(), "1999", true); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(6 * time.Second)
	for e.s.EchoStatus("1999").On && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	on := e.f.echoOn("1999")
	if on {
		t.Fatal("echo mode stayed on without a page")
	}
}

func TestEchoKeptWhileMeterPolls(t *testing.T) {
	e := newEnv(t)
	e.s.EchoIdle = 1500 * time.Millisecond
	if _, err := e.s.SetEcho(ctx(), "1999", true); err != nil {
		t.Fatal(err)
	}
	end := time.Now().Add(3 * time.Second)
	for time.Now().Before(end) {
		if _, err := e.s.Meter(ctx(), "1999"); err != nil {
			t.Fatal(err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !e.s.EchoStatus("1999").On {
		t.Fatal("echo switched off while the page is open")
	}
}

func TestEchoAdoptedWhenFoundOn(t *testing.T) {
	e := newEnv(t)
	e.f.dev["1999"].echo = true // switched on from the terminal
	e.s.EchoIdle = 0
	e.s.EchoTimeout = 1200 * time.Millisecond
	st, err := e.s.Read(ctx(), "1999")
	if err != nil || !st.Params.Echo || !st.Echo.On {
		t.Fatalf("%+v %v", st, err)
	}
	deadline := time.Now().Add(6 * time.Second)
	for e.s.EchoStatus("1999").On && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	on := e.f.echoOn("1999")
	if on {
		t.Fatal("adopted echo mode was not switched off")
	}
}

func TestCloseSwitchesEchoOff(t *testing.T) {
	e := newEnv(t)
	if _, err := e.s.SetEcho(ctx(), "1999", true); err != nil {
		t.Fatal(err)
	}
	e.s.Close(ctx())
	if e.f.echoOn("1999") {
		t.Fatal("echo on after Close")
	}
}

func TestSaveRecordsAndSwitchesEchoOff(t *testing.T) {
	e := newEnv(t)
	if _, err := e.s.Read(ctx(), "1999"); err != nil {
		t.Fatal(err)
	}
	rx := 600
	if _, err := e.s.Apply(ctx(), "1999", Patch{RxVoice: &rx}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.SetEcho(ctx(), "1999", true); err != nil {
		t.Fatal(err)
	}
	e.f.saveWrites = func(dev string, d *fakeDev) {
		e.c.set("simpleusb.conf", strings.Replace(simpleConf, "rxmixerset = 650", "rxmixerset = 600", 1))
	}
	res, err := e.s.Save(ctx(), "1999", "alice smith")
	if err != nil || !res.Saved || res.Warning != "" || res.Commit != "abc1234" {
		t.Fatalf("%+v %v", res, err)
	}
	if len(res.Changed) != 1 || res.Changed[0] != "rxVoice" {
		t.Fatalf("changed: %v", res.Changed)
	}
	if e.f.echoOn("1999") || e.s.EchoStatus("1999").On {
		t.Fatal("echo left on by a save")
	}
	if len(e.r.calls) != 1 || e.r.calls[0] != "simpleusb.conf|Radio tuning saved from web UI (device 1999)|alice_smith" {
		t.Fatalf("%v", e.r.calls)
	}
	// the saved state is the new baseline: nothing is unsaved now
	st, err := e.s.Read(ctx(), "1999")
	if err != nil || len(st.Dirty) != 0 || st.Saved.RxVoice != 600 {
		t.Fatalf("%+v %v", st, err)
	}
	// and the commands of the save are the closed set
	for _, c := range e.f.commands() {
		if !Allowed(c) {
			t.Errorf("command outside the set: %q", c)
		}
	}
}

func TestSaveWarnings(t *testing.T) {
	e := newEnv(t)
	if _, err := e.s.Read(ctx(), "1999"); err != nil {
		t.Fatal(err)
	}
	// the file does not show the running value (save may have failed)
	rx := 600
	if _, err := e.s.Apply(ctx(), "1999", Patch{RxVoice: &rx}); err != nil {
		t.Fatal(err)
	}
	e.r.err = errors.New("helper down")
	res, err := e.s.Save(ctx(), "1999", "")
	if err != nil || !res.Saved {
		t.Fatalf("%+v %v", res, err)
	}
	if !strings.Contains(res.Warning, "rxVoice") || !strings.Contains(res.Warning, "config history") || res.Commit != "" {
		t.Fatalf("warning: %q", res.Warning)
	}
	if e.r.calls[0][len(e.r.calls[0])-6:] != "|aslcp" {
		t.Fatalf("author: %v", e.r.calls)
	}
	// a driver that does not confirm is an error and nothing is recorded
	e2 := newEnv(t)
	e2.f.saveReply = "something else"
	if _, err := e2.s.Save(ctx(), "1999", "a"); code(err) != CodeCommandFailed {
		t.Fatal(err)
	}
	if len(e2.r.calls) != 0 {
		t.Fatal("recorded a failed save")
	}
}

func TestAMIErrors(t *testing.T) {
	e := newEnv(t)
	e.s.AMI = errCommander{}
	if _, err := e.s.Devices(ctx()); code(err) != CodeUnavailable {
		t.Fatal(err)
	}
}

type errCommander struct{}

func (errCommander) Command(context.Context, string) ([]string, error) {
	return nil, context.DeadlineExceeded
}

func TestOnlyAllowedCommandsAreSent(t *testing.T) {
	e := newEnv(t)
	rx, tx, b, c := 1, 2, true, "pp"
	rxd := 40
	steps := func() {
		_, _ = e.s.Read(ctx(), "1999")
		_, _ = e.s.Apply(ctx(), "1999", Patch{RxVoice: &rx, TxA: &tx, TxB: &tx, RxBoost: &b, PreEmphasis: &b, DeEmphasis: &b, PlFilter: &b,
			PttInvert: &b, CarrierFrom: &c, CtcssFrom: &c, RxOnDelayMs: &rxd, TxOffDelayMs: &rxd})
		_, _ = e.s.Revert(ctx(), "1999")
		_, _ = e.s.Meter(ctx(), "1999")
		_, _ = e.s.SetEcho(ctx(), "1999", true)
		_, _ = e.s.SetEcho(ctx(), "1999", false)
		_, _ = e.s.Save(ctx(), "1999", "a")
	}
	steps()
	if len(e.f.commands()) < 30 {
		t.Fatalf("only %d commands", len(e.f.commands()))
	}
	for _, c := range e.f.commands() {
		if !Allowed(c) {
			t.Errorf("command outside the set: %q", c)
		}
	}
}
