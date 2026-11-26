package cm108

import (
	"fmt"
	"sync"
	"time"
)

// HID is the hidraw side of the chip. The real implementation is in
// hid_linux.go; tests use a fake.
type HID interface {
	// WriteReport sends a 5-byte output report.
	WriteReport(report []byte) error
	// Inputs returns the latest input byte. It never blocks.
	Inputs() (byte, error)
	Close() error
}

// Clock lets tests drive the COS poll loop.
type Clock interface {
	Now() time.Time
	// NewTicker returns a channel that ticks every d and a stop function.
	NewTicker(d time.Duration) (<-chan time.Time, func())
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }
func (realClock) NewTicker(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTicker(d)
	return t.C, t.Stop
}

// Config selects and tunes the interface. The zero value gives the SHARI
// defaults.
type Config struct {
	// SysRoot and DevRoot default to /sys and /dev.
	SysRoot, DevRoot string
	// BusPath picks one device when several are plugged in.
	BusPath string
	// PTTGPIO is the GPIO pin (1 to 4) that keys the transmitter. Default 3.
	PTTGPIO int
	// PTTActiveLow drives the pin low to transmit (chan_simpleusb invertptt).
	PTTActiveLow bool
	// OutputMask is the GPIO direction byte. The PTT pin is always added.
	// Default [DefaultOutputMask] without the pins read as COS or CTCSS.
	OutputMask byte
	// COS is where the carrier comes from. The default (empty) is GPIO2,
	// the volume-down input, which is what the SHARI uses.
	COS Source
	// COSActiveLow means the carrier is present when the input is low
	// (chan_simpleusb carrierfrom=usb). The default, false, is usbinvert:
	// carrier present when the input is high, which is what the SHARI needs.
	COSActiveLow bool
	// CTCSS, when a pin, is a second input that must be present together
	// with COS (chan_simpleusb ctcssfrom). Default none.
	CTCSS          Source
	CTCSSActiveLow bool
	// Debounce is how long a COS change must hold. Default 30 ms.
	Debounce time.Duration
	// PollInterval is how often the input is sampled. Default 10 ms.
	PollInterval time.Duration
	// Clock defaults to the system clock.
	Clock Clock
}

// squelch returns how the carrier is read.
func (c Config) squelch() Squelch {
	return Squelch{COS: c.COS, COSActiveLow: c.COSActiveLow, CTCSS: c.CTCSS, CTCSSActiveLow: c.CTCSSActiveLow}
}

func (c Config) withDefaults() (Config, byte, error) {
	if c.PTTGPIO == 0 {
		c.PTTGPIO = 3
	}
	bit, err := GPIOBit(c.PTTGPIO)
	if err != nil {
		return c, 0, err
	}
	if c.COS == "" {
		c.COS = SourceGPIO2
	}
	if c.CTCSS == "" {
		c.CTCSS = SourceNone
	}
	if !c.COS.Valid() {
		return c, 0, fmt.Errorf("cm108: COS source %q is not one of none, always, gpio1 to gpio4", c.COS)
	}
	if !c.CTCSS.Valid() || c.CTCSS == SourceAlways {
		return c, 0, fmt.Errorf("cm108: CTCSS source %q is not one of none, gpio1 to gpio4", c.CTCSS)
	}
	for _, in := range []Source{c.COS, c.CTCSS} {
		if in.Pin() == c.PTTGPIO {
			return c, 0, fmt.Errorf("cm108: GPIO%d cannot be both the PTT output and an input", c.PTTGPIO)
		}
	}
	if c.COS.Pin() != 0 && c.COS == c.CTCSS {
		return c, 0, fmt.Errorf("cm108: COS and CTCSS cannot both use GPIO%d", c.COS.Pin())
	}
	if c.OutputMask == 0 {
		c.OutputMask = DefaultOutputMask
		for _, in := range []Source{c.COS, c.CTCSS} {
			if b, err := GPIOBit(in.Pin()); err == nil {
				c.OutputMask &^= b
			}
		}
	}
	c.OutputMask |= bit
	if c.Debounce == 0 {
		c.Debounce = 30 * time.Millisecond
	}
	if c.PollInterval == 0 {
		c.PollInterval = 10 * time.Millisecond
	}
	if c.Clock == nil {
		c.Clock = realClock{}
	}
	return c, bit, nil
}

// Device is an open interface: PTT out, COS in.
type Device struct {
	Info Info
	cfg  Config
	pttB byte
	hid  HID

	mu   sync.Mutex
	ptt  bool
	cos  *Debouncer
	subs chan bool

	stop chan struct{}
	done chan struct{}
	once sync.Once
}

// NewDevice wraps an open HID handle. Open does this for real hardware.
func NewDevice(info Info, hid HID, cfg Config) (*Device, error) {
	cfg, bit, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}
	d := &Device{
		Info: info, cfg: cfg, pttB: bit, hid: hid,
		subs: make(chan bool, 16),
		stop: make(chan struct{}), done: make(chan struct{}),
		cos: NewDebouncer(cfg.Debounce, false),
	}
	// Program the pin directions and make sure we are not keyed.
	if err := d.SetPTT(false); err != nil {
		return nil, fmt.Errorf("cm108: initialize outputs: %w", err)
	}
	if in, err := hid.Inputs(); err == nil {
		d.cos = NewDebouncer(cfg.Debounce, cfg.squelch().Carrier(in))
	} else if cfg.COS == SourceAlways {
		d.cos = NewDebouncer(cfg.Debounce, true)
	}
	go d.run()
	return d, nil
}

// SetPTT keys or unkeys the transmitter.
func (d *Device) SetPTT(on bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	var data byte
	if on != d.cfg.PTTActiveLow {
		data = d.pttB
	}
	rep := OutputReport(data, d.cfg.OutputMask)
	if err := d.hid.WriteReport(rep[:]); err != nil {
		return fmt.Errorf("cm108: set PTT: %w", err)
	}
	d.ptt = on
	return nil
}

// PTT reports the last state written.
func (d *Device) PTT() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.ptt
}

// COS returns the debounced carrier state.
func (d *Device) COS() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cos.State()
}

// Changes delivers each debounced COS change. A slow reader loses the oldest
// pending changes; use COS for the current value.
func (d *Device) Changes() <-chan bool { return d.subs }

func (d *Device) run() {
	defer close(d.done)
	tick, stop := d.cfg.Clock.NewTicker(d.cfg.PollInterval)
	defer stop()
	for {
		select {
		case <-d.stop:
			return
		case <-tick:
			d.poll()
		}
	}
}

// poll takes one sample and publishes a change.
func (d *Device) poll() {
	in, err := d.hid.Inputs()
	if err != nil {
		return
	}
	d.step(d.cfg.Clock.Now(), d.cfg.squelch().Carrier(in))
}

// step feeds one raw carrier sample through the debouncer.
func (d *Device) step(now time.Time, raw bool) {
	d.mu.Lock()
	state, changed := d.cos.Update(now, raw)
	d.mu.Unlock()
	if !changed {
		return
	}
	for {
		select {
		case d.subs <- state:
			return
		default:
			select { // full: drop the oldest
			case <-d.subs:
			default:
			}
		}
	}
}

// Close unkeys the transmitter and releases the device.
func (d *Device) Close() error {
	var err error
	d.once.Do(func() {
		close(d.stop)
		<-d.done
		err = d.SetPTT(false)
		if cerr := d.hid.Close(); err == nil {
			err = cerr
		}
	})
	return err
}
