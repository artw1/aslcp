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

// Source says where a squelch signal comes from.
type Source string

// The sources. A pin source reads that GPIO's bit of the HID input byte:
// GPIO1 is the chip's volume-up input (bit 0), GPIO2 volume-down, GPIO3
// mute-playback and GPIO4 mute-record.
const (
	SourceNone   Source = "none"   // no signal: never present
	SourceAlways Source = "always" // always present (squelch held open)
	SourceGPIO1  Source = "gpio1"
	SourceGPIO2  Source = "gpio2"
	SourceGPIO3  Source = "gpio3"
	SourceGPIO4  Source = "gpio4"
)

// Pin returns the GPIO number (1 to 4) of a pin source, or 0.
func (s Source) Pin() int {
	switch s {
	case SourceGPIO1:
		return 1
	case SourceGPIO2:
		return 2
	case SourceGPIO3:
		return 3
	case SourceGPIO4:
		return 4
	}
	return 0
}

// PinSource returns the source for GPIO n (1 to 4), or SourceNone.
func PinSource(n int) Source {
	switch n {
	case 1:
		return SourceGPIO1
	case 2:
		return SourceGPIO2
	case 3:
		return SourceGPIO3
	case 4:
		return SourceGPIO4
	}
	return SourceNone
}

// Valid reports whether s is a known source ("" is not).
func (s Source) Valid() bool {
	return s == SourceNone || s == SourceAlways || s.Pin() != 0
}

// Sense reads one signal from the input byte. A pin source is present when
// its bit is set, or when it is clear if activeLow. None is never present and
// always is always present. The empty source counts as none.
func Sense(in byte, src Source, activeLow bool) bool {
	switch src {
	case SourceAlways:
		return true
	case "", SourceNone:
		return false
	}
	bit, err := GPIOBit(src.Pin())
	if err != nil {
		return false
	}
	return (in&bit != 0) != activeLow
}

// Squelch says how the carrier is read: COS from its source and, when a
// CTCSS source is set, only while the CTCSS input is present as well (what
// simpleusb does with ctcssfrom).
type Squelch struct {
	COS            Source
	COSActiveLow   bool
	CTCSS          Source
	CTCSSActiveLow bool
}

// Carrier decodes the carrier state from the input byte.
func (q Squelch) Carrier(in byte) bool {
	if !Sense(in, q.COS, q.COSActiveLow) {
		return false
	}
	if q.CTCSS == "" || q.CTCSS == SourceNone {
		return true
	}
	return Sense(in, q.CTCSS, q.CTCSSActiveLow)
}

// Carrier decodes the carrier-detect state of the SHARI wiring from the input
// byte. The SHARI squelch line drives volume-down, and chan_simpleusb is run
// with carrierfrom=usbinvert on it, so the default (activeLow false) means the
// carrier is present when the bit is set. With activeLow true the carrier is
// present when the bit is clear (carrierfrom=usb).
func Carrier(in byte, activeLow bool) bool {
	return Sense(in, SourceGPIO2, activeLow)
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
