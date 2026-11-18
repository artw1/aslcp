package rpt

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/ami"
)

func loadMsg(t *testing.T, name string) *ami.Message {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	m, err := ami.ReadMessage(bufio.NewReader(f))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return m
}

func secPtr(n int) *time.Duration { d := time.Duration(n) * time.Second; return &d }

func TestClassify(t *testing.T) {
	tests := map[string]NodeKind{
		"1999": KindAllStar, "55555": KindAllStar, "2000000": KindAllStar,
		"3141592": KindEchoLink, "3000000": KindEchoLink, "31415926": KindAllStar, "314159": KindAllStar,
		"W1AW-P": KindPhonePortal, "1234-P": KindPhonePortal,
		"N0CALL": KindClient, "web-client": KindClient,
		"": KindUnknown, "-x": KindUnknown, "12ab": KindUnknown,
	}
	for in, want := range tests {
		if got := Classify(in); got != want {
			t.Errorf("Classify(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestParseConn(t *testing.T) {
	pad := func(name, ip, rc, dir, tm, st string) string {
		// Same layout as "%-10s%-20s%-12s%-11s%-20s%-20s".
		f := func(s string, w int) string { return s + strings.Repeat(" ", max(0, w-len(s))) }
		return f(name, 10) + f(ip, 20) + f(rc, 12) + f(dir, 11) + f(tm, 20) + f(st, 20)
	}
	tests := []struct {
		name string
		in   string
		want Conn
		err  bool
	}{
		{"with ip", pad("2000", "192.0.2.10", "0", "OUT", "00:12:34", "ESTABLISHED"),
			Conn{Node: "2000", Kind: KindAllStar, IP: "192.0.2.10", Direction: DirOut, Connected: 12*time.Minute + 34*time.Second, State: "ESTABLISHED"}, false},
		{"empty ip", pad("N0CALL", "", "3", "IN", "00:00:45", "ESTABLISHED"),
			Conn{Node: "N0CALL", Kind: KindClient, Reconnects: 3, Direction: DirIn, Connected: 45 * time.Second, State: "ESTABLISHED"}, false},
		{"none placeholder", pad("2000", "(none)", "0", "OUT", "00:00:01", "CONNECTING"),
			Conn{Node: "2000", Kind: KindAllStar, Direction: DirOut, Connected: time.Second, State: "CONNECTING"}, false},
		{"over 24h and over 99h", pad("1999", "", "1", "IN", "123:05:09", "ESTABLISHED"),
			Conn{Node: "1999", Kind: KindAllStar, Reconnects: 1, Direction: DirIn, Connected: 123*time.Hour + 5*time.Minute + 9*time.Second, State: "ESTABLISHED"}, false},
		{"echolink", pad("3141592", "", "0", "IN", "00:00:00", "ESTABLISHED"),
			Conn{Node: "3141592", Kind: KindEchoLink, Direction: DirIn, State: "ESTABLISHED"}, false},
		{"too short", "2000 OUT", Conn{}, true},
		{"bad direction", pad("2000", "", "0", "SIDEWAYS", "00:00:00", "ESTABLISHED"), Conn{}, true},
		{"bad time", pad("2000", "", "0", "IN", "xx:00:00", "ESTABLISHED"), Conn{}, true},
		{"bad state", pad("2000", "", "0", "IN", "00:00:00", "WEIRD"), Conn{}, true},
		{"bad reconnects", pad("2000", "", "x", "IN", "00:00:00", "ESTABLISHED"), Conn{}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseConn(tc.in)
			if (err != nil) != tc.err {
				t.Fatalf("err = %v", err)
			}
			if !tc.err && got != tc.want {
				t.Errorf("got %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func TestParseLinkedNodes(t *testing.T) {
	tests := []struct {
		in   string
		want []LinkedNode
	}{
		{"<NONE>", nil},
		{"", nil},
		{"T1234", []LinkedNode{{"1234", KindAllStar, ModeTransceive}}},
		{"T1234, R2000, L2001, C2002", []LinkedNode{
			{"1234", KindAllStar, ModeTransceive}, {"2000", KindAllStar, ModeMonitor},
			{"2001", KindAllStar, ModeLocalMonitor}, {"2002", KindAllStar, ModeConnecting}}},
		{"TN0CALL, T3141592, TW1AW-P", []LinkedNode{
			{"N0CALL", KindClient, ModeTransceive}, {"3141592", KindEchoLink, ModeTransceive},
			{"W1AW-P", KindPhonePortal, ModeTransceive}}},
		{"T1234, R000000", []LinkedNode{{"1234", KindAllStar, ModeTransceive}}}, // truncation marker
	}
	for _, tc := range tests {
		if got := ParseLinkedNodes(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("ParseLinkedNodes(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestParseALinks(t *testing.T) {
	tests := []struct {
		in   string
		n    int
		want []ALink
		err  bool
	}{
		{"", 0, nil, false},
		{"0", 0, nil, false},
		{"1,2000TK", 1, []ALink{{"2000", KindAllStar, ModeTransceive, true}}, false},
		{"3,2000TU,55555RK,N0CALLCU", 3, []ALink{
			{"2000", KindAllStar, ModeTransceive, false},
			{"55555", KindAllStar, ModeMonitor, true},
			{"N0CALL", KindClient, ModeConnecting, false}}, false},
		{"2,3141592TK,000000RU", 2, []ALink{{"3141592", KindEchoLink, ModeTransceive, true}}, false}, // truncation marker dropped
		{"x,2000TK", 0, nil, true},
		{"1,2000XK", 0, nil, true},
		{"1,2000TZ", 0, nil, true},
		{"1,TK", 0, nil, true},
	}
	for _, tc := range tests {
		n, got, err := ParseALinks(tc.in)
		if (err != nil) != tc.err {
			t.Errorf("%q: err = %v", tc.in, err)
			continue
		}
		if !tc.err && (n != tc.n || !reflect.DeepEqual(got, tc.want)) {
			t.Errorf("%q: got %d %v, want %d %v", tc.in, n, got, tc.n, tc.want)
		}
	}
}

func TestParseLinks(t *testing.T) {
	n, l := ParseLinks("3,T2000,R2001,C2002")
	if n != 3 || len(l) != 3 || l[1].Mode != ModeMonitor || l[2].Mode != ModeConnecting {
		t.Errorf("got %d %v", n, l)
	}
	if n, l := ParseLinks(""); n != 0 || l != nil {
		t.Errorf("empty: %d %v", n, l)
	}
}

func TestParseHMS(t *testing.T) {
	for in, want := range map[string]time.Duration{
		"00:00:00": 0, "00:12:34": 12*time.Minute + 34*time.Second, "1000:00:01": 1000*time.Hour + time.Second,
	} {
		got, err := ParseHMS(in)
		if err != nil || got != want {
			t.Errorf("%q: %v %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "1:2", "a:b:c", "-1:00:00", "1:2:3:4"} {
		if _, err := ParseHMS(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestParseXStatBasic(t *testing.T) {
	x, err := ParseXStat(loadMsg(t, "xstat_basic.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if x.Node != "1999" || len(x.Conns) != 5 {
		t.Fatalf("node=%q conns=%d", x.Node, len(x.Conns))
	}
	if c := x.Conns[2]; c.Node != "N0CALL" || c.IP != "" || c.Direction != DirIn || c.Kind != KindClient {
		t.Errorf("conn[2] = %+v", c)
	}
	if c := x.Conns[1]; c.Reconnects != 2 || c.IP != "198.51.100.20" || c.Connected != 3*time.Hour+time.Second {
		t.Errorf("conn[1] = %+v", c)
	}
	if len(x.Network) != 6 || x.Network[3] != (LinkedNode{"55555", KindAllStar, ModeMonitor}) {
		t.Errorf("network = %v", x.Network)
	}
	if x.RxKeyed || !x.TxKeyed || x.ExtTxKeyed {
		t.Errorf("keyed rx=%v tx=%v etx=%v", x.RxKeyed, x.TxKeyed, x.ExtTxKeyed)
	}
	if x.NumLinks != 6 || len(x.Links) != 6 || x.NumALinks != 4 || len(x.ALinks) != 4 {
		t.Errorf("counts: %d %d %d %d", x.NumLinks, len(x.Links), x.NumALinks, len(x.ALinks))
	}
	if !x.ALinks[0].Keyed || x.ALinks[1].Keyed || x.ALinks[1].Mode != ModeMonitor {
		t.Errorf("alinks = %v", x.ALinks)
	}
	if x.Vars["RPT_AUTOPATCHUP"] != "0" || x.Vars["RPT_STAT_RXKEYED"] != "0" {
		t.Errorf("vars = %v", x.Vars)
	}
	f := x.Flags
	want := Flags{Valid: true, AutopatchDisabled: true, TotState: TotReset, IderState: IderClean, PatchState: PatchDown, TelMode: 1}
	if f != want {
		t.Errorf("flags = %+v\nwant   %+v", f, want)
	}
	if x.State["patch_ena"] != "1" || x.State["tel_mode"] != "1" {
		t.Errorf("state = %v", x.State)
	}
}

func TestParseXStatNoLinksAndDisabledFlags(t *testing.T) {
	x, err := ParseXStat(loadMsg(t, "xstat_nolinks.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(x.Conns) != 0 || x.Network != nil || x.NumLinks != 0 || len(x.ALinks) != 0 {
		t.Errorf("unexpected links: %+v", x)
	}
	want := Flags{Valid: true, ParrotEnabled: true, TxDisabled: true, TotDisabled: true, LinkFunsDisabled: true,
		SchedulerDisabled: true, UserFunsDisabled: true, AlternateTail: true, NoIncomingConns: true,
		TotState: TotTimedOut, IderState: IderQueuedInTail, PatchState: PatchUp, TelMode: 3}
	if x.Flags != want {
		t.Errorf("flags = %+v\nwant   %+v", x.Flags, want)
	}
	if TotTimedOut.String() != "timed-out" || PatchUp.String() != "up" || IderClean.String() != "clean" {
		t.Error("String methods")
	}
}

func TestParseXStatLongLivedAndConnecting(t *testing.T) {
	x, err := ParseXStat(loadMsg(t, "xstat_longlived.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if x.Conns[0].Connected != 123*time.Hour+5*time.Minute+9*time.Second || x.Conns[0].Reconnects != 1 {
		t.Errorf("%+v", x.Conns[0])
	}
	if x.Conns[1].State != StateConnecting || x.Conns[1].IP != "192.0.2.9" || x.Conns[1].Kind != KindPhonePortal {
		t.Errorf("%+v", x.Conns[1])
	}
	if x.Flags.Valid {
		t.Error("flags valid without trailing lines")
	}
}

func TestParseXStatErrors(t *testing.T) {
	m := &ami.Message{Fields: []ami.Header{{Key: "Conn", Value: "garbage"}}}
	if _, err := ParseXStat(m); err == nil {
		t.Error("bad Conn accepted")
	}
	m = &ami.Message{Fields: []ami.Header{{Key: "Var", Value: "RPT_ALINKS=1,2000QQ"}}}
	if _, err := ParseXStat(m); err == nil {
		t.Error("bad ALINKS accepted")
	}
}

func TestSawStatAndMerge(t *testing.T) {
	x, err := ParseXStat(loadMsg(t, "xstat_basic.txt"))
	if err != nil {
		t.Fatal(err)
	}
	saw, err := ParseSawStat(loadMsg(t, "sawstat_basic.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(saw) != 5 || !saw[0].Keyed || *saw[0].SinceKey != 5*time.Second || *saw[0].SinceUnkey != 300*time.Second ||
		saw[2].SinceKey != nil || saw[2].SinceUnkey != nil || saw[2].Keyed {
		t.Fatalf("saw = %+v", saw)
	}
	now := time.Unix(1700000000, 0)
	st := Merge("1999", x, saw, now)
	if st.Node != "1999" || !st.TxKeyed || st.RxKeyed || st.Updated != now || len(st.Links) != 5 {
		t.Fatalf("status = %+v", st)
	}
	by := map[string]Link{}
	for _, l := range st.Links {
		by[l.Node] = l
	}
	if l := by["2000"]; l.Mode != ModeTransceive || !l.Keyed || *l.SinceKey != 5*time.Second || l.Direction != DirOut {
		t.Errorf("2000 = %+v", l)
	}
	if l := by["55555"]; l.Mode != ModeMonitor || l.Keyed || *l.SinceUnkey != 100*time.Second {
		t.Errorf("55555 = %+v", l)
	}
	if l := by["N0CALL"]; l.SinceKey != nil || l.SinceUnkey != nil || l.Mode != ModeTransceive {
		t.Errorf("N0CALL = %+v", l)
	}
	if l := by["2001"]; l.Mode != ModeLocalMonitor {
		t.Errorf("2001 should be inferred local monitor: %+v", l)
	}
	// Without SawStat the ALINKS keyed flag is used.
	st = Merge("1999", x, nil, now)
	for _, l := range st.Links {
		if l.Node == "2000" && (!l.Keyed || l.SinceKey != nil) {
			t.Errorf("fallback keyed: %+v", l)
		}
	}
	// Clone is deep.
	a := Merge("1999", x, saw, now)
	b := a.Clone()
	*b.Links[0].SinceKey = time.Hour
	b.Links[0].Node = "x"
	if a.Links[0].Node == "x" || *a.Links[0].SinceKey == time.Hour {
		t.Error("Clone shares memory")
	}

	empty, err := ParseSawStat(loadMsg(t, "sawstat_empty.txt"))
	if err != nil || len(empty) != 0 {
		t.Errorf("empty saw: %v %v", empty, err)
	}
	for _, bad := range []string{"2000 1 5", "2000 x 5 5", "2000 1 y 5", "2000 1 5 z"} {
		if _, err := ParseSawStat(&ami.Message{Fields: []ami.Header{{Key: "Conn", Value: bad}}}); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestParseLocalNodesAndRptStat(t *testing.T) {
	nodes, err := ParseLocalNodesXML(loadMsg(t, "localnodes_xml.txt").Body)
	if err != nil || !reflect.DeepEqual(nodes, []string{"1999", "2000"}) {
		t.Errorf("xml: %v %v", nodes, err)
	}
	if n, err := ParseLocalNodesXML([]string{`<?xml version="1.0"?>`, `<nodes>`, `</nodes>`}); err != nil || len(n) != 0 {
		t.Errorf("empty xml: %v %v", n, err)
	}
	if _, err := ParseLocalNodesXML([]string{"not xml <"}); err == nil {
		t.Error("garbage accepted")
	}
	nodes, err = ParseRptStat(loadMsg(t, "rptstat.txt"))
	if err != nil || !reflect.DeepEqual(nodes, []string{"1999", "2000"}) {
		t.Errorf("rptstat: %v %v", nodes, err)
	}
	nodes, err = ParseRptStat(loadMsg(t, "rptstat_none.txt"))
	if err != nil || len(nodes) != 0 {
		t.Errorf("rptstat none: %v %v", nodes, err)
	}
	if _, err := ParseRptStat(&ami.Message{}); err == nil {
		t.Error("empty rptstat accepted")
	}
}

func TestParseUptime(t *testing.T) {
	b, _ := os.ReadFile("testdata/uptime.txt")
	u, err := ParseUptime(strings.Split(strings.TrimSpace(string(b)), "\n"))
	if err != nil || u.System != 86400*time.Second || u.LastReload != 3600*time.Second {
		t.Errorf("%+v %v", u, err)
	}
	if _, err := ParseUptime([]string{"No such command"}); err == nil {
		t.Error("garbage accepted")
	}
}

func TestParseEvent(t *testing.T) {
	mk := func(name, val string) *ami.Message {
		return &ami.Message{Fields: []ami.Header{
			{Key: "Event", Value: name}, {Key: "Node", Value: "1999"}, {Key: "Channel", Value: "SimpleUSB/usb"},
			{Key: "EventValue", Value: val}, {Key: "LastKeyedTime", Value: "Tue Nov 11 10:00:00 2025"},
			{Key: "LastTxKeyedTime", Value: "Thu Jan  1 00:00:00 1970"}}}
	}
	e, ok := ParseEvent(mk(EvRxKeyed, "1"))
	if !ok || e.Kind != EventRxKeyed || !e.Keyed || e.Node != "1999" || e.Channel != "SimpleUSB/usb" {
		t.Fatalf("%+v", e)
	}
	if e.LastKeyed.IsZero() || e.LastKeyed.Year() != 2025 || !e.LastTxKeyed.IsZero() {
		t.Errorf("times: %v %v", e.LastKeyed, e.LastTxKeyed)
	}
	tests := []struct {
		name, val string
		check     func(Event) bool
	}{
		{EvTxKeyed, "0", func(e Event) bool { return e.Kind == EventTxKeyed && !e.Keyed }},
		{EvExtTxKeyed, "1", func(e Event) bool { return e.Kind == EventExtTxKeyed && e.Keyed }},
		{EvAutopatchUp, "1", func(e Event) bool { return e.Kind == EventAutopatchUp && e.Keyed }},
		{EvNumLinks, "4", func(e Event) bool { return e.Kind == EventNumLinks && e.Count == 4 && e.Err == nil }},
		{EvNumALinks, "x", func(e Event) bool { return e.Kind == EventNumALinks && e.Err != nil }},
		{EvLinks, "2,T2000,R2001", func(e Event) bool { return e.Kind == EventLinks && e.Count == 2 && len(e.Links) == 2 }},
		{EvALinks, "2,2000TK,2001RU", func(e Event) bool {
			return e.Kind == EventALinks && len(e.ALinks) == 2 && e.ALinks[0].Keyed && !e.ALinks[1].Keyed
		}},
		{EvALinks, "", func(e Event) bool { return e.Kind == EventALinks && len(e.ALinks) == 0 && e.Err == nil }},
		{EvALinks, "junk", func(e Event) bool { return e.Err != nil }},
		{"RPT_SOMETHINGNEW", "abc", func(e Event) bool { return e.Kind == EventOther && e.Name == "RPT_SOMETHINGNEW" && e.Value == "abc" }},
		{"MDC-1200", "x", func(e Event) bool { return e.Kind == EventOther }},
	}
	for _, tc := range tests {
		e, ok := ParseEvent(mk(tc.name, tc.val))
		if !ok || !tc.check(e) {
			t.Errorf("%s=%q: %+v ok=%v", tc.name, tc.val, e, ok)
		}
	}
	for _, m := range []*ami.Message{
		{Fields: []ami.Header{{Key: "Response", Value: "Success"}}},
		{Fields: []ami.Header{{Key: "Event", Value: "FullyBooted"}}},
	} {
		if _, ok := ParseEvent(m); ok {
			t.Errorf("non-rpt message accepted: %v", m.Lines())
		}
	}
}

func TestParseConnLongName(t *testing.T) {
	line := fmt.Sprintf("%-10s%-20s%-12d%-11s%-20s%-20s", "W1ABCDEF-P", "10.0.0.5", 0, "IN", "00:01:02", "ESTABLISHED")
	c, err := ParseConn(line)
	if err != nil {
		t.Fatal(err)
	}
	if c.Node != "W1ABCDEF-P" || c.IP != "10.0.0.5" {
		t.Fatalf("got node %q ip %q", c.Node, c.IP)
	}
	line = fmt.Sprintf("%-10s%-20s%-12d%-11s%-20s%-20s", "W1ABCDEFGH-P", "192.168.1.9", 0, "IN", "00:01:02", "ESTABLISHED")
	if c, err = ParseConn(line); err != nil || c.Node != "W1ABCDEFGH-P" || c.IP != "192.168.1.9" {
		t.Fatalf("got node %q ip %q err %v", c.Node, c.IP, err)
	}
}

func TestMergeDedupesLinksAndNetwork(t *testing.T) {
	now := time.Unix(1700000000, 0)
	x := &XStat{
		Conns: []Conn{
			{Node: "1998", Direction: DirOut, State: "CONNECTING", Connected: time.Second},
			{Node: "1997", Direction: DirIn, State: StateEstablished, Connected: time.Minute},
			{Node: "1998", Direction: DirIn, State: StateEstablished, Connected: time.Hour},
			{Node: "1997", Direction: DirOut, State: StateEstablished, Connected: time.Second},
			{Node: "1999", Direction: DirIn, State: "CONNECTING"},
			{Node: "1999", Direction: DirOut, State: "CONNECTING"},
		},
		Network: []LinkedNode{
			{Node: "1998", Mode: ModeTransceive}, {Node: "1997", Mode: ModeLocalMonitor},
			{Node: "1998", Mode: ModeLocalMonitor}, {Node: "1997", Mode: ModeLocalMonitor},
		},
	}
	st := Merge("1996", x, nil, now)
	if len(st.Links) != 3 {
		t.Fatalf("links: %+v", st.Links)
	}
	// first position kept; the ESTABLISHED duplicate replaces a CONNECTING first
	if st.Links[0].Node != "1998" || st.Links[0].State != StateEstablished || st.Links[0].Direction != DirIn || st.Links[0].Connected != time.Hour {
		t.Fatalf("1998: %+v", st.Links[0])
	}
	// both ESTABLISHED: the first wins
	if st.Links[1].Node != "1997" || st.Links[1].Direction != DirIn || st.Links[1].Connected != time.Minute {
		t.Fatalf("1997: %+v", st.Links[1])
	}
	// both CONNECTING: the first wins
	if st.Links[2].Node != "1999" || st.Links[2].Direction != DirIn {
		t.Fatalf("1999: %+v", st.Links[2])
	}
	if len(st.Network) != 2 || st.Network[0].Node != "1998" || st.Network[0].Mode != ModeTransceive || st.Network[1].Node != "1997" {
		t.Fatalf("network: %+v", st.Network)
	}
	if len(x.Network) != 4 {
		t.Fatal("input modified")
	}
}
