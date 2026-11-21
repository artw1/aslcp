package public

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/nodelist"
	"github.com/artw1/aslcp/internal/rpt"
	"github.com/artw1/aslcp/internal/store"
)

var ipLike = regexp.MustCompile(`\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}|[0-9a-f]{1,4}(:[0-9a-f]{0,4}){3,}`)

type lookup map[string]nodelist.Info

func (l lookup) Lookup(n string) (nodelist.Info, bool) { i, ok := l[n]; return i, ok }

func ptr(d time.Duration) *time.Duration { return &d }

func sampleStatus(now time.Time) rpt.NodeStatus {
	return rpt.NodeStatus{
		Node: "1000", RxKeyed: true, NumLinks: 3, Updated: now.Add(-3 * time.Second),
		Links: []rpt.Link{
			{Node: "2000", Kind: rpt.KindAllStar, IP: "192.0.2.58", Direction: rpt.DirOut, Connected: time.Minute, State: rpt.StateEstablished, Mode: rpt.ModeTransceive, Keyed: true, SinceKey: ptr(time.Second), SinceUnkey: ptr(5 * time.Second)},
			{Node: "198.51.100.9", Kind: rpt.KindUnknown, IP: "198.51.100.9", Direction: rpt.DirIn, Connected: time.Second, State: rpt.StateConnecting, Mode: rpt.ModeTransceive},
			{Node: "N0CALL-IP-2001:db8::1", Kind: rpt.KindClient, IP: "2001:db8::1", Direction: rpt.DirIn, State: rpt.StateEstablished, Mode: rpt.ModeMonitor},
		},
		Network: []rpt.LinkedNode{{Node: "2000", Mode: rpt.ModeTransceive}, {Node: "2001", Mode: rpt.ModeTransceive}},
	}
}

func TestBuildNodeNeverContainsIPs(t *testing.T) {
	now := time.Now()
	l := lookup{
		"1000": {Node: "1000", Callsign: "N0AAA", Description: "Home 10.1.2.3 node", Location: "Here"},
		"2000": {Node: "2000", Callsign: "N0BBB", Description: "Hub at 192.168.0.5", Location: "fe80:0:0:0:1:2:3:4"},
	}
	for _, v := range []View{{Callsigns: true, Network: true, Now: now}, {Callsigns: false, Network: true, Now: now}} {
		n := BuildNode(sampleStatus(now), l, v, true)
		b, _ := json.Marshal(n)
		if m := ipLike.Find(b); m != nil {
			t.Fatalf("IP-like text %q in %s", m, b)
		}
		for _, k := range []string{`"ip"`, "192.0.2.58", "198.51.100", "2001:db8"} {
			if strings.Contains(string(b), k) {
				t.Fatalf("%s in %s", k, b)
			}
		}
		if len(n.Links) != 3 || n.Links[1].Node != "unknown" || n.Links[2].Node != "unknown" {
			t.Fatalf("%+v", n.Links)
		}
	}
	// field names of the public structs: no IP/system-like fields exist
	for _, v := range []any{Node{}, Link{}, NetNode{}, Activity{}, Snapshot{}, Info{}} {
		b, _ := json.Marshal(v)
		for _, bad := range []string{"ip", "host", "load", "version", "temp", "secret"} {
			if regexp.MustCompile(`"` + bad + `"`).Match(b) {
				t.Errorf("%T has a %q field", v, bad)
			}
		}
	}
}

func TestBuildNodeCallsignHidingAndAges(t *testing.T) {
	now := time.Now()
	l := lookup{"2000": {Node: "2000", Callsign: "N0BBB", Description: "Hub", Location: "X"}, "1000": {Node: "1000", Callsign: "N0AAA"}}
	shown := BuildNode(sampleStatus(now), l, View{Callsigns: true, Network: true, Now: now}, true)
	if shown.Info == nil || shown.Info.Callsign != "N0AAA" || shown.Links[0].Info == nil || shown.Links[0].Info.Callsign != "N0BBB" || shown.Network[0].Callsign != "N0BBB" {
		t.Fatalf("%+v", shown)
	}
	// ages are brought forward by the 3 s since the data was updated
	if shown.Links[0].Connected != 63 || *shown.Links[0].SinceKey != 4 || *shown.Links[0].SinceUnkey != 8 || shown.Links[1].SinceKey != nil {
		t.Fatalf("%+v", shown.Links)
	}
	if shown.Links[1].Mode != "C" || shown.Links[0].Kind != "allstar" {
		t.Fatalf("%+v", shown.Links)
	}
	hidden := BuildNode(sampleStatus(now), l, View{Callsigns: false, Network: true, Now: now}, true)
	if hidden.Info == nil || hidden.Info.Callsign != "N0AAA" { // the owner's own node stays
		t.Fatalf("%+v", hidden.Info)
	}
	b, _ := json.Marshal(hidden.Links)
	if strings.Contains(string(b), "N0BBB") || strings.Contains(string(b), `"info"`) {
		t.Fatalf("%s", b)
	}
	if hidden.Network[0].Callsign != "" || hidden.Network[0].Node != "2000" {
		t.Fatalf("%+v", hidden.Network)
	}
	if n := BuildNode(sampleStatus(now), l, View{Network: false, Now: now}, true); n.Network != nil {
		t.Fatal("network must be omitted when disabled")
	}
	if n := BuildNode(sampleStatus(now), l, View{Network: true, Now: now}, false); n.Network != nil {
		t.Fatal("network must be omitted from node events")
	}
}

func TestScrub(t *testing.T) {
	for in, want := range map[string]string{
		"plain text":              "plain text",
		"Net at 20:00":            "Net at 20:00",
		"Node 10.0.0.1 here":      "Node  here",
		"v6 2001:db8:0:0:1:2:3:4": "v6",
		"fe80::1":                 "",
		"Rm 12.5":                 "Rm 12.5",
	} {
		if got := scrub(in); strings.TrimSpace(got) != strings.TrimSpace(want) {
			t.Errorf("scrub(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNodeSelection(t *testing.T) {
	m := rpt.NewMonitor(nil, rpt.MonitorConfig{Nodes: []string{"1", "2", "3"}})
	s := New(Options{Store: nil})
	s.SetMonitor(m)
	ids := func(nodes ...string) []string {
		c := s.Settings()
		c.Nodes = nodes
		c.Enabled = true
		s.cfg.Store(&c)
		var out []string
		for _, n := range s.Snapshot().Nodes {
			out = append(out, n.Node)
		}
		return out
	}
	if got := strings.Join(ids(), ","); got != "1,2,3" {
		t.Fatal(got)
	}
	if got := strings.Join(ids("3", "1"), ","); got != "1,3" {
		t.Fatal(got)
	}
	if got := strings.Join(ids("9"), ","); got != "" {
		t.Fatal(got)
	}
}

func TestClientKey(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "192.0.2.1:5555"
	r.Header.Set("X-Forwarded-For", "10.9.9.9, 198.51.100.4")
	if k := ClientKey(r, false); k != "192.0.2.1" {
		t.Fatal(k)
	}
	if k := ClientKey(r, true); k != "198.51.100.4" { // rightmost: added by the trusted proxy
		t.Fatal(k)
	}
	r.Header.Set("X-Forwarded-For", "garbage")
	if k := ClientKey(r, true); k != "192.0.2.1" {
		t.Fatal(k)
	}
	r.RemoteAddr = "[2001:db8:1:2:aaaa:bbbb:cccc:dddd]:1"
	r.Header.Del("X-Forwarded-For")
	if k := ClientKey(r, false); k != "2001:db8:1:2::" {
		t.Fatal(k)
	}
}

func TestLimiter(t *testing.T) {
	l := newLimiter(2, 3)
	now := time.Now()
	for i := 0; i < 3; i++ {
		if !l.allow("a", now) {
			t.Fatal(i)
		}
	}
	if l.allow("a", now) || !l.allow("b", now) {
		t.Fatal("burst")
	}
	if !l.allow("a", now.Add(500*time.Millisecond)) || l.allow("a", now.Add(500*time.Millisecond)) {
		t.Fatal("refill at 2/s")
	}
}

func TestMethodsAndDisabled(t *testing.T) {
	s := New(Options{})
	w := httptest.NewRecorder()
	s.Status(w, httptest.NewRequest("GET", "/api/public/status", nil))
	if w.Code != http.StatusNotFound {
		t.Fatal(w.Code)
	}
}

func TestSettingsValidate(t *testing.T) {
	ok := DefaultSettings()
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := ok
	bad.Nodes = []string{"12;rm"}
	if bad.Validate() == nil {
		t.Fatal("node")
	}
	bad = ok
	bad.Title = strings.Repeat("é", 81)
	if bad.Validate() == nil {
		t.Fatal("title")
	}
}

func TestBuildNetworkDedupesScrubbed(t *testing.T) {
	st := rpt.NodeStatus{Node: "1997", Network: []rpt.LinkedNode{
		{Node: "1998", Mode: rpt.ModeTransceive},
		{Node: "192.0.2.10", Mode: rpt.ModeTransceive},
		{Node: "192.0.2.11", Mode: rpt.ModeTransceive},
		{Node: "1999", Mode: rpt.ModeTransceive},
	}}
	got := BuildNetwork(st, nil, View{})
	seen := map[string]bool{}
	for _, n := range got {
		if seen[n.Node] {
			t.Fatalf("duplicate %q in %+v", n.Node, got)
		}
		seen[n.Node] = true
	}
	if len(got) != 3 {
		t.Fatalf("want 3 entries, got %+v", got)
	}
}

func TestBridgeNodeLabelled(t *testing.T) {
	now := time.Now()
	v := View{Callsigns: false, Network: true, Now: now, Bridge: "1999", BridgeName: "DVSwitch linked node"}
	st := rpt.NodeStatus{Node: "1997", Updated: now,
		Links:   []rpt.Link{{Node: "1999", Kind: rpt.KindAllStar, State: rpt.StateEstablished, Mode: rpt.ModeTransceive}, {Node: "1998", Kind: rpt.KindAllStar, State: rpt.StateEstablished, Mode: rpt.ModeTransceive}},
		Network: []rpt.LinkedNode{{Node: "1999", Mode: rpt.ModeTransceive}, {Node: "1998", Mode: rpt.ModeTransceive}}}
	n := BuildNode(st, nil, v, true)
	if n.Links[0].Node != "1999" || n.Links[0].Label != "DVSwitch linked node" || n.Links[1].Label != "" {
		t.Fatalf("links: %+v", n.Links)
	}
	if n.Network[0].Label != "DVSwitch linked node" || n.Network[1].Label != "" {
		t.Fatalf("network: %+v", n.Network)
	}
	act := BuildActivity([]store.Activity{{Node: "1997", Link: "1999", Start: now, Duration: time.Second}, {Node: "1997", Link: "1998", Start: now, Duration: time.Second}}, nil, v)
	if act[0].Label != "DVSwitch linked node" || act[1].Label != "" {
		t.Fatalf("activity: %+v", act)
	}
	if got := BuildNode(st, nil, View{Now: now}, false).Links[0].Label; got != "" {
		t.Fatalf("no bridge configured but label %q", got)
	}
}
