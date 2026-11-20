package api_test

import (
	"strings"
	"testing"

	"github.com/artw1/aslcp/internal/helper"
)

// Taking over a bridge node that has no record (docs/DVSWITCH.md).

const handBridge = "\n[1998]\nrxchannel = USRP/127.0.0.1:34001:32001\nduplex = 0\n"

func adoptEnv(t *testing.T, rptConf, modules string) *privEnv {
	t.Helper()
	pe := newPrivEnvNode(t, rptConf, false, node1998)
	pe.dh.SetFile("modules.conf", modules)
	pe.dv.usrp = helper.DVUSRPView{Address: "127.0.0.1", RxPort: 34001, TxPort: 32001}
	return pe
}

func (pe *privEnv) adoptable() bool {
	pe.t.Helper()
	var v struct {
		BridgeNode struct {
			Node                      string
			Owned, Present, Adoptable bool
		}
	}
	if c := pe.do("admin", "GET", "/api/dvswitch/setup", nil, &v); c != 200 {
		pe.t.Fatal(c)
	}
	if v.BridgeNode.Owned {
		pe.t.Fatal("owned before adopt")
	}
	return v.BridgeNode.Adoptable
}

const modLoaded = "[modules]\nautoload = yes\nload = chan_usrp.so\n"

func TestDVAdoptSuccessThenRemoveRevertsToStock(t *testing.T) {
	rptConf := nodesRpt + handBridge
	rptConf = strings.Replace(rptConf, "[nodes]\n", "[nodes]\n1998 = radio@127.0.0.1/1998,NONE\n", 1)
	pe := adoptEnv(t, rptConf, modLoaded)
	if !pe.adoptable() {
		t.Fatal("not adoptable")
	}
	var out struct {
		Adopted             bool
		Node, ChanUsrpWas   string
		UsrpRestoredToStock bool
	}
	if c := pe.do("admin", "POST", "/api/dvswitch/private-node/adopt", map[string]any{}, &out); c != 200 || !out.Adopted || out.Node != "1998" {
		t.Fatalf("%d %+v", c, out)
	}
	r := pe.dv.priv
	if r == nil || !r.StanzaAdded || !r.NodesLineAdded || r.ChanUsrpWas != "noload" || r.UsrpWas == nil || r.UsrpWas.RxPort != 31001 || r.UsrpUntouched {
		t.Fatalf("record %+v", r)
	}
	if !strings.Contains(r.StanzaText, "[1998]") || !strings.Contains(r.NodesLine, "1998") {
		t.Fatalf("texts %+v", r)
	}
	if !auditHas(pe.env, "dvswitch.private.adopt") {
		t.Error("no audit entry")
	}
	// nothing was written to the config
	if pe.file("rpt.conf") != rptConf || pe.file("modules.conf") != modLoaded {
		t.Fatal("adopt changed the files")
	}
	// a second adopt is refused
	expectErr(t, pe.env, "admin", "POST", "/api/dvswitch/private-node/adopt", map[string]any{}, 409, "already_managed")

	// removal now reverts to the ASL3 stock state
	c, rm := pe.remove(map[string]any{"restart": true})
	if c != 200 || !rm.Applied || rm.KeepRecord {
		t.Fatalf("%d %+v", c, rm)
	}
	if strings.Contains(pe.file("rpt.conf"), "1998") {
		t.Fatalf("not removed:\n%s", pe.file("rpt.conf"))
	}
	if !strings.Contains(pe.file("modules.conf"), "noload = chan_usrp.so") || strings.Contains(pe.file("modules.conf"), "load = chan_usrp.so\n[") {
		t.Fatalf("modules.conf:\n%s", pe.file("modules.conf"))
	}
	if len(pe.dv.usrpCalls) != 1 || pe.dv.usrpCalls[0].RxPort != 31001 || pe.dv.usrpCalls[0].TxPort != 31001 {
		t.Fatalf("ports %+v", pe.dv.usrpCalls)
	}
}

func TestDVAdoptIniNotPointingHereIsUntouched(t *testing.T) {
	pe := adoptEnv(t, strings.Replace(nodesRpt, "[nodes]\n", "[nodes]\n1998 = radio@127.0.0.1/1998,NONE\n", 1)+handBridge, modLoaded)
	pe.dv.usrp = helper.DVUSRPView{Address: "127.0.0.1", RxPort: 40000, TxPort: 40001}
	if c := pe.do("admin", "POST", "/api/dvswitch/private-node/adopt", map[string]any{}, nil); c != 200 {
		t.Fatal(c)
	}
	if r := pe.dv.priv; r == nil || !r.UsrpUntouched || r.UsrpWas != nil {
		t.Fatalf("%+v", r)
	}
}

func TestDVAdoptRefusals(t *testing.T) {
	line := "[nodes]\n1998 = radio@127.0.0.1/1998,NONE\n"
	good := strings.Replace(nodesRpt, "[nodes]\n", line, 1) + handBridge
	cases := []struct {
		name, rpt, mod, code string
		status               int
	}{
		{"no bridge node", nodesRpt, modLoaded, "no_bridge_node", 409},
		{"no nodes line", nodesRpt + handBridge, modLoaded, "not_adoptable", 409},
		{"other host", strings.Replace(good, "127.0.0.1:34001", "192.0.2.5:34001", 1), modLoaded, "not_adoptable", 409},
		{"one port", strings.Replace(good, ":34001:32001", ":34001", 1), modLoaded, "not_adoptable", 409},
		{"not loaded", good, modConf, "not_adoptable", 409},
		{"public node", strings.ReplaceAll(good, "1998", "2001"), modLoaded, "not_adoptable", 409},
		{"two usrp nodes", good + "\n[1999]\nrxchannel = USRP/127.0.0.1:35001:36001\n", modLoaded, "not_adoptable", 409},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pe := adoptEnv(t, tc.rpt, tc.mod)
			if pe.adoptable() {
				t.Error("reported adoptable")
			}
			expectErr(t, pe.env, "admin", "POST", "/api/dvswitch/private-node/adopt", map[string]any{}, tc.status, tc.code)
			if pe.dv.priv != nil {
				t.Fatal("record written")
			}
		})
	}
}

func TestDVAdoptRoles(t *testing.T) {
	pe := adoptEnv(t, strings.Replace(nodesRpt, "[nodes]\n", "[nodes]\n1998 = radio@127.0.0.1/1998,NONE\n", 1)+handBridge, modLoaded)
	for _, role := range []string{"", "viewer", "op"} {
		want, code := 403, "forbidden"
		if role == "" {
			want, code = 401, "unauthorized"
		}
		expectErr(t, pe.env, role, "POST", "/api/dvswitch/private-node/adopt", map[string]any{}, want, code)
	}
	if pe.dv.priv != nil {
		t.Fatal("adopted by an unauthorised user")
	}
}
