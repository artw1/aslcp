package helper

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	dmrOKLine   = "M: 2026-11-18 01:23:15.162 DMR, Logged into the master successfully: 3103.master.brandmeister.network:62031\n"
	dmrFailLine = "M: 2026-11-18 01:24:15.162 DMR, Login to the master has failed, retrying login ...\n"
)

// dmrOps is cfgOps with DMR enabled in the ini, a unit started at 01:00 UTC
// and an empty log dir.
func dmrOps(t *testing.T, active string) (*Ops, string) {
	t.Helper()
	o, f := cfgOps(t)
	d, _ := dvFileByID(DVFileMMDVMBridge)
	p := o.dvFilePath(d)
	b, _ := os.ReadFile(p)
	s := strings.Replace(string(b), "[DMR]\nEnable=0", "[DMR]\nEnable=1", 1)
	s = strings.Replace(s, "[DMR Network]\nEnable=0", "[DMR Network]\nEnable=1", 1)
	if err := os.WriteFile(p, []byte(s), 0o640); err != nil {
		t.Fatal(err)
	}
	f.rules["systemctl show --timestamp=utc"] = resp{out: "LoadState=loaded\nActiveState=" + active + "\nActiveEnterTimestamp=Wed 2026-11-18 01:00:00 UTC\n"}
	o.DVLogDir = filepath.Join(t.TempDir(), "mmdvm")
	if err := os.MkdirAll(o.DVLogDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return o, o.DVLogDir
}

func writeLog(t *testing.T, dir, day, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "MMDVM_Bridge-"+day+".log"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func dmrState(t *testing.T, o *Ops) DVDMRStatus {
	t.Helper()
	r, err := o.DVSwitchDMRStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestDMRStatusRealLine(t *testing.T) {
	o, dir := dmrOps(t, "active")
	writeLog(t, dir, "2026-11-18", "I: 2026-11-18 01:00:01.000 Starting\n"+dmrOKLine)
	r := dmrState(t, o)
	if r.State != DMRLoggedIn || r.Master != "3103.master.brandmeister.network:62031" || r.At != "2026-11-18T01:23:15Z" || r.Since != "2026-11-18T01:00:00Z" {
		t.Fatalf("%+v", r)
	}
	if r.LastLine != "DMR, Logged into the master successfully: 3103.master.brandmeister.network:62031" {
		t.Fatalf("lastLine %q", r.LastLine)
	}
}

func TestDMRStatusVerdictLines(t *testing.T) {
	cases := map[string]string{
		"DMR, Login to the master has failed, retrying login ...":                     DMRLoginFailed,
		"DMR, Login to the master has failed, retrying network ...":                   DMRLoginFailed,
		"DMR, Connection to the master has timed out, retrying connection":            DMRTimedOut,
		"DMR, Socket has failed, retrying connection to the master":                   DMRSocketFailed,
		"DMR, Socket has failed when writing data to the master, retrying connection": DMRSocketFailed,
		"DMR, Master is closing down":                                                 DMRMasterClosing,
	}
	for msg, want := range cases {
		o, dir := dmrOps(t, "active")
		writeLog(t, dir, "2026-11-18", "M: 2026-11-18 01:10:00.000 "+msg+"\n")
		if r := dmrState(t, o); r.State != want || r.Master != "" || r.LastLine != msg {
			t.Errorf("%q: %+v", msg, r)
		}
	}
}

func TestDMRStatusLatestWins(t *testing.T) {
	o, dir := dmrOps(t, "active")
	writeLog(t, dir, "2026-11-18", dmrFailLine+dmrOKLine+"M: 2026-11-18 01:30:00.000 DMR, Slot 2, received network voice header\n")
	if r := dmrState(t, o); r.State != DMRLoggedIn {
		t.Fatalf("%+v", r)
	}
	writeLog(t, dir, "2026-11-18", dmrOKLine+dmrFailLine)
	if r := dmrState(t, o); r.State != DMRLoginFailed || r.Master != "" {
		t.Fatalf("%+v", r)
	}
}

func TestDMRStatusIgnoresBeforeStart(t *testing.T) {
	o, dir := dmrOps(t, "active")
	// a success from before the restart at 01:00 must not count
	writeLog(t, dir, "2026-11-18", "M: 2026-11-18 00:59:59.900 DMR, Logged into the master successfully: 3103.master.brandmeister.network:62031\n")
	if r := dmrState(t, o); r.State != DMRConnecting || r.At != "" {
		t.Fatalf("%+v", r)
	}
	// a line in the very second of the start counts
	writeLog(t, dir, "2026-11-18", "M: 2026-11-18 01:00:00.100 DMR, Login to the master has failed, retrying login ...\n")
	if r := dmrState(t, o); r.State != DMRLoginFailed {
		t.Fatalf("%+v", r)
	}
}

func TestDMRStatusMidnight(t *testing.T) {
	o, dir := dmrOps(t, "active")
	// the unit started at 23:50 UTC yesterday: the verdict is in yesterday's file
	o.Run.(*fakeRunner).rules["systemctl show --timestamp=utc"] = resp{out: "LoadState=loaded\nActiveState=active\nActiveEnterTimestamp=Tue 2026-11-17 23:50:00 UTC\n"}
	writeLog(t, dir, "2026-11-17", "M: 2026-11-17 23:55:00.000 DMR, Logged into the master successfully: 3103.master.brandmeister.network:62031\n")
	writeLog(t, dir, "2026-11-18", "I: 2026-11-18 00:00:00.000 Opening log\n")
	if r := dmrState(t, o); r.State != DMRLoggedIn || r.At != "2026-11-17T23:55:00Z" {
		t.Fatalf("%+v", r)
	}
	// a later verdict in the new file wins over the older file
	writeLog(t, dir, "2026-11-18", "M: 2026-11-18 00:05:00.000 DMR, Connection to the master has timed out, retrying connection\n")
	if r := dmrState(t, o); r.State != DMRTimedOut {
		t.Fatalf("%+v", r)
	}
	// only the newest two files are read: an old third file is ignored
	writeLog(t, dir, "2026-11-16", "M: 2026-11-18 00:10:00.000 DMR, Master is closing down\n")
	if r := dmrState(t, o); r.State != DMRTimedOut {
		t.Fatalf("%+v", r)
	}
}

func TestDMRStatusMissingAndSymlinks(t *testing.T) {
	o, dir := dmrOps(t, "active")
	if r := dmrState(t, o); r.State != DMRConnecting {
		t.Fatalf("empty dir: %+v", r)
	}
	o.DVLogDir = filepath.Join(dir, "nope")
	if r := dmrState(t, o); r.State != DMRConnecting {
		t.Fatalf("missing dir: %+v", r)
	}
	o.DVLogDir = dir
	// a symlink named like a log is never followed, even to a real file
	target := filepath.Join(t.TempDir(), "secret.log")
	os.WriteFile(target, []byte(dmrOKLine), 0o644)
	if err := os.Symlink(target, filepath.Join(dir, "MMDVM_Bridge-2026-11-18.log")); err != nil {
		t.Fatal(err)
	}
	if r := dmrState(t, o); r.State != DMRConnecting {
		t.Fatalf("symlink: %+v", r)
	}
	// a directory with a log name and a FIFO are not regular files
	os.Mkdir(filepath.Join(dir, "MMDVM_Bridge-2026-11-19.log"), 0o755)
	if r := dmrState(t, o); r.State != DMRConnecting {
		t.Fatalf("dir: %+v", r)
	}
	// the raw reader refuses a symlink too (the dir listing race)
	if _, ok := dvReadTail(filepath.Join(dir, "MMDVM_Bridge-2026-11-18.log")); ok {
		t.Fatal("dvReadTail followed a symlink")
	}
	// other names are ignored
	os.WriteFile(filepath.Join(dir, "MMDVM_Bridge-2026-11-20.log.1"), []byte(dmrOKLine), 0o644)
	os.WriteFile(filepath.Join(dir, "Other-2026-11-20.log"), []byte(dmrOKLine), 0o644)
	if r := dmrState(t, o); r.State != DMRConnecting {
		t.Fatalf("names: %+v", r)
	}
}

func TestDMRStatusTail(t *testing.T) {
	o, dir := dmrOps(t, "active")
	// the verdict is older than the tail window: it is cut off; a cut first
	// line is never parsed
	pad := strings.Repeat("M: 2026-11-18 01:30:00.000 DMR, Slot 2, received network voice header from N0CALL to TG 9\n", 4000)
	writeLog(t, dir, "2026-11-18", dmrOKLine+pad)
	if r := dmrState(t, o); r.State != DMRConnecting {
		t.Fatalf("old verdict beyond the tail: %+v", r)
	}
	writeLog(t, dir, "2026-11-18", pad+dmrFailLine)
	if r := dmrState(t, o); r.State != DMRLoginFailed {
		t.Fatalf("%+v", r)
	}
	// a huge line without a newline is bounded and does not hang or match
	writeLog(t, dir, "2026-11-18", strings.Repeat("x", 600<<10))
	if r := dmrState(t, o); r.State != DMRConnecting {
		t.Fatalf("%+v", r)
	}
}

func TestDMRStatusRedaction(t *testing.T) {
	o, dir := dmrOps(t, "active")
	writeLog(t, dir, "2026-11-18", "M: 2026-11-18 01:10:00.000 DMR, Login to the master has failed, id 1234567 or 123456789 retrying login ...\n")
	r := dmrState(t, o)
	if r.State != DMRLoginFailed || strings.Contains(r.LastLine, "1234567") || strings.Contains(r.LastLine, "123456789") || !strings.Contains(r.LastLine, "***") {
		t.Fatalf("%+v", r)
	}
	// a short number stays
	writeLog(t, dir, "2026-11-18", "M: 2026-11-18 01:10:00.000 DMR, Login to the master has failed, port 62031 retrying login ...\n")
	if r := dmrState(t, o); !strings.Contains(r.LastLine, "62031") {
		t.Fatalf("%+v", r)
	}
	// the master host is redacted as well
	writeLog(t, dir, "2026-11-18", "M: 2026-11-18 01:10:00.000 DMR, Logged into the master successfully: 1234567.example.org:62031\n")
	if r := dmrState(t, o); r.State != DMRLoggedIn || strings.Contains(r.Master, "1234567") || strings.Contains(r.LastLine, "1234567") {
		t.Fatalf("%+v", r)
	}
	// a hostile "master" is not echoed
	writeLog(t, dir, "2026-11-18", "M: 2026-11-18 01:10:00.000 DMR, Logged into the master successfully: <script>:1\n")
	if r := dmrState(t, o); r.Master != "" {
		t.Fatalf("%+v", r)
	}
	// the password never reaches the reply: it is not in the log lines and the
	// ini is only read for the enable flags
	jb, _ := json.Marshal(r)
	if b := string(jb); strings.Contains(b, dvTestPass) || strings.Contains(strings.ToLower(b), "password") {
		t.Fatal(b)
	}
}

func TestDMRStatusStates(t *testing.T) {
	// not installed: unit not loaded
	o, _ := dmrOps(t, "active")
	o.Run.(*fakeRunner).rules["systemctl show --timestamp=utc"] = resp{out: "LoadState=not-found\nActiveState=inactive\nActiveEnterTimestamp=\n"}
	if r := dmrState(t, o); r.State != DMRNotInstalled {
		t.Fatalf("%+v", r)
	}
	// not installed: ini missing
	o, _ = dmrOps(t, "active")
	d, _ := dvFileByID(DVFileMMDVMBridge)
	os.Remove(o.dvFilePath(d))
	if r := dmrState(t, o); r.State != DMRNotInstalled {
		t.Fatalf("%+v", r)
	}
	// disabled: DMR off wins over a stopped unit
	o, _ = dmrOps(t, "inactive")
	b, _ := os.ReadFile(o.dvFilePath(d))
	os.WriteFile(o.dvFilePath(d), []byte(strings.Replace(string(b), "[DMR]\nEnable=1", "[DMR]\nEnable=0", 1)), 0o640)
	if r := dmrState(t, o); r.State != DMRDisabled {
		t.Fatalf("%+v", r)
	}
	// not running
	o, dir := dmrOps(t, "inactive")
	writeLog(t, dir, "2026-11-18", dmrOKLine)
	if r := dmrState(t, o); r.State != DMRNotRunning || r.Since == "" {
		t.Fatalf("%+v", r)
	}
	// activating counts as running; an unknown start time gives no verdict
	o, dir = dmrOps(t, "activating")
	writeLog(t, dir, "2026-11-18", dmrOKLine)
	if r := dmrState(t, o); r.State != DMRLoggedIn {
		t.Fatalf("%+v", r)
	}
	o.Run.(*fakeRunner).rules["systemctl show --timestamp=utc"] = resp{out: "LoadState=loaded\nActiveState=active\nActiveEnterTimestamp=Wed 2026-11-18 01:00:00 CET\n"}
	if r := dmrState(t, o); r.State != DMRConnecting {
		t.Fatalf("non-UTC start must not be guessed: %+v", r)
	}
	// systemctl failing means not installed
	o.Run.(*fakeRunner).rules["systemctl show --timestamp=utc"] = resp{err: fmt.Errorf("boom")}
	if r := dmrState(t, o); r.State != DMRNotInstalled {
		t.Fatalf("%+v", r)
	}
}

func TestDMRStatusOpAndArgs(t *testing.T) {
	o, dir := dmrOps(t, "active")
	writeLog(t, dir, "2026-11-18", dmrOKLine)
	v, err := o.HandleRequest(context.Background(), Request{Op: OpDVSwitchDMRStatus})
	if err != nil || v.(DVDMRStatus).State != DMRLoggedIn {
		t.Fatalf("%v %+v", err, v)
	}
	// it takes no argument
	if _, err := o.HandleRequest(context.Background(), Request{Op: OpDVSwitchDMRStatus, DVSwitch: &DVSwitchRequest{}}); err == nil {
		t.Fatal("dvswitch field accepted")
	}
	if !knownOp(OpDVSwitchDMRStatus) {
		t.Fatal("not a known op")
	}
}

func TestDMRRestartScope(t *testing.T) {
	o, f := cfgOps(t)
	stfuUnitDisabled(f) // with stfu enabled, see TestBMRestartScope
	r, err := o.DVSwitchRestart(context.Background(), &DVSwitchRequest{Scope: "dmr"})
	if err != nil || len(r.Units) != 1 || r.Units[0] != "mmdvm_bridge" {
		t.Fatalf("%v %+v", err, r)
	}
	var restart string
	for _, c := range f.calls {
		if strings.HasPrefix(c, "systemctl restart") {
			restart = c
		}
	}
	if restart != "systemctl restart mmdvm_bridge.service" {
		t.Fatalf("%q", restart)
	}
	r, err = o.DVSwitchRestart(context.Background(), &DVSwitchRequest{Scope: "dmr", Action: "start"})
	if err != nil || r.Action != "start" || len(r.Units) != 1 {
		t.Fatalf("%v %+v", err, r)
	}
	if _, err := o.DVSwitchRestart(context.Background(), &DVSwitchRequest{Scope: "mmdvm_bridge"}); err == nil {
		t.Fatal("a unit name is not a scope")
	}
}

func TestDemoDMRStatusCycle(t *testing.T) {
	d := NewDemoDVSwitch()
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	d.now = func() time.Time { return now }
	ctx := context.Background()
	st := func() DVDMRStatus { r, _ := d.DVSwitchDMRStatus(ctx); return r }
	if st().State != DMRNotInstalled {
		t.Fatal("not installed first")
	}
	d.installed, d.deps = true, true
	if r := st(); r.State != DMRLoggedIn || r.Master != "192.0.2.10:62031" {
		t.Fatalf("%+v", r)
	}
	want := []string{DMRLoginFailed, DMRTimedOut, DMRSocketFailed, DMRLoggedIn}
	for _, w := range want {
		if _, err := d.DVSwitchRestart(ctx, DVSwitchRequest{Scope: "dmr"}); err != nil {
			t.Fatal(err)
		}
		if r := st(); r.State != DMRConnecting {
			t.Fatalf("connecting first: %+v", r)
		}
		now = now.Add(demoDMRConnect)
		if r := st(); r.State != w {
			t.Fatalf("want %s: %+v", w, r)
		}
	}
	// a restart of the gateways leaves the login alone
	before := d.dmrRuns
	d.DVSwitchRestart(ctx, DVSwitchRequest{Scope: "gateways"})
	if d.dmrRuns != before {
		t.Fatal("gateways restart touched the DMR login")
	}
	// DMR off
	d.cfg().modes.DMR = false
	if st().State != DMRDisabled {
		t.Fatal("disabled")
	}
}

func TestDemoOpsLogDirIsTemp(t *testing.T) {
	dir := t.TempDir()
	o, err := NewDemoOps(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(o.DVLogDir, dir) || o.dvLogDir() == DefaultDVLogDir {
		t.Fatalf("demo reads %q", o.dvLogDir())
	}
}
