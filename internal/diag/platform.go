package diag

import (
	"os"
	"runtime"
	"strings"
)

// Hardware choices of the bug report form's "hardware" dropdown (the labels
// must match .github/ISSUE_TEMPLATE/bug_report.yml).
const (
	HWPi5     = "Raspberry Pi 5"
	HWPi4     = "Raspberry Pi 4"
	HWPi3     = "Raspberry Pi 3"
	HWPiZero2 = "Raspberry Pi Zero 2 W"
	HWARM     = "Other ARM board"
	HWPC      = "PC (amd64)"
	HWOther   = "Other"
)

// Platform describes the host.
type Platform struct {
	GOOS     string `json:"os"`
	GOARCH   string `json:"arch"`
	Model    string `json:"model"`    // /proc/device-tree/model, "" when absent
	Distro   string `json:"distro"`   // /etc/os-release PRETTY_NAME
	Kernel   string `json:"kernel"`   // /proc/sys/kernel/osrelease
	Hardware string `json:"hardware"` // one of the HW* choices
}

// PlatformPaths are the files read by ReadPlatform (injectable for tests).
type PlatformPaths struct {
	DeviceModel, OSRelease, Kernel string
}

// DefaultPlatformPaths are the real locations.
var DefaultPlatformPaths = PlatformPaths{
	DeviceModel: "/proc/device-tree/model",
	OSRelease:   "/etc/os-release",
	Kernel:      "/proc/sys/kernel/osrelease",
}

// ReadPlatform collects the platform information; missing files are skipped.
func ReadPlatform(p PlatformPaths) Platform {
	pl := Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	if b, err := os.ReadFile(p.DeviceModel); err == nil {
		pl.Model = strings.TrimSpace(strings.Trim(string(b), "\x00"))
	}
	if b, err := os.ReadFile(p.OSRelease); err == nil {
		pl.Distro = ParseOSRelease(string(b))
	}
	if b, err := os.ReadFile(p.Kernel); err == nil {
		pl.Kernel = strings.TrimSpace(string(b))
	}
	pl.Hardware = HardwareChoice(pl.Model, pl.GOARCH)
	return pl
}

// ParseOSRelease returns PRETTY_NAME (unquoted) from os-release content.
func ParseOSRelease(s string) string {
	for _, l := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(l), "=")
		if ok && k == "PRETTY_NAME" {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}

// HardwareChoice maps the device-tree model and GOARCH to a dropdown label.
func HardwareChoice(model, goarch string) string {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "raspberry pi 5"):
		return HWPi5
	case strings.Contains(m, "raspberry pi 4"), strings.Contains(m, "raspberry pi compute module 4"):
		return HWPi4
	case strings.Contains(m, "raspberry pi zero 2"):
		return HWPiZero2
	case strings.Contains(m, "raspberry pi 3"):
		return HWPi3
	}
	switch goarch {
	case "amd64":
		return HWPC
	case "arm", "arm64":
		return HWARM
	}
	return HWOther
}
