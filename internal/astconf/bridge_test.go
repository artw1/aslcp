package astconf

import (
	"os"
	"strings"
	"testing"
)

func corpus(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/corpus/asl3-dist/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestAddBridgeNodeTemplate(t *testing.T) {
	rpt := corpus(t, "rpt.conf")
	r, err := AddBridgeNode(rpt, BridgeSpec{Node: "1997", RxPort: 34001, TxPort: 32001, Local: "1999"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Layout != LayoutTemplate || !r.StanzaAdded || !r.NodesLineAdded {
		t.Fatalf("%+v", r)
	}
	out := string(r.Out)
	if !strings.Contains(out, "[1997](node-main)\nrxchannel = USRP/127.0.0.1:34001:32001\nduplex = 0\ncallerid = \"Digital modes 1997\" <0000000000>\nhangtime = 0\nalthangtime = 0\nholdofftelem = 1\ntelemdefault = 0\ntelemdynamic = 0\nnounkeyct = 1\nlinktolink = yes\nidtime = 0\ntailmessagetime = 0\n") {
		t.Fatalf("stanza:\n%s", r.StanzaText)
	}
	if !strings.Contains(out, "nounkeyct = 1\n") {
		t.Fatal("neutral keys missing")
	}
	if r.NodesLine != "1997 = radio@127.0.0.1/1997,NONE" || !strings.Contains(out, r.NodesLine+"\n") {
		t.Fatalf("nodes line %q", r.NodesLine)
	}
	// the original text is untouched except for the additions
	if !strings.HasPrefix(out[:len(rpt)/2], string(rpt[:len(rpt)/2])[:200]) {
		t.Fatal("head changed")
	}
}

func TestAddBridgeNodeFlatAndMinimal(t *testing.T) {
	flat := "[nodes]\n1999 = radio@127.0.0.1/1999,NONE\n\n[1999]\nrxchannel = SimpleUSB/1999\nduplex = 2\nfunctions = functions-1999\ntelemetry = telemetry-1999\nidrecording = /x/y\n"
	r, err := AddBridgeNode([]byte(flat), BridgeSpec{Node: "1997", RxPort: 34001, TxPort: 32001, Local: "1999"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Layout != LayoutFlat {
		t.Fatal(r.Layout)
	}
	want := "[1997]\nrxchannel = USRP/127.0.0.1:34001:32001\nduplex = 0\ncallerid = \"Digital modes 1997\" <0000000000>\nhangtime = 0\nalthangtime = 0\nholdofftelem = 1\ntelemdefault = 0\ntelemdynamic = 0\nnounkeyct = 1\nlinktolink = yes\nidtime = 0\ntailmessagetime = 0\nfunctions = functions-1999\ntelemetry = telemetry-1999"
	if r.StanzaText != want {
		t.Fatalf("stanza %q", r.StanzaText)
	}
	for _, k := range []string{"statpost_url", "idrecording"} {
		if strings.Contains(r.StanzaText, k) {
			t.Fatalf("%s written", k)
		}
	}
	if strings.Contains(r.StanzaText, "idrecording") {
		t.Fatal("idrecording copied")
	}
	if !strings.Contains(string(r.Out), "1999 = radio@127.0.0.1/1999,NONE\n1997 = radio@127.0.0.1/1997,NONE\n") {
		t.Fatalf("%s", r.Out)
	}
	for _, k := range []string{"hangtime = 0", "idtime = 0", "linktolink = yes"} {
		if !strings.Contains(r.StanzaText, k) {
			t.Fatalf("flat: %s missing", k)
		}
	}
	m, err := AddBridgeNode([]byte("[general]\n"), BridgeSpec{Node: "1997", RxPort: 34001, TxPort: 32001})
	if err != nil || m.Layout != LayoutMinimal || !strings.Contains(string(m.Out), "[nodes]\n1997 = radio@127.0.0.1/1997,NONE") || !strings.Contains(m.StanzaText, "idtime = 0") {
		t.Fatalf("%v %s", err, m.Out)
	}
}

func TestAddBridgeNodeExisting(t *testing.T) {
	in := "[nodes]\n1997 = radio@127.0.0.1/1997,NONE\n[1997]\nrxchannel = USRP/127.0.0.1:1111:2222\nduplex = 1\n"
	r, err := AddBridgeNode([]byte(in), BridgeSpec{Node: "1997", RxPort: 34001, TxPort: 32001, Local: "1999"})
	if err != nil || r.Layout != LayoutExisting || r.StanzaAdded || r.NodesLineAdded {
		t.Fatalf("%v %+v", err, r)
	}
	if r.StanzaText != "[1997]\nrxchannel = USRP/127.0.0.1:34001:32001\nduplex = 0" {
		t.Fatalf("existing stanza touched beyond rxchannel/duplex:\n%s", r.StanzaText)
	}
	if !strings.Contains(string(r.Out), "rxchannel = USRP/127.0.0.1:34001:32001\nduplex = 0\n") {
		t.Fatalf("%s", r.Out)
	}
	if _, err := AddBridgeNode([]byte("[1997]\nrxchannel = SimpleUSB/1997\n"), BridgeSpec{Node: "1997", RxPort: 1, TxPort: 2}); err == nil {
		t.Fatal("non-USRP node accepted")
	}
}

func TestRemoveBridgeNodeRoundTrip(t *testing.T) {
	for _, layout := range []string{"template", "flat"} {
		in := corpus(t, "rpt.conf")
		local := "1999"
		if layout == "flat" {
			in = []byte("[nodes]\n1999 = radio@127.0.0.1/1999,NONE\n\n[1999]\nrxchannel = SimpleUSB/1999\nduplex = 2\n")
		}
		r, err := AddBridgeNode(in, BridgeSpec{Node: "1997", RxPort: 34001, TxPort: 32001, Local: local})
		if err != nil {
			t.Fatal(err)
		}
		out, rs, rl, probs := RemoveBridgeNode(r.Out, "1997", r.StanzaText, r.NodesLine, r.BlankAdded)
		if !rs || !rl || len(probs) != 0 {
			t.Fatalf("%s: %v %v %v", layout, rs, rl, probs)
		}
		if string(out) != string(in) {
			t.Fatalf("%s: round trip differs:\n%s", layout, out)
		}
	}
}

func TestRemoveBridgeNodeChangedByHand(t *testing.T) {
	r, _ := AddBridgeNode([]byte("[nodes]\n\n[1999]\nrxchannel = SimpleUSB/1999\n"), BridgeSpec{Node: "1997", RxPort: 34001, TxPort: 32001, Local: "1999"})
	edited := strings.Replace(string(r.Out), "duplex = 0", "duplex = 0\nhangtime = 99", 1)
	edited = strings.Replace(edited, "1997 = radio@127.0.0.1/1997,NONE", "1997 = radio@10.0.0.1/1997,NONE", 1)
	out, rs, rl, probs := RemoveBridgeNode([]byte(edited), "1997", r.StanzaText, r.NodesLine, r.BlankAdded)
	if rs || rl || len(probs) != 2 || string(out) != edited {
		t.Fatalf("%v %v %v", rs, rl, probs)
	}
	for _, p := range probs {
		if !strings.Contains(p, "changed by hand") {
			t.Fatal(p)
		}
	}
}

func TestSetModuleLoad(t *testing.T) {
	mod := corpus(t, "modules.conf")
	out, was, err := SetModuleLoad(mod, ModuleUSRP)
	if err != nil || was != ModNoload {
		t.Fatalf("%v %s", err, was)
	}
	if !strings.Contains(string(out), "load    = chan_usrp.so                   ; USRP Channel Module\n") {
		t.Fatalf("not edited in place:\n%s", grepLine(string(out), "usrp"))
	}
	if strings.Count(string(out), "noload") != strings.Count(string(mod), "noload")-1 {
		t.Fatal("other noload lines changed")
	}
	back, ch, err := RevertModuleLoad(out, ModuleUSRP, ModNoload)
	if err != nil || !ch || string(back) != string(mod) {
		t.Fatalf("revert differs: %v %v", err, ch)
	}
	// already load: unchanged
	out2, was2, _ := SetModuleLoad(out, ModuleUSRP)
	if was2 != ModLoad || string(out2) != string(out) {
		t.Fatal("load not left alone")
	}
	if _, ch, _ := RevertModuleLoad(out, ModuleUSRP, ModLoad); ch {
		t.Fatal("reverted a module ASLCP did not change")
	}
	// absent: a load line is added, and removed again
	abs := "[modules]\nautoload = no\nnoload = app_gps.so\n"
	out3, was3, _ := SetModuleLoad([]byte(abs), ModuleUSRP)
	if was3 != ModAbsent || !strings.HasSuffix(string(out3), "load = chan_usrp.so\n") {
		t.Fatalf("%s %s", was3, out3)
	}
	back3, ch3, _ := RevertModuleLoad(out3, ModuleUSRP, ModAbsent)
	if !ch3 || string(back3) != abs {
		t.Fatalf("%q", back3)
	}
}

func grepLine(s, sub string) string {
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, sub) {
			return l
		}
	}
	return ""
}

func TestRevertBridgeNodeGoneVersusEdited(t *testing.T) {
	base := "[nodes]\n\n[1999]\nrxchannel = SimpleUSB/1999\n"
	r, _ := AddBridgeNode([]byte(base), BridgeSpec{Node: "1997", RxPort: 34001, TxPort: 32001, Local: "1999"})
	// untouched: exact revert, no problems
	out, rs, rl, ps := RevertBridgeNode(r.Out, "1997", r.StanzaText, r.NodesLine, r.BlankAdded)
	if !rs || !rl || len(ps) != 0 || string(out) != base {
		t.Fatalf("%v %v %v\n%s", rs, rl, ps, out)
	}
	// already gone: reported as Gone, not as a blocker
	_, rs, rl, ps = RevertBridgeNode([]byte(base), "1997", r.StanzaText, r.NodesLine, r.BlankAdded)
	if rs || rl || len(ps) != 2 {
		t.Fatalf("%v %v %v", rs, rl, ps)
	}
	for _, p := range ps {
		if !p.Gone {
			t.Fatalf("not marked gone: %+v", p)
		}
	}
	// hand-edited: a blocker, text untouched
	edited := strings.Replace(string(r.Out), "duplex = 0", "duplex = 0\nhangtime = 99", 1)
	out, rs, _, ps = RevertBridgeNode([]byte(edited), "1997", r.StanzaText, r.NodesLine, r.BlankAdded)
	if rs || len(ps) != 1 || ps[0].Gone || !strings.Contains(ps[0].Msg, "changed by hand") || !strings.Contains(string(out), "hangtime = 99") {
		t.Fatalf("%v %+v", rs, ps)
	}
}

func TestExistingBridgeNode(t *testing.T) {
	rpt := "[1999]\nrxchannel = USRP/127.0.0.1:34001:32001\n\n[nodes]\n1999 = radio@127.0.0.1/1999,NONE\n"
	st, line, ok := ExistingBridgeNode([]byte(rpt), "1999")
	if !ok || !strings.Contains(st, "[1999]") || line != "1999 = radio@127.0.0.1/1999,NONE" {
		t.Fatalf("%q %q %v", st, line, ok)
	}
	if _, line, ok := ExistingBridgeNode([]byte("[1999]\nrxchannel = x\n"), "1999"); !ok || line != "" {
		t.Fatalf("no nodes section: %q %v", line, ok)
	}
	for _, bad := range []string{"[1998]\na = b\n", "[1999](!)\na = b\n", "[1999]\na = b\n[1999]\nc = d\n"} {
		if _, _, ok := ExistingBridgeNode([]byte(bad), "1999"); ok {
			t.Errorf("accepted %q", bad)
		}
	}
	if !HasNodeStanza([]byte(rpt), "1999") || HasNodeStanza([]byte(rpt), "1998") || HasNodeStanza([]byte("[1999](!)\n"), "1999") {
		t.Error("HasNodeStanza")
	}
}

func TestModuleState(t *testing.T) {
	for in, want := range map[string]string{
		"[modules]\nautoload = yes\nnoload = chan_usrp.so\n": ModNoload,
		"[modules]\nautoload = yes\nload = chan_usrp.so\n":   ModExplicit,
		"[modules]\nautoload = yes\n":                        ModAuto,
		"[modules]\nautoload = no\n":                         ModOff,
	} {
		if got, err := ModuleState([]byte(in), "chan_usrp.so"); err != nil || got != want {
			t.Errorf("%q: %q %v, want %q", in, got, err, want)
		}
	}
}
