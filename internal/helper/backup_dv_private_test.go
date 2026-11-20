package helper

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/artw1/aslcp/internal/backup"
)

const dvBkRpt = "[1999]\nrxchannel = SimpleUSB/usb\n\n[1998]\nrxchannel = USRP/127.0.0.1:34001:32001\nduplex = 0\n\n[nodes]\n1998 = radio@127.0.0.1/1998,NONE\n"

func dvBkRecord(node string) DVPrivateRecord {
	return DVPrivateRecord{Version: 1, Node: node, StanzaAdded: true,
		StanzaText:     "[" + node + "]\nrxchannel = USRP/127.0.0.1:34001:32001\nduplex = 0",
		NodesLineAdded: true, NodesLine: node + " = radio@127.0.0.1/" + node + ",NONE",
		ChanUsrpWas: "noload", UsrpWas: &DVUSRP{Address: "127.0.0.1", RxPort: 31001, TxPort: 31001}, At: "2026-11-20T08:00:00Z"}
}

// dvPrivSource is a node with DVSwitch, the bridge node 1998 in rpt.conf and
// its record.
func dvPrivSource(t *testing.T, rec DVPrivateRecord) *bkNode {
	t.Helper()
	src := newBkNode(t, "1.0", nil)
	src.dvSource()
	src.put("etc-asterisk/rpt.conf", dvBkRpt, 0o640)
	b, _ := json.Marshal(rec)
	src.put("var/dv-state/privatenode.json", string(b), 0o600)
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

func TestBackupDVPrivateRoundTripInstalled(t *testing.T) {
	src := dvPrivSource(t, dvBkRecord("1998"))
	arc := src.archive(BackupRequest{DVSwitch: true})
	m := dvEntries(t, arc)
	e := m.Find(backup.PathDVSwitchPrivate)
	if e == nil || e.Mode != "0600" || !m.Parts.DVSwitch {
		t.Fatalf("record not archived: %+v", e)
	}
	// backup-info counts it
	if info, err := src.o.BackupInfo(context.Background()); err != nil {
		t.Fatal(err)
	} else {
		for _, p := range info.Parts {
			if p.ID == "dvswitch" && p.Size <= int64(len(dvBkAB)+len(dvBkMac)+len(dvBkMM)+len(dvBkYSF)) {
				t.Errorf("record not counted: %+v", p)
			}
		}
	}

	dst := newBkNode(t, "1.0", nil)
	dst.put("opt/Analog_Bridge/Analog_Bridge.ini", dvBkOld, 0o644)
	dst.fake.rules["systemctl restart"] = resp{}
	res, err := restoreInto(t, dst, arc, RestoreRequest{})
	if err != nil || !res.OK {
		t.Fatalf("%+v %v", res, err)
	}
	st := dvStep(res)
	if st == nil || !st.OK || !strings.Contains(st.Detail, "bridge node 1998 is managed by ASLCP again") {
		t.Fatalf("step = %+v", st)
	}
	if strings.Contains(st.Detail, "stanza") || strings.Contains(st.Detail, "radio@") {
		t.Errorf("the detail shows record content: %q", st.Detail)
	}
	rec, err := ReadDVPrivate(dst.o.dvPrivatePath())
	if err != nil || rec == nil || rec.Node != "1998" || !rec.StanzaAdded || rec.ChanUsrpWas != "noload" || rec.UsrpWas == nil || rec.UsrpWas.RxPort != 31001 {
		t.Fatalf("record = %+v %v", rec, err)
	}
	if fi, err := os.Stat(dst.o.dvPrivatePath()); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("record mode: %v %v", fi, err)
	}
	if fi, err := os.Stat(dst.o.dvStateDir()); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("state dir mode: %v %v", fi, err)
	}
	if ents, _ := os.ReadDir(dst.o.dvStateDir()); len(ents) != 1 {
		t.Errorf("leftover files in the state dir: %v", ents)
	}
}

func TestBackupDVPrivateRoundTripPending(t *testing.T) {
	src := dvPrivSource(t, dvBkRecord("1998"))
	arc := src.archive(BackupRequest{DVSwitch: true})
	dst := newBkNode(t, "1.0", nil) // DVSwitch not installed
	res, err := restoreInto(t, dst, arc, RestoreRequest{})
	if err != nil || !res.OK {
		t.Fatalf("%+v %v", res, err)
	}
	st := dvStep(res)
	if st == nil || !strings.HasPrefix(st.Detail, DVRestorePendingDetail) || !strings.Contains(st.Detail, "bridge node record is saved too") {
		t.Fatalf("step = %+v", st)
	}
	// staged root-only next to the ini files, not yet in the state dir
	pd := dst.o.dvPendingDir()
	if fi, err := os.Stat(filepath.Join(pd, dvPrivateStaged)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("staged record: %v %v", fi, err)
	}
	if _, err := os.Stat(dst.o.dvPrivatePath()); err == nil {
		t.Fatal("the record was written before DVSwitch is installed")
	}
	if ps := dst.o.dvPendingStatus(); !ps.Pending || len(ps.Files) != 4 {
		t.Errorf("status = %+v", ps)
	}
	mf, _ := os.ReadFile(filepath.Join(pd, dvPendingManifest))
	if strings.Contains(string(mf), "radio@") || strings.Contains(string(mf), "stanza") {
		t.Error("the pending manifest holds record content")
	}
	// the install applies it
	dst.put("opt/Analog_Bridge/Analog_Bridge.ini", dvBkOld, 0o644)
	dst.put("opt/MMDVM_Bridge/MMDVM_Bridge.ini", dvBkOldM, 0o600)
	dst.fake.rules["systemctl restart"] = resp{}
	var w bytes.Buffer
	if err := dst.o.RunDVSwitchStep(context.Background(), StepRestoreApply, &w); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.String(), "bridge node 1998 is managed by ASLCP again") || strings.Contains(w.String(), "radio@") {
		t.Errorf("log = %q", w.String())
	}
	rec, err := ReadDVPrivate(dst.o.dvPrivatePath())
	if err != nil || rec == nil || rec.Node != "1998" {
		t.Fatalf("record = %+v %v", rec, err)
	}
	if fi, _ := os.Stat(dst.o.dvPrivatePath()); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode().Perm())
	}
	if dst.o.dvPendingStatus().Pending {
		t.Error("pending set kept")
	}
}

func TestBackupDVPrivateDroppedWithoutStanza(t *testing.T) {
	// the record names node 1997, which the backed-up rpt.conf lacks
	src := dvPrivSource(t, dvBkRecord("1997"))
	arc := src.archive(BackupRequest{DVSwitch: true})
	dst := newBkNode(t, "1.0", nil)
	dst.put("opt/Analog_Bridge/Analog_Bridge.ini", dvBkOld, 0o644)
	dst.fake.rules["systemctl restart"] = resp{}
	res, err := restoreInto(t, dst, arc, RestoreRequest{})
	if err != nil || !res.OK {
		t.Fatalf("%+v %v", res, err)
	}
	st := dvStep(res)
	if st == nil || !strings.Contains(st.Detail, "was not restored: the restored rpt.conf has no node 1997") {
		t.Fatalf("step = %+v", st)
	}
	if _, err := os.Stat(dst.o.dvPrivatePath()); err == nil {
		t.Error("a record without its stanza was written")
	}
	// a template of that name is not a stanza either
	src2 := dvPrivSource(t, dvBkRecord("1998"))
	src2.put("etc-asterisk/rpt.conf", "[1998](!)\nduplex = 0\n", 0o640)
	dst2 := newBkNode(t, "1.0", nil)
	dst2.put("opt/Analog_Bridge/Analog_Bridge.ini", dvBkOld, 0o644)
	dst2.fake.rules["systemctl restart"] = resp{}
	if _, err := restoreInto(t, dst2, src2.archive(BackupRequest{DVSwitch: true}), RestoreRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dst2.o.dvPrivatePath()); err == nil {
		t.Error("a template counted as the stanza")
	}
	// pending path: nothing is staged for the record either
	dst3 := newBkNode(t, "1.0", nil)
	if _, err := restoreInto(t, dst3, arc, RestoreRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst3.o.dvPendingDir(), dvPrivateStaged)); err == nil {
		t.Error("an unusable record was staged")
	}
}

func TestBackupDVPrivateStanzaGoneBeforeInstall(t *testing.T) {
	src := dvPrivSource(t, dvBkRecord("1998"))
	arc := src.archive(BackupRequest{DVSwitch: true})
	dst := newBkNode(t, "1.0", nil)
	if _, err := restoreInto(t, dst, arc, RestoreRequest{}); err != nil {
		t.Fatal(err)
	}
	dst.put("etc-asterisk/rpt.conf", "[1999]\nrxchannel = SimpleUSB/usb\n", 0o640) // edited since
	dst.put("opt/Analog_Bridge/Analog_Bridge.ini", dvBkOld, 0o644)
	dst.fake.rules["systemctl restart"] = resp{}
	var w bytes.Buffer
	if err := dst.o.RunDVSwitchStep(context.Background(), StepRestoreApply, &w); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.String(), "bridge node record from your backup was not restored") {
		t.Errorf("log = %q", w.String())
	}
	if _, err := os.Stat(dst.o.dvPrivatePath()); err == nil {
		t.Error("record written")
	}
	if dst.get("opt/Analog_Bridge/Analog_Bridge.ini") != dvBkAB {
		t.Error("the ini files must still be applied")
	}
}

func TestBackupDVPrivateInvalidRecordsDropped(t *testing.T) {
	bad := map[string]string{
		"unknown field": `{"version":1,"node":"1998","stanzaAdded":false,"nodesLineAdded":false,"chanUsrpWas":"noload","x":1}`,
		"version":       `{"version":2,"node":"1998","stanzaAdded":false,"nodesLineAdded":false,"chanUsrpWas":"noload"}`,
		"public node":   `{"version":1,"node":"2001","stanzaAdded":false,"nodesLineAdded":false,"chanUsrpWas":"noload"}`,
		"not a number":  `{"version":1,"node":"19x8","stanzaAdded":false,"nodesLineAdded":false,"chanUsrpWas":"noload"}`,
		"chan_usrp":     `{"version":1,"node":"1998","stanzaAdded":false,"nodesLineAdded":false,"chanUsrpWas":"maybe"}`,
		"not json":      `[1998]`,
	}
	for name, content := range bad {
		src := dvPrivSource(t, dvBkRecord("1998"))
		src.put("var/dv-state/privatenode.json", content, 0o600)
		arc := src.archive(BackupRequest{DVSwitch: true})
		// a record that is not valid is not even archived
		if dvEntries(t, arc).Find(backup.PathDVSwitchPrivate) != nil {
			t.Errorf("%s: archived", name)
		}
		if len(dvEntries(t, arc).Skipped) == 0 {
			t.Errorf("%s: not reported as skipped", name)
		}
	}
	// an oversized record is not archived either
	src := dvPrivSource(t, dvBkRecord("1998"))
	big := strings.Repeat("x", dvPrivateMax+1)
	src.put("var/dv-state/privatenode.json", big, 0o600)
	m := dvEntries(t, src.archive(BackupRequest{DVSwitch: true}))
	if m.Find(backup.PathDVSwitchPrivate) != nil {
		t.Error("an oversized record was archived")
	}
}

func TestBackupDVPrivateOlderBackupChangesNothing(t *testing.T) {
	src := newBkNode(t, "1.0", nil)
	src.dvSource() // no record
	src.put("etc-asterisk/rpt.conf", dvBkRpt, 0o640)
	arc := src.archive(BackupRequest{DVSwitch: true})
	if dvEntries(t, arc).Find(backup.PathDVSwitchPrivate) != nil {
		t.Fatal("record archived without one")
	}
	dst := newBkNode(t, "1.0", nil)
	dst.put("opt/Analog_Bridge/Analog_Bridge.ini", dvBkOld, 0o644)
	dst.fake.rules["systemctl restart"] = resp{}
	// a record the node already has stays as it is
	existing, _ := json.Marshal(dvBkRecord("1998"))
	dst.put("var/dv-state/privatenode.json", string(existing), 0o600)
	res, err := restoreInto(t, dst, arc, RestoreRequest{})
	if err != nil || !res.OK {
		t.Fatalf("%+v %v", res, err)
	}
	if st := dvStep(res); st == nil || strings.Contains(st.Detail, "bridge node") {
		t.Errorf("step = %+v", st)
	}
	if got := dst.get("var/dv-state/privatenode.json"); got != string(existing) {
		t.Error("the existing record changed")
	}
	// and without one there is still none
	dst2 := newBkNode(t, "1.0", nil)
	dst2.put("opt/Analog_Bridge/Analog_Bridge.ini", dvBkOld, 0o644)
	dst2.fake.rules["systemctl restart"] = resp{}
	if _, err := restoreInto(t, dst2, arc, RestoreRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dst2.o.dvPrivatePath()); err == nil {
		t.Error("a record appeared from nowhere")
	}
}

func TestBackupDVPrivateSkipsSymlinkSource(t *testing.T) {
	src := dvPrivSource(t, dvBkRecord("1998"))
	p := filepath.Join(src.base, "var/dv-state/privatenode.json")
	os.Remove(p)
	if err := os.Symlink("/etc/passwd", p); err != nil {
		t.Fatal(err)
	}
	m := dvEntries(t, src.archive(BackupRequest{DVSwitch: true}))
	if m.Find(backup.PathDVSwitchPrivate) != nil || len(m.Skipped) == 0 {
		t.Errorf("symlink followed: %v", m.Skipped)
	}
}

func TestBackupDVPrivateRestoreDoesNotFollowSymlink(t *testing.T) {
	src := dvPrivSource(t, dvBkRecord("1998"))
	arc := src.archive(BackupRequest{DVSwitch: true})
	dst := newBkNode(t, "1.0", nil)
	dst.put("opt/Analog_Bridge/Analog_Bridge.ini", dvBkOld, 0o644)
	dst.fake.rules["systemctl restart"] = resp{}
	// the record path is a symlink to a file elsewhere: nothing is written
	// through it, the restore says so and goes on
	target := filepath.Join(dst.base, "elsewhere.txt")
	os.WriteFile(target, []byte("keep"), 0o644)
	os.MkdirAll(dst.o.dvStateDir(), 0o700)
	if err := os.Symlink(target, dst.o.dvPrivatePath()); err != nil {
		t.Fatal(err)
	}
	res, err := restoreInto(t, dst, arc, RestoreRequest{})
	if err != nil || !res.OK {
		t.Fatalf("%+v %v", res, err)
	}
	if b, _ := os.ReadFile(target); string(b) != "keep" {
		t.Errorf("the symlink target was written: %q", b)
	}
	if st := dvStep(res); st == nil || !strings.Contains(st.Detail, "the bridge node record could not be restored") {
		t.Errorf("step = %+v", st)
	}
}

func TestBackupDVPrivateRestoreValidatesArchiveContent(t *testing.T) {
	for name, content := range map[string]string{
		"unknown field": `{"version":1,"node":"1998","stanzaAdded":false,"nodesLineAdded":false,"chanUsrpWas":"noload","x":1}`,
		"public node":   `{"version":1,"node":"2001","stanzaAdded":false,"nodesLineAdded":false,"chanUsrpWas":"noload"}`,
		"not json":      `[1998]`,
		"too large":     `{"version":1,"node":"1998","stanzaAdded":false,"nodesLineAdded":false,"chanUsrpWas":"noload","stanzaText":"` + strings.Repeat("x", dvPrivateMax) + `"}`,
	} {
		src := newBkNode(t, "1.0", nil)
		src.dvSource()
		src.put("etc-asterisk/rpt.conf", dvBkRpt, 0o640)
		arc := src.archiveExtra(BackupRequest{DVSwitch: true}, map[string]string{backup.PathDVSwitchPrivate: content})
		dst := newBkNode(t, "1.0", nil)
		dst.put("opt/Analog_Bridge/Analog_Bridge.ini", dvBkOld, 0o644)
		dst.fake.rules["systemctl restart"] = resp{}
		res, err := restoreInto(t, dst, arc, RestoreRequest{})
		if err != nil || !res.OK {
			t.Errorf("%s: %+v %v", name, res, err)
			continue
		}
		if _, err := os.Stat(dst.o.dvPrivatePath()); err == nil {
			t.Errorf("%s: record written", name)
		}
		if st := dvStep(res); st == nil || !strings.Contains(st.Detail, "was not restored") {
			t.Errorf("%s: step = %+v", name, st)
		}
	}
}
