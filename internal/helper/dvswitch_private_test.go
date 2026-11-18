package helper

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/artw1/aslcp/internal/astconf"
)

// svcRunner passes git through and records (and fakes) systemctl.
type svcRunner struct{ restarts int }

func (r *svcRunner) Run(ctx context.Context, argv ...string) (string, int, error) {
	if len(argv) >= 3 && argv[0] == "systemctl" && argv[1] == "restart" && argv[2] == "asterisk" {
		r.restarts++
		return "", 0, nil
	}
	return ExecRunner{}.Run(ctx, argv...)
}

const privRpt = "[nodes]\n1999 = radio@127.0.0.1/1999,NONE\n\n[1999]\nrxchannel = SimpleUSB/1999\nduplex = 2\n"
const privMods = "[modules]\nautoload = no\nnoload  = chan_usrp.so                   ; USRP Channel Module\n"

func privEnv(t *testing.T) (*cfgEnv, *svcRunner, astconf.BridgeResult, string) {
	e := newCfg(t)
	r := &svcRunner{}
	e.o.Run = r
	e.o.DVStateDir = filepath.Join(t.TempDir(), "state")
	br, err := astconf.AddBridgeNode([]byte(privRpt), astconf.BridgeSpec{Node: "1997", RxPort: 34001, TxPort: 32001, Local: "1999"})
	if err != nil {
		t.Fatal(err)
	}
	mods, was, _ := astconf.SetModuleLoad([]byte(privMods), astconf.ModuleUSRP)
	e.put("rpt.conf", string(br.Out), 0o644)
	e.put("modules.conf", string(mods), 0o644)
	_, err = e.o.DVSwitchPrivate(context.Background(), &DVSwitchRequest{Private: &DVPrivate{Action: "set", Record: &DVPrivateRecord{
		Version: 1, Node: "1997", StanzaAdded: br.StanzaAdded, StanzaText: br.StanzaText, BlankAdded: br.BlankAdded,
		NodesLineAdded: br.NodesLineAdded, NodesLine: br.NodesLine, ChanUsrpWas: was}}})
	if err != nil {
		t.Fatal(err)
	}
	return e, r, br, string(mods)
}

func TestDVPrivateRevertMatching(t *testing.T) {
	e, run, _, _ := privEnv(t)
	var out bytes.Buffer
	if err := e.o.RunDVSwitchStep(context.Background(), StepPrivateRev, &out); err != nil {
		t.Fatal(err)
	}
	if got := e.read("rpt.conf"); got != privRpt {
		t.Fatalf("rpt.conf not restored:\n%s", got)
	}
	if got := e.read("modules.conf"); got != privMods {
		t.Fatalf("modules.conf not restored:\n%s", got)
	}
	if run.restarts != 1 {
		t.Fatalf("restarts %d", run.restarts)
	}
	if rec, _ := ReadDVPrivate(e.o.dvPrivatePath()); rec != nil {
		t.Fatal("record kept after a clean revert")
	}
	if strings.Contains(out.String(), "warning") {
		t.Fatal(out.String())
	}
}

func TestDVPrivateRevertChangedByHandIsRefused(t *testing.T) {
	e, run, _, _ := privEnv(t)
	e.put("rpt.conf", strings.Replace(e.read("rpt.conf"), "duplex = 0", "duplex = 0\nhangtime = 5", 1), 0o644)
	var out bytes.Buffer
	if err := e.o.RunDVSwitchStep(context.Background(), StepPrivateRev, &out); err != nil {
		t.Fatal(err)
	}
	rpt := e.read("rpt.conf")
	if !strings.Contains(rpt, "[1997]") || !strings.Contains(rpt, "hangtime = 5") {
		t.Fatal("a stanza changed by hand was removed")
	}
	if strings.Contains(rpt, "1997 = radio@") {
		t.Fatal("the matching [nodes] line should still be removed")
	}
	if !strings.Contains(out.String(), "changed by hand") || !strings.Contains(out.String(), "record is kept") {
		t.Fatalf("no clear report:\n%s", out.String())
	}
	if !strings.Contains(e.read("modules.conf"), "noload  = chan_usrp.so") {
		t.Fatal("chan_usrp not restored")
	}
	if rec, _ := ReadDVPrivate(e.o.dvPrivatePath()); rec == nil {
		t.Fatal("the record must stay so that a later uninstall can retry")
	}
	if run.restarts != 1 {
		t.Fatal("asterisk restarts once, for what was reverted")
	}
}

func TestDVPrivateRevertNothingRecordedOrChanged(t *testing.T) {
	e := newCfg(t)
	r := &svcRunner{}
	e.o.Run = r
	e.o.DVStateDir = filepath.Join(t.TempDir(), "state")
	var out bytes.Buffer
	if err := e.o.RunDVSwitchStep(context.Background(), StepPrivateRev, &out); err != nil || r.restarts != 0 {
		t.Fatalf("%v %d", err, r.restarts)
	}
	if e.read("rpt.conf") != "[general]\nnode=1\n" {
		t.Fatal("touched a config ASLCP did not change")
	}
}

func TestDVPrivateRecordRoundTripAndValidation(t *testing.T) {
	o := &Ops{DVStateDir: filepath.Join(t.TempDir(), "state")}
	ctx := context.Background()
	bad := []DVPrivateRecord{
		{Version: 1, Node: "2000", ChanUsrpWas: "load"},
		{Version: 1, Node: "abc", ChanUsrpWas: "load"},
		{Version: 1, Node: "1997", ChanUsrpWas: "maybe"},
		{Version: 1, Node: "1997", ChanUsrpWas: "load", StanzaAdded: true, StanzaText: "[1996]\n"},
	}
	for _, r := range bad {
		r := r
		if _, err := o.DVSwitchPrivate(ctx, &DVSwitchRequest{Private: &DVPrivate{Action: "set", Record: &r}}); err == nil {
			t.Fatalf("accepted %+v", r)
		}
	}
	rep, err := o.DVSwitchPrivate(ctx, &DVSwitchRequest{Private: &DVPrivate{Action: "set", Record: &DVPrivateRecord{Version: 1, Node: "1997", ChanUsrpWas: "noload"}}})
	if err != nil || rep.Record == nil || rep.Record.At == "" {
		t.Fatalf("%v %+v", err, rep)
	}
	if fi, _ := os.Stat(o.dvPrivatePath()); fi.Mode().Perm() != 0o600 {
		t.Fatal("record must be 0600")
	}
	rep, _ = o.DVSwitchPrivate(ctx, &DVSwitchRequest{Private: &DVPrivate{Action: "clear"}})
	if rep.Record != nil {
		t.Fatal("not cleared")
	}
}

func TestDVUSRPOp(t *testing.T) {
	o, _ := cfgOps(t)
	ctx := context.Background()
	res, err := o.DVSwitchUSRP(ctx, &DVSwitchRequest{USRP: &DVUSRP{Address: "127.0.0.1", RxPort: 34001, TxPort: 32001}})
	if err != nil || len(res.Files) != 1 {
		t.Fatalf("%v %+v", err, res)
	}
	v, _ := o.DVSwitchSetup(ctx)
	if v.USRP.Address != "127.0.0.1" || v.USRP.RxPort != 34001 || v.USRP.TxPort != 32001 {
		t.Fatalf("%+v", v.USRP)
	}
	if v.History0() != 1 {
		t.Fatalf("no history copy: %d", v.History0())
	}
	for _, bad := range []DVUSRP{{Address: "192.0.2.5"}, {RxPort: 80}, {TxPort: 70000}, {RxPort: 40000, TxPort: 40000}} {
		if _, err := o.DVSwitchUSRP(ctx, &DVSwitchRequest{USRP: &bad}); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	if _, err := o.DVSwitchRollback(ctx, &DVSwitchRequest{File: DVFileAnalogBridge}); err != nil {
		t.Fatal(err)
	}
	if v, _ := o.DVSwitchSetup(ctx); v.USRP.RxPort == 34001 {
		t.Fatal("rollback did not restore the ini")
	}
	if len(v2(o).ReservedPorts) < 8 {
		t.Fatal("reserved ports missing")
	}
}

func v2(o *Ops) DVSwitchSetup { v, _ := o.DVSwitchSetup(context.Background()); return v }

// History0 is the number of history copies of Analog_Bridge.ini.
func (v DVSwitchSetup) History0() int {
	for _, f := range v.Files {
		if f.ID == DVFileAnalogBridge {
			return f.History
		}
	}
	return -1
}

// A removal puts the stock ports back, which are equal; only Restore allows that.
func TestDVUSRPOpRestoreAllowsEqualPorts(t *testing.T) {
	o, _ := cfgOps(t)
	ctx := context.Background()
	if _, err := o.DVSwitchUSRP(ctx, &DVSwitchRequest{USRP: &DVUSRP{RxPort: 34001, TxPort: 32001}}); err != nil {
		t.Fatal(err)
	}
	if _, err := o.DVSwitchUSRP(ctx, &DVSwitchRequest{USRP: &DVUSRP{RxPort: 31001, TxPort: 31001}}); err == nil {
		t.Fatal("equal ports accepted without Restore")
	}
	s := DVStockUSRP
	s.Address, s.Restore = "", true
	if _, err := o.DVSwitchUSRP(ctx, &DVSwitchRequest{USRP: &s}); err != nil {
		t.Fatal(err)
	}
	if v := v2(o); v.USRP.RxPort != 31001 || v.USRP.TxPort != 31001 {
		t.Fatalf("%+v", v.USRP)
	}
	// still range-checked
	if _, err := o.DVSwitchUSRP(ctx, &DVSwitchRequest{USRP: &DVUSRP{RxPort: 80, TxPort: 80, Restore: true}}); err == nil {
		t.Fatal("port 80 accepted")
	}
}

func TestDVPrivateRecordUsrpWas(t *testing.T) {
	o := &Ops{DVStateDir: filepath.Join(t.TempDir(), "state")}
	ctx := context.Background()
	set := func(r DVPrivateRecord) error {
		_, err := o.DVSwitchPrivate(ctx, &DVSwitchRequest{Private: &DVPrivate{Action: "set", Record: &r}})
		return err
	}
	if err := set(DVPrivateRecord{Version: 1, Node: "1997", ChanUsrpWas: "load", UsrpWas: &DVUSRP{RxPort: 31001, TxPort: 31001}}); err != nil {
		t.Fatal(err)
	}
	rec, _ := ReadDVPrivate(o.dvPrivatePath())
	if rec.UsrpWas == nil || rec.UsrpWas.RxPort != 31001 {
		t.Fatalf("%+v", rec)
	}
	for _, w := range []*DVUSRP{{RxPort: 80, TxPort: 31001}, {RxPort: 31001, TxPort: 31001, Address: "192.0.2.5"}, {RxPort: 31001, TxPort: 31001, Restore: true}, {}} {
		if err := set(DVPrivateRecord{Version: 1, Node: "1997", ChanUsrpWas: "load", UsrpWas: w}); err == nil {
			t.Fatalf("accepted %+v", w)
		}
	}
}

func TestPlanPrivateRevert(t *testing.T) {
	r, _ := astconf.AddBridgeNode([]byte("[1999]\nrxchannel = SimpleUSB/1999\n"), astconf.BridgeSpec{Node: "1997", RxPort: 34001, TxPort: 32001, Local: "1999"})
	rec := &DVPrivateRecord{Version: 1, Node: "1997", StanzaAdded: true, StanzaText: r.StanzaText, BlankAdded: r.BlankAdded, NodesLineAdded: true, NodesLine: r.NodesLine, ChanUsrpWas: "noload"}
	rpt := string(r.Out)
	mods := "[modules]\nload = chan_usrp.so\n"
	rv := PlanPrivateRevert(rec, &rpt, &mods)
	if !rv.RemovedStanza || !rv.RemovedLine || !rv.RevertedModule || len(rv.Problems) != 0 || len(rv.Gone) != 0 {
		t.Fatalf("%+v", rv)
	}
	if strings.Contains(*rv.Rpt, "1997") || !strings.Contains(*rv.Modules, "noload = chan_usrp.so") {
		t.Fatalf("%q %q", *rv.Rpt, *rv.Modules)
	}
	// hand-edited stanza: a problem; the line goes; a missing module line is "gone"
	edited := strings.Replace(rpt, "duplex = 0", "duplex = 0\nhangtime = 5", 1)
	bare := "[modules]\n"
	rv = PlanPrivateRevert(rec, &edited, &bare)
	if rv.RemovedStanza || !rv.RemovedLine || len(rv.Problems) != 1 || !strings.Contains(rv.Problems[0], "changed by hand") || len(rv.Gone) != 1 || rv.Modules != nil {
		t.Fatalf("%+v", rv)
	}
	// unreadable files are problems
	rv = PlanPrivateRevert(rec, nil, nil)
	if len(rv.Problems) != 2 || rv.Rpt != nil {
		t.Fatalf("%+v", rv)
	}
}
