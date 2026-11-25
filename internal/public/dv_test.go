package public

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func sampleDV(now time.Time) DVInput {
	return DVInput{
		Now: &DVCallIn{Mode: "DMR", Call: "N0CALL", Target: "TG 91", Slot: 2, Start: now.Add(-5 * time.Second), Name: "Pat Example", Place: "Springfield, XX"},
		Heard: []DVCallIn{
			{Mode: "YSF", Call: "N1CALL", Target: "YSF-TEST", Start: now.Add(-time.Minute), Name: "Sam Example", Place: "Shelbyville, XX"},
			{Mode: "DMR", Call: "1234567", Target: "TG 91", Slot: 1, Start: now.Add(-2 * time.Minute)},
			{Mode: "DMR", Call: "N0CALL", Target: "1234567", Private: true, Start: now.Add(-3 * time.Minute)},
			{Mode: "M17", Call: "N1CALL", Target: "ysf.example.net:42000", Start: now.Add(-4 * time.Minute)},
			{Mode: "M17", Call: "N1CALL", Target: "203.0.113.5", Start: now.Add(-5 * time.Minute)},
			{Mode: "BOGUS", Call: "N1CALL", Start: now},
		},
	}
}

func newDVService(show bool, callsigns bool, in func() DVInput) *Service {
	s := New(Options{DV: in})
	c := DefaultSettings()
	c.Enabled, c.ShowDV, c.ShowCallsigns = true, show, callsigns
	s.cfg.Store(&c)
	return s
}

func TestDVAbsentWhenSettingOff(t *testing.T) {
	var called atomic.Int32
	s := newDVService(false, true, func() DVInput { called.Add(1); return sampleDV(time.Now()) })
	snap := s.Snapshot()
	if snap.DV != nil || snap.ShowDV {
		t.Fatalf("DV data with the setting off: %+v", snap.DV)
	}
	b, _ := json.Marshal(snap)
	for _, k := range []string{`"dv"`, "N0CALL", "N1CALL", "YSF-TEST", "Pat Example"} {
		if strings.Contains(string(b), k) {
			t.Fatalf("%s in %s", k, b)
		}
	}
	if called.Load() != 0 {
		t.Fatal("the DV source was read with the setting off")
	}
	// an update event is not sent either
	cl, _ := s.hub.add("k")
	s.DVChanged()
	select {
	case m := <-cl.ch:
		t.Fatalf("event with the setting off: %s", m)
	case <-time.After(30 * time.Millisecond):
	}
}

func TestDVPresentWhenSettingOn(t *testing.T) {
	now := time.Now()
	s := newDVService(true, true, func() DVInput { return sampleDV(now) })
	snap := s.Snapshot()
	if !snap.ShowDV || snap.DV == nil || snap.DV.Now == nil {
		t.Fatalf("%+v", snap.DV)
	}
	n := snap.DV.Now
	if n.Mode != "DMR" || n.Callsign != "N0CALL" || n.Target != "TG 91" || n.Slot != 2 || n.Name != "Pat Example" || n.Location != "Springfield, XX" || n.At != now.Add(-5*time.Second).Unix() {
		t.Fatalf("%+v", n)
	}
	// private call, unknown mode dropped; DMR ID is no callsign; host and IP targets dropped
	h := snap.DV.Heard
	if len(h) != 4 {
		t.Fatalf("%+v", h)
	}
	if h[0].Callsign != "N1CALL" || h[0].Target != "YSF-TEST" || h[0].Location != "Shelbyville, XX" {
		t.Fatalf("%+v", h[0])
	}
	if h[1].Callsign != "" || h[1].Slot != 1 {
		t.Fatalf("a DMR ID must not be published as a callsign: %+v", h[1])
	}
	if h[2].Target != "" || h[3].Target != "" {
		t.Fatalf("host or address as target: %+v %+v", h[2], h[3])
	}
	b, _ := json.Marshal(snap)
	for _, bad := range []string{"1234567", "example.net", "203.0.113"} {
		if strings.Contains(string(b), bad) {
			t.Fatalf("%s in %s", bad, b)
		}
	}
}

func TestDVCallsignsHidden(t *testing.T) {
	now := time.Now()
	s := newDVService(true, false, func() DVInput { return sampleDV(now) })
	snap := s.Snapshot()
	if snap.DV == nil || snap.DV.Now == nil || len(snap.DV.Heard) == 0 {
		t.Fatalf("%+v", snap.DV)
	}
	b, _ := json.Marshal(snap.DV)
	for _, bad := range []string{"N0CALL", "N1CALL", "Pat", "Example", "Springfield", `"callsign"`, `"name"`, `"location"`} {
		if strings.Contains(string(b), bad) {
			t.Fatalf("%s in %s", bad, b)
		}
	}
	if snap.DV.Now.Mode != "DMR" || snap.DV.Now.Target != "TG 91" {
		t.Fatalf("%+v", snap.DV.Now)
	}
}

func TestDVJSONHasNoForbiddenFields(t *testing.T) {
	now := time.Now()
	for _, cs := range []bool{true, false} {
		s := newDVService(true, cs, func() DVInput { return sampleDV(now) })
		b, _ := json.Marshal(s.Snapshot().DV)
		var v map[string]any
		_ = json.Unmarshal(b, &v)
		var keys []string
		var walk func(any)
		walk = func(x any) {
			switch t := x.(type) {
			case map[string]any:
				for k, e := range t {
					keys = append(keys, k)
					walk(e)
				}
			case []any:
				for _, e := range t {
					walk(e)
				}
			}
		}
		walk(v)
		allowed := map[string]bool{"now": true, "heard": true, "mode": true, "callsign": true, "target": true, "slot": true, "at": true, "name": true, "location": true}
		for _, k := range keys {
			if !allowed[k] {
				t.Errorf("unexpected key %q in %s", k, b)
			}
		}
	}
	for _, v := range []any{DV{}, DVCall{Mode: "DMR", Callsign: "N0CALL", Target: "TG 1", Slot: 1, Name: "x", Location: "y"}} {
		b, _ := json.Marshal(v)
		for _, bad := range []string{"ip", "master", "host", "dmrId", "id", "server", "login", "bridge", "version"} {
			if strings.Contains(string(b), `"`+bad+`"`) {
				t.Errorf("%T has a %q field: %s", v, bad, b)
			}
		}
	}
}

func TestDVEventsAreCoalesced(t *testing.T) {
	now := time.Now()
	s := newDVService(true, true, func() DVInput { return sampleDV(now) })
	s.DVInterval = 80 * time.Millisecond
	cl, _ := s.hub.add("k")
	for i := 0; i < 20; i++ {
		s.DVChanged()
	}
	time.Sleep(250 * time.Millisecond)
	n := len(cl.ch)
	if n < 1 || n > 2 {
		t.Fatalf("20 changes within one interval gave %d events, want 1 or 2", n)
	}
	if m := string(<-cl.ch); !strings.HasPrefix(m, "event: dv\n") {
		t.Fatal(m)
	}
	s.Close()
}

func TestDVSettingPersists(t *testing.T) {
	st := memSettings{}
	if LoadSettings(st, false).ShowDV {
		t.Fatal("must default to off")
	}
	c := DefaultSettings()
	c.ShowDV = true
	if err := SaveSettings(st, c); err != nil {
		t.Fatal(err)
	}
	if !LoadSettings(st, false).ShowDV {
		t.Fatal("not stored")
	}
}

type memSettings map[string]string

func (m memSettings) GetSetting(k string) (string, error) {
	v, ok := m[k]
	if !ok {
		return "", errMissing
	}
	return v, nil
}
func (m memSettings) SetSetting(k, v string) error { m[k] = v; return nil }

var errMissing = &missingErr{}

type missingErr struct{}

func (*missingErr) Error() string { return "missing" }
