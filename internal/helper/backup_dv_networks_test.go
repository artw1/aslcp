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

// Every DMR network's settings, the inactive one's password included, and
// enabled state; [DMR Network] holds DMR - Other.
const dvBkNets = `{"version":1,"stfuStay":true,"networks":{` +
	`"BM":{"enabled":true},` +
	`"TGIF":{"password":"example-pass"},` +
	`"DMR":{"enabled":true,"address":"192.0.2.10","port":62031}}}`

func dvNetsSource(t *testing.T) *bkNode {
	t.Helper()
	src := newBkNode(t, "1.0", nil)
	src.dvSource()
	src.put("var/dv-state/networks.json", dvBkNets, 0o600)
	return src
}

func checkRestoredNets(t *testing.T, o *Ops) {
	t.Helper()
	fi, err := os.Stat(o.dvNetworksPath())
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("networks.json: %v %v", fi, err)
	}
	st := o.dvNetGet()
	if !st.STFUStay || !st.Networks[DVModeBM].Enabled || st.Networks[DVModeTGIF] != (dvNetState{Password: "example-pass"}) ||
		st.Networks[DVModeDMR] != (dvNetState{Enabled: true, Address: "192.0.2.10", Port: 62031}) {
		t.Fatalf("restored %+v", st)
	}
}

func TestBackupDVNetworksRoundTripInstalled(t *testing.T) {
	src := dvNetsSource(t)
	arc := src.archive(BackupRequest{DVSwitch: true})
	m := dvEntries(t, arc)
	if e := m.Find(backup.PathDVSwitchNetworks); e == nil || e.Mode != "0600" || !m.Parts.DVSwitch {
		t.Fatalf("not archived: %+v", e)
	}
	dst := newBkNode(t, "1.0", nil)
	dst.put("opt/Analog_Bridge/Analog_Bridge.ini", dvBkOld, 0o644)
	dst.put("var/dv-state/networks.json", `{"version":1,"networks":{}}`, 0o600)
	dst.fake.rules["systemctl restart"] = resp{}
	res, err := restoreInto(t, dst, arc, RestoreRequest{})
	if err != nil || !res.OK {
		t.Fatalf("%+v %v", res, err)
	}
	st := dvStep(res)
	if st == nil || !st.OK || !strings.Contains(st.Detail, "DMR network settings restored") || strings.Contains(st.Detail, "example-pass") {
		t.Fatalf("step = %+v", st)
	}
	checkRestoredNets(t, dst.o)
}

func TestBackupDVNetworksRoundTripPending(t *testing.T) {
	src := dvNetsSource(t)
	arc := src.archive(BackupRequest{DVSwitch: true})
	dst := newBkNode(t, "1.0", nil) // DVSwitch not installed
	res, err := restoreInto(t, dst, arc, RestoreRequest{})
	if err != nil || !res.OK {
		t.Fatalf("%+v %v", res, err)
	}
	if st := dvStep(res); st == nil || !strings.Contains(st.Detail, "DMR network settings are saved too") {
		t.Fatalf("step = %+v", st)
	}
	pd := dst.o.dvPendingDir()
	if fi, err := os.Stat(filepath.Join(pd, dvNetworksStaged)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("staged: %v %v", fi, err)
	}
	if _, err := os.Stat(dst.o.dvNetworksPath()); err == nil {
		t.Fatal("written before DVSwitch is installed")
	}
	// the install: defaults first (every network off, nothing stored), then
	// the staged settings win
	dst.put("opt/Analog_Bridge/Analog_Bridge.ini", dvBkOld, 0o644)
	dst.put("opt/MMDVM_Bridge/MMDVM_Bridge.ini", dvBkOldM, 0o600)
	dst.fake.rules["systemctl restart"] = resp{}
	var w bytes.Buffer
	ctx := context.Background()
	if err := dst.o.RunDVSwitchStep(ctx, StepInstallDefaults, &w); err != nil {
		t.Fatal(err)
	}
	if err := dst.o.RunDVSwitchStep(ctx, StepRestoreApply, &w); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.String(), "DMR network settings restored") || strings.Contains(w.String(), "example-pass") {
		t.Errorf("log = %q", w.String())
	}
	checkRestoredNets(t, dst.o)
}

func TestBackupDVNetworksInvalidAndOlder(t *testing.T) {
	// an invalid file is not archived
	src := newBkNode(t, "1.0", nil)
	src.dvSource()
	src.put("var/dv-state/networks.json", `{"version":1,"networks":{"TGIF":{"address":"192.0.2.10"}}}`, 0o600)
	if m := dvEntries(t, src.archive(BackupRequest{DVSwitch: true})); m.Find(backup.PathDVSwitchNetworks) != nil {
		t.Fatal("invalid settings archived")
	}
	// an older backup (none) leaves the node's settings alone
	dst := newBkNode(t, "1.0", nil)
	dst.put("opt/Analog_Bridge/Analog_Bridge.ini", dvBkOld, 0o644)
	dst.put("var/dv-state/networks.json", dvBkNets, 0o600)
	dst.fake.rules["systemctl restart"] = resp{}
	if res, err := restoreInto(t, dst, src.archive(BackupRequest{DVSwitch: true}), RestoreRequest{}); err != nil || !res.OK {
		t.Fatalf("%+v %v", res, err)
	}
	if dst.get("var/dv-state/networks.json") != dvBkNets {
		t.Error("an older backup changed the settings")
	}
}
