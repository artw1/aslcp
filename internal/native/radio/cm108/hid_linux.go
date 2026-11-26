package cm108

import (
	"fmt"
	"sync"
	"unsafe"

	"golang.org/x/sys/unix"
)

// hidiocGInput is HIDIOCGINPUT(len): _IOC(_IOC_READ|_IOC_WRITE, 'H', 0x0A, len),
// available since Linux 5.11.
func hidiocGInput(n uintptr) uintptr {
	return (3 << 30) | (n << 16) | ('H' << 8) | 0x0A
}

type hidraw struct {
	fd int

	mu       sync.Mutex
	last     byte
	getInput bool // HIDIOCGINPUT works
}

func openHidraw(path string, inactive byte) (*hidraw, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("cm108: open %s: %w", path, err)
	}
	h := &hidraw{fd: fd, getInput: true, last: inactive}
	return h, nil
}

func (h *hidraw) WriteReport(report []byte) error {
	_, err := unix.Write(h.fd, report)
	return err
}

// Inputs asks for the current report with HIDIOCGINPUT, which is
// authoritative. Kernels without it fall back to the reports the chip pushes
// on every change. Pushed reports are always drained so they do not queue up.
func (h *hidraw) Inputs() (byte, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var buf [64]byte
	for {
		n, err := unix.Read(h.fd, buf[:])
		if err != nil || n <= 0 {
			break
		}
		if v, perr := ParseInput(buf[:n]); perr == nil {
			h.last = v
		}
	}
	if h.getInput {
		var rep [5]byte
		_, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(h.fd), hidiocGInput(uintptr(len(rep))), uintptr(unsafe.Pointer(&rep[0])))
		if e != 0 {
			h.getInput = false
		} else if v, err := ParseInput(rep[:]); err == nil {
			h.last = v
		}
	}
	return h.last, nil
}

func (h *hidraw) Close() error { return unix.Close(h.fd) }

// Open detects the interface (see [Discover]) and opens its hidraw node.
func Open(cfg Config) (*Device, error) {
	info, err := Discover(cfg.SysRoot, cfg.DevRoot, cfg.BusPath)
	if err != nil {
		return nil, err
	}
	// Until the chip reports, assume no carrier: the COS pin at its
	// inactive level.
	var inactive byte
	if cfg.COSActiveLow {
		if b, err := GPIOBit(cfg.COS.Pin()); err == nil {
			inactive = b
		} else if cfg.COS == "" {
			inactive = InVolumeDown
		}
	}
	h, err := openHidraw(info.Hidraw, inactive)
	if err != nil {
		return nil, err
	}
	d, err := NewDevice(info, h, cfg)
	if err != nil {
		h.Close()
		return nil, err
	}
	return d, nil
}
