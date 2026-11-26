package cm108

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/artw1/aslcp/internal/native/radio/usbsys"
)

// SupportedIDs lists the USB ids of the sound chips chan_simpleusb accepts,
// all C-Media: its table matches 0x000c-0x000f and 0x6a00-0x6aff by mask; the
// 0x6a00 entry here covers the first of that range.
var SupportedIDs = []usbsys.ID{
	{Vendor: 0x0d8c, Product: 0x000c}, // CM108 and the 0x000d to 0x000f variants
	{Vendor: 0x0d8c, Product: 0x000d},
	{Vendor: 0x0d8c, Product: 0x000e}, // CM109
	{Vendor: 0x0d8c, Product: 0x000f},
	{Vendor: 0x0d8c, Product: 0x0008}, // CM119
	{Vendor: 0x0d8c, Product: 0x0012}, // CM108B (SHARI)
	{Vendor: 0x0d8c, Product: 0x0013}, // CM119B
	{Vendor: 0x0d8c, Product: 0x013a}, // CM119A
	{Vendor: 0x0d8c, Product: 0x013c}, // CM108AH
	{Vendor: 0x0d8c, Product: 0x6a00}, // third-party CM108 boards (0x6a00 family, mask 0xff00)
}

// ErrNotFound is returned when no supported sound chip is present.
var ErrNotFound = errors.New("cm108: no supported USB sound interface found")

// Info describes a detected interface.
type Info struct {
	// BusPath is the USB bus path, such as "1-1.2".
	BusPath string
	ID      usbsys.ID
	Product string
	// Hidraw is the device node, such as /dev/hidraw0.
	Hidraw string
	// Card is the ALSA card number; capture and playback are device 0.
	Card int
}

// Discover finds the first supported interface under sysRoot (default /sys)
// that has both a hidraw node and an ALSA card. devRoot (default /dev) is the
// directory the node paths are built from. busPath selects one device when
// several are plugged in; empty takes the first by bus path.
func Discover(sysRoot, devRoot, busPath string) (Info, error) {
	if sysRoot == "" {
		sysRoot = usbsys.DefaultRoot
	}
	if devRoot == "" {
		devRoot = "/dev"
	}
	devs, err := usbsys.Find(sysRoot, SupportedIDs, busPath)
	if err != nil {
		return Info{}, err
	}
	var lastErr error = ErrNotFound
	for _, d := range devs {
		hid, cards := d.Hidraw(), d.Cards()
		if len(hid) == 0 {
			lastErr = fmt.Errorf("cm108: %s (%s) has no hidraw node: %w", d.Name, d.ID, ErrNotFound)
			continue
		}
		if len(cards) == 0 {
			lastErr = fmt.Errorf("cm108: %s (%s) has no ALSA card: %w", d.Name, d.ID, ErrNotFound)
			continue
		}
		return Info{
			BusPath: d.Name,
			ID:      d.ID,
			Product: d.Product,
			Hidraw:  filepath.Join(devRoot, hid[0]),
			Card:    cards[0],
		}, nil
	}
	return Info{}, lastErr
}
