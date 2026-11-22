package api_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/artw1/aslcp/internal/dv/ctl"
	"github.com/artw1/aslcp/internal/helper"
)

type netView struct {
	Mode        string
	Available   bool
	Enabled     bool
	Address     string
	Port        int
	PasswordSet bool
	Options     []struct{ Key string }
	Values      map[string]string
	Login       string
	Detail      string
	Applied     bool
	ApplyError  string
}

func TestDVAvailabilityFromEngine(t *testing.T) {
	// BM listed as available by the engine: the snapshot and the gate follow it.
	e, _, _ := dvEnv(t, ctl.ModeLoop, ctl.ModeBM)
	s := waitDV(t, e, func(s dvSnap) bool { return s.Running })
	for _, m := range s.Modes {
		if m.ID == "BM" && !m.Available {
			t.Fatalf("BM should be available: %+v", s.Modes)
		}
		if m.ID == "YSF" && m.Available {
			t.Fatalf("YSF should not be: %+v", s.Modes)
		}
	}
	expectErr(t, e, "op", "POST", "/api/dv/mode", map[string]string{"mode": "YSF"}, 409, "not_available")
}

func TestDVNetworksGetPut(t *testing.T) {
	e, fe, _ := dvEnv(t, ctl.ModeLoop, ctl.ModeBM)
	waitDV(t, e, func(s dvSnap) bool { return s.Running })

	var list struct {
		Identity struct {
			Callsign string
			DMRID    uint32
		}
		Modes []netView
	}
	if c := e.do("admin", "GET", "/api/dv/networks", nil, &list); c != 200 || len(list.Modes) < 2 {
		t.Fatalf("%d %+v", c, list)
	}
	var bm *netView
	for i := range list.Modes {
		if list.Modes[i].Mode == "BM" {
			bm = &list.Modes[i]
		}
	}
	if bm == nil || !bm.Available || len(bm.Options) == 0 {
		t.Fatalf("%+v", bm)
	}

	// not valid input is refused with 422 and changes nothing
	expectErr(t, e, "admin", "PUT", "/api/dv/networks/BM", map[string]any{"port": 99999}, 422, "invalid_field")
	expectErr(t, e, "admin", "PUT", "/api/dv/networks/BM", map[string]any{"options": map[string]string{"nope": "1"}}, 422, "invalid_field")
	expectErr(t, e, "admin", "PUT", "/api/dv/networks/NOPE", map[string]any{}, 404, "not_found")
	expectErr(t, e, "admin", "PUT", "/api/dv/identity", map[string]any{"callsign": "bad call", "dmrId": 1234567}, 422, "invalid_field")
	expectErr(t, e, "admin", "PUT", "/api/dv/identity", map[string]any{"callsign": "N0CALL", "dmrId": 12}, 422, "invalid_field")
	// roles: settings are admin only
	expectErr(t, e, "op", "GET", "/api/dv/networks", nil, 403, "forbidden")
	expectErr(t, e, "op", "PUT", "/api/dv/networks/BM", map[string]any{}, 403, "forbidden")

	var id struct {
		Callsign string
		DMRID    uint32
		Applied  bool
	}
	if c := e.do("admin", "PUT", "/api/dv/identity", map[string]any{"callsign": "N0CALL", "dmrId": 1234567}, &id); c != 200 || id.Callsign != "N0CALL" || id.DMRID != 1234567 {
		t.Fatalf("%d %+v", c, id)
	}
	var v netView
	if c := e.do("admin", "PUT", "/api/dv/networks/BM", map[string]any{"address": "192.0.2.10", "port": 62031,
		"password": "example-pass", "options": map[string]string{"client_id": "4321"}, "enabled": true}, &v); c != 200 {
		t.Fatalf("%d", c)
	}
	if !v.PasswordSet || v.Address != "192.0.2.10" || v.Port != 62031 || !v.Enabled || v.Values["client_id"] != "4321" {
		t.Fatalf("%+v", v)
	}
	// the password never comes back, in any reply
	for _, p := range []string{"/api/dv/networks", "/api/dv/networks/BM", "/api/dv/identity", "/api/dv/status"} {
		_, raw, _ := e.raw("GET", p, nil, e.ck["admin"], nil)
		if strings.Contains(string(raw), "example-pass") {
			t.Fatalf("password in GET %s: %s", p, raw)
		}
	}
	// nor in the audit trail
	var audit json.RawMessage
	e.do("admin", "GET", "/api/audit", nil, &audit)
	if strings.Contains(string(audit), "example-pass") {
		t.Fatalf("password in audit: %s", audit)
	}
	// a write without a password keeps the stored one; clearPassword drops it
	if c := e.do("admin", "PUT", "/api/dv/networks/BM", map[string]any{"port": 54005}, &v); c != 200 || !v.PasswordSet {
		t.Fatalf("%d %+v", c, v)
	}
	if c := e.do("admin", "PUT", "/api/dv/networks/BM", map[string]any{"clearPassword": true}, &v); c != 200 || v.PasswordSet {
		t.Fatalf("%d %+v", c, v)
	}
	_ = fe
}

func TestDVNetworkIncomplete(t *testing.T) {
	e, fe, _ := dvEnv(t, ctl.ModeLoop, ctl.ModeBM)
	waitDV(t, e, func(s dvSnap) bool { return s.Running })
	fe.mu.Lock()
	fe.netErr = helper.ErrDVNetIncomplete
	fe.mu.Unlock()
	expectErr(t, e, "admin", "PUT", "/api/dv/networks/BM", map[string]any{"enabled": true}, 422, "incomplete")
}

func TestDVNetworksTGIFDMRNXDN(t *testing.T) {
	e, _, _ := dvEnv(t, ctl.ModeLoop, ctl.ModeBM, ctl.ModeTGIF, ctl.ModeDMR, ctl.ModeNXDN)
	waitDV(t, e, func(s dvSnap) bool { return s.Running })
	var list struct {
		Modes []struct {
			netView
			DefaultAddress string
			DefaultPort    int
			NeedsIdentity  bool
			Fields         struct{ Address, Port, Password bool }
		}
	}
	if c := e.do("admin", "GET", "/api/dv/networks", nil, &list); c != 200 {
		t.Fatal(c)
	}
	got := map[string]int{}
	for _, m := range list.Modes {
		got[m.Mode]++
		switch m.Mode {
		case "TGIF":
			if !m.Available || m.DefaultAddress != "tgif.network" || m.DefaultPort != 62031 || !m.Fields.Password || len(m.Options) == 0 {
				t.Fatalf("%+v", m)
			}
		case "DMR":
			if !m.Available || !m.Fields.Address || !m.Fields.Password || len(m.Options) == 0 {
				t.Fatalf("%+v", m)
			}
		case "NXDN":
			if !m.Available || m.Fields.Address || m.Fields.Password || !m.NeedsIdentity || len(m.Options) == 0 {
				t.Fatalf("%+v", m)
			}
		}
	}
	if got["TGIF"] != 1 || got["DMR"] != 1 || got["NXDN"] != 1 {
		t.Fatalf("%v", got)
	}
	// validation
	expectErr(t, e, "admin", "PUT", "/api/dv/networks/TGIF", map[string]any{"options": map[string]string{"slot": "5"}}, 422, "invalid_field")
	expectErr(t, e, "admin", "PUT", "/api/dv/networks/DMR", map[string]any{"options": map[string]string{"nxdn_id": "1"}}, 422, "invalid_field")
	expectErr(t, e, "admin", "PUT", "/api/dv/networks/NXDN", map[string]any{"password": "example-pass"}, 422, "invalid_field")
	expectErr(t, e, "admin", "PUT", "/api/dv/networks/NXDN", map[string]any{"options": map[string]string{"nxdn_id": "0"}}, 422, "invalid_field")
	var v netView
	if c := e.do("admin", "PUT", "/api/dv/networks/DMR", map[string]any{"address": "192.0.2.10", "port": 62031, "password": "example-pass",
		"options": map[string]string{"options": "TS2=1997;"}}, &v); c != 200 || !v.PasswordSet || v.Values["options"] != "TS2=1997;" {
		t.Fatalf("%d %+v", c, v)
	}
	for _, p := range []string{"/api/dv/networks", "/api/dv/networks/DMR", "/api/dv/networks/TGIF"} {
		_, raw, _ := e.raw("GET", p, nil, e.ck["admin"], nil)
		if strings.Contains(string(raw), "example-pass") {
			t.Fatalf("password in GET %s: %s", p, raw)
		}
	}
}

func TestDVNetworksYSF(t *testing.T) {
	e, _, _ := dvEnv(t, ctl.ModeLoop, ctl.ModeBM, ctl.ModeYSF)
	waitDV(t, e, func(s dvSnap) bool { return s.Running })
	var list struct {
		Modes []struct {
			netView
			AddressOptional bool
			DefaultAddress  string
			DefaultPort     int
			NeedsIdentity   bool
			Fields          struct{ Address, Port, Password bool }
		}
	}
	if c := e.do("admin", "GET", "/api/dv/networks", nil, &list); c != 200 {
		t.Fatal(c)
	}
	found := 0
	for _, m := range list.Modes {
		if m.Mode != "YSF" {
			continue
		}
		found++
		if !m.Available || !m.AddressOptional || m.DefaultAddress != "" || m.DefaultPort != 42000 || !m.Fields.Address || !m.Fields.Port || m.Fields.Password || !m.NeedsIdentity || len(m.Options) != 3 {
			t.Fatalf("%+v", m)
		}
		for _, o := range m.Options {
			if o.Key == "tick_ms" {
				t.Fatal("tick_ms is not a setting")
			}
		}
	}
	if found != 1 {
		t.Fatalf("YSF listed %d times", found)
	}
	expectErr(t, e, "admin", "PUT", "/api/dv/networks/YSF", map[string]any{"password": "example-pass"}, 422, "invalid_field")
	expectErr(t, e, "admin", "PUT", "/api/dv/networks/YSF", map[string]any{"options": map[string]string{"tick_ms": "10"}}, 422, "invalid_field")
	var v netView
	if c := e.do("admin", "PUT", "/api/dv/networks/YSF", map[string]any{"enabled": true, "options": map[string]string{"poll_ms": "4000"}}, &v); c != 200 || !v.Enabled || v.Values["poll_ms"] != "4000" {
		t.Fatalf("%d %+v", c, v)
	}
}

func TestDVNetworksP25(t *testing.T) {
	e, _, _ := dvEnv(t, ctl.ModeLoop, ctl.ModeBM, ctl.ModeP25)
	waitDV(t, e, func(s dvSnap) bool { return s.Running })
	var list struct {
		Modes []struct {
			netView
			AddressOptional bool
			DefaultAddress  string
			DefaultPort     int
			NeedsIdentity   bool
			Fields          struct{ Address, Port, Password bool }
		}
	}
	if c := e.do("admin", "GET", "/api/dv/networks", nil, &list); c != 200 {
		t.Fatal(c)
	}
	found := 0
	for _, m := range list.Modes {
		if m.Mode != "P25" {
			continue
		}
		found++
		if !m.Available || !m.AddressOptional || m.DefaultAddress != "" || m.DefaultPort != 41000 || !m.Fields.Address || !m.Fields.Port || m.Fields.Password || !m.NeedsIdentity || len(m.Options) != 4 {
			t.Fatalf("%+v", m)
		}
		for _, o := range m.Options {
			if o.Key == "tick_ms" {
				t.Fatal("tick_ms is not a setting")
			}
		}
	}
	if found != 1 {
		t.Fatalf("P25 listed %d times", found)
	}
	expectErr(t, e, "admin", "PUT", "/api/dv/networks/P25", map[string]any{"password": "example-pass"}, 422, "invalid_field")
	expectErr(t, e, "admin", "PUT", "/api/dv/networks/P25", map[string]any{"options": map[string]string{"tick_ms": "10"}}, 422, "invalid_field")
	expectErr(t, e, "admin", "PUT", "/api/dv/networks/P25", map[string]any{"options": map[string]string{"p25_id": "16777216"}}, 422, "invalid_field")
	var v netView
	if c := e.do("admin", "PUT", "/api/dv/networks/P25", map[string]any{"enabled": true, "options": map[string]string{"p25_id": "1999"}}, &v); c != 200 || !v.Enabled || v.Values["p25_id"] != "1999" {
		t.Fatalf("%d %+v", c, v)
	}
}
