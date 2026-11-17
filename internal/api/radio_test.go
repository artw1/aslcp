package api_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/artw1/aslcp/internal/helper"
	"github.com/artw1/aslcp/internal/susb"
)

// radioFake is a minimal chan_simpleusb behind the AMI command interface.
type radioFake struct {
	mu     sync.Mutex
	rx     int
	echo   bool
	cmds   []string
	active string
}

func (f *radioFake) Command(_ context.Context, cmd string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cmds = append(f.cmds, cmd)
	switch {
	case cmd == "susb active":
		return []string{"Active Simple USB Radio device is [" + f.active + "]."}, nil
	case cmd == "susb active show":
		return []string{"Device [1999] is assigned to device=1-1.1 card=1"}, nil
	case strings.HasPrefix(cmd, "susb active "):
		f.active = strings.TrimPrefix(cmd, "susb active ")
		return []string{"Active (command) Simple USB Radio device set to [" + f.active + "]"}, nil
	case cmd == "susb tune save":
		return []string{"Saved radio tuning settings to simpleusb.conf"}, nil
	case cmd == "susb tune menu-support 1":
		return []string{"1999"}, nil
	case cmd == "susb tune menu-support 2":
		return []string{"Active radio interface is [1999]", "Device String is 1-1.1", "Card is 1"}, nil
	case cmd == "susb tune menu-support 0+4":
		e := "0"
		if f.echo {
			e = "1"
		}
		return []string{fmt.Sprintf("500,0,%s,0,1,1,1,0,4,0,0,0,%d,31,37,35", e, f.rx)}, nil
	case cmd == "susb tune menu-support Y":
		return []string{"RxAudioStats: Pk -60.0  Avg Pwr -70  Min -80  Max -50  dBFS  ClipCnt 0"}, nil
	case strings.HasPrefix(cmd, "susb tune menu-support c"):
		f.rx, _ = strconv.Atoi(cmd[len("susb tune menu-support c"):])
		return []string{fmt.Sprintf("Channel 1999: Changed setting on RX Channel to %d", f.rx)}, nil
	case strings.HasPrefix(cmd, "susb tune menu-support k"):
		f.echo = strings.HasSuffix(cmd, "1")
		if f.echo {
			return []string{"Echo Mode changed to Enabled"}, nil
		}
		return []string{"Echo Mode changed to Disabled"}, nil
	case strings.HasPrefix(cmd, "susb tune menu-support "):
		a := strings.TrimPrefix(cmd, "susb tune menu-support ")
		n, _ := strconv.Atoi(a[1:])
		st := map[bool]string{true: "Enabled", false: "Disabled"}[n > 0]
		switch a[0] {
		case 'f':
			return []string{fmt.Sprintf("Channel 1999: Changed setting on TX Channel A to %d", n)}, nil
		case 'g':
			return []string{fmt.Sprintf("Channel 1999: Changed setting on TX Channel B to %d", n)}, nil
		case 'm':
			return []string{"RxBoost changed to " + st}, nil
		case 'n':
			return []string{"Pre-emphasis changed to " + st}, nil
		case 'o':
			return []string{"De-emphasis changed to " + st}, nil
		case 'p':
			return []string{"PL Filter changed to " + st}, nil
		case 'q':
			return []string{"PTT mode changed to " + map[bool]string{true: "Open", false: "Ground"}[n > 0]}, nil
		case 'r':
			return []string{"Carrier From changed to " + []string{"no", "N/A", "N/A", "usb", "usbinvert", "pp", "ppinvert"}[n]}, nil
		case 's':
			return []string{"CTCSS From changed to " + []string{"no", "usb", "usbinvert", "N/A", "pp", "ppinvert"}[n]}, nil
		case 't':
			return []string{fmt.Sprintf("RX On Delay From changed to %d", n)}, nil
		case 'u':
			return []string{fmt.Sprintf("TX Off Delay From changed to %d", n)}, nil
		}
	}
	return []string{"Unknown command"}, nil
}

func (f *radioFake) count(cmd string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.cmds {
		if c == cmd {
			n++
		}
	}
	return n
}

type radioFiles map[string]string

func (r radioFiles) ConfigList(context.Context) ([]helper.ConfigInfo, error) {
	var out []helper.ConfigInfo
	for p := range r {
		out = append(out, helper.ConfigInfo{Path: p})
	}
	return out, nil
}

func (r radioFiles) ConfigRead(_ context.Context, p string) (helper.ConfigContent, error) {
	s, ok := r[p]
	if !ok {
		return helper.ConfigContent{}, fmt.Errorf("%s", helper.ErrNoSuchFile)
	}
	return helper.ConfigContent{Path: p, Content: s}, nil
}

type radioRec struct{ n int }

func (r *radioRec) ConfigRecord(context.Context, string, string, string) (helper.ConfigApplyResult, error) {
	r.n++
	return helper.ConfigApplyResult{Commit: "abc1234"}, nil
}

func withRadio(t *testing.T) (*env, *radioFake) {
	t.Helper()
	f := &radioFake{rx: 650, active: "1999"}
	e := newEnv(t, opts{tuning: func() *susb.Service {
		s := susb.New(f)
		s.Config = radioFiles{
			"rpt.conf":       "[" + localNode + "]\nrxchannel=SimpleUSB/1999\n",
			"simpleusb.conf": "[1999]\nrxmixerset=650\ntxmixaset=500\ntxmixbset=0\n",
		}
		s.Recorder = &radioRec{}
		return s
	}})
	return e, f
}

func TestRadioAuthz(t *testing.T) {
	e, _ := withRadio(t)
	routes := [][2]string{
		{"GET", "/api/radio"}, {"GET", "/api/radio/1999"}, {"PUT", "/api/radio/1999/params"},
		{"GET", "/api/radio/1999/meter"}, {"POST", "/api/radio/1999/echo"},
		{"POST", "/api/radio/1999/save"}, {"POST", "/api/radio/1999/revert"},
	}
	for _, role := range []string{"op", "viewer"} {
		for _, r := range routes {
			expectErr(t, e, role, r[0], r[1], map[string]any{}, 403, "forbidden")
		}
	}
}

func TestRadioUnavailable(t *testing.T) {
	e := newEnv(t, opts{})
	var out struct {
		Available bool
		Reason    string
		Devices   []any
	}
	if c := e.do("admin", "GET", "/api/radio", nil, &out); c != 200 || out.Available || out.Reason == "" {
		t.Fatalf("%d %+v", c, out)
	}
	expectErr(t, e, "admin", "GET", "/api/radio/1999", nil, 503, "radio_unavailable")
}

func TestRadioListAndRead(t *testing.T) {
	e, _ := withRadio(t)
	var out struct {
		Available bool
		Devices   []struct {
			Name, Node string
			Links      []string
		}
	}
	if c := e.do("admin", "GET", "/api/radio", nil, &out); c != 200 || !out.Available || len(out.Devices) != 1 ||
		out.Devices[0].Name != "1999" || out.Devices[0].Node != localNode || out.Devices[0].Links == nil {
		t.Fatalf("%d %+v", c, out)
	}
	var st struct {
		Device string
		Params struct{ RxVoice int }
		Dirty  []string
	}
	if c := e.do("admin", "GET", "/api/radio/1999", nil, &st); c != 200 || st.Params.RxVoice != 650 || len(st.Dirty) != 0 {
		t.Fatalf("%d %+v", c, st)
	}
	expectErr(t, e, "admin", "GET", "/api/radio/nope", nil, 404, "unknown_device")
}

func TestRadioParamsMeterSaveRevert(t *testing.T) {
	e, f := withRadio(t)
	if c := e.do("admin", "GET", "/api/radio/1999", nil, nil); c != 200 {
		t.Fatal(c)
	}
	var res struct {
		Applied []struct{ Field string }
		Dirty   []string
	}
	if c := e.do("admin", "PUT", "/api/radio/1999/params", map[string]any{"rxVoice": 700}, &res); c != 200 || len(res.Applied) != 1 || len(res.Dirty) != 1 {
		t.Fatalf("%d %+v", c, res)
	}
	expectErr(t, e, "admin", "PUT", "/api/radio/1999/params", map[string]any{"rxVoice": 1000}, 400, "invalid")
	expectErr(t, e, "admin", "PUT", "/api/radio/1999/params", map[string]any{}, 400, "invalid")
	var m struct{ Pk float64 }
	if c := e.do("admin", "GET", "/api/radio/1999/meter", nil, &m); c != 200 || m.Pk != -60 {
		t.Fatalf("%d %+v", c, m)
	}
	if c := e.do("admin", "POST", "/api/radio/1999/revert", nil, &res); c != 200 || len(res.Dirty) != 0 || f.rx != 650 {
		t.Fatalf("%d %+v %d", c, res, f.rx)
	}
	var sv struct{ Saved bool }
	if c := e.do("admin", "POST", "/api/radio/1999/save", nil, &sv); c != 200 || !sv.Saved {
		t.Fatalf("%d %+v", c, sv)
	}
	if n := countAudit(t, e, "radio.save"); n != 1 {
		t.Fatalf("audit rows: %d", n)
	}
}

func TestRadioEcho(t *testing.T) {
	e, _ := withRadio(t)
	var es struct{ On bool }
	if c := e.do("admin", "POST", "/api/radio/1999/echo", map[string]any{"on": true}, &es); c != 200 || !es.On {
		t.Fatalf("%d %+v", c, es)
	}
	if c := e.do("admin", "POST", "/api/radio/1999/echo", map[string]any{"on": false}, &es); c != 200 || es.On {
		t.Fatal(c)
	}
	if n := countAudit(t, e, "radio.echo"); n != 2 {
		t.Fatalf("echo audit rows: %d", n)
	}
}

func countAudit(t *testing.T, e *env, action string) int {
	t.Helper()
	au, err := e.st.ListAudit(100)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, a := range au {
		if a.Action == action {
			n++
		}
	}
	return n
}
