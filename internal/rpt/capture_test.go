package rpt

import (
	"bufio"
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/ami"
)

// readCapture parses a raw AMI transcript (banner first) into messages.
func readCapture(t *testing.T, path string) []*ami.Message {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r := bufio.NewReader(f)
	if _, err := r.ReadString('\n'); err != nil { // banner
		t.Fatal(err)
	}
	var msgs []*ami.Message
	for {
		m, err := ami.ReadMessage(r)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		msgs = append(msgs, m)
	}
	return msgs
}

func TestRealCaptureASL3(t *testing.T) {
	msgs := readCapture(t, "testdata/capture/asl3-22.10.1-node.txt")
	byID := map[string]*ami.Message{}
	var events []Event
	var xml *ami.Message
	for _, m := range msgs {
		switch {
		case m.Get("ActionID") != "":
			byID[m.Get("ActionID")] = m
		case m.Event() != "":
			if e, ok := ParseEvent(m); ok {
				events = append(events, e)
			}
		default:
			xml = m
		}
	}

	// RptLocalNodes: raw XML body, no Response/ActionID.
	if xml == nil {
		t.Fatal("no RptLocalNodes XML message")
	}
	nodes, err := ParseLocalNodesXML(xml.Lines())
	if err != nil || len(nodes) != 2 || nodes[0] != "2001" || nodes[1] != "1998" {
		t.Fatalf("local nodes = %v, %v", nodes, err)
	}
	if n, err := ParseRptStat(byID["rptstat"]); err != nil || len(n) != 2 {
		t.Fatalf("rptstat = %v, %v", n, err)
	}

	// Active node: one outbound transceive link into a 159-node network.
	x, err := ParseXStat(byID["XStat-2001"])
	if err != nil {
		t.Fatal(err)
	}
	if len(x.Conns) != 1 {
		t.Fatalf("conns = %+v", x.Conns)
	}
	c := x.Conns[0]
	if c.Node != "50241" || c.IP != "192.0.2.58" || c.Direction != DirOut || c.Connected != 29*time.Second || c.State != StateEstablished {
		t.Fatalf("conn = %+v", c)
	}
	if x.NumLinks != 159 || x.NumALinks != 1 || x.TxKeyed || x.RxKeyed || x.ExtTxKeyed {
		t.Fatalf("xstat flags = %+v", x)
	}
	if len(x.ALinks) != 1 || x.ALinks[0].Node != "50241" || x.ALinks[0].Keyed {
		t.Fatalf("alinks = %+v", x.ALinks)
	}
	if _, ok := x.Vars["XX_RPT_RXKEYED"]; !ok {
		t.Error("unknown Var lines should be kept")
	}
	saw, err := ParseSawStat(byID["SawStat-2001"])
	if err != nil || len(saw) != 1 || saw[0].Node != "50241" || saw[0].Keyed {
		t.Fatalf("sawstat = %+v, %v", saw, err)
	}
	st := Merge("2001", x, saw, time.Now())
	if len(st.Links) != 1 || st.Links[0].Mode != ModeTransceive {
		t.Fatalf("merged links = %+v", st.Links)
	}
	// Network list contains callsign entries and duplicates; it must be
	// de-duplicated for display.
	seen := map[string]bool{}
	for _, n := range st.Network {
		if seen[n.Node] {
			t.Errorf("duplicate network entry %q", n.Node)
		}
		seen[n.Node] = true
	}
	for _, want := range []string{"N0AAA", "HOIP-400000", "1528793"} {
		if !seen[want] {
			t.Errorf("network missing %q", want)
		}
	}

	// Idle node: no links, SawStat with no Conn lines.
	x2, err := ParseXStat(byID["XStat-1998"])
	if err != nil || len(x2.Conns) != 0 || len(x2.Network) != 0 || x2.NumLinks != 0 || len(x2.ALinks) != 0 {
		t.Fatalf("idle xstat = %+v, %v", x2, err)
	}
	if saw2, err := ParseSawStat(byID["SawStat-1998"]); err != nil || len(saw2) != 0 {
		t.Fatalf("idle sawstat = %+v, %v", saw2, err)
	}

	up, err := ParseUptime(byID["uptime"].All("Output"))
	if err != nil || up.System != 120660*time.Second {
		t.Fatalf("uptime = %+v, %v", up, err)
	}

	// Events: per-link keying toggles, TX keying, link drop (empty ALINKS).
	var keyedToggles, tx, emptyALinks int
	for _, e := range events {
		switch e.Kind {
		case EventALinks:
			if e.Value == "" {
				emptyALinks++
				if len(e.ALinks) != 0 {
					t.Errorf("empty RPT_ALINKS gave %+v", e.ALinks)
				}
			} else if len(e.ALinks) == 1 && e.ALinks[0].Keyed {
				keyedToggles++
			}
		case EventTxKeyed:
			tx++
		}
		if e.Node != "2001" && e.Kind != EventOther {
			t.Errorf("event node = %q", e.Node)
		}
	}
	if keyedToggles < 2 || tx < 3 || emptyALinks != 1 {
		t.Fatalf("events: keyed=%d tx=%d emptyALinks=%d", keyedToggles, tx, emptyALinks)
	}
}

func TestNodeStatCapture(t *testing.T) {
	var m *ami.Message
	for _, x := range readCapture(t, "testdata/capture/asl3-22.10.1-node.txt") {
		if x.Get("ActionID") == "NodeStat-2001" {
			m = x
		}
	}
	if m == nil {
		t.Fatal("no NodeStat reply in capture")
	}
	got := ParseNodeStat(m)
	if len(got) != 28 {
		t.Fatalf("fields: %d %v", len(got), got)
	}
	if got[0] != (StatField{"IsRemoteBase", "NO"}) || got[1].Key != "NodeState" {
		t.Fatalf("order: %v", got[:2])
	}
	want := map[string]string{"KeyupsToday": "2", "TxTimeToday": "00:00:33:172", "NodesCurrentlyConnectedToUs": "50241", "IdentifierState": "QUEUED FOR CLEANUP"}
	for _, f := range got {
		if f.Key == "Response" || f.Key == "ActionID" {
			t.Fatalf("bookkeeping key kept: %v", f)
		}
		if v, ok := want[f.Key]; ok {
			if f.Value != v {
				t.Fatalf("%s=%q want %q", f.Key, f.Value, v)
			}
			delete(want, f.Key)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing: %v", want)
	}
}
