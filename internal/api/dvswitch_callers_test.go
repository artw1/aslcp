package api_test

import (
	"strings"
	"testing"

	"github.com/artw1/aslcp/internal/api"
	"github.com/artw1/aslcp/internal/helper"
)

func TestDVSnapshotCallerAndHeard(t *testing.T) {
	f := newCtlDV()
	cur := helper.DVCaller{Call: "N0CALL", DMRID: "1234567", Dst: "91", Slot: 2, At: "2026-11-18 10:00:01"}
	old := helper.DVCaller{Call: "N0TEST", DMRID: "1234568", Dst: "91", Slot: 2, At: "2026-11-18 09:58:00"}
	f.info.Callers = &helper.DVCallers{Current: &cur, Recent: []helper.DVCaller{cur, old}}
	e := ctlEnv(t, f)
	expectErr(t, e, "", "GET", "/api/dvswitch/snapshot", nil, 401, "unauthorized")
	for _, role := range []string{"viewer", "op", "admin"} {
		var sn api.DVSnapshot
		if c := e.do(role, "GET", "/api/dvswitch/snapshot", nil, &sn); c != 200 {
			t.Fatalf("%s: %d", role, c)
		}
		if sn.Caller == nil || *sn.Caller != cur || len(sn.Heard) != 2 || sn.Heard[1] != old {
			t.Errorf("%s: %+v %+v", role, sn.Caller, sn.Heard)
		}
	}
	// no callers: an empty list, never null
	e = ctlEnv(t, newCtlDV())
	code, raw, _ := e.raw("GET", "/api/dvswitch/snapshot", nil, e.ck["viewer"], nil)
	if code != 200 || !strings.Contains(string(raw), `"heard":[]`) || strings.Contains(string(raw), `"caller"`) {
		t.Fatalf("%d %s", code, raw)
	}
}

func TestDVSnapshotPrivateFlag(t *testing.T) {
	f := newCtlDV()
	f.info.Private = true
	e := ctlEnv(t, f)
	var sn api.DVSnapshot
	if c := e.do("viewer", "GET", "/api/dvswitch/snapshot", nil, &sn); c != 200 || !sn.Bridge.Private {
		t.Fatalf("%d %+v", c, sn.Bridge)
	}
	// private is a DMR matter
	f = newCtlDV()
	f.info.Private, f.info.Mode = true, "P25"
	e = ctlEnv(t, f)
	var sn2 api.DVSnapshot
	if c := e.do("viewer", "GET", "/api/dvswitch/snapshot", nil, &sn2); c != 200 || sn2.Bridge.Private {
		t.Fatalf("%d %+v", c, sn2.Bridge)
	}
}

func TestDVSwitchIdentityInfo(t *testing.T) {
	f := &fakeDV{}
	e := newEnv(t, opts{dvswitch: f})
	body := map[string]any{"callsign": "N0CALL", "dmrId": "1234567", "essid": "01",
		"info": map[string]any{"location": "Example Town", "description": "Test hotspot", "url": "https://example.org", "frequencyHz": 434000000}}
	if code, raw, _ := e.raw("PUT", "/api/dvswitch/identity", body, e.ck["admin"], nil); code != 200 {
		t.Fatalf("%d %s", code, raw)
	}
	if f.last.Identity == nil || f.last.Identity.Info == nil || f.last.Identity.Info.Location != "Example Town" || f.last.Identity.Info.FrequencyHz != 434000000 {
		t.Fatalf("%+v", f.last.Identity)
	}
	aud, _ := e.st.ListAudit(50)
	found := false
	for _, a := range aud {
		found = found || a.Action == "dvswitch.identity" && strings.Contains(a.Detail, "info") && !strings.Contains(a.Detail, "Example Town")
	}
	if !found {
		t.Error("audit lacks the key name or holds a value")
	}
	delete(body, "info")
	f.last = helper.DVSwitchRequest{}
	e.raw("PUT", "/api/dvswitch/identity", body, e.ck["admin"], nil)
	if f.last.Identity == nil || f.last.Identity.Info != nil {
		t.Fatalf("%+v", f.last.Identity)
	}
}
