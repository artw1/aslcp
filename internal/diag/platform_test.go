package diag

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHardwareChoice(t *testing.T) {
	cases := []struct{ model, arch, want string }{
		{"Raspberry Pi 5 Model B Rev 1.0", "arm64", HWPi5},
		{"Raspberry Pi 4 Model B Rev 1.5", "arm64", HWPi4},
		{"Raspberry Pi 3 Model B Plus Rev 1.3", "arm", HWPi3},
		{"Raspberry Pi Zero 2 W Rev 1.0", "arm64", HWPiZero2},
		{"Orange Pi 5", "arm64", HWARM},
		{"", "amd64", HWPC},
		{"", "riscv64", HWOther},
	}
	for _, c := range cases {
		if got := HardwareChoice(c.model, c.arch); got != c.want {
			t.Errorf("%q/%s: %q want %q", c.model, c.arch, got, c.want)
		}
	}
}

func TestReadPlatform(t *testing.T) {
	d := t.TempDir()
	w := func(n, s string) string { p := filepath.Join(d, n); _ = os.WriteFile(p, []byte(s), 0o644); return p }
	pl := ReadPlatform(PlatformPaths{
		DeviceModel: w("model", "Raspberry Pi 4 Model B Rev 1.5\x00"),
		OSRelease:   w("os", "NAME=\"Debian\"\nPRETTY_NAME=\"Debian GNU/Linux 12 (bookworm)\"\n"),
		Kernel:      w("k", "6.1.0-rpi8\n"),
	})
	if pl.Model != "Raspberry Pi 4 Model B Rev 1.5" || pl.Distro != "Debian GNU/Linux 12 (bookworm)" || pl.Kernel != "6.1.0-rpi8" || pl.Hardware != HWPi4 {
		t.Errorf("%+v", pl)
	}
	if pl := ReadPlatform(PlatformPaths{DeviceModel: "/nonexistent"}); pl.Model != "" || pl.Hardware == "" {
		t.Errorf("%+v", pl)
	}
}
