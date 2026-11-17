package demo

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// demoRadio is the simulated SimpleUSB interface ("usb") that the tuning
// page works against in demo mode. Its replies follow chan_simpleusb.
type demoRadio struct {
	tx, txb, rx                      int
	echo, boost, pre, de, pl, invert bool
	carrier, ctcss                   int
	rxon, txoff                      int // frames
}

func newDemoRadio() *demoRadio {
	return &demoRadio{tx: 500, rx: 650, pre: true, de: true, pl: true, carrier: 4, rxon: 0}
}

var (
	carrierNames = []string{"no", "N/A", "N/A", "usb", "usbinvert", "pp", "ppinvert"}
	ctcssNames   = []string{"no", "usb", "usbinvert", "N/A", "pp", "ppinvert"}
)

func b2s(b bool) string {
	if b {
		return "Enabled"
	}
	return "Disabled"
}

func b2d(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

// SaveConf is the content "susb tune save" writes for the device.
func (r *demoRadio) saveConf(dev string) string {
	yn := func(b bool) string {
		if b {
			return "yes"
		}
		return "no"
	}
	return fmt.Sprintf("[%s]\nrxmixerset = %d\ntxmixaset = %d\ntxmixbset = %d\nrxboost = %s\npreemphasis = %s\ndeemphasis = %s\nplfilter = %s\ninvertptt = %s\ncarrierfrom = %s\nctcssfrom = %s\nrxondelay = %d\ntxoffdelay = %d\n",
		dev, r.rx, r.tx, r.txb, yn(r.boost), yn(r.pre), yn(r.de), yn(r.pl), yn(r.invert),
		carrierNames[r.carrier], ctcssNames[r.ctcss], r.rxon, r.txoff)
}

const demoDevice = "usb"

// susbCommand answers the chan_simpleusb CLI commands the tuning service
// uses; ok is false for any other command.
func (s *Sim) susbCommand(cmd string) ([]string, bool) {
	if !strings.HasPrefix(cmd, "susb ") {
		return nil, false
	}
	s.mu.Lock()
	if s.radio == nil {
		s.radio = newDemoRadio()
	}
	r := s.radio
	s.mu.Unlock()
	switch {
	case cmd == "susb active":
		return []string{"Active Simple USB Radio device is [" + demoDevice + "]."}, true
	case cmd == "susb active show":
		return []string{"Device [" + demoDevice + "] is assigned to device=1-1.1 card=1"}, true
	case cmd == "susb active "+demoDevice:
		return []string{"Active (command) Simple USB Radio device set to [" + demoDevice + "]"}, true
	case strings.HasPrefix(cmd, "susb active "):
		return []string{"No device [" + strings.TrimPrefix(cmd, "susb active ") + "] exists"}, true
	case cmd == "susb tune save":
		s.mu.Lock()
		conf := r.saveConf(demoDevice)
		hook := s.OnTuneSave
		s.mu.Unlock()
		if hook != nil {
			hook(conf)
		}
		return []string{"Saved radio tuning settings to simpleusb.conf"}, true
	}
	arg, found := strings.CutPrefix(cmd, "susb tune menu-support ")
	if !found {
		return []string{"Unknown command"}, true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch arg {
	case "1":
		return []string{demoDevice}, true
	case "2":
		return []string{"Active radio interface is [" + demoDevice + "]", "Device String is 1-1.1", "Card is 1"}, true
	case "0+4":
		return []string{fmt.Sprintf("%d,%d,%s,%s,%s,%s,%s,%s,%d,%d,%d,%d,%d,31,37,35", r.tx, r.txb, b2d(r.echo), b2d(r.boost),
			b2d(r.pre), b2d(r.de), b2d(r.pl), b2d(r.invert), r.carrier, r.ctcss, r.rxon, r.txoff, r.rx)}, true
	case "Y":
		t := float64(time.Since(s.start)) / float64(time.Second)
		pk := -38 + 14*math.Sin(t/2.3) + 3*math.Sin(t*1.7)
		avg := pk - 17
		return []string{fmt.Sprintf("RxAudioStats: Pk %5.1f  Avg Pwr %3.0f  Min %3.0f  Max %3.0f  dBFS  ClipCnt %d",
			pk, avg, avg-6, pk-3, 0)}, true
	}
	if len(arg) < 2 {
		return []string{"Unknown command"}, true
	}
	n, err := strconv.Atoi(arg[1:])
	if err != nil {
		return []string{"Unknown command"}, true
	}
	switch arg[0] {
	case 'c':
		r.rx = n
		return []string{fmt.Sprintf("Channel %s: Changed setting on RX Channel to %d", demoDevice, n)}, true
	case 'f':
		r.tx = n
		return []string{fmt.Sprintf("Channel %s: Changed setting on TX Channel A to %d", demoDevice, n)}, true
	case 'g':
		r.txb = n
		return []string{fmt.Sprintf("Channel %s: Changed setting on TX Channel B to %d", demoDevice, n)}, true
	case 'k':
		r.echo = n > 0
		return []string{"Echo Mode changed to " + b2s(r.echo)}, true
	case 'm':
		r.boost = n > 0
		return []string{"RxBoost changed to " + b2s(r.boost)}, true
	case 'n':
		r.pre = n > 0
		return []string{"Pre-emphasis changed to " + b2s(r.pre)}, true
	case 'o':
		r.de = n > 0
		return []string{"De-emphasis changed to " + b2s(r.de)}, true
	case 'p':
		r.pl = n > 0
		return []string{"PL Filter changed to " + b2s(r.pl)}, true
	case 'q':
		r.invert = n > 0
		m := "Ground"
		if r.invert {
			m = "Open"
		}
		return []string{"PTT mode changed to " + m}, true
	case 'r':
		if n < len(carrierNames) {
			r.carrier = n
			return []string{"Carrier From changed to " + carrierNames[n]}, true
		}
	case 's':
		if n < len(ctcssNames) {
			r.ctcss = n
			return []string{"CTCSS From changed to " + ctcssNames[n]}, true
		}
	case 't':
		r.rxon = n
		return []string{fmt.Sprintf("RX On Delay From changed to %d", n)}, true
	case 'u':
		r.txoff = n
		return []string{fmt.Sprintf("TX Off Delay From changed to %d", n)}, true
	}
	return []string{"Unknown command"}, true
}
