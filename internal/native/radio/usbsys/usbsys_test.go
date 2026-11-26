package usbsys

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// fakeTree builds a sysfs lookalike. Each device is a list of relative
// directories plus idVendor/idProduct files.
func fakeTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	dev := filepath.Join(root, "bus", "usb", "devices")
	mk := func(p string) {
		if err := os.MkdirAll(filepath.Join(dev, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	wr := func(p, s string) {
		if err := os.WriteFile(filepath.Join(dev, p), []byte(s+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// CM108B with hidraw and sound.
	mk("1-1.2/1-1.2:1.0/sound/card1")
	mk("1-1.2/1-1.2:1.3/0003:0D8C:0012.0004/hidraw/hidraw0")
	wr("1-1.2/idVendor", "0d8c")
	wr("1-1.2/idProduct", "0012")
	wr("1-1.2/product", "USB Audio Device")
	// CH340 serial.
	mk("1-1.3/1-1.3:1.0/ttyUSB0")
	wr("1-1.3/idVendor", "1a86")
	wr("1-1.3/idProduct", "7523")
	// Unrelated device and a root hub.
	mk("1-1.4/1-1.4:1.0")
	wr("1-1.4/idVendor", "046d")
	wr("1-1.4/idProduct", "c52b")
	mk("usb1")
	wr("usb1/idVendor", "1d6b")
	wr("usb1/idProduct", "0002")
	// A symlink loop like sysfs has; it must not be followed.
	if err := os.Symlink("../../..", filepath.Join(dev, "1-1.2/1-1.2:1.0/subsystem")); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestFindAndMap(t *testing.T) {
	root := fakeTree(t)
	tests := []struct {
		name    string
		ids     []ID
		bus     string
		wantN   int
		hidraw  []string
		cards   []int
		ttys    []string
		product string
	}{
		{"cm108b", []ID{{0x0d8c, 0x000c}, {0x0d8c, 0x0012}}, "", 1, []string{"hidraw0"}, []int{1}, nil, "USB Audio Device"},
		{"ch340", []ID{{0x1a86, 0x7523}}, "", 1, nil, nil, []string{"ttyUSB0"}, ""},
		{"bus path match", []ID{{0x0d8c, 0x0012}}, "1-1.2", 1, []string{"hidraw0"}, []int{1}, nil, "USB Audio Device"},
		{"bus path mismatch", []ID{{0x0d8c, 0x0012}}, "1-9", 0, nil, nil, nil, ""},
		{"absent", []ID{{0x1234, 0x5678}}, "", 0, nil, nil, nil, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			devs, err := Find(root, tc.ids, tc.bus)
			if err != nil {
				t.Fatal(err)
			}
			if len(devs) != tc.wantN {
				t.Fatalf("got %d devices, want %d", len(devs), tc.wantN)
			}
			if tc.wantN == 0 {
				return
			}
			d := devs[0]
			if !reflect.DeepEqual(d.Hidraw(), tc.hidraw) {
				t.Errorf("hidraw = %v, want %v", d.Hidraw(), tc.hidraw)
			}
			if !reflect.DeepEqual(d.Cards(), tc.cards) {
				t.Errorf("cards = %v, want %v", d.Cards(), tc.cards)
			}
			if !reflect.DeepEqual(d.TTYs(), tc.ttys) {
				t.Errorf("ttys = %v, want %v", d.TTYs(), tc.ttys)
			}
			if d.Product != tc.product {
				t.Errorf("product = %q", d.Product)
			}
		})
	}
}

func TestListMissingRoot(t *testing.T) {
	if _, err := List(t.TempDir()); err == nil {
		t.Fatal("expected error for a root without bus/usb")
	}
}

func TestIDString(t *testing.T) {
	if got := (ID{0x0d8c, 0x12}).String(); got != "0d8c:0012" {
		t.Fatalf("got %q", got)
	}
}
