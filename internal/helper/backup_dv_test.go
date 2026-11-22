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
	dvBkRpt = "[1999]\nrxchannel = SimpleUSB/usb\n\n[1998]\nrxchannel = USRP/127.0.0.1:34001:32001\nduplex = 0\n\n[nodes]\n1998 = radio@127.0.0.1/1998,NONE\n"
	// Every DMR network's settings, the inactive one's password included.
	dvBkNets = `{"version":1,"networks":{` +
		`"BM":{"enabled":true},` +
		`"TGIF":{"password":"example-pass"},` +
		`"DMR":{"enabled":true,"address":"192.0.2.10","port":62031}}}`
)

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

// dvFullSource is a node with the bridge node 1998 and its
// record, and the network settings.
func dvFullSource(t *testing.T) *bkNode {
	t.Helper()
	src := newBkNode(t, "1.0", nil)
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

func TestBackupDVRoundTrip(t *testing.T) {
	src := dvFullSource(t)
	arc := src.archive(BackupRequest{DV: true})
	m := dvEntries(t, arc)
	if !m.Parts.DV {
		t.Fatal("part flag missing")
	}
	if dvHasLegacyFiles(m) {
		t.Error("new backups carry no ini files of the previous stack")
	}
	if e := m.FindDVPrivate(); e == nil || e.Mode != "0600" {
		t.Errorf("record: %+v", e)
	}
	if e := m.FindDVNetworks(); e == nil || e.Mode != "0600" {
		t.Errorf("networks: %+v", e)
	}

	dst := newBkNode(t, "1.0", nil)
	res, err := restoreInto(t, dst, arc, RestoreRequest{})
	if err != nil || !res.OK {
		t.Fatalf("%+v %v", res, err)
	}
	st := dvStep(res)
	if st == nil || !st.OK || !strings.Contains(st.Detail, "bridge node 1998 is managed by ASLCP again") ||
		!strings.Contains(st.Detail, "DMR network settings restored") || strings.Contains(st.Detail, "example-pass") ||
		strings.Contains(st.Detail, "radio@") || strings.Contains(st.Detail, DVRestoreIgnoredDetail) {
		t.Fatalf("step = %+v", st)
	}
	checkRestoredState(t, dst.o)
	if res.DVLegacySettings {
		t.Error("no legacy settings in this archive")
	}
}

// An archive made by 1.x carries the ini files of the previous stack: they are
// ignored and the step says so; nothing is written below /opt.
func TestRestoreDVIgnoresLegacyFiles(t *testing.T) {
	m := &backup.Manifest{Parts: backup.Parts{DV: true}, Files: []backup.Entry{
		{Path: backup.DVPath("MMDVM_Bridge/MMDVM_Bridge.ini"), Type: backup.TypeFile},
		{Path: backup.DVPath("YSFGateway/YSFGateway.ini"), Type: backup.TypeFile},
	}}
	if !dvHasLegacyFiles(m) || dvHasFiles(m) {
		t.Fatal("classification")
	}
	n := newBkNode(t, "1.0", nil)
	d, err := n.o.restoreDV(nil, m)
	if err != nil || d != DVRestoreIgnoredDetail {
		t.Fatalf("%q %v", d, err)
	}
	if _, err := os.Stat(filepath.Join(n.base, "opt")); err == nil {
		t.Fatal("something was written below /opt")
	}
	m.Files = nil
	if dvHasLegacyFiles(m) {
		t.Fatal("no files")
	}
}

func TestBackupDVEmptyChangesNothing(t *testing.T) {
	src := newBkNode(t, "1.0", nil)
	dst := newBkNode(t, "1.0", nil)
	dst.put("var/dv-state/networks.json", dvBkNets, 0o600)
	if res, err := restoreInto(t, dst, src.archive(BackupRequest{DV: true}), RestoreRequest{}); err != nil || !res.OK {
		t.Fatalf("%+v %v", res, err)
	}
	if dst.get("var/dv-state/networks.json") != dvBkNets {
		t.Error("a backup without the part changed the settings")
	}
}

func TestBackupDVInvalidNetworksNotArchived(t *testing.T) {
	src := newBkNode(t, "1.0", nil)
	src.put("var/dv-state/networks.json", `{"version":1,"networks":{"NXDN":{"address":"192.0.2.10"}}}`, 0o600)
	if m := dvEntries(t, src.archive(BackupRequest{DV: true})); m.FindDVNetworks() != nil {
		t.Fatal("invalid settings archived")
	}
}

func TestBackupDVSkipsSymlinkSource(t *testing.T) {
	n := newBkNode(t, "1.0", nil)
	if err := os.MkdirAll(filepath.Join(n.base, "var/dv-state"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(n.base, "var/dv-state/networks.json")); err != nil {
		t.Fatal(err)
	}
	m := dvEntries(t, n.archive(BackupRequest{DV: true}))
	if m.FindDVNetworks() != nil {
		t.Error("a symlink was archived")
	}
}

func TestBackupInfoDVPart(t *testing.T) {
	n := newBkNode(t, "1.0", nil)
	n.put("var/dv-state/networks.json", dvBkNets, 0o600)
	info, err := n.o.BackupInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range info.Parts {
		if p.ID == "dv" {
			if !p.Present || p.Size != int64(len(dvBkNets)) {
				t.Errorf("%+v", p)
			}
			return
		}
	}
	t.Error("no dv part")
}
