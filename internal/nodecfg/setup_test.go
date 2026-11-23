package nodecfg

import (
	"errors"
	"strings"
	"testing"
)

func setupWorld(t *testing.T) *World {
	ext := strings.Replace(readCorpus(t, "extensions.conf"), "HOMENPA = 999", "NODE=1999\nHOMENPA = 999", 1)
	return fixture(t, map[string]string{
		"extensions.conf":             ext,
		"manager.conf":                "[general]\nenabled = yes\n\n[admin]\nsecret = old-secret\nread = all\n",
		"rpt_http_registrations.conf": "[general]\nregister => 1234:abcdef@register.allstarlink.org\n",
	})
}

func goodSetup() SetupRequest {
	return SetupRequest{OldNode: "1999", Node: "1997", Password: "example-pass", Callsign: "n0call", Radio: RadioSimpleUSB, Preset: "simplex", AMI: true, AMISecret: "0123456789abcdef"}
}

func TestPlanSetupPrivateRenumber(t *testing.T) {
	w := setupWorld(t)
	p, err := w.PlanSetup(goodSetup())
	if err != nil {
		t.Fatal(err)
	}
	if p.Blocked() {
		t.Fatalf("blocked: %+v", p.Errors)
	}
	if p.Public || !p.Renumber || !p.Restart || p.Node != "1997" {
		t.Fatalf("%+v", p.Plan)
	}
	rpt := p.contents[FileRpt]
	for _, want := range []string{"\n1997 = radio@127.0.0.1/1997,NONE", "[1997](node-main)", "rxchannel = SimpleUSB/1997", "idrecording = |iN0CALL"} {
		if !strings.Contains(rpt, want) {
			t.Errorf("rpt.conf lacks %q", want)
		}
	}
	if strings.Contains(rpt, "\n1999 = radio") || strings.Contains(rpt, "\n[1999](node-main)") {
		t.Error("old number left in rpt.conf")
	}
	if !strings.Contains(p.contents[FileSimple], "\n[1997](node-main)") || strings.Contains(p.contents[FileSimple], "\n[1999](node-main)") {
		t.Error("device stanza not renamed")
	}
	if !strings.Contains(p.contents[FileExt], "NODE=1997") {
		t.Errorf("NODE not renumbered")
	}
	if strings.Contains(p.contents[FileManager], "old-secret") || !strings.Contains(p.contents[FileManager], "0123456789abcdef") {
		t.Error("AMI secret not set")
	}
	// private: registration line untouched (still the sample)
	if _, ok := p.contents[FileHTTPReg]; ok {
		t.Error("a private node must not register")
	}
	// secrets never show in diffs or changes
	for _, f := range p.Files {
		if strings.Contains(f.Diff, "0123456789abcdef") || strings.Contains(f.Diff, "old-secret") {
			t.Errorf("secret in the diff of %s", f.Path)
		}
	}
	for _, c := range p.Changes {
		if strings.Contains(c.To, "0123456789abcdef") || strings.Contains(c.From, "old-secret") {
			t.Errorf("secret in change %+v", c)
		}
	}
	// the planned files form a valid world: the node is found and nothing old remains
	w2 := w.With(p.contents)
	found := false
	for _, n := range w2.LocalNodes() {
		found = found || n == "1997"
		if n == "1999" {
			t.Error("1999 still a node")
		}
	}
	if !found {
		t.Fatal("1997 is not a node")
	}
	if n := node(t, w2, "1997"); n.Radio.Type != RadioSimpleUSB || !n.Radio.DeviceStanza {
		t.Fatalf("radio %+v", n.Radio)
	}
	if len(p.Steps) != 7 {
		t.Fatalf("steps %+v", p.Steps)
	}
}

func TestPlanSetupPublicRegisters(t *testing.T) {
	w := setupWorld(t)
	in := goodSetup()
	in.Node, in.AMI = "2001", false
	p, err := w.PlanSetup(in)
	if err != nil || p.Blocked() {
		t.Fatalf("%v %+v", err, p)
	}
	if !p.Public {
		t.Fatal("not public")
	}
	reg := p.contents[FileHTTPReg]
	if !strings.Contains(reg, "register => 2001:example-pass@register.allstarlink.org") || strings.Contains(reg, "1234:abcdef") {
		t.Fatalf("registration %q", reg)
	}
	if _, ok := p.contents[FileManager]; ok {
		t.Error("manager.conf changed without AMI")
	}
	// the secret is masked in the diff
	for _, f := range p.Files {
		if strings.Contains(f.Diff, "example-pass") {
			t.Errorf("password in diff of %s", f.Path)
		}
	}
	// IAX instead
	in.Registration = &RegChange{Method: RegIAX}
	p, err = w.PlanSetup(in)
	if err != nil || !strings.Contains(p.contents[FileIAX], "register => 2001:example-pass@") {
		t.Fatalf("%v", err)
	}
}

func TestPlanSetupEchoLinkAndHub(t *testing.T) {
	w := setupWorld(t)
	in := goodSetup()
	in.Node, in.Radio, in.Preset = "1998", RadioHub, "hub"
	in.EchoLink = &SetupEchoLink{Call: "n0call-l", Password: "example-pass", Email: "n0call@example.net", Name: "Pat", QTH: "Example City"}
	p, err := w.PlanSetup(in)
	if err != nil || p.Blocked() {
		t.Fatalf("%v %+v", err, p)
	}
	el := p.contents[FileEcho]
	for _, want := range []string{"call = N0CALL-L", "astnode = 1998", "email = n0call@example.net"} {
		if !strings.Contains(el, want) {
			t.Errorf("echolink.conf lacks %q", want)
		}
	}
	if !strings.Contains(p.contents[FileRpt], "rxchannel = Local/pseudo") {
		t.Error("hub rxchannel")
	}
	if !strings.Contains(p.contents[FileModules], "chan_echolink.so") && w.moduleState("chan_echolink.so") == "absent" {
		t.Error("echolink module not ensured")
	}
}

func TestPlanSetupSameNumber(t *testing.T) {
	w := setupWorld(t)
	in := goodSetup()
	in.Node = "1999"
	p, err := w.PlanSetup(in)
	if err != nil || p.Renumber || p.Blocked() {
		t.Fatalf("%v %+v", err, p)
	}
	if !strings.Contains(p.contents[FileRpt], "\n1999 = radio@127.0.0.1/1999,NONE") {
		t.Error("[nodes] touched")
	}
}

func TestPlanSetupRefusals(t *testing.T) {
	w := setupWorld(t)
	cases := map[string]func(*SetupRequest){
		"bad node":         func(r *SetupRequest) { r.Node = "12ab" },
		"zero node":        func(r *SetupRequest) { r.Node = "0" },
		"long node":        func(r *SetupRequest) { r.Node = "12345678" },
		"unknown old":      func(r *SetupRequest) { r.OldNode = "1996" },
		"bad password":     func(r *SetupRequest) { r.Password = "a b" },
		"shell password":   func(r *SetupRequest) { r.Password = "$(id)" },
		"empty password":   func(r *SetupRequest) { r.Password = "" },
		"bad callsign":     func(r *SetupRequest) { r.Callsign = "hello world" },
		"usbradio":         func(r *SetupRequest) { r.Radio = "usbradio" },
		"no preset":        func(r *SetupRequest) { r.Preset = "" },
		"hub preset+usb":   func(r *SetupRequest) { r.Preset = "hub" },
		"private register": func(r *SetupRequest) { r.Registration = &RegChange{Method: RegHTTP} },
		"bad el call": func(r *SetupRequest) {
			r.EchoLink = &SetupEchoLink{Call: "N0CALL", Password: "x", Email: "a@example.net", Name: "n", QTH: "q"}
		},
		"bad el email": func(r *SetupRequest) {
			r.EchoLink = &SetupEchoLink{Call: "N0CALL-L", Password: "x", Email: "nope", Name: "n", QTH: "q"}
		},
		"ami no secret": func(r *SetupRequest) { r.AMISecret = "" },
	}
	for name, mod := range cases {
		in := goodSetup()
		mod(&in)
		_, err := w.PlanSetup(in)
		var ie *InvalidError
		if !errors.As(err, &ie) {
			t.Errorf("%s: want InvalidError, got %v", name, err)
		}
	}
	// the new number may not exist already
	in := goodSetup()
	w2 := w.With(map[string]string{FileRpt: strings.Replace(readCorpus(t, FileRpt), "[1999](node-main)", "[1999](node-main)\n[1997](node-main)\n", 1)})
	if _, err := w2.PlanSetup(in); err == nil {
		t.Error("existing node number must be refused")
	}
	// AMI without an [admin] user is refused, not guessed
	w3 := fixture(t, map[string]string{"manager.conf": "[general]\nenabled=yes\n"})
	var re *RefuseError
	if _, err := w3.PlanSetup(goodSetup()); !errors.As(err, &re) {
		t.Errorf("want RefuseError, got %v", err)
	}
}

func TestGenerateSecret(t *testing.T) {
	a, _ := GenerateSecret()
	b, _ := GenerateSecret()
	if len(a) != 32 || a == b {
		t.Fatalf("%q %q", a, b)
	}
}
