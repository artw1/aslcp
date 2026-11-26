package cm108

import (
	"errors"
	"fmt"
	"time"
)

// Input report bits (byte 0 of the 4-byte HID input report).
const (
	InVolumeUp   = 0x01
	InVolumeDown = 0x02
	InMutePlay   = 0x04
	InMuteRec    = 0x08
)

// DefaultOutputMask is the GPIO direction mask chan_simpleusb programs for
// its default interface type: GPIO3 (PTT) and GPIO4 as outputs.
const DefaultOutputMask = 0x0c

// GPIOBit returns the bit of GPIO n (1 to 4) in the report bytes.
func GPIOBit(n int) (byte, error) {
	if n < 1 || n > 4 {
		return 0, fmt.Errorf("cm108: GPIO%d does not exist (1 to 4)", n)
	}
	return 1 << (n - 1), nil
}

// OutputReport builds the 5-byte hidraw write that sets GPIO outputs: report
// id 0 (stripped by the kernel because the chip uses no report ids), a
// reserved byte, the GPIO data byte, the GPIO direction mask byte and a
// reserved byte. A direction bit of 1 makes the pin an output.
func OutputReport(data, dir byte) [5]byte {
	return [5]byte{0, 0, data, dir, 0}
}

// ErrShortReport is returned for an input report with no data byte.
var ErrShortReport = errors.New("cm108: input report too short")

// ParseInput returns the input byte (volume and mute buttons plus any GPIO
// inputs wired there) from a HID input report. A 5-byte buffer is a report
// that carries the leading report id (HIDIOCGINPUT does), a 4-byte one has
// none (a hidraw read).
func ParseInput(report []byte) (byte, error) {
	switch {
	case len(report) >= 5:
		return report[1], nil
	case len(report) >= 1:
		return report[0], nil
	}
	return 0, ErrShortReport
}

// Carrier decodes the carrier-detect state from the input byte. The SHARI
// squelch line drives volume-down, and chan_simpleusb is run with
// carrierfrom=usbinvert on it, so the default (activeLow false) means the
// carrier is present when the bit is set. With activeLow true the carrier is
// present when the bit is clear (carrierfrom=usb).
func Carrier(in byte, activeLow bool) bool {
	bit := in&InVolumeDown != 0
	return bit != activeLow
}

// Debouncer filters a noisy boolean. A new value is accepted only after it
// has been seen continuously for the window.
type Debouncer struct {
	window  time.Duration
	state   bool
	pending bool
	want    bool
	since   time.Time
}

// NewDebouncer returns a debouncer that starts in state initial.
func NewDebouncer(window time.Duration, initial bool) *Debouncer {
	return &Debouncer{window: window, state: initial}
}

// State returns the accepted value.
func (d *Debouncer) State() bool { return d.state }

// Update feeds one raw sample taken at now and returns the accepted state
// and whether it just changed.
func (d *Debouncer) Update(now time.Time, raw bool) (state, changed bool) {
	if raw == d.state {
		d.pending = false
		return d.state, false
	}
	if !d.pending || d.want != raw {
		d.pending, d.want, d.since = true, raw, now
	}
	if now.Sub(d.since) >= d.window {
		d.state, d.pending = raw, false
		return d.state, true
	}
	return d.state, false
}
