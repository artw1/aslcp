package helper

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const talkSTFU = `I: 2026-11-21 02:58:21.706 DMR, ODMR Begin Tx: src = 1234567, dst = 91 (GROUP)
I: 2026-11-21 02:58:24.195 DMR, ODMR End Tx:DMR frame count was 42 frames
I: 2026-11-21 02:58:55.184 taFormat = 2
I: 2026-11-21 02:58:55.184 TA = N0CALL Alex
M: 2026-11-20 17:28:02.023 DMR, Setting [STFU] Password -> example-pass
I: 2026-11-21 03:10:00.000 DMR, ODMR Begin Tx: src = 1234568, dst = 1998 (PRIVATE)
I: 2026-11-21 03:10:01.500 DMR, ODMR End Tx:DMR frame count was 0 frames
I: 2026-11-21 03:20:00.000 DMR, ODMR Begin Tx: src = 1234569, dst = 91 (GROUP)
`

func TestParseSTFUTalks(t *testing.T) {
	got := parseSTFUTalks([]byte(talkSTFU))
	if len(got) != 3 {
		t.Fatalf("talks = %+v", got)
	}
	a := got[0]
	if a.DMRID != "1234567" || a.Dst != "91" || a.Private || a.Kind != DVTalkDMR || a.Via != DVViaSTFU || a.Frames != 42 {
		t.Fatalf("first = %+v", a)
	}
	st, _ := time.Parse(time.RFC3339Nano, a.Start)
	en, _ := time.Parse(time.RFC3339Nano, a.End)
	if st.Format("2006-01-02T15:04:05.000Z") != "2026-11-21T02:58:21.706Z" || st.Location() != time.UTC {
		t.Fatalf("start = %s (the log is UTC)", a.Start)
	}
	if d := en.Sub(st); d != 42*60*time.Millisecond {
		t.Fatalf("duration = %v, want 42 frames at 60 ms", d)
	}
	// no frame count: the end line's own time
	b := got[1]
	bs, _ := time.Parse(time.RFC3339Nano, b.Start)
	be, _ := time.Parse(time.RFC3339Nano, b.End)
	if !b.Private || be.Sub(bs) != 1500*time.Millisecond {
		t.Fatalf("second = %+v", b)
	}
	// still open: no end
	if got[2].End != "" || got[2].DMRID != "1234569" {
		t.Fatalf("third = %+v", got[2])
	}
	// talker alias lines are ignored and the password line never leaks
	for _, x := range got {
		if x.Call != "" {
			t.Fatalf("callsign from the alias: %+v", x)
		}
	}
}

func TestParseSTFUTalksBeginAfterBegin(t *testing.T) {
	got := parseSTFUTalks([]byte("I: 2026-11-21 03:00:00.000 DMR, ODMR Begin Tx: src = 1234567, dst = 91 (GROUP)\n" +
		"I: 2026-11-21 03:00:05.000 DMR, ODMR Begin Tx: src = 1234568, dst = 91 (GROUP)\n" +
		"I: 2026-11-21 03:00:06.000 DMR, ODMR End Tx:DMR frame count was 10 frames\n" +
		"I: 2026-11-21 03:00:07.000 DMR, ODMR End Tx:DMR frame count was 10 frames\n"))
	if len(got) != 2 || got[0].End != "" || got[1].End == "" {
		t.Fatalf("got %+v", got)
	}
}

const talkAB = `I: 2026-11-21 02:58:21.700 Begin TX: src=1234567 rpt=123456701 dst=91 slot=2 cc=1 call=
I: 2026-11-21 03:05:00.000 ambeMode = YSFN
I: 2026-11-21 03:05:10.337 Begin TX: src=1234571 rpt=123456701 dst=9 slot=2 cc=0 call=N1CALL
`

func TestParseABTalksAndMerge(t *testing.T) {
	ab := parseABTalks([]byte(talkAB), "BM")
	if len(ab) != 2 || ab[0].Kind != DVTalkDMR || ab[1].Kind != DVTalkYSF || ab[1].Call != "N1CALL" {
		t.Fatalf("ab = %+v", ab)
	}
	if ab[1].Start != "2026-11-21T03:05:10Z" {
		t.Fatalf("start = %s (UTC, like the DMR status)", ab[1].Start)
	}
	// without an ambeMode line the panel decides
	if k := parseABTalks([]byte("I: 2026-11-21 03:05:10.337 Begin TX: src=1 rpt=1 dst=9 slot=2 cc=0 call=N1CALL\n"), "YSF"); len(k) != 1 || k[0].Kind != DVTalkYSF {
		t.Fatalf("panel fallback = %+v", k)
	}
	all := mergeTalks(parseSTFUTalks([]byte(talkSTFU)), ab)
	// the AB begin of the STFU call (same ID, 6 ms apart) is dropped; the YSF one stays
	var ysf, dup int
	for _, x := range all {
		if x.Kind == DVTalkYSF {
			ysf++
		}
		if x.DMRID == "1234567" {
			dup++
		}
	}
	if ysf != 1 || dup != 1 || len(all) != 4 {
		t.Fatalf("merged = %+v", all)
	}
	if all[0].Via != DVViaSTFU || all[1].Kind != DVTalkYSF {
		t.Fatalf("order = %+v", all)
	}
}

func TestCollectTalks(t *testing.T) {
	dir := t.TempDir()
	o := &Ops{DVSTFULog: filepath.Join(dir, "STFU.log"), DVBridgeLogDir: dir, DVHostDir: dir}
	if err := os.WriteFile(o.DVSTFULog, []byte(talkSTFU), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Analog_Bridge.log"), []byte(talkAB), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "DMRIds.dat"), []byte("1234567 N0CALL Alex\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	talks := o.dvCollectTalks("BM", "1998")
	if len(talks) != 4 {
		t.Fatalf("talks = %+v", talks)
	}
	if talks[0].Call != "N0CALL" {
		t.Fatalf("ID list fallback missing: %+v", talks[0])
	}
	if !talks[2].Private { // dst is the gateway ID
		t.Fatalf("private call not flagged: %+v", talks[2])
	}
}
