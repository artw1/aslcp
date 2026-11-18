package helper

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const stfuShowCmd = "systemctl show --timestamp=utc -p LoadState,ActiveState,ActiveEnterTimestamp stfu.service"

func stfuShowState(f *fakeRunner, active string) {
	f.mu.Lock()
	f.rules[stfuShowCmd] = resp{out: "LoadState=loaded\nActiveState=" + active + "\nActiveEnterTimestamp=Wed 2026-11-18 18:08:50 UTC\n"}
	f.mu.Unlock()
}

// demandEnv: a fake Analog_Bridge in mode, BrandMeister set up on demand,
// stfu stopped. Starting stfu (systemctl start) makes it active and, when
// login is set, writes the login log; sleeps counts the injected waits.
func demandEnv(t *testing.T, mode string, login bool) (*Ops, *fakeRunner, *fakeAB, *int) {
	t.Helper()
	o, f, ab := stfuTuneEnv(t, mode)
	sleeps := new(int)
	o.DVSleep = func(context.Context, time.Duration) { *sleeps++ }
	f.rules["systemctl stop"] = resp{}
	stfuShowState(f, "inactive")
	f.hook = func(cmd string) {
		switch cmd {
		case "systemctl start stfu.service":
			stfuShowState(f, "active")
			if login {
				_ = os.WriteFile(o.DVSTFULog, []byte(stfuLog), 0o600)
			}
		case "systemctl stop stfu.service":
			stfuShowState(f, "inactive")
		}
	}
	return o, f, ab, sleeps
}

// stfuTuneEnv is tuneEnv with BrandMeister configured (password, ID).
func stfuTuneEnv(t *testing.T, mode string) (*Ops, *fakeRunner, *fakeAB) {
	t.Helper()
	o, ab := tuneEnv(t, mode, "")
	f := o.Run.(*fakeRunner)
	o.DVSTFULog = filepath.Join(t.TempDir(), "dvswitch", "STFU.log")
	if err := os.MkdirAll(filepath.Dir(o.DVSTFULog), 0o755); err != nil {
		t.Fatal(err)
	}
	writeDVIni(t, o, DVFileDVSwitch, "STFU", "BMPassword", "example-pass")
	writeDVIni(t, o, DVFileDVSwitch, "STFU", "UserID", "1234567")
	return o, f, ab
}

func tuneTo(t *testing.T, o *Ops, mode, tune string) DVTuneResult {
	t.Helper()
	res, err := o.DVSwitchTune(context.Background(), &DVSwitchRequest{Tune: &DVTune{Mode: mode, Tune: tune}})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestSTFULogBinaryIP(t *testing.T) {
	for _, tail := range []string{"", "\x00\xc0\x00\x02\x0a", "\xc0\x00\x02\x01", "\x00\x00\x00\x00"} {
		var st DVDMRStatus
		parseSTFULog([]byte("I0 2026-11-18 18:08:53.823 Successful connection to BM server at ip address:"+tail+"\n"), &st)
		if st.State != DMRLoggedIn {
			t.Errorf("tail %q: %q", tail, st.State)
		}
		if strings.ContainsAny(st.LastLine, "\x00\xc0") {
			t.Errorf("binary in the last line: %q", st.LastLine)
		}
	}
	// a password split by a control byte is still dropped
	var st DVDMRStatus
	parseSTFULog([]byte("I: 2026-11-18 18:08:53.402 DMR, Setting [STFU] pass\x00word -> example-pass\n"), &st)
	if st.LastLine != "" {
		t.Fatalf("%q", st.LastLine)
	}
}

func TestSTFULogRestartDropsOldLogin(t *testing.T) {
	// a login from an earlier run, then the new run's settings: connecting
	var st DVDMRStatus
	parseSTFULog([]byte("I0 2026-11-18 17:00:00.000 Successful connection to BM server at ip address:\n"+
		"I: 2026-11-18 18:08:53.400 DMR, Setting [STFU] BMAddress -> 3102.repeater.net\n"), &st)
	if st.State != DMRConnecting {
		t.Fatal(st.State)
	}
	parseSTFULog([]byte("I0 2026-11-18 18:08:53.823 Successful connection to BM server at ip address:\n"), &st)
	if st.State != DMRLoggedIn {
		t.Fatal(st.State)
	}
}

func TestTuneEnterBMStartsAndWaits(t *testing.T) {
	o, f, ab, sleeps := demandEnv(t, "DMR", true)
	res := tuneTo(t, o, "BM", "91")
	if !res.Switched || res.BMLoginPending {
		t.Fatalf("%+v", res)
	}
	if f.called("systemctl start stfu.service") != 1 || *sleeps != 0 {
		t.Fatalf("start %d sleeps %d", f.called("systemctl start stfu.service"), *sleeps)
	}
	if got, want := strings.Join(ab.recv(), "|"), ab.switchCmds("BM")+"|txTg=91|info"; got != want {
		t.Fatalf("datagrams %q, want %q", got, want)
	}
	// already in BM and running: nothing is started again
	tuneTo(t, o, "BM", "92")
	if f.called("systemctl start stfu.service") != 1 {
		t.Fatal("started again")
	}
}

func TestTuneEnterBMLoginTimeout(t *testing.T) {
	o, f, ab, sleeps := demandEnv(t, "DMR", false)
	res := tuneTo(t, o, "BM", "91")
	if !res.Switched || !res.BMLoginPending {
		t.Fatalf("%+v", res)
	}
	if *sleeps != 40 { // 10 s in 250 ms polls
		t.Fatalf("sleeps %d", *sleeps)
	}
	if got, want := strings.Join(ab.recv(), "|"), ab.switchCmds("BM")+"|txTg=91|info"; got != want {
		t.Fatalf("datagrams %q, want %q", got, want)
	}
	if f.called("systemctl stop") != 0 {
		t.Fatal("stopped on entering")
	}
}

func TestTuneLeaveBMStops(t *testing.T) {
	o, f, _, _ := demandEnv(t, "DMR", true)
	tuneTo(t, o, "BM", "91")
	res := tuneTo(t, o, "DMR", "4000")
	if !res.Switched || res.Left != DVModeBM || res.STFUStopFailed || f.called("systemctl stop stfu.service") != 1 {
		t.Fatalf("%+v stop %d", res, f.called("systemctl stop stfu.service"))
	}
	// stop is ordered after the repoint: the unlink went out first
	// a failed stop is reported, the switch still happens
	tuneTo(t, o, "BM", "91")
	f.mu.Lock()
	f.rules["systemctl stop"] = resp{code: 1}
	f.mu.Unlock()
	f.hook = nil
	stfuShowState(f, "active")
	res = tuneTo(t, o, "P25", "10100")
	if !res.Switched || !res.STFUStopFailed {
		t.Fatalf("%+v", res)
	}
}

func TestTuneStayLoggedInNeverStops(t *testing.T) {
	o, f, _, _ := demandEnv(t, "DMR", true)
	if err := o.dvSTFUStaySet(true); err != nil {
		t.Fatal(err)
	}
	stfuShowState(f, "active")
	tuneTo(t, o, "BM", "91")
	tuneTo(t, o, "DMR", "4000")
	if f.called("systemctl start") != 0 || f.called("systemctl stop") != 0 {
		t.Fatalf("calls %v", f.calls)
	}
}

func TestResumeEnsuresStopped(t *testing.T) {
	// a resume to the mode the panel is already in still stops a running stfu
	o, f, _, _ := demandEnv(t, "DMR", true)
	stfuShowState(f, "active")
	res, err := o.DVSwitchTune(context.Background(), &DVSwitchRequest{Tune: &DVTune{Mode: "DMR", ModeOnly: true}})
	if err != nil || res.Switched || f.called("systemctl stop stfu.service") != 1 {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestBMStatusIdleWhileStopped(t *testing.T) {
	o, f, _, _ := demandEnv(t, "DMR", true)
	st, err := o.DVSwitchDMRStatus(context.Background())
	if err != nil || st.BM == nil || st.BM.State != DMRIdle {
		t.Fatalf("%+v %v", st.BM, err)
	}
	_ = f
	tuneTo(t, o, "BM", "91")
	st, _ = o.DVSwitchDMRStatus(context.Background())
	if st.BM == nil || st.BM.State != DMRLoggedIn {
		t.Fatalf("%+v", st.BM)
	}
}
