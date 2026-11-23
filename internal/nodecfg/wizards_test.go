package nodecfg

import (
	"errors"
	"strings"
	"testing"
)

func wizWorld(t *testing.T) *World {
	ext := strings.Replace(readCorpus(t, "extensions.conf"), "HOMENPA = 999", "NODE=1999\nHOMENPA = 999", 1)
	return fixture(t, map[string]string{
		"extensions.conf":             ext,
		"manager.conf":                "[general]\nenabled = yes\nbindaddr = 127.0.0.1\nport = 5038\n\n[admin]\nsecret = old-secret\nread = all\npermit = 127.0.0.1/255.255.255.255\n",
		"rpt_http_registrations.conf": "[general]\nregister => 2001:example-old@register.example.net\n",
		"echolink.conf":               strings.Replace(readCorpus(t, "echolink.conf"), "astnode = 1999", "astnode = 1999", 1),
	})
}

func TestPlanAddNodeInheritsTemplate(t *testing.T) {
	w := wizWorld(t)
	p, err := w.PlanAddNode(AddNodeRequest{Node: "1997", Password: "example-pass", Callsign: "n0call", Radio: RadioSimpleUSB, Preset: "simplex"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Blocked() {
		t.Fatalf("blocked: %+v", p.Errors)
	}
	rpt := p.contents[FileRpt]
	for _, want := range []string{"[1997](node-main)", "1997 = radio@127.0.0.1/1997,NONE", "rxchannel = SimpleUSB/1997", "idrecording = |iN0CALL"} {
		if !strings.Contains(rpt, want) {
			t.Errorf("rpt.conf lacks %q", want)
		}
	}
	if !strings.Contains(rpt, "[1999](node-main)") {
		t.Error("the existing node was changed")
	}
	if !strings.Contains(p.contents[FileSimple], "[1997]") {
		t.Error("no device section")
	}
	if !p.Restart {
		t.Error("a new node needs a restart")
	}
	if !hasCode(p.Warnings, "second_simpleusb") {
		t.Errorf("no second adapter warning: %+v", p.Warnings)
	}
	// diffs are against the real files: only additions to rpt.conf
	for _, f := range p.Files {
		if f.Path == FileRpt && strings.Contains(f.Diff, "\n-[1999]") {
			t.Error("diff removes the existing stanza")
		}
	}
	// the result loads and both nodes are there
	w2 := w.With(p.contents)
	got := w2.LocalNodes()
	if len(got) != 2 {
		t.Fatalf("nodes after: %v", got)
	}
}

func TestPlanAddNodePublicRegistersHub(t *testing.T) {
	w := wizWorld(t)
	p, err := w.PlanAddNode(AddNodeRequest{Node: "2002", Password: "example-pass", Callsign: "N0CALL", Radio: RadioHub, Preset: "hub"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.contents[FileRpt], "rxchannel = Local/pseudo") {
		t.Error("hub rxchannel missing")
	}
	if !strings.Contains(p.contents[FileHTTPReg], "register => 2002:example-pass@") {
		t.Errorf("registration not added: %q", p.contents[FileHTTPReg])
	}
	if !strings.Contains(p.contents[FileHTTPReg], "2001:example-old@") {
		t.Error("the other node's registration was lost")
	}
	if _, ok := p.contents[FileSimple]; ok {
		t.Error("a hub needs no device section")
	}
}

func TestPlanAddNodeRefusals(t *testing.T) {
	w := wizWorld(t)
	good := AddNodeRequest{Node: "1997", Password: "example-pass", Callsign: "N0CALL", Radio: RadioSimpleUSB, Preset: "simplex"}
	for name, mod := range map[string]func(*AddNodeRequest){
		"exists":     func(r *AddNodeRequest) { r.Node = "1999" },
		"registered": func(r *AddNodeRequest) { r.Node = "2001" },
		"badnode":    func(r *AddNodeRequest) { r.Node = "x1" },
		"usbradio":   func(r *AddNodeRequest) { r.Radio = "usbradio" },
		"hubpreset":  func(r *AddNodeRequest) { r.Preset = "hub" },
		"private":    func(r *AddNodeRequest) { r.Registration = &RegChange{Method: RegHTTP} },
		"callsign":   func(r *AddNodeRequest) { r.Callsign = "no way" },
	} {
		r := good
		mod(&r)
		_, err := w.PlanAddNode(r)
		var ie *InvalidError
		if !errors.As(err, &ie) {
			t.Errorf("%s: want InvalidError, got %v", name, err)
		}
	}
	// a device section named like the node counts as existing
	w3 := fixture(t, map[string]string{"simpleusb.conf": readCorpus(t, "simpleusb.conf") + "\n[1997]\ncarrierfrom = usb\n"})
	if _, err := w3.PlanAddNode(good); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("device section not seen: %v", err)
	}
}

func TestPlanAddNodeFlatStanza(t *testing.T) {
	rpt := readCorpus(t, "rpt.conf")
	flat := rpt + "\n[1997]\nrxchannel = Local/pseudo\nduplex = 1\ncontext = radio\nfunctions = functions\ntelemetry = telemetry\n"
	flat = strings.Replace(flat, "[1999](node-main)", "[1999]", 1)
	flat = strings.Replace(flat, "\n[1997]\n", "\n[1995]\n", 1)
	w := fixture(t, map[string]string{"rpt.conf": flat})
	p, err := w.PlanAddNode(AddNodeRequest{Node: "1997", Password: "example-pass", Callsign: "N0CALL", Radio: RadioHub, Preset: "hub", From: "1995"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(p.contents[FileRpt], "[1997](") {
		t.Error("flat source gave a templated stanza")
	}
	if !strings.Contains(p.contents[FileRpt], "functions = functions") {
		t.Errorf("flat basics not copied:\n%s", p.contents[FileRpt][len(p.contents[FileRpt])-400:])
	}
	if len(p.Notes) == 0 {
		t.Error("no note about the flat copy")
	}
}

func TestPlanRenumberPrivate(t *testing.T) {
	w := wizWorld(t)
	p, err := w.PlanRenumber(RenumberRequest{OldNode: "1999", Node: "1997"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Blocked() {
		t.Fatalf("blocked: %+v", p.Errors)
	}
	rpt := p.contents[FileRpt]
	for _, want := range []string{"[1997](node-main)", "1997 = radio@127.0.0.1/1997,NONE", "rxchannel = SimpleUSB/1997"} {
		if !strings.Contains(rpt, want) {
			t.Errorf("rpt.conf lacks %q", want)
		}
	}
	if strings.Contains(rpt, "\n[1999]") {
		t.Error("old stanza left")
	}
	if !strings.Contains(p.contents[FileSimple], "[1997]") || strings.Contains(p.contents[FileSimple], "\n[1999]") {
		t.Error("device section not renamed")
	}
	if !strings.Contains(p.contents[FileExt], "NODE=1997") {
		t.Error("extensions NODE not renamed")
	}
	if !p.Restart || !p.Renumber || len(p.Manual) < 4 {
		t.Errorf("restart/manual: %+v", p)
	}
	for _, s := range p.Steps {
		if s.ID == "savenode" && s.Skipped == "" {
			t.Error("savenode should be skipped unless requested")
		}
	}
}

func TestPlanRenumberRegistrationAndSavenode(t *testing.T) {
	reg := "[general]\nregister => 2001:example-old@register.example.net\n"
	rpt := strings.NewReplacer("[1999](node-main)", "[2001](node-main)").Replace(readCorpus(t, "rpt.conf"))
	w := fixture(t, map[string]string{"rpt.conf": rpt, "rpt_http_registrations.conf": reg,
		"simpleusb.conf": strings.Replace(readCorpus(t, "simpleusb.conf"), "[1999]", "[2001]", 1)})
	// the registered node needs its new password
	if _, err := w.PlanRenumber(RenumberRequest{OldNode: "2001", Node: "2002"}); err == nil {
		t.Fatal("no password: want an error")
	}
	if _, err := w.PlanRenumber(RenumberRequest{OldNode: "2001", Node: "2002", Savenode: true}); err == nil {
		t.Fatal("savenode without password: want an error")
	}
	p, err := w.PlanRenumber(RenumberRequest{OldNode: "2001", Node: "2002", Password: "example-pass", Savenode: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.contents[FileHTTPReg], "register => 2002:example-pass@register.example.net") {
		t.Errorf("registration: %q", p.contents[FileHTTPReg])
	}
	for _, f := range p.Files {
		if strings.Contains(f.Diff, "example-pass") || strings.Contains(f.Diff, "example-old") {
			t.Errorf("secret in diff of %s", f.Path)
		}
	}
	// to a private number: the registration goes
	p, err = w.PlanRenumber(RenumberRequest{OldNode: "2001", Node: "1997"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(p.contents[FileHTTPReg], "register =>") {
		t.Errorf("a private node must not stay registered: %q", p.contents[FileHTTPReg])
	}
}

func TestPlanRenumberRefusals(t *testing.T) {
	w := wizWorld(t)
	for name, r := range map[string]RenumberRequest{
		"same":       {OldNode: "1999", Node: "1999"},
		"unknown":    {OldNode: "1990", Node: "1997"},
		"registered": {OldNode: "1999", Node: "2001"},
		"bad":        {OldNode: "1999", Node: "0"},
	} {
		if _, err := w.PlanRenumber(r); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	// USBRadio is not edited
	rpt := strings.Replace(readCorpus(t, "rpt.conf"), "rxchannel = SimpleUSB/1999      ;", "rxchannel = Radio/1999 ;", 1)
	w2 := fixture(t, map[string]string{"rpt.conf": rpt})
	_, err := w2.PlanRenumber(RenumberRequest{OldNode: "1999", Node: "1997"})
	var re *RefuseError
	if !errors.As(err, &re) || re.Code != "radio_unsupported" {
		t.Errorf("want radio_unsupported, got %v", err)
	}
}

func TestPlanRenumberHubKeepsDevice(t *testing.T) {
	rpt := strings.Replace(readCorpus(t, "rpt.conf"), "rxchannel = SimpleUSB/1999      ;", "rxchannel = Local/pseudo ;", 1)
	w := fixture(t, map[string]string{"rpt.conf": rpt})
	p, err := w.PlanRenumber(RenumberRequest{OldNode: "1999", Node: "1997"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.contents[FileSimple]; ok {
		t.Error("the unused device section must be left alone")
	}
	if len(p.Notes) == 0 {
		t.Error("expected a note about the device section")
	}
}

func TestRenumberReportsOtherReferences(t *testing.T) {
	voter := "[general]\n\n[1999]\nPassword = example-pass\n"
	w := fixture(t, map[string]string{"voter.conf": voter})
	p, err := w.PlanRenumber(RenumberRequest{OldNode: "1999", Node: "1997"})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range p.Warnings {
		if f.Code == "renumber_reference" && f.File == FileVoter {
			found = true
		}
	}
	if !found {
		t.Errorf("voter.conf reference not reported: %+v", p.Warnings)
	}
}

func TestRadioSwitchNotes(t *testing.T) {
	w := wizWorld(t)
	p := mustPlan(t, w, Request{Node: "1999", Radio: &RadioChange{Type: RadioHub}})
	if len(p.Notes) < 2 || !strings.Contains(p.Notes[0], "device section [1999] stays") {
		t.Errorf("notes: %v", p.Notes)
	}
	if !strings.Contains(p.contents[FileRpt], "rxchannel = Local/pseudo") {
		t.Error("rxchannel not changed")
	}
	if _, ok := p.contents[FileSimple]; ok {
		t.Error("device section must stay untouched")
	}
	if !p.Restart {
		t.Error("a radio change needs a restart")
	}
	// back to SimpleUSB reuses the kept section
	w2 := w.With(p.contents)
	p2 := mustPlan(t, w2, Request{Node: "1999", Radio: &RadioChange{Type: RadioSimpleUSB}})
	if _, ok := p2.contents[FileSimple]; ok {
		t.Error("the kept device section should be reused")
	}
}

func TestAMIChecks(t *testing.T) {
	w := wizWorld(t)
	p := mustPlan(t, w, Request{Node: "1999", Changes: []Change{{ID: "manager.conf:general:enabled", Value: "no"}}})
	if !p.Blocked() || !hasCode(p.Errors, CodeAMIDisabled) {
		t.Errorf("enabled=no must block: %+v", p.Errors)
	}
	p = mustPlan(t, w, Request{Node: "1999", Changes: []Change{{ID: "manager.conf:general:bindaddr", Value: "0.0.0.0"}}})
	if p.Blocked() || !hasCode(p.Warnings, CodeAMIExposed) {
		t.Errorf("non-loopback must warn: %+v %+v", p.Errors, p.Warnings)
	}
	p = mustPlan(t, w, Request{Node: "1999", Changes: []Change{{ID: "manager.conf:general:port", Value: "5040"}}})
	if !hasCode(p.Warnings, CodeAMIPort) {
		t.Errorf("port change must warn: %+v", p.Warnings)
	}
	p = mustPlan(t, w, Request{Node: "1999", Changes: []Change{{ID: "manager.conf:admin:permit", Value: "127.0.0.1/255.255.255.255"}, {ID: "manager.conf:admin:secret", Value: "example-pass"}}})
	for _, f := range p.Files {
		if strings.Contains(f.Diff, "example-pass") || strings.Contains(f.Diff, "old-secret") {
			t.Error("secret in the diff")
		}
	}
	ov := w.Analyze()
	if !ov.AMI.Present || !ov.AMI.Loopback || !ov.AMI.HasSecret || len(ov.AMI.Fields) == 0 {
		t.Errorf("AMI info: %+v", ov.AMI)
	}
	for _, f := range ov.AMI.Fields {
		if f.Key == "secret" && (f.Value != "" && f.Value != Masked) {
			t.Errorf("secret shown: %q", f.Value)
		}
	}
}

func TestSimpleUSBDeviceKeys(t *testing.T) {
	w := wizWorld(t)
	p := mustPlan(t, w, Request{Node: "1999", Changes: []Change{
		{ID: "simpleusb.conf:device:devstr", Value: "1-1.4:1.0"},
		{ID: "simpleusb.conf:device:carrierfrom", Value: "usbinvert"},
	}})
	if !strings.Contains(p.contents[FileSimple], "devstr= 1-1.4:1.0") {
		t.Errorf("devstr not written: %q", p.Files[0].Diff)
	}
}
