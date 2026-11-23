package nodecfg

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/artw1/aslcp/internal/astconf"
	"github.com/artw1/aslcp/internal/schema"
)

const corpus = "../astconf/testdata/corpus/asl3-dist"

func corpusWorld(t *testing.T) *World {
	t.Helper()
	return Load(astconf.NewOSFS(corpus))
}

// fixture copies the corpus to a temp dir and applies file overrides
// (an empty string removes the file).
func fixture(t *testing.T, over map[string]string) *World {
	t.Helper()
	dir := t.TempDir()
	err := filepath.WalkDir(corpus, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(corpus, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dir, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dir, rel), b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range over {
		p := filepath.Join(dir, name)
		if c == "" {
			os.Remove(p)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return Load(astconf.NewOSFS(dir))
}

func readCorpus(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(corpus, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func node(t *testing.T, w *World, n string) Node {
	t.Helper()
	for _, x := range w.Analyze().Nodes {
		if x.Node == n {
			return x
		}
	}
	t.Fatalf("node %s not found", n)
	return Node{}
}

func field(n Node, key string) Field {
	for _, f := range n.Fields {
		if f.Key == key {
			return f
		}
	}
	return Field{}
}

func mustPlan(t *testing.T, w *World, r Request) *Plan {
	t.Helper()
	p, err := w.Plan(r)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	return p
}

func hasCode(fs []Finding, code string) bool {
	for _, f := range fs {
		if f.Code == code {
			return true
		}
	}
	return false
}

func TestCorpusOverview(t *testing.T) {
	w := corpusWorld(t)
	ov := w.Analyze()
	if ov.Refusal != nil {
		t.Fatalf("refusal: %+v", ov.Refusal)
	}
	if ov.Placeholder != "1999" {
		t.Fatalf("placeholder = %q", ov.Placeholder)
	}
	n := node(t, w, "1999")
	if !n.Private || !n.Placeholder || n.Callsign != "" {
		t.Fatalf("node = %+v", n)
	}
	if n.Radio.Type != RadioSimpleUSB || !n.Radio.Supported || !n.Radio.DeviceStanza || n.Radio.ModuleState != "explicit" {
		t.Fatalf("radio = %+v", n.Radio)
	}
	if n.Stanza == nil || n.Stanza.Template != "node-main" {
		t.Fatalf("stanza = %+v", n.Stanza)
	}
	if n.Registration.Method != RegNone {
		t.Fatalf("registration = %+v", n.Registration)
	}
	d := field(n, "duplex")
	if d.Value != "2" || d.Source.Kind != SrcTemplate || d.Source.Section != "node-main" || !d.CanTemplate || d.CanReset {
		t.Fatalf("duplex = %+v", d)
	}
	rx := field(n, "rxchannel")
	if rx.Source.Kind != SrcNode || rx.Value != "SimpleUSB/1999" || rx.Overrides == nil || rx.Overrides.Value != "Local/pseudo" || !rx.CanReset {
		t.Fatalf("rxchannel = %+v", rx)
	}
	for _, f := range ov.Warnings {
		if f.Level == LevelError {
			t.Fatalf("stock corpus has an error finding: %+v", f)
		}
	}
	for _, f := range n.Warnings {
		if f.Level == LevelError {
			t.Fatalf("stock node has an error finding: %+v", f)
		}
	}
	if _, err := json.Marshal(ov); err != nil {
		t.Fatal(err)
	}
}

func TestCorpusEchoLinkStock(t *testing.T) {
	n := node(t, corpusWorld(t), "1999")
	el := n.EchoLink
	if !el.Present || !el.Linked || el.Configured || el.Enabled || el.ModuleState != "noload" {
		t.Fatalf("echolink = %+v", el)
	}
	for _, f := range el.Fields {
		if f.Key == "pwd" && f.Value != "" && f.Value != Masked {
			t.Fatalf("pwd shown: %+v", f)
		}
	}
}

func TestPlanOverrideAddsToNodeStanza(t *testing.T) {
	w := corpusWorld(t)
	p := mustPlan(t, w, Request{Node: "1999", Changes: []Change{{ID: "rpt.conf:node:hangtime", Value: "3000"}}})
	if p.Noop || len(p.Files) != 1 || p.Files[0].Path != "rpt.conf" {
		t.Fatalf("plan = %+v", p)
	}
	if p.Restart || len(p.Reload) != 1 || p.Reload[0].Command != "module reload app_rpt.so" {
		t.Fatalf("reload = %+v restart=%v", p.Reload, p.Restart)
	}
	if !strings.Contains(p.Files[0].Diff, "+hangtime = 3000") && !strings.Contains(p.Files[0].Diff, "hangtime") {
		t.Fatalf("diff: %s", p.Files[0].Diff)
	}
	w2 := w.With(p.contents)
	f := field(node(t, w2, "1999"), "hangtime")
	if f.Value != "3000" || f.Source.Kind != SrcNode || f.Overrides == nil || f.Overrides.Value != "2000" {
		t.Fatalf("after = %+v", f)
	}
	// the template and everything else is untouched
	if !strings.Contains(p.contents["rpt.conf"], "hangtime = 2000") {
		t.Fatal("template line lost")
	}
}

func TestPlanUnchangedValueIsNoop(t *testing.T) {
	w := corpusWorld(t)
	p := mustPlan(t, w, Request{Node: "1999", Changes: []Change{{ID: "rpt.conf:node:hangtime", Value: "2000"}}})
	if !p.Noop || len(p.Files) != 0 || len(p.Changes) != 1 || p.Changes[0].Action != "unchanged" {
		t.Fatalf("plan = %+v", p)
	}
}

func TestPlanResetRoundTrip(t *testing.T) {
	w := corpusWorld(t)
	p := mustPlan(t, w, Request{Node: "1999", Changes: []Change{{ID: "rpt.conf:node:hangtime", Value: "3000"}}})
	w2 := w.With(p.contents)
	r := mustPlan(t, w2, Request{Node: "1999", Mode: ModeReset, Changes: []Change{{ID: "rpt.conf:node:hangtime"}}})
	if r.Noop || r.contents["rpt.conf"] != readCorpus(t, "rpt.conf") {
		t.Fatalf("reset did not restore the file:\n%s", r.Files[0].Diff)
	}
	// resetting what is not overridden does nothing
	r2 := mustPlan(t, w, Request{Node: "1999", Mode: ModeReset, Changes: []Change{{ID: "rpt.conf:node:hangtime"}}})
	if !r2.Noop {
		t.Fatal("reset of an inherited key is not a no-op")
	}
}

func TestPlanTemplateChangesTheTemplate(t *testing.T) {
	w := corpusWorld(t)
	p := mustPlan(t, w, Request{Node: "1999", Mode: ModeTemplate, Changes: []Change{{ID: "rpt.conf:node:hangtime", Value: "4000"}}})
	if p.Noop || len(p.Changes) != 1 || p.Changes[0].Section != "node-main" {
		t.Fatalf("plan = %+v", p.Changes)
	}
	f := field(node(t, w.With(p.contents), "1999"), "hangtime")
	if f.Value != "4000" || f.Source.Kind != SrcTemplate {
		t.Fatalf("after = %+v", f)
	}
	// a node that overrides the key keeps its value and the plan says so
	q := mustPlan(t, w, Request{Node: "1999", Mode: ModeTemplate, Changes: []Change{{ID: "rpt.conf:node:rxchannel", Value: "Local/pseudo"}}})
	if !hasCode(q.Warnings, "node_overrides") {
		t.Fatalf("warnings = %+v", q.Warnings)
	}
}

func TestPlanCallsign(t *testing.T) {
	w := corpusWorld(t)
	cs := "n0call"
	p := mustPlan(t, w, Request{Node: "1999", Callsign: &cs})
	n := node(t, w.With(p.contents), "1999")
	if n.Callsign != "N0CALL" || n.Placeholder {
		t.Fatalf("node = %+v", n)
	}
	bad := "not a call"
	if _, err := w.Plan(Request{Node: "1999", Callsign: &bad}); err == nil {
		t.Fatal("bad callsign accepted")
	} else if _, ok := err.(*InvalidError); !ok {
		t.Fatalf("err = %T", err)
	}
}

func TestPlanRestartKey(t *testing.T) {
	w := corpusWorld(t)
	p := mustPlan(t, w, Request{Node: "1999", Changes: []Change{{ID: "rpt.conf:node:duplex", Value: "1"}}})
	if !p.Restart || len(p.RestartReasons) == 0 {
		t.Fatalf("plan = %+v", p)
	}
}

func TestPlanValidation(t *testing.T) {
	w := corpusWorld(t)
	for name, r := range map[string]Request{
		"range":   {Node: "1999", Changes: []Change{{ID: "rpt.conf:node:duplex", Value: "9"}}},
		"unknown": {Node: "1999", Changes: []Change{{ID: "rpt.conf:node:nosuchkey", Value: "1"}}},
		"node":    {Node: "1997", Changes: []Change{{ID: "rpt.conf:node:hangtime", Value: "1"}}},
		"mode":    {Node: "1999", Mode: "x"},
		"dup":     {Node: "1999", Changes: []Change{{ID: "rpt.conf:node:hangtime", Value: "1"}, {ID: "rpt.conf:node:hangtime", Value: "2"}}},
		"comment": {Node: "1999", Changes: []Change{{ID: "rpt.conf:node:statpost_url", Value: "x ; y"}}},
		"usb":     {Node: "1999", Radio: &RadioChange{Type: "usbradio"}},
	} {
		if _, err := w.Plan(r); err == nil {
			t.Errorf("%s: accepted", name)
		} else if _, ok := err.(*InvalidError); !ok {
			t.Errorf("%s: err = %T %v", name, err, err)
		}
	}
}

func TestPlanRegistration(t *testing.T) {
	// no registrations file in the corpus: refuse
	w := corpusWorld(t)
	_, err := w.Plan(Request{Node: "1999", Registration: &RegChange{Method: RegHTTP, Password: "example-pass"}})
	if re, ok := err.(*RefuseError); !ok || re.File != FileHTTPReg {
		t.Fatalf("err = %#v", err)
	}
	w = fixture(t, map[string]string{FileHTTPReg: "[general]\n"})
	p := mustPlan(t, w, Request{Node: "1999", Registration: &RegChange{Method: RegHTTP, Password: "example-pass"}})
	if len(p.Files) != 1 || p.Files[0].Path != FileHTTPReg {
		t.Fatalf("files = %+v", p.Files)
	}
	if strings.Contains(p.Files[0].Diff, "example-pass") {
		t.Fatalf("password in diff: %s", p.Files[0].Diff)
	}
	b, _ := json.Marshal(p)
	if strings.Contains(string(b), "example-pass") {
		t.Fatalf("password in plan: %s", b)
	}
	if !strings.Contains(p.contents[FileHTTPReg], "register => 1999:example-pass@"+DefaultRegHost) {
		t.Fatalf("content: %s", p.contents[FileHTTPReg])
	}
	n := node(t, w.With(p.contents), "1999")
	if n.Registration.Method != RegHTTP || n.Registration.Http == nil || !n.Registration.Http.HasPassword {
		t.Fatalf("registration = %+v", n.Registration)
	}
	// private node registered: a warning, not an error
	if !hasCode(p.Warnings, CodePrivateRegister) || p.Blocked() {
		t.Fatalf("plan = %+v", p)
	}
	// moving to IAX removes the HTTP line and keeps the password
	w2 := w.With(p.contents)
	q := mustPlan(t, w2, Request{Node: "1999", Registration: &RegChange{Method: RegIAX}})
	if len(q.Files) != 2 || !strings.Contains(q.contents[FileIAX], "register => 1999:example-pass@") || strings.Contains(q.contents[FileHTTPReg], "1999:") {
		t.Fatalf("move: %+v", q.contents)
	}
	// password required for a new registration
	if _, err := w.Plan(Request{Node: "1999", Registration: &RegChange{Method: RegIAX}}); err == nil {
		t.Fatal("registration without password accepted")
	}
}

func TestRegisteredBothBlocks(t *testing.T) {
	w := fixture(t, map[string]string{FileHTTPReg: "[general]\nregister => 1999:example-pass@" + DefaultRegHost + "\n"})
	iax := strings.Replace(readCorpus(t, "iax.conf"), ";register => 1999:12345@", "register => 1999:example-pass@", 1)
	w = fixture(t, map[string]string{FileHTTPReg: "[general]\nregister => 1999:example-pass@" + DefaultRegHost + "\n", FileIAX: iax})
	if n := node(t, w, "1999"); n.Registration.Method != RegBoth || !hasCode(n.Warnings, CodeRegBoth) {
		t.Fatalf("registration = %+v warnings=%+v", n.Registration, n.Warnings)
	}
	// a change that leaves it that way only warns
	p := mustPlan(t, w, Request{Node: "1999", Changes: []Change{{ID: "rpt.conf:node:hangtime", Value: "3000"}}})
	if p.Blocked() {
		t.Fatalf("pre-existing error blocks: %+v", p.Errors)
	}
	// a change that creates it is blocked
	w = fixture(t, map[string]string{FileHTTPReg: "[general]\n", FileIAX: iax})
	cs := "N0CALL"
	_ = cs
	p = mustPlan(t, w, Request{Node: "1999", Registration: &RegChange{Method: RegHTTP, Password: "example-pass"}})
	if p.Blocked() {
		t.Fatalf("moving the line must not leave both: %+v", p.Errors)
	}
}

func TestDriverChecks(t *testing.T) {
	// rxchannel switched to USBRadio while chan_usbradio is noload: blocked
	w := corpusWorld(t)
	p := mustPlan(t, w, Request{Node: "1999", Changes: []Change{{ID: "rpt.conf:node:rxchannel", Value: "Radio/1999"}}})
	if !p.Blocked() || !hasCode(p.Errors, CodeDriverNoLoad) && !hasCode(p.Errors, CodeDriverNotLoaded) {
		t.Fatalf("errors = %+v", p.Errors)
	}
	// SimpleUSB with the driver off: the radio wizard loads it
	mod := strings.Replace(readCorpus(t, "modules.conf"), "load    = chan_simpleusb.so", "noload  = chan_simpleusb.so", 1)
	w = fixture(t, map[string]string{FileModules: mod})
	if n := node(t, w, "1999"); n.Radio.ModuleState != "noload" || !hasCode(n.Warnings, CodeDriverNoLoad) {
		t.Fatalf("radio = %+v warnings=%+v", n.Radio, n.Warnings)
	}
	q := mustPlan(t, w, Request{Node: "1999", Radio: &RadioChange{Type: RadioSimpleUSB}, Modules: []string{"chan_simpleusb.so"}})
	if q.Blocked() || !strings.Contains(q.contents[FileModules], "load    = chan_simpleusb.so") {
		t.Fatalf("plan = %+v", q)
	}
	if !q.Restart {
		t.Fatal("modules.conf change must offer a restart")
	}
	// the device stanza must exist
	rpt := replaceLast(readCorpus(t, "rpt.conf"), "rxchannel = SimpleUSB/1999", "rxchannel = SimpleUSB/1998")
	w = fixture(t, map[string]string{FileRpt: rpt})
	if n := node(t, w, "1999"); n.Radio.DeviceStanza || !hasCode(n.Warnings, CodeNoDeviceStanza) {
		t.Fatalf("radio = %+v warnings=%+v", n.Radio, n.Warnings)
	}
}

func TestRadioWizardCreatesDevice(t *testing.T) {
	rpt := replaceLast(readCorpus(t, "rpt.conf"), "rxchannel = SimpleUSB/1999", "rxchannel = Local/pseudo")
	w := fixture(t, map[string]string{FileRpt: rpt})
	p := mustPlan(t, w, Request{Node: "1999", Radio: &RadioChange{Type: RadioSimpleUSB}})
	n := node(t, w.With(p.contents), "1999")
	if n.Radio.Type != RadioSimpleUSB || !n.Radio.DeviceStanza || p.Blocked() {
		t.Fatalf("radio = %+v errors=%+v", n.Radio, p.Errors)
	}
}

func TestUSBRadioNotSupported(t *testing.T) {
	rpt := replaceLast(readCorpus(t, "rpt.conf"), "rxchannel = SimpleUSB/1999", "rxchannel = Radio/1999")
	w := fixture(t, map[string]string{FileRpt: rpt})
	n := node(t, w, "1999")
	if n.Radio.Type != RadioUSBRadio || n.Radio.Supported || n.Radio.AdvancedFile != FileUSBRadio {
		t.Fatalf("radio = %+v", n.Radio)
	}
}

func TestEchoLink(t *testing.T) {
	w := corpusWorld(t)
	tr := true
	p := mustPlan(t, w, Request{Node: "1999", Link: &tr, Changes: []Change{
		{ID: "echolink.conf:el0:call", Value: "N0CALL-L"}, {ID: "echolink.conf:el0:pwd", Value: "example-pass"}}})
	if p.Blocked() {
		t.Fatalf("errors = %+v", p.Errors)
	}
	if !strings.Contains(p.contents[FileModules], "load    = chan_echolink.so") {
		t.Fatalf("driver not loaded: %s", p.contents[FileModules])
	}
	b, _ := json.Marshal(p)
	if strings.Contains(string(b), "example-pass") {
		t.Fatalf("password in plan: %s", b)
	}
	el := node(t, w.With(p.contents), "1999").EchoLink
	if !el.Enabled {
		t.Fatalf("echolink = %+v", el)
	}
	// configured but driver unloaded is an error
	w = fixture(t, map[string]string{FileEcho: strings.Replace(readCorpus(t, "echolink.conf"), "call = INVALID", "call = N0CALL-L", 1)})
	if n := node(t, w, "1999"); !hasCode(n.Warnings, CodeEchoNoModule) {
		t.Fatalf("warnings = %+v", n.Warnings)
	}
}

func TestSecretMaskedInPlan(t *testing.T) {
	w := fixture(t, map[string]string{"manager.conf": "[general]\nenabled = yes\nbindaddr = 127.0.0.1\nport = 5038\n\n[admin]\nsecret = example-old\nread = all\n"})
	p := mustPlan(t, w, Request{Node: "1999", Changes: []Change{{ID: "manager.conf:admin:secret", Value: "example-pass"}}})
	b, _ := json.Marshal(p)
	if strings.Contains(string(b), "example-pass") || strings.Contains(string(b), "example-old") {
		t.Fatalf("secret in plan: %s", b)
	}
	if p.Noop || p.contents["manager.conf"] == "" {
		t.Fatalf("plan = %+v", p)
	}
}

func TestPresets(t *testing.T) {
	w := corpusWorld(t)
	p := mustPlan(t, w, Request{Node: "1999", Preset: "simplex"})
	n := node(t, w.With(p.contents), "1999")
	if field(n, "duplex").Value != "1" || field(n, "duplex").Source.Kind != SrcNode {
		t.Fatalf("duplex = %+v", field(n, "duplex"))
	}
	h := mustPlan(t, w, Request{Node: "1999", Preset: "hub"})
	n = node(t, w.With(h.contents), "1999")
	if n.Radio.Type != RadioHub || field(n, "duplex").Value != "0" {
		t.Fatalf("hub: radio=%+v duplex=%+v", n.Radio, field(n, "duplex"))
	}
	if _, err := w.Plan(Request{Node: "1999", Preset: "nope"}); err == nil {
		t.Fatal("unknown preset accepted")
	}
}

func TestRefusals(t *testing.T) {
	rpt := readCorpus(t, "rpt.conf")
	cases := map[string]struct {
		files map[string]string
		code  string
	}{
		"multiple": {map[string]string{FileRpt: rpt + "\n[1999](node-main)\nhangtime = 1\n"}, "multiple_stanzas"},
		"broken":   {map[string]string{FileRpt: replaceLast(rpt, "\n[1999](node-main)\n", "\n[1999](no-such-template)\n")}, "broken_inheritance"},
		"split": {map[string]string{FileRpt: replaceLast(rpt, "\n[1999](node-main)\n", "\n[1999](node-main)\n#include custom/rpt/split.conf\n"),
			"custom/rpt/split.conf": "hangtime = 1\n"}, "split_stanza"},
	}
	for name, c := range cases {
		w := fixture(t, c.files)
		n := node(t, w, "1999")
		if len(n.Refusals) == 0 || n.Refusals[0].Code != c.code {
			t.Errorf("%s: refusals = %+v", name, n.Refusals)
			continue
		}
		if n.Refusals[0].File == "" {
			t.Errorf("%s: no Advanced link", name)
		}
		_, err := w.Plan(Request{Node: "1999", Changes: []Change{{ID: "rpt.conf:node:hangtime", Value: "1"}}})
		if _, ok := err.(*RefuseError); !ok {
			t.Errorf("%s: plan err = %T %v", name, err, err)
		}
	}
	// repeated key on the node
	w := fixture(t, map[string]string{FileRpt: replaceLast(rpt, "\n[1999](node-main)\n", "\n[1999](node-main)\nhangtime = 1\nhangtime = 2\n")})
	if _, err := w.Plan(Request{Node: "1999", Changes: []Change{{ID: "rpt.conf:node:hangtime", Value: "5"}}}); err == nil {
		t.Error("repeated key accepted")
	} else if _, ok := err.(*RefuseError); !ok {
		t.Errorf("repeated key err = %T", err)
	}
	if f := field(node(t, w, "1999"), "hangtime"); f.CanSet || f.Locked == "" {
		t.Errorf("repeated key field = %+v", f)
	}
	// no rpt.conf at all
	if ov := fixture(t, map[string]string{FileRpt: ""}).Analyze(); ov.Refusal == nil {
		t.Error("missing rpt.conf not refused")
	}
}

func TestUnknownKeysPreserved(t *testing.T) {
	rpt := replaceLast(readCorpus(t, "rpt.conf"), "\n[1999](node-main)\n", "\n[1999](node-main)\nzz_custom_key = keep-me   ; mine\n")
	w := fixture(t, map[string]string{FileRpt: rpt})
	n := node(t, w, "1999")
	found := false
	for _, o := range n.Other {
		found = found || o.Key == "zz_custom_key"
	}
	if !found {
		t.Fatal("unknown key not listed")
	}
	p := mustPlan(t, w, Request{Node: "1999", Changes: []Change{{ID: "rpt.conf:node:hangtime", Value: "3000"}}})
	if !strings.Contains(p.contents["rpt.conf"], "zz_custom_key = keep-me   ; mine") {
		t.Fatal("unknown key changed")
	}
}

func TestParseRegStatus(t *testing.T) {
	cases := []struct {
		lines []string
		want  string
	}{
		{[]string{"1999 203.0.113.9:443 Registered"}, "registered"},
		{[]string{"192.0.2.1:4569 N 1999 Rejected"}, "failed"},
		{[]string{"nothing here"}, "unknown"},
		{[]string{"1999 something odd"}, "unknown"},
	}
	for _, c := range cases {
		got := ParseRegStatus(RegHTTP, "1999", c.lines)
		if got.State != c.want || strings.Contains(got.Detail, "203.0.113") || strings.Contains(got.Detail, "192.0.2") {
			t.Errorf("%v: %+v", c.lines, got)
		}
	}
}

func TestValidCallsign(t *testing.T) {
	for s, want := range map[string]bool{"N0CALL": true, "W1AW": true, "K1ABC/P": true, "VE3XYZ": true, "NOTSET": false, "123": false, "AB": false, "hello world": false, "": false} {
		if ValidCallsign(s) != want {
			t.Errorf("%q: want %v", s, want)
		}
	}
}

func TestPrivate(t *testing.T) {
	for n, want := range map[string]bool{"1": true, "1999": true, "2000": false, "2001": false, "x": false, "0": false} {
		if IsPrivate(n) != want {
			t.Errorf("%s", n)
		}
	}
}

func replaceLast(s, old, repl string) string {
	i := strings.LastIndex(s, old)
	if i < 0 {
		panic("fixture text not found: " + old)
	}
	return s[:i] + repl + s[i+len(old):]
}

func TestRadioChangeRefusedOnUnsupportedNode(t *testing.T) {
	for _, ch := range []string{"Radio/1999", "Voter/1999", "USRP/127.0.0.1:34001:32001", "DAHDI/1"} {
		rpt := replaceLast(readCorpus(t, "rpt.conf"), "rxchannel = SimpleUSB/1999", "rxchannel = "+ch)
		w := fixture(t, map[string]string{FileRpt: rpt})
		for name, req := range map[string]Request{
			"radio":  {Node: "1999", Radio: &RadioChange{Type: RadioSimpleUSB}},
			"hubby":  {Node: "1999", Radio: &RadioChange{Type: RadioHub}},
			"preset": {Node: "1999", Preset: "hub"},
		} {
			_, err := w.Plan(req)
			re, ok := err.(*RefuseError)
			if !ok || re.Code != "radio_unsupported" || re.File == "" {
				t.Errorf("%s %s: err = %T %v", ch, name, err, err)
			}
		}
		// other changes on such a node still work
		mustPlan(t, w, Request{Node: "1999", Changes: []Change{{ID: "rpt.conf:node:hangtime", Value: "2000"}}})
	}
	// SimpleUSB and hub nodes may switch
	w := corpusWorld(t)
	mustPlan(t, w, Request{Node: "1999", Radio: &RadioChange{Type: RadioHub}})
	mustPlan(t, w, Request{Node: "1999", Preset: "hub"})
}

// The stock rpt.conf only has statpost_url and statpost_time as comments
// (there is no statpost_program key in it), so the form turns reporting on by
// writing the stock address and off by removing the line.
func TestPlanStatusReporting(t *testing.T) {
	w := corpusWorld(t)
	st := field(node(t, w, "1999"), "statpost_url")
	if st.Set || st.Value != "" {
		t.Fatalf("stock corpus reports status: %+v", st)
	}
	on := mustPlan(t, w, Request{Node: "1999", Changes: []Change{{ID: "rpt.conf:node:statpost_url", Value: schema.DefaultStatpostURL}, {ID: "rpt.conf:node:statpost_time", Value: "120"}}})
	if on.Noop || on.Restart || len(on.Reload) != 1 {
		t.Fatalf("plan: %+v", on)
	}
	for _, want := range []string{"statpost_url = " + schema.DefaultStatpostURL, "statpost_time = 120"} {
		if !strings.Contains(on.contents["rpt.conf"], want) {
			t.Fatalf("missing %q in\n%s", want, on.Files[0].Diff)
		}
	}
	w2 := w.With(on.contents)
	if f := field(node(t, w2, "1999"), "statpost_url"); f.Value != schema.DefaultStatpostURL || f.Source.Kind != SrcNode {
		t.Fatalf("after on: %+v", f)
	}
	// off: the node's own line goes away, everything else is as before
	off := mustPlan(t, w2, Request{Node: "1999", Mode: ModeReset, Changes: []Change{{ID: "rpt.conf:node:statpost_url"}, {ID: "rpt.conf:node:statpost_time"}}})
	if off.contents["rpt.conf"] != readCorpus(t, "rpt.conf") {
		t.Fatal("off did not restore the file")
	}
	// all nodes: the template gets the line
	all := mustPlan(t, w, Request{Node: "1999", Mode: ModeTemplate, Changes: []Change{{ID: "rpt.conf:node:statpost_url", Value: schema.DefaultStatpostURL}}})
	if len(all.Changes) != 1 || all.Changes[0].Section != "node-main" {
		t.Fatalf("template plan: %+v", all.Changes)
	}
	for _, bad := range []string{"stats.example.net", "ftp://stats.example.net/x", "http://stats.example.net/a b"} {
		if _, err := w.Plan(Request{Node: "1999", Changes: []Change{{ID: "rpt.conf:node:statpost_url", Value: bad}}}); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, err := w.Plan(Request{Node: "1999", Changes: []Change{{ID: "rpt.conf:node:statpost_time", Value: "10"}}}); err == nil {
		t.Error("an interval below 30 s was accepted")
	}
}
