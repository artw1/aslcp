package helper

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/artw1/aslcp/internal/backup"
)

const (
	dvBkAB  = "[GENERAL]\nsubscriberID = 1234567\n\n[DMR]\ntxTg = 9\n"
	dvBkMM  = "[General]\nCallsign=N0CALL\n\n[DMR Network]\nPassword=example-pass\n"
	dvBkYSF = "[General]\nCallsign=N0CALL\n"
	dvBkMac = "# macro\n*1 = tune 9\n"
	dvBkOld = "[GENERAL]\nsubscriberID = 7654321\n"
	dvBkRpt = "[1999]\nrxchannel = SimpleUSB/usb\n\n[1998]\nrxchannel = USRP/127.0.0.1:34001:32001\nduplex = 0\n\n[nodes]\n1998 = radio@127.0.0.1/1998,NONE\n"
	// Every DMR network's settings, the inactive one's password included.
	dvBkNets = `{"version":1,"networks":{` +
		`"BM":{"enabled":true},` +
		`"TGIF":{"password":"example-pass"},` +
		`"DMR":{"enabled":true,"address":"192.0.2.10","port":62031}}}`
)

func (n *bkNode) dvSource() {
	n.put("opt/Analog_Bridge/Analog_Bridge.ini", dvBkAB, 0o644)
	n.put("opt/Analog_Bridge/dvsm.macro", dvBkMac, 0o644)
	n.put("opt/MMDVM_Bridge/MMDVM_Bridge.ini", dvBkMM, 0o640)
	n.put("opt/YSFGateway/YSFGateway.ini", dvBkYSF, 0o644)
}

func dvEntries(t *testing.T, arc []byte) *backup.Manifest {
	t.Helper()
	m, err := backup.Read(strings.NewReader(string(arc)), nil)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func dvBkRecord(node string) DVPrivateRecord {
	return DVPrivateRecord{Version: 1, Node: node, StanzaAdded: true,
		StanzaText:     "[" + node + "]\nrxchannel = USRP/127.0.0.1:34001:32001\nduplex = 0",
		NodesLineAdded: true, NodesLine: node + " = radio@127.0.0.1/" + node + ",NONE",
		ChanUsrpWas: "noload", At: "2026-11-20T08:00:00Z"}
}

// dvFullSource is a node with the legacy files, the bridge node 1998 and its
// record, and the network settings.
func dvFullSource(t *testing.T) *bkNode {
	t.Helper()
	src := newBkNode(t, "1.0", nil)
	src.dvSource()
	src.put("etc-asterisk/rpt.conf", dvBkRpt, 0o640)
	b, _ := json.Marshal(dvBkRecord("1998"))
	src.put("var/dv-state/privatenode.json", string(b), 0o600)
	src.put("var/dv-state/networks.json", dvBkNets, 0o600)
	return src
}

func dvStep(res RestoreResult) *RestoreStep {
	for i := range res.Steps {
		if res.Steps[i].Name == dvStepName {
			return &res.Steps[i]
		}
	}
	return nil
}

func checkRestoredState(t *testing.T, o *Ops) {
	t.Helper()
	rec, err := ReadDVPrivate(o.dvPrivatePath())
	if err != nil || rec == nil || rec.Node != "1998" || !rec.StanzaAdded {
		t.Fatalf("record = %+v %v", rec, err)
	}
	for _, p := range []string{o.dvPrivatePath(), o.dvNetworksPath()} {
		if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v %v", p, fi, err)
		}
	}
	raw, _ := os.ReadFile(o.dvNetworksPath())
	if !strings.Contains(string(raw), "example-pass") || !strings.Contains(string(raw), "192.0.2.10") {
		t.Fatalf("networks.json = %s", raw)
	}
}

func TestBackupDVRoundTripInstalled(t *testing.T) {
	src := dvFullSource(t)
	arc := src.archive(BackupRequest{DV: true})
	m := dvEntries(t, arc)
	if !m.Parts.DV {
		t.Fatal("part flag missing")
	}
	for _, p := range []string{"Analog_Bridge/Analog_Bridge.ini", "Analog_Bridge/dvsm.macro", "MMDVM_Bridge/MMDVM_Bridge.ini", "YSFGateway/YSFGateway.ini"} {
		if m.Find(backup.DVPath(p)) == nil {
			t.Errorf("%s not archived", p)
		}
	}
	if e := m.FindDVPrivate(); e == nil || e.Mode != "0600" {
		t.Errorf("record: %+v", e)
	}
	if e := m.FindDVNetworks(); e == nil || e.Mode != "0600" {
		t.Errorf("networks: %+v", e)
	}

	dst := newBkNode(t, "1.0", nil)
	dst.put("opt/Analog_Bridge/Analog_Bridge.ini", dvBkOld, 0o644)
	res, err := restoreInto(t, dst, arc, RestoreRequest{})
	if err != nil || !res.OK {
		t.Fatalf("%+v %v", res, err)
	}
	st := dvStep(res)
	if st == nil || !st.OK || !strings.Contains(st.Detail, "bridge node 1998 is managed by ASLCP again") ||
		!strings.Contains(st.Detail, "DMR network settings restored") || strings.Contains(st.Detail, "example-pass") ||
		strings.Contains(st.Detail, "radio@") {
		t.Fatalf("step = %+v", st)
	}
	checkRestoredState(t, dst.o)
	if got := dst.get("opt/Analog_Bridge/Analog_Bridge.ini"); got != dvBkAB {
		t.Errorf("ini not restored: %q", got)
	}
	if dst.o.dvPendingStatus().Pending {
		t.Error("nothing should be pending when the files were written")
	}
}

func TestBackupDVRoundTripPending(t *testing.T) {
	src := dvFullSource(t)
	arc := src.archive(BackupRequest{DV: true})
	dst := newBkNode(t, "1.0", nil) // the previous stack is not installed
	dst.fake.rules["dpkg-query -W -f=${Package}\t${db:Status-Status}"] = resp{}
	res, err := restoreInto(t, dst, arc, RestoreRequest{})
	if err != nil || !res.OK {
		t.Fatalf("%+v %v", res, err)
	}
	st := dvStep(res)
	if st == nil || !strings.Contains(st.Detail, DVRestorePendingDetail) {
		t.Fatalf("step = %+v", st)
	}
	// the engine's own state is restored directly, the ini files are staged
	checkRestoredState(t, dst.o)
	if _, err := os.Stat(filepath.Join(dst.base, "opt", "Analog_Bridge")); err == nil {
		t.Fatal("files written to the legacy directory")
	}
	ps := dst.o.dvPendingStatus()
	if !ps.Pending || len(ps.Files) != 4 {
		t.Fatalf("status = %+v", ps)
	}
	mf, _ := os.ReadFile(filepath.Join(dst.o.dvPendingDir(), dvPendingManifest))
	if strings.Contains(string(mf), "example-pass") {
		t.Error("the pending manifest holds file content")
	}
	// discard-only
	if _, err := dst.o.DVRestoreDiscard(context.Background()); err != nil {
		t.Fatal(err)
	}
	if dst.o.dvPendingStatus().Pending {
		t.Error("discard left the set")
	}
}

func TestDVPendingStageRefusesBadPath(t *testing.T) {
	n := newBkNode(t, "1.0", nil)
	items := []dvItem{{rel: "Analog_Bridge/Analog_Bridge.ini", e: backup.Entry{Owner: "root", Group: "root", Mode: "0644"}, data: []byte(dvBkAB)}}
	if err := n.o.dvStagePending(items, "2026-11-20 08:15"); err != nil {
		t.Fatal(err)
	}
	if err := n.o.dvStagePending([]dvItem{{rel: "../etc/passwd", data: []byte("x")}}, "d"); err == nil {
		t.Error("a path outside the allowlist was staged")
	}
	if !n.o.dvPendingStatus().Pending {
		t.Fatal("a refused stage must keep the old set")
	}
}

func TestBackupDVOlderBackupChangesNothing(t *testing.T) {
	src := newBkNode(t, "1.0", nil)
	src.dvSource()
	dst := newBkNode(t, "1.0", nil)
	dst.put("opt/Analog_Bridge/Analog_Bridge.ini", dvBkOld, 0o644)
	dst.put("var/dv-state/networks.json", dvBkNets, 0o600)
	if res, err := restoreInto(t, dst, src.archive(BackupRequest{DV: true}), RestoreRequest{}); err != nil || !res.OK {
		t.Fatalf("%+v %v", res, err)
	}
	if dst.get("var/dv-state/networks.json") != dvBkNets {
		t.Error("an older backup changed the settings")
	}
}

func TestBackupDVInvalidNetworksNotArchived(t *testing.T) {
	src := newBkNode(t, "1.0", nil)
	src.dvSource()
	src.put("var/dv-state/networks.json", `{"version":1,"networks":{"TGIF":{"address":"192.0.2.10"}}}`, 0o600)
	if m := dvEntries(t, src.archive(BackupRequest{DV: true})); m.FindDVNetworks() != nil {
		t.Fatal("invalid settings archived")
	}
}

func TestBackupDVSkipsSymlinkSource(t *testing.T) {
	n := newBkNode(t, "1.0", nil)
	n.dvSource()
	p := filepath.Join(n.base, "opt/YSFGateway/YSFGateway.ini")
	os.Remove(p)
	if err := os.Symlink("/etc/passwd", p); err != nil {
		t.Fatal(err)
	}
	m := dvEntries(t, n.archive(BackupRequest{DV: true}))
	if m.Find(backup.DVPath("YSFGateway/YSFGateway.ini")) != nil {
		t.Error("a symlink was archived")
	}
}

func TestBackupInfoDVPart(t *testing.T) {
	n := newBkNode(t, "1.0", nil)
	n.dvSource()
	info, err := n.o.BackupInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range info.Parts {
		if p.ID == "dv" {
			want := int64(len(dvBkAB) + len(dvBkMac) + len(dvBkMM) + len(dvBkYSF))
			if !p.Present || p.Size != want {
				t.Errorf("%+v want size %d", p, want)
			}
			return
		}
	}
	t.Error("no dv part")
}

func TestDVBackupFilesMatchAllowlist(t *testing.T) {
	want := map[string]bool{}
	for _, f := range dvFiles {
		want[f.rel] = false
	}
	for _, rel := range backup.DVFiles {
		if _, ok := want[rel]; ok {
			want[rel] = true
		}
	}
	for rel, ok := range want {
		if !ok {
			t.Errorf("%s is in the legacy list but not in the backup part", rel)
		}
	}
	if len(backup.DVFiles) != len(dvFiles)+1 {
		t.Error("the backup part is the legacy files plus dvsm.macro")
	}
}
