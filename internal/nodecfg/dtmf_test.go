package nodecfg

import (
	"errors"
	"strings"
	"testing"
)

func TestDTMFCorpus(t *testing.T) {
	d, err := corpusWorld(t).DTMFFor("1999")
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]DTMFGroup{}
	for _, g := range d.Groups {
		by[g.Kind] = g
	}
	f := by["functions"]
	if f.Stanza != "functions" || len(f.Items) == 0 {
		t.Fatalf("functions group: %+v", f)
	}
	find := func(g DTMFGroup, code string) DTMFItem {
		for _, it := range g.Items {
			if it.Code == code {
				return it
			}
		}
		t.Fatalf("no item %q in %s", code, g.Kind)
		return DTMFItem{}
	}
	it := find(f, "70")
	if it.Action != "ilink,5" || it.Desc != "System status" || !it.Inherited || it.Section != "functions-main" || it.File != "rpt.conf" || it.Line == 0 {
		t.Fatalf("code 70: %+v", it)
	}
	if find(f, "5").Desc == "" || find(f, "722").Desc != "Give Time of Day (global)" {
		t.Fatalf("descriptions: %+v %+v", find(f, "5"), find(f, "722"))
	}
	// link and phone functions use the same stanza: not repeated
	for _, k := range []string{"link_functions", "phone_functions"} {
		if by[k].SameAs != "functions" || len(by[k].Items) != 0 {
			t.Fatalf("%s: %+v", k, by[k])
		}
	}
	if tg := by["telemetry"]; len(tg.Items) == 0 || find(tg, "patchup").Desc != "when a patch starts" || find(tg, "ct1").Desc != "" {
		t.Fatalf("telemetry: %+v", tg)
	}
	// the stock macro stanza is empty (all examples are commented out); the
	// node does not set macro, so the stanza name is assumed
	if m := by["macro"]; m.Stanza != "macro" || !m.Assumed || m.Note == "" {
		t.Fatalf("macro: %+v", m)
	}
}

func TestDTMFUnknownAndCustomMacros(t *testing.T) {
	rpt := readCorpus(t, "rpt.conf")
	rpt = strings.Replace(rpt, "[functions](functions-main)\n", "[functions](functions-main)\n123 = frobnicate,7,x\n801 = ilink,9,1999,\"hi\"\n", 1)
	w := fixture(t, map[string]string{
		"rpt.conf":               rpt,
		"custom/rpt/macros.conf": "[macro]\n1 = *31999\n2 = *1199\n",
	})
	d, err := w.DTMFFor("1999")
	if err != nil {
		t.Fatal(err)
	}
	var fn, mac DTMFGroup
	for _, g := range d.Groups {
		switch g.Kind {
		case "functions":
			fn = g
		case "macro":
			mac = g
		}
	}
	var unknown, text DTMFItem
	for _, it := range fn.Items {
		switch it.Code {
		case "123":
			unknown = it
		case "801":
			text = it
		}
	}
	if unknown.Action != "frobnicate,7,x" || unknown.Desc != "" {
		t.Fatalf("unknown code must be raw: %+v", unknown)
	}
	if text.Desc != "Send Text Message" || text.Action != `ilink,9,1999,"hi"` {
		t.Fatalf("extra arguments: %+v", text)
	}
	if len(mac.Items) != 2 || mac.Items[0].File != "custom/rpt/macros.conf" || mac.Items[0].Line != 2 || mac.Items[0].Action != "*31999" {
		t.Fatalf("macros from the included file: %+v", mac)
	}
}

func TestDTMFRefusals(t *testing.T) {
	var re *RefuseError
	if _, err := corpusWorld(t).DTMFFor("4242"); !errors.As(err, &re) {
		t.Fatalf("unknown node: %v", err)
	}
	// a functions stanza defined twice is not guessed
	rpt := readCorpus(t, "rpt.conf") + "\n[functions]\n9 = ilink,1\n"
	d, err := fixture(t, map[string]string{"rpt.conf": rpt}).DTMFFor("1999")
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range d.Groups {
		if g.Kind == "functions" && (len(g.Items) != 0 || !strings.Contains(g.Note, "more than once")) {
			t.Fatalf("functions: %+v", g)
		}
	}
}

// Every description comes from the stock comments: nothing is invented.
func TestDTMFTablesFromCorpus(t *testing.T) {
	text := strings.ToLower(readCorpus(t, "rpt.conf"))
	for k, d := range dtmfCorpus {
		if !strings.Contains(text, strings.ToLower(d)) {
			t.Errorf("%s: %q is not in the stock rpt.conf", k, d)
		}
	}
	for k, d := range dtmfTelemetry {
		if !strings.Contains(text, strings.ToLower(d)) {
			t.Errorf("telemetry %s: %q is not in the stock rpt.conf", k, d)
		}
	}
	if DescribeAction("ilink,5") != "System status" || DescribeAction("macro") == "" || DescribeAction("nope,1") != "" || DescribeAction("") != "" {
		t.Error("DescribeAction")
	}
}
