package backupsvc

import (
	"errors"
	"strings"
	"testing"

	"github.com/artw1/aslcp/internal/backup"
	"github.com/artw1/aslcp/internal/helper"
	"github.com/artw1/aslcp/internal/legacy"
	"github.com/artw1/aslcp/internal/store"
)

func previewFor(m *backup.Manifest, plan helper.RestoreResult) Preview {
	s := &Service{}
	p, _ := s.preview(m, plan, store.Inspection{}, nil)
	return p
}

func hasWarning(p Preview, code string) (string, bool) {
	for _, w := range p.Warnings {
		if w.Code == code {
			return w.Message, true
		}
	}
	return "", false
}

func TestPreviewCrossReleaseWarning(t *testing.T) {
	m := &backup.Manifest{Format: 1, ASL3Version: "3.10.8-1.deb12", OSVersionCodename: "bookworm"}
	plan := helper.RestoreResult{ASL3Version: "3.10.8-1.deb13", OSCodename: "trixie"}
	p := previewFor(m, plan)
	msg, ok := hasWarning(p, "os_release")
	if !ok || !strings.Contains(msg, "Debian 12 (bookworm)") || !strings.Contains(msg, "Debian 13 (trixie)") || !strings.Contains(msg, ".dpkg-dist") {
		t.Fatalf("warning = %q, %v", msg, ok)
	}
	if p.Backup.OSCodename != "bookworm" || p.Node.OSCodename != "trixie" {
		t.Errorf("os fields: %+v %+v", p.Backup, p.Node)
	}
}

func TestPreviewCrossReleaseDerived(t *testing.T) {
	// An old backup without OS fields: derived from the .debNN suffix.
	m := &backup.Manifest{Format: 1, AsteriskVersion: "22.10.1+asl3-3.10.8-1.deb12"}
	plan := helper.RestoreResult{AsteriskVersion: "22.10.1+asl3-3.10.8-1.deb13"}
	p := previewFor(m, plan)
	if _, ok := hasWarning(p, "os_release"); !ok {
		t.Fatal("no cross-release warning for a derived codename")
	}
}

func TestPreviewSameOrUnknownRelease(t *testing.T) {
	m := &backup.Manifest{Format: 1, OSVersionCodename: "trixie"}
	if _, ok := hasWarning(previewFor(m, helper.RestoreResult{OSCodename: "trixie"}), "os_release"); ok {
		t.Error("warning for the same release")
	}
	if _, ok := hasWarning(previewFor(&backup.Manifest{Format: 1}, helper.RestoreResult{OSCodename: "trixie"}), "os_release"); ok {
		t.Error("warning for an unknown backup release")
	}
}

func TestAppRptCheck(t *testing.T) {
	ok := []string{"Module                         Description                              Use Count  Status      Support Level", "app_rpt.so                     Radio Repeater/Remote Base Program       0          Running              extended", "1 modules loaded"}
	if c := AppRptCheck(ok, nil); !c.OK {
		t.Errorf("loaded: %+v", c)
	}
	if c := AppRptCheck([]string{"0 modules loaded"}, nil); c.OK {
		t.Errorf("not loaded passed: %+v", c)
	}
	if c := AppRptCheck(nil, errors.New("timeout")); c.OK || c.Detail != "timeout" {
		t.Errorf("error: %+v", c)
	}
	if NodeAnswers([]string{"No such command 'rpt show variables 1'"}) || !NodeAnswers([]string{"Variable listing for node 1:"}) {
		t.Error("NodeAnswers")
	}
}

func TestPreviewDVPackagesNotListed(t *testing.T) {
	m := &backup.Manifest{Format: 1, Packages: []backup.Package{
		{Name: legacy.PackagePrefix + "-base", Version: "1.7.0"}, {Name: "analog-bridge", Version: "1"}, {Name: "stfu", Version: "1"},
		{Name: "supermon", Version: "1"}, {Name: "m17-gateway", Version: "1"}}}
	plan := helper.RestoreResult{DVNotInstalled: true}
	p := previewFor(m, plan)
	if got := strings.Join(p.MissingPackages, " "); got != "m17-gateway supermon" {
		t.Errorf("missing = %q", got)
	}
	if strings.Contains(p.InstallCommand, legacy.PackagePrefix) || strings.Contains(p.InstallCommand, "stfu") {
		t.Errorf("command = %q", p.InstallCommand)
	}
	msg, ok := hasWarning(p, "dv_not_installed")
	if !ok || !strings.Contains(msg, "applied automatically when you enable digital modes") {
		t.Errorf("warning = %q %v", msg, ok)
	}
	for _, c := range p.Checklist {
		if c.Item == "Other software" && strings.Contains(c.Value, legacy.PackagePrefix) {
			t.Errorf("checklist lists the old stack: %+v", c)
		}
	}
	if _, ok := hasWarning(previewFor(m, helper.RestoreResult{}), "dv_not_installed"); ok {
		t.Error("warning although digital modes are installed")
	}
}
