package api

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/dv/ctl"
	"github.com/artw1/aslcp/internal/helper"
	"github.com/artw1/aslcp/internal/hostlists"
	"github.com/artw1/aslcp/internal/nowcalling"
	"github.com/artw1/aslcp/internal/radioid"
)

// netEngine answers the network settings only; the rest of DVEngine is nil.
type netEngine struct {
	DVEngine
	mu   sync.Mutex
	nets map[string]helper.DVNetworkView
}

func (e *netEngine) DVNetworkGet(context.Context) (helper.DVNetworkReply, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var r helper.DVNetworkReply
	for _, v := range e.nets {
		r.Networks = append(r.Networks, v)
	}
	return r, nil
}

func testRadioID(t *testing.T) *radioid.Manager {
	t.Helper()
	dir := t.TempDir()
	csv := "RADIO_ID,CALLSIGN,FIRST_NAME,LAST_NAME,CITY,STATE,COUNTRY\n1234567,N0CALL,Alex,Example,Springfield,Illinois,United States\n"
	if err := os.WriteFile(filepath.Join(dir, radioid.FileName), []byte(csv), 0o600); err != nil {
		t.Fatal(err)
	}
	rid := radioid.New(radioid.Options{Dir: dir})
	rid.Load()
	return rid
}

// A Homebrew master gives the DMR ID as the "callsign": it is no callsign, and
// the radioid.net entry of the ID supplies one.
func TestDMRIDIsNotACallsign(t *testing.T) {
	s := &Server{d: Deps{RadioID: testRadioID(t)}, nc: &ncState{}}
	d := &dvSnapper{s: s, now: time.Now}
	hb := ctl.Talk{Dir: "rx", Mode: ctl.ModeDMR, Call: "1234567", DMRID: 1234567, TG: 91, Start: time.Now()}

	if c := d.caller(hb, nil); c.Call != "N0CALL" || c.DMRID != "1234567" {
		t.Fatalf("heard: %+v", c)
	}
	nt := dvNowTalks([]ctl.Talk{hb})
	if nt[0].Call != "" || nt[0].DMRID != "1234567" {
		t.Fatalf("now talk: %+v", nt[0])
	}
	call := &nowcalling.Call{Kind: nowcalling.KindDMR, Call: nt[0].Call, DMRID: nt[0].DMRID}
	s.ncResolve(call)
	if call.Call != "N0CALL" || call.Name == "" || call.City != "Springfield" {
		t.Fatalf("resolved: %+v", call)
	}

	// no radioid entry: no callsign, the DMR ID stays
	un := ctl.Talk{Dir: "rx", Mode: ctl.ModeDMR, Call: "2345678", DMRID: 2345678, TG: 91}
	if c := d.caller(un, nil); c.Call != "" || c.DMRID != "2345678" {
		t.Fatalf("unknown: %+v", c)
	}
	// without any list
	d0 := &dvSnapper{s: &Server{}, now: time.Now}
	if c := d0.caller(hb, nil); c.Call != "" || c.DMRID != "1234567" {
		t.Fatalf("no list: %+v", c)
	}
}

func TestDMRCallsignKinds(t *testing.T) {
	s := &Server{d: Deps{RadioID: testRadioID(t)}}
	d := &dvSnapper{s: s, now: time.Now}
	// BrandMeister sends a real callsign: unchanged, whatever the list says
	bm := ctl.Talk{Dir: "rx", Mode: ctl.ModeBM, Call: "N0CALL", DMRID: 2345678, TG: 91}
	if c := d.caller(bm, nil); c.Call != "N0CALL" {
		t.Fatalf("BM: %+v", c)
	}
	if nt := dvNowTalks([]ctl.Talk{bm}); nt[0].Call != "N0CALL" {
		t.Fatalf("BM now: %+v", nt[0])
	}
	// the Parrot's private reply (ID 9990, here as a Homebrew-style ID call)
	p := ctl.Talk{Dir: "rx", Mode: ctl.ModeDMR, Call: "9990", DMRID: parrotID, Private: true}
	if nt := dvNowTalks([]ctl.Talk{p}); nt[0].Call != "Parrot" {
		t.Fatalf("parrot: %+v", nt[0])
	}
	if c := d.caller(p, nil); c.Call != "" || !c.Private {
		t.Fatalf("parrot heard: %+v", c)
	}
	// the echo test keeps its name
	e := ctl.Talk{Dir: "rx", Mode: ctl.ModeLoop, Call: ctl.EchoTestCall}
	if c := d.caller(e, nil); c.Call != ctl.EchoTestName {
		t.Fatalf("echo: %+v", c)
	}
	// NXDN is not a DMR network: no radioid lookup, call untouched
	nx := ctl.Talk{Dir: "rx", Mode: ctl.ModeNXDN, Call: "1234567", DMRID: 1234567}
	if c := d.caller(nx, nil); c.Call != "1234567" {
		t.Fatalf("nxdn: %+v", c)
	}
}

func TestDVMasterName(t *testing.T) {
	items := []hostlists.Item{{Value: "master.example.net", Name: "DMR+_IPSC2-Example"}}
	if n := dvMasterName("Master.Example.NET ", items); n != "DMR+_IPSC2-Example" {
		t.Fatal(n)
	}
	if n := dvMasterName("192.0.2.10", items); n != "192.0.2.10" {
		t.Fatal(n)
	}
	if n := dvMasterName("  ", items); n != "" {
		t.Fatal(n)
	}
}

func TestFillMasters(t *testing.T) {
	dir := t.TempDir()
	hosts := "DMR+_IPSC2-Example\t0\tdmrplus.example.net\tpassw0rd\t55555\n" +
		"BM_9999_Example\t9999\tbm.example.net\tpassw0rd\t62031\n"
	if err := os.WriteFile(filepath.Join(dir, "DMR_Hosts.txt"), []byte(hosts), 0o600); err != nil {
		t.Fatal(err)
	}
	hl := hostlists.New(hostlists.Options{Dir: dir, Enabled: func() bool { return true }})
	hl.Load()
	eng := &netEngine{nets: map[string]helper.DVNetworkView{
		"BM":   {Mode: "BM", Enabled: true, Address: "bm.example.net"},
		"DMR":  {Mode: "DMR", Enabled: true, Address: "192.0.2.10"},
		"TGIF": {Mode: "TGIF", Enabled: true, Address: "tgif.example.net"},
	}}
	s := &Server{d: Deps{DVEngine: eng, HostLists: hl}}
	now := time.Now()
	d := &dvSnapper{s: s, now: func() time.Time { return now }}
	mv := func() []DVModeView {
		out := []DVModeView{}
		for _, id := range []string{"LOOP", "BM", "TGIF", "DMR", "YSF"} {
			out = append(out, DVModeView{ID: id, Enabled: true, Available: true})
		}
		return out
	}
	get := func(m []DVModeView, id string) string {
		for _, v := range m {
			if v.ID == id {
				return v.Master
			}
		}
		return "?"
	}
	m := mv()
	d.fillMasters(t.Context(), m)
	if got := get(m, "BM"); got != "9999 – Example" {
		t.Fatalf("BM list match: %q", got)
	}
	if got := get(m, "DMR"); got != "192.0.2.10" {
		t.Fatalf("DMR typed address: %q", got)
	}
	for _, id := range []string{"LOOP", "TGIF", "YSF"} {
		if got := get(m, id); got != "" {
			t.Fatalf("%s has a master: %q", id, got)
		}
	}
	// a DMR - Other address on the list gives the entry's name; re-read after the cache expires
	eng.mu.Lock()
	v := eng.nets["DMR"]
	v.Address = "dmrplus.example.net"
	eng.nets["DMR"] = v
	eng.mu.Unlock()
	now = now.Add(time.Minute)
	m = mv()
	d.fillMasters(t.Context(), m)
	if got := get(m, "DMR"); got != "DMR+_IPSC2-Example" {
		t.Fatalf("DMR list match: %q", got)
	}
	// stable between builds
	m2 := mv()
	d.fillMasters(t.Context(), m2)
	if get(m2, "DMR") != get(m, "DMR") {
		t.Fatal("name not stable")
	}
}

// A DMR call carries a DMR ID, so Last heard gets the name and place of the
// ID from the radioid.net list (it used to skip every caller with an ID).
func TestDMRCallerGetsNameAndPlace(t *testing.T) {
	s := &Server{d: Deps{RadioID: testRadioID(t)}, nc: &ncState{}}
	d := &dvSnapper{s: s, now: time.Now}
	for _, mode := range []string{ctl.ModeBM, ctl.ModeDMR} {
		call := "N0CALL"
		if mode == ctl.ModeDMR {
			call = "1234567" // a Homebrew master sends the ID
		}
		tk := ctl.Talk{ID: 7, Dir: "rx", Mode: mode, Call: call, DMRID: 1234567, TG: 91, Start: time.Now()}
		c := d.caller(tk, nil)
		if c.Call != "N0CALL" || c.Name != "Alex Example" || c.Place != "Springfield, Illinois" {
			t.Fatalf("%s: %+v", mode, c)
		}
		// the entry kept in the heard list and filled again at snapshot time
		d.heard = nil
		d.endTalkLocked(tk, time.Now())
		h := d.heard[0]
		h.Name, h.Place = "", ""
		d.fillPerson(&h)
		if h.Name != "Alex Example" || h.Place != "Springfield, Illinois" {
			t.Fatalf("%s heard: %+v", mode, h)
		}
	}
	// an ID the list does not know: no person, and no panic
	un := ctl.Talk{Dir: "rx", Mode: ctl.ModeDMR, Call: "2345678", DMRID: 2345678, TG: 91}
	if c := d.caller(un, nil); c.Name != "" || c.Place != "" {
		t.Fatalf("unknown: %+v", c)
	}
	// a private call shows no person
	pr := ctl.Talk{Dir: "rx", Mode: ctl.ModeDMR, DMRID: 1234567, Private: true}
	if c := d.caller(pr, nil); c.Name != "" {
		t.Fatalf("private: %+v", c)
	}
}
