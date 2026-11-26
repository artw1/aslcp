package radio

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// usbDev adds a fake USB device to a sysfs tree. kids lists directories made
// below it, such as the hidraw and sound nodes.
func usbDev(t *testing.T, root, bus, vid, pid string, kids ...string) {
	t.Helper()
	dev := filepath.Join(root, "bus", "usb", "devices", bus)
	if err := os.MkdirAll(dev, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dev, "idVendor"), []byte(vid+"\n"), 0o644)
	os.WriteFile(filepath.Join(dev, "idProduct"), []byte(pid+"\n"), 0o644)
	os.WriteFile(filepath.Join(dev, "product"), []byte("USB Audio Device\n"), 0o644)
	for _, k := range kids {
		os.MkdirAll(filepath.Join(dev, k), 0o755)
	}
}

func chipDev(t *testing.T, root, pid string) {
	usbDev(t, root, "1-1.2", "0d8c", pid, "1-1.2:1.0/sound/card2", "1-1.2:1.3/0003:0D8C:0012.0004/hidraw/hidraw3")
}

func TestDetectChips(t *testing.T) {
	tests := []struct {
		pid  string
		chip string
	}{
		{"000c", "CM108"}, {"000d", "CM108"}, {"000e", "CM109"}, {"000f", "CM109"},
		{"0008", "CM119"}, {"0012", "CM108B"}, {"0013", "CM119B"}, {"013a", "CM119A"},
		{"013c", "CM108AH"}, {"6a00", "CM108-compatible"}, {"6a07", "CM108-compatible"}, {"6aff", "CM108-compatible"},
	}
	for _, tc := range tests {
		root := t.TempDir()
		chipDev(t, root, tc.pid)
		info, err := Detect(Config{SysRoot: root, DevRoot: "/dev"})
		if err != nil {
			t.Fatalf("%s: %v", tc.pid, err)
		}
		if !info.Detected || info.Chip != tc.chip || info.USBID != "0d8c:"+tc.pid || info.BusPath != "1-1.2" {
			t.Errorf("%s: info %+v", tc.pid, info)
		}
		if info.Product != "USB Audio Device" {
			t.Errorf("%s: product %q", tc.pid, info.Product)
		}
		// Only the CM108B is a SHARI without the serial bridge.
		if info.SHARI != (tc.chip == "CM108B") {
			t.Errorf("%s: SHARI = %v", tc.pid, info.SHARI)
		}
	}
}

func TestDetectUnsupported(t *testing.T) {
	root := t.TempDir()
	usbDev(t, root, "1-1.2", "0d8c", "7000", "1-1.2:1.0/sound/card2", "1-1.2:1.3/hidraw/hidraw3")
	usbDev(t, root, "1-1.3", "046d", "0825", "1-1.3:1.0/sound/card3")
	if _, err := Detect(Config{SysRoot: root}); !errors.Is(err, ErrNoRadio) {
		t.Fatalf("err = %v, want ErrNoRadio", err)
	}
}

func TestDetectSHARIBySerialBridge(t *testing.T) {
	root := t.TempDir()
	chipDev(t, root, "000c")
	usbDev(t, root, "1-1.4", "1a86", "7523", "1-1.4:1.0/ttyUSB0")
	info, err := Detect(Config{SysRoot: root})
	if err != nil || !info.SHARI || info.Chip != "CM108" {
		t.Fatalf("info %+v, %v", info, err)
	}
}

// With no ALSA device to open, Open stops after detection and the resolved
// interface; this checks the preset choice per hardware without a sound card.
func TestOpenChoosesPreset(t *testing.T) {
	root := t.TempDir()
	chipDev(t, root, "013a") // a CM119A, no serial bridge: not a SHARI
	_, info, _ := Open(Config{SysRoot: root, DevRoot: root})
	if info.Interface.Preset != PresetNone || !info.Detected || info.Chip != "CM119A" || info.Present {
		t.Errorf("auto with a plain interface: %+v", info)
	}
	r, info, err := Open(Config{SysRoot: root, DevRoot: root, Preset: PresetNone})
	if err != nil || info.Present || info.Chip != "CM119A" {
		t.Fatalf("none preset: %+v, %v", info, err)
	}
	r.Close()
	_, info, _ = Open(Config{SysRoot: root, DevRoot: root, Preset: PresetURI, Overrides: Overrides{PTTPin: 4}})
	if info.Interface.Preset != PresetURI || info.Interface.PTTPin != 4 || info.Interface.SA818 {
		t.Errorf("uri: %+v", info.Interface)
	}
	root2 := t.TempDir()
	chipDev(t, root2, "0012")
	_, info, _ = Open(Config{SysRoot: root2, DevRoot: root2})
	if info.Interface.Preset != PresetSHARI || !info.Interface.SA818 || info.Chip != "CM108B" {
		t.Errorf("auto with a CM108B: %+v", info)
	}
	if _, _, err := Open(Config{SysRoot: root2, DevRoot: root2, Preset: PresetURI, Overrides: Overrides{PTTPin: 2}}); err == nil {
		t.Error("pin conflict accepted")
	}
}
