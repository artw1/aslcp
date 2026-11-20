package helper

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/artw1/aslcp/internal/backup"
)

const (
	dvBkAB   = "[GENERAL]\nsubscriberID = 1234567\n\n[DMR]\ntxTg = 9\n"
	dvBkMM   = "[General]\nCallsign=N0CALL\n\n[DMR Network]\nPassword=example-pass\n"
	dvBkYSF  = "[General]\nCallsign=N0CALL\n"
	dvBkMac  = "# macro\n*1 = tune 9\n"
	dvBkOld  = "[GENERAL]\nsubscriberID = 7654321\n"
	dvBkOldM = "[General]\nCallsign=OLD\n\n[DMR Network]\nPassword=passw0rd\n"
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

func TestBackupDVSwitchRoundTripKeepsSecretsAndHistory(t *testing.T) {
	src := newBkNode(t, "1.0", nil)
	src.dvSource()
	arc := src.archive(BackupRequest{DVSwitch: true})
	m := dvEntries(t, arc)
	if !m.Parts.DVSwitch {
		t.Fatal("part flag missing")
	}
	for _, p := range []string{"Analog_Bridge/Analog_Bridge.ini", "Analog_Bridge/dvsm.macro", "MMDVM_Bridge/MMDVM_Bridge.ini", "YSFGateway/YSFGateway.ini"} {
		if m.Find("dvswitch/opt/"+p) == nil {
			t.Errorf("%s not archived", p)
		}
	}
	if m.Find("dvswitch/opt/P25Gateway/P25Gateway.ini") != nil {
		t.Error("a missing gateway file must not be archived")
	}
	if e := m.Find("dvswitch/opt/MMDVM_Bridge/MMDVM_Bridge.ini"); e == nil || e.Mode != "0640" {
		t.Errorf("mode not recorded: %+v", e)
	}

	dst := newBkNode(t, "1.0", nil)
	dst.put("opt/Analog_Bridge/Analog_Bridge.ini", dvBkOld, 0o644)
	dst.put("opt/MMDVM_Bridge/MMDVM_Bridge.ini", dvBkOldM, 0o600)
	dst.put("opt/Analog_Bridge/untouched.txt", "keep\n", 0o644)
	res, err := restoreInto(t, dst, arc, RestoreRequest{})
	if err != nil || !res.OK {
		t.Fatalf("%+v %v", res, err)
	}
	if got := dst.get("opt/MMDVM_Bridge/MMDVM_Bridge.ini"); got != dvBkMM { // no redaction
		t.Errorf("mmdvm = %q", got)
	}
	if dst.get("opt/Analog_Bridge/Analog_Bridge.ini") != dvBkAB || dst.get("opt/Analog_Bridge/dvsm.macro") != dvBkMac {
		t.Error("files not restored")
	}
	if dst.get("opt/Analog_Bridge/untouched.txt") != "keep\n" {
		t.Error("an unrelated file changed")
	}
	if fi, _ := os.Stat(filepath.Join(dst.base, "opt/MMDVM_Bridge/MMDVM_Bridge.ini")); fi.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v", fi.Mode().Perm())
	}
	// the gateway directory is missing here: skipped, not created
	if _, err := os.Stat(filepath.Join(dst.base, "opt/YSFGateway")); err == nil {
		t.Error("a missing gateway directory was created")
	}
	var step *RestoreStep
	for i := range res.Steps {
		if res.Steps[i].Name == dvStepName {
			step = &res.Steps[i]
		}
	}
	if step == nil || !step.OK || !strings.Contains(step.Detail, "skipped") {
		t.Fatalf("step = %+v", step)
	}
	// history copies of the replaced files, the old content
	names := dst.o.dvHistoryNames(DVFileAnalogBridge)
	if len(names) != 1 {
		t.Fatalf("history = %v", names)
	}
	b, _ := os.ReadFile(filepath.Join(dst.o.dvHistDir(DVFileAnalogBridge), names[0]))
	if string(b) != dvBkOld {
		t.Errorf("history copy = %q", b)
	}
	if len(dst.o.dvHistoryNames(DVFileMMDVMBridge)) != 1 {
		t.Error("no history copy of MMDVM_Bridge.ini")
	}
	if len(dst.o.dvHistoryNames("dvsm_macro")) != 0 {
		t.Error("a new file has nothing to copy")
	}
}

func TestBackupDVSwitchRestoreNotInstalled(t *testing.T) {
	src := newBkNode(t, "1.0", nil)
	src.dvSource()
	arc := src.archive(BackupRequest{DVSwitch: true})
	dst := newBkNode(t, "1.0", nil) // no /opt/Analog_Bridge

	dry, err := restoreInto(t, dst, arc, RestoreRequest{DryRun: true})
	if err != nil || !dry.OK || !dry.DVSwitchNotInstalled {
		t.Fatalf("dry run: %+v %v", dry, err)
	}
	res, err := restoreInto(t, dst, arc, RestoreRequest{})
	if err != nil || !res.OK || !res.DVSwitchNotInstalled {
		t.Fatalf("%+v %v", res, err)
	}
	found := false
	for _, s := range res.Steps {
		if s.Name == dvStepName {
			found = s.OK && s.Detail == DVRestorePendingDetail
		}
	}
	if !found {
		t.Errorf("steps = %+v", res.Steps)
	}
	if _, err := os.Stat(filepath.Join(dst.base, "opt")); err == nil {
		t.Error("/opt was created although DVSwitch is not installed")
	}
	// the files are staged, root-only, with the manifest data
	pd := dst.o.dvPendingDir()
	if fi, err := os.Stat(pd); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("pending dir: %v %v", fi, err)
	}
	ents, _ := os.ReadDir(pd)
	if len(ents) != 5 { // four files and the manifest
		t.Errorf("pending dir has %d entries", len(ents))
	}
	for _, e := range ents {
		if fi, _ := e.Info(); fi.Mode().Perm() != 0o600 {
			t.Errorf("%s mode %v", e.Name(), fi.Mode().Perm())
		}
	}
	ps := dst.o.dvPendingStatus()
	if !ps.Pending || ps.BackupDate == "" || len(ps.Files) != 4 {
		t.Errorf("pending status = %+v", ps)
	}
	mf, _ := os.ReadFile(filepath.Join(pd, dvPendingManifest))
	if strings.Contains(string(mf), "example-pass") {
		t.Error("the manifest holds file content")
	}

	// a second restore replaces the set
	dst.o.dvStagePending([]dvItem{{rel: "Analog_Bridge/Analog_Bridge.ini", e: backup.Entry{Owner: "root", Group: "root", Mode: "0644"}, data: []byte(dvBkAB)}}, "2026-01-01 00:00")
	if ps := dst.o.dvPendingStatus(); len(ps.Files) != 1 || ps.BackupDate != "2026-01-01 00:00" {
		t.Errorf("replaced set = %+v", ps)
	}
}

func TestDVSwitchPendingApplyOnInstall(t *testing.T) {
	src := newBkNode(t, "1.0", nil)
	src.dvSource()
	arc := src.archive(BackupRequest{DVSwitch: true})
	dst := newBkNode(t, "1.0", nil)
	if res, err := restoreInto(t, dst, arc, RestoreRequest{}); err != nil || !res.OK {
		t.Fatalf("%+v %v", res, err)
	}
	// no DVSwitch yet: the apply step does nothing and keeps the set
	var w bytes.Buffer
	dst.fake.rules["systemctl try-restart"] = resp{}
	if err := dst.o.RunDVSwitchStep(context.Background(), StepRestoreApply, &w); err != nil || !strings.Contains(w.String(), "not installed") {
		t.Fatalf("%v %q", err, w.String())
	}
	if !dst.o.dvPendingStatus().Pending {
		t.Fatal("pending set lost although DVSwitch is not installed")
	}
	// the install creates the stock files (no YSFGateway directory: that
	// gateway is not installed)
	dst.put("opt/Analog_Bridge/Analog_Bridge.ini", dvBkOld, 0o644)
	dst.put("opt/MMDVM_Bridge/MMDVM_Bridge.ini", dvBkOldM, 0o600)
	w.Reset()
	if err := dst.o.RunDVSwitchStep(context.Background(), StepRestoreApply, &w); err != nil {
		t.Fatal(err)
	}
	if dst.get("opt/MMDVM_Bridge/MMDVM_Bridge.ini") != dvBkMM || dst.get("opt/Analog_Bridge/Analog_Bridge.ini") != dvBkAB || dst.get("opt/Analog_Bridge/dvsm.macro") != dvBkMac {
		t.Error("pending files not applied")
	}
	if fi, _ := os.Stat(filepath.Join(dst.base, "opt/MMDVM_Bridge/MMDVM_Bridge.ini")); fi.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v", fi.Mode().Perm())
	}
	if len(dst.o.dvHistoryNames(DVFileMMDVMBridge)) != 1 {
		t.Error("no history copy of the stock file")
	}
	if _, err := os.Stat(filepath.Join(dst.base, "opt/YSFGateway")); err == nil {
		t.Error("a missing gateway directory was created")
	}
	if !strings.Contains(w.String(), "skipped: YSFGateway is not installed") || strings.Contains(w.String(), "example-pass") {
		t.Errorf("log = %q", w.String())
	}
	if dst.o.dvPendingStatus().Pending {
		t.Error("pending set not deleted")
	}
	if dst.fake.called("systemctl try-restart analog_bridge.service mmdvm_bridge.service") != 1 {
		t.Errorf("calls = %v", dst.fake.calls)
	}
	// nothing pending: silent no-op
	w.Reset()
	if err := dst.o.RunDVSwitchStep(context.Background(), StepRestoreApply, &w); err != nil || w.Len() != 0 {
		t.Errorf("%v %q", err, w.String())
	}
}

func TestDVSwitchPendingDiscardAndInvalid(t *testing.T) {
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
	for k, v := range dvRules() {
		n.fake.rules[k] = v
	}
	if _, err := n.o.DVSwitchRestoreDiscard(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n.o.dvPendingStatus().Pending {
		t.Error("discard left the set")
	}
	// a manifest naming a file outside the allowlist is dropped, never applied
	n.o.dvStagePending(items, "d")
	os.WriteFile(filepath.Join(n.o.dvPendingDir(), dvPendingManifest), []byte(`{"version":1,"backupDate":"d","files":[{"rel":"../../etc/passwd","owner":"root","group":"root","mode":"0644"}]}`), 0o600)
	n.put("opt/Analog_Bridge/x", "", 0o644)
	var w bytes.Buffer
	if err := n.o.RunDVSwitchStep(context.Background(), StepRestoreApply, &w); err != nil || !strings.Contains(w.String(), "discarded") {
		t.Fatalf("%v %q", err, w.String())
	}
	if _, err := os.Stat(n.o.dvPendingDir()); err == nil {
		t.Error("invalid set kept")
	}
}

func TestIsDVSwitchPackage(t *testing.T) {
	for _, n := range []string{"dvswitch-base", "dvswitch-server", "analog-bridge", "mmdvm-bridge", "md380-emu", "ysfgateway", "p25gateway", "nxdngateway", "stfu"} {
		if !IsDVSwitchPackage(n) {
			t.Errorf("%s not recognised", n)
		}
	}
	for _, n := range []string{"wget", "asl3", "m17-gateway", "supermon"} {
		if IsDVSwitchPackage(n) {
			t.Errorf("%s wrongly recognised", n)
		}
	}
}

func TestInstallScriptAppliesPending(t *testing.T) {
	o := dvOps(t, &fakeRunner{})
	sc := o.dvInstallScript()
	a, b, c := strings.Index(sc, "apt-get install -y"), strings.Index(sc, "dvswitch-step restore-apply"), strings.Index(sc, "dvswitch-step install-check")
	if a < 0 || b < a || c < b {
		t.Errorf("order wrong in %q", sc)
	}
}

func TestBackupDVSwitchRestoreRefusesSymlinkAndKeepsAsterisk(t *testing.T) {
	src := newBkNode(t, "1.0", nil)
	src.dvSource()
	arc := src.archive(BackupRequest{DVSwitch: true})
	dst := newBkNode(t, "1.0", nil)
	dst.put("opt/Analog_Bridge/dvsm.macro", "old\n", 0o644)
	dst.put("victim.txt", "victim\n", 0o644)
	link := filepath.Join(dst.base, "opt/Analog_Bridge/Analog_Bridge.ini")
	if err := os.Symlink(filepath.Join(dst.base, "victim.txt"), link); err != nil {
		t.Fatal(err)
	}
	res, err := restoreInto(t, dst, arc, RestoreRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || !strings.Contains(res.Error, dvStepName) {
		t.Fatalf("restore must fail at the DVSwitch step: %+v", res)
	}
	if dst.get("victim.txt") != "victim\n" {
		t.Error("the symlink target was written")
	}
	if dst.get("opt/Analog_Bridge/dvsm.macro") != "old\n" {
		t.Error("nothing may be written when a target is refused")
	}
	if fi, _ := os.Lstat(link); fi.Mode()&os.ModeSymlink == 0 {
		t.Error("the symlink was replaced")
	}
	started := 0
	for _, c := range dst.fake.calls {
		if c == "systemctl start asterisk" {
			started++
		}
	}
	if started == 0 {
		t.Error("Asterisk was not started again")
	}
}

func TestBackupDVSwitchOptionalAndOlderBackups(t *testing.T) {
	src := newBkNode(t, "1.0", nil)
	src.dvSource()
	// not requested: nothing under dvswitch
	m := dvEntries(t, src.archive(BackupRequest{}))
	if m.Parts.DVSwitch {
		t.Error("part included without being asked")
	}
	for _, e := range m.Files {
		if strings.HasPrefix(e.Path, "dvswitch/") {
			t.Errorf("unexpected %s", e.Path)
		}
	}
	// requested, DVSwitch not installed: no part, no error
	empty := newBkNode(t, "1.0", nil)
	if m := dvEntries(t, empty.archive(BackupRequest{DVSwitch: true})); m.Parts.DVSwitch {
		t.Error("part without DVSwitch")
	}
	// restoring an archive without the part leaves /opt alone
	dst := newBkNode(t, "1.0", nil)
	dst.put("opt/Analog_Bridge/Analog_Bridge.ini", dvBkOld, 0o644)
	res, err := restoreInto(t, dst, src.archive(BackupRequest{}), RestoreRequest{})
	if err != nil || !res.OK || res.DVSwitchNotInstalled {
		t.Fatalf("%+v %v", res, err)
	}
	if dst.get("opt/Analog_Bridge/Analog_Bridge.ini") != dvBkOld {
		t.Error("an older backup changed DVSwitch files")
	}
	for _, s := range res.Steps {
		if s.Name == dvStepName {
			t.Error("DVSwitch step without the part")
		}
	}
}

func TestBackupDVSwitchSkipsSymlinkSource(t *testing.T) {
	src := newBkNode(t, "1.0", nil)
	src.dvSource()
	if err := os.Remove(filepath.Join(src.base, "opt/YSFGateway/YSFGateway.ini")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(src.base, "opt/YSFGateway/YSFGateway.ini")); err != nil {
		t.Fatal(err)
	}
	m := dvEntries(t, src.archive(BackupRequest{DVSwitch: true}))
	if m.Find("dvswitch/opt/YSFGateway/YSFGateway.ini") != nil {
		t.Error("a symlink was followed into the backup")
	}
	if len(m.Skipped) == 0 {
		t.Error("the skipped file is not reported")
	}
}

func TestBackupInfoDVSwitchPart(t *testing.T) {
	n := newBkNode(t, "1.0", nil)
	n.dvSource()
	info, err := n.o.BackupInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range info.Parts {
		if p.ID == "dvswitch" {
			want := int64(len(dvBkAB) + len(dvBkMac) + len(dvBkMM) + len(dvBkYSF))
			if !p.Present || p.Size != want {
				t.Errorf("%+v want size %d", p, want)
			}
			return
		}
	}
	t.Error("no dvswitch part")
}

func TestDVBackupFilesMatchAllowlist(t *testing.T) {
	want := map[string]bool{}
	for _, f := range dvFiles {
		want[f.rel] = false
	}
	for _, rel := range backup.DVSwitchFiles {
		if _, ok := want[rel]; ok {
			want[rel] = true
		}
	}
	for rel, ok := range want {
		if !ok {
			t.Errorf("%s is allowlisted for setup but not in the backup part", rel)
		}
	}
	if len(backup.DVSwitchFiles) != len(dvFiles)+1 {
		t.Error("the backup part is the allowlisted files plus dvsm.macro")
	}
}
