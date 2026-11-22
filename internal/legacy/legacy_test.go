package legacy

import (
	"strings"
	"testing"
)

func TestIsLegacyPackage(t *testing.T) {
	if !IsLegacyPackage(PackagePrefix+"-base") || !IsLegacyPackage(PackagePrefix) {
		t.Error("prefix not recognised")
	}
	if IsLegacyPackage("md380-emu") || IsLegacyPackage("asl3") {
		t.Error("unrelated package matched")
	}
}

func TestConstantsConsistent(t *testing.T) {
	if !strings.HasPrefix(OldStateDir+"-", OldStateDir) || !strings.HasPrefix(OldHistoryDir, OldStateDir+"-") {
		t.Errorf("state dirs: %q %q", OldStateDir, OldHistoryDir)
	}
	if !strings.HasPrefix(KeyURL, "https://") || !strings.HasPrefix(AptRepoURL, "http://") {
		t.Error("urls")
	}
	if !strings.HasSuffix(OldSettingsPrefix, ".") || len(Units) == 0 {
		t.Error("settings prefix / units")
	}
}
