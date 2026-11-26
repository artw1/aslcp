// Package usbsys walks the Linux sysfs USB tree to find a device by USB id
// and map it to the kernel nodes it exposes: hidraw nodes, ALSA cards and
// serial ports. The sysfs root is a parameter so tests can use a fake tree.
package usbsys

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// DefaultRoot is the real sysfs mount point.
const DefaultRoot = "/sys"

// maxDepth bounds the descent below a USB device directory. Real trees nest
// interface, HID device and hidraw directories; four levels is enough.
const maxDepth = 5

// ID is a USB vendor and product id pair.
type ID struct {
	Vendor, Product uint16
}

// String formats the id as vvvv:pppp.
func (i ID) String() string { return fmt.Sprintf("%04x:%04x", i.Vendor, i.Product) }

// Device is one USB device found in sysfs.
type Device struct {
	// Path is the sysfs directory of the device.
	Path string
	// Name is the bus path such as "1-1.2".
	Name string
	ID   ID
	// Product and Serial are the descriptor strings, empty if absent.
	Product string
	Serial  string
}

// List returns every non-interface USB device below root (for example
// "/sys"), sorted by bus path.
func List(root string) ([]Device, error) {
	base := filepath.Join(root, "bus", "usb", "devices")
	ents, err := os.ReadDir(base)
	if err != nil {
		return nil, fmt.Errorf("usbsys: read %s: %w", base, err)
	}
	var out []Device
	for _, e := range ents {
		name := e.Name()
		if strings.Contains(name, ":") || strings.HasPrefix(name, "usb") {
			continue
		}
		dir := filepath.Join(base, name)
		v, err1 := readHex(filepath.Join(dir, "idVendor"))
		p, err2 := readHex(filepath.Join(dir, "idProduct"))
		if err1 != nil || err2 != nil {
			continue
		}
		out = append(out, Device{
			Path:    dir,
			Name:    name,
			ID:      ID{Vendor: v, Product: p},
			Product: readString(filepath.Join(dir, "product")),
			Serial:  readString(filepath.Join(dir, "serial")),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Find returns the devices whose id is in ids. When busPath is not empty only
// the device with that bus path ("1-1.2") is returned.
func Find(root string, ids []ID, busPath string) ([]Device, error) {
	return FindFunc(root, func(id ID) bool {
		for _, w := range ids {
			if id == w {
				return true
			}
		}
		return false
	}, busPath)
}

// FindFunc is like [Find] but takes a matcher, for id families such as
// "every product 0x6a00 to 0x6aff".
func FindFunc(root string, match func(ID) bool, busPath string) ([]Device, error) {
	all, err := List(root)
	if err != nil {
		return nil, err
	}
	var out []Device
	for _, d := range all {
		if busPath != "" && d.Name != busPath {
			continue
		}
		if match(d.ID) {
			out = append(out, d)
		}
	}
	return out, nil
}

// Hidraw returns the hidraw node names ("hidraw0") of the device.
func (d Device) Hidraw() []string {
	return d.collect("hidraw", func(n string) bool { return strings.HasPrefix(n, "hidraw") })
}

// Cards returns the ALSA card numbers of the device.
func (d Device) Cards() []int {
	var out []int
	for _, n := range d.collect("sound", func(n string) bool {
		_, err := strconv.Atoi(strings.TrimPrefix(n, "card"))
		return strings.HasPrefix(n, "card") && err == nil
	}) {
		c, _ := strconv.Atoi(strings.TrimPrefix(n, "card"))
		out = append(out, c)
	}
	return out
}

// TTYs returns the serial port names ("ttyUSB0", "ttyACM0") of the device.
func (d Device) TTYs() []string {
	var out []string
	walk(d.Path, 0, func(dir string, ents []os.DirEntry) {
		for _, e := range ents {
			n := e.Name()
			if e.IsDir() && (strings.HasPrefix(n, "ttyUSB") || strings.HasPrefix(n, "ttyACM")) {
				out = append(out, n)
			}
		}
	})
	sort.Strings(out)
	return dedupe(out)
}

// collect lists the entries of every directory called parent below the device.
func (d Device) collect(parent string, keep func(string) bool) []string {
	var out []string
	walk(d.Path, 0, func(dir string, ents []os.DirEntry) {
		if filepath.Base(dir) != parent {
			return
		}
		for _, e := range ents {
			if e.IsDir() && keep(e.Name()) {
				out = append(out, e.Name())
			}
		}
	})
	sort.Strings(out)
	return dedupe(out)
}

// walk visits real directories only. Sysfs symlinks (subsystem, driver,
// device) point back up the tree and would loop, so they are never followed.
func walk(dir string, depth int, fn func(dir string, ents []os.DirEntry)) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	fn(dir, ents)
	if depth >= maxDepth {
		return
	}
	for _, e := range ents {
		if e.IsDir() {
			walk(filepath.Join(dir, e.Name()), depth+1, fn)
		}
	}
}

func dedupe(s []string) []string {
	var out []string
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}

func readString(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func readHex(path string) (uint16, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	v, err := strconv.ParseUint(strings.TrimSpace(string(b)), 16, 16)
	if err != nil {
		return 0, fmt.Errorf("usbsys: parse %s: %w", path, err)
	}
	return uint16(v), nil
}
