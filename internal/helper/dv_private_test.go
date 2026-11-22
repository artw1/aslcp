package helper

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/artw1/aslcp/internal/astconf"
)

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
		if _, err := o.DVPrivate(ctx, &DVRequest{Private: &r}); err == nil {
			t.Fatalf("accepted %+v", r)
		}
	}
	if _, err := o.DVPrivate(ctx, &DVRequest{}); err != nil {
		t.Fatalf("get on empty: %v", err)
	}
	rep, err := o.DVPrivate(ctx, &DVRequest{Private: &DVPrivateRecord{Version: 1, Node: "1997", ChanUsrpWas: "noload"}})
	if err != nil || rep.Record == nil || rep.Record.At == "" {
		t.Fatalf("%v %+v", err, rep)
	}
	if fi, _ := os.Stat(o.dvPrivatePath()); fi.Mode().Perm() != 0o600 {
		t.Fatal("record must be 0600")
	}
	rep, err = o.DVPrivate(ctx, &DVRequest{})
	if err != nil || rep.Record == nil || rep.Record.Node != "1997" {
		t.Fatalf("get: %v %+v", err, rep)
	}
	if _, err := o.DVPrivate(ctx, &DVRequest{Clear: true, Private: &DVPrivateRecord{Version: 1, Node: "1997", ChanUsrpWas: "load"}}); err == nil {
		t.Fatal("set and clear together accepted")
	}
	rep, _ = o.DVPrivate(ctx, &DVRequest{Clear: true})
	if rep.Record != nil {
		t.Fatal("not cleared")
	}
}

func TestPlanPrivateRemove(t *testing.T) {
	r, _ := astconf.AddBridgeNode([]byte("[1999]\nrxchannel = SimpleUSB/1999\n"), astconf.BridgeSpec{Node: "1997", RxPort: 34001, TxPort: 32001, Local: "1999"})
	rec := &DVPrivateRecord{Version: 1, Node: "1997", StanzaAdded: true, StanzaText: r.StanzaText, BlankAdded: r.BlankAdded, NodesLineAdded: true, NodesLine: r.NodesLine, ChanUsrpWas: "noload"}
	rpt := string(r.Out)
	mods := "[modules]\nload = chan_usrp.so\n"
	rv := PlanPrivateRemove(rec, &rpt, &mods)
	if !rv.RemovedStanza || !rv.RemovedLine || !rv.RevertedModule || len(rv.Problems) != 0 || len(rv.Gone) != 0 {
		t.Fatalf("%+v", rv)
	}
	if strings.Contains(*rv.Rpt, "1997") || !strings.Contains(*rv.Modules, "noload = chan_usrp.so") {
		t.Fatalf("%q %q", *rv.Rpt, *rv.Modules)
	}
	edited := strings.Replace(rpt, "duplex = 0", "duplex = 0\nhangtime = 5", 1)
	bare := "[modules]\n"
	rv = PlanPrivateRemove(rec, &edited, &bare)
	if rv.RemovedStanza || !rv.RemovedLine || len(rv.Problems) != 1 || !strings.Contains(rv.Problems[0], "changed by hand") || len(rv.Gone) != 1 || rv.Modules != nil {
		t.Fatalf("%+v", rv)
	}
	rv = PlanPrivateRemove(rec, nil, nil)
	if len(rv.Problems) != 2 || rv.Rpt != nil {
		t.Fatalf("%+v", rv)
	}
}
