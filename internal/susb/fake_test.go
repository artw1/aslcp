package susb

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/artw1/aslcp/internal/helper"
)

// fakeRadio is a fake chan_simpleusb behind the Commander interface. Its
// replies follow the driver's strings.
type fakeRadio struct {
	mu     sync.Mutex
	order  []string
	dev    map[string]*fakeDev
	active string
	log    []string
	// hooks
	failCmd    map[string][]string // exact command -> reply override
	noChannel  bool
	saveReply  string
	saveWrites func(dev string, d *fakeDev) // called on save
}

type fakeDev struct {
	txa, txb, rx                     int
	echo, boost, pre, de, pl, invert bool
	carrier, ctcss                   int
	rxon, txoff                      int // frames
	micplay, spkr, mic               int
	notFound                         bool
	pk                               float64
}

func newFake(names ...string) *fakeRadio {
	f := &fakeRadio{dev: map[string]*fakeDev{}, failCmd: map[string][]string{}, active: names[0]}
	for _, n := range names {
		f.order = append(f.order, n)
		f.dev[n] = &fakeDev{txa: 500, rx: 650, pre: true, de: true, pl: true, carrier: 4, micplay: 31, spkr: 37, mic: 35, pk: -76.3}
	}
	return f
}

func b2i(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func (f *fakeRadio) commands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.log...)
}

func (f *fakeRadio) Command(ctx context.Context, cmd string) ([]string, error) {
	f.mu.Lock()
	f.log = append(f.log, cmd)
	defer f.mu.Unlock()
	if r, ok := f.failCmd[cmd]; ok {
		return r, nil
	}
	return f.handle(cmd), nil
}

func (f *fakeRadio) cur() *fakeDev { return f.dev[f.active] }

func (f *fakeRadio) handle(cmd string) []string {
	switch {
	case cmd == "susb active":
		return []string{fmt.Sprintf("Active Simple USB Radio device is [%s].", f.active)}
	case cmd == "susb active show":
		var out []string
		for i, n := range f.order {
			out = append(out, fmt.Sprintf("Device [%s] is assigned to device=1-1.%d card=%d", n, i+1, i+1))
		}
		return out
	case strings.HasPrefix(cmd, "susb active "):
		n := strings.TrimPrefix(cmd, "susb active ")
		if _, ok := f.dev[n]; !ok {
			return []string{fmt.Sprintf("No device [%s] exists", n)}
		}
		f.active = n
		return []string{fmt.Sprintf("Active (command) Simple USB Radio device set to [%s]", n)}
	case cmd == "susb tune save":
		if f.saveWrites != nil {
			f.saveWrites(f.active, f.cur())
		}
		if f.saveReply != "" {
			return []string{f.saveReply}
		}
		return []string{"Saved radio tuning settings to simpleusb.conf"}
	}
	arg, ok := strings.CutPrefix(cmd, "susb tune menu-support ")
	if !ok {
		return []string{"Unknown command"}
	}
	d := f.cur()
	switch {
	case arg == "1":
		return []string{strings.Join(f.order, ",")}
	case arg == "2":
		if d.notFound {
			return []string{"Device " + f.active + " is selected, the associated USB device string 1-1 was not found"}
		}
		return []string{"Active radio interface is [" + f.active + "]", "Device String is 1-1.1", "Card is 1",
			"Rx Level currently set to " + strconv.Itoa(d.rx)}
	case arg == "0+4":
		return []string{fmt.Sprintf("%d,%d,%s,%s,%s,%s,%s,%s,%d,%d,%d,%d,%d,%d,%d,%d", d.txa, d.txb, b2i(d.echo), b2i(d.boost),
			b2i(d.pre), b2i(d.de), b2i(d.pl), b2i(d.invert), d.carrier, d.ctcss, d.rxon, d.txoff, d.rx, d.micplay, d.spkr, d.mic)}
	case arg == "Y":
		return []string{fmt.Sprintf("RxAudioStats: Pk %5.1f  Avg Pwr %3.0f  Min %3.0f  Max %3.0f  dBFS  ClipCnt %d", d.pk, -89.0, -90.0, -87.0, 0)}
	}
	n, _ := strconv.Atoi(arg[1:])
	state := func(v bool) string {
		if v {
			return "Enabled"
		}
		return "Disabled"
	}
	switch arg[0] {
	case 'c':
		d.rx = n
		return []string{fmt.Sprintf("Channel %s: Changed setting on RX Channel to %d", f.active, n)}
	case 'f':
		d.txa = n
		return []string{fmt.Sprintf("Channel %s: Changed setting on TX Channel A to %d", f.active, n)}
	case 'g':
		d.txb = n
		return []string{fmt.Sprintf("Channel %s: Changed setting on TX Channel B to %d", f.active, n)}
	case 'k':
		d.echo = n > 0
		return []string{"Echo Mode changed to " + state(d.echo)}
	case 'm':
		d.boost = n > 0
		return []string{"RxBoost changed to " + state(d.boost)}
	case 'n':
		d.pre = n > 0
		return []string{"Pre-emphasis changed to " + state(d.pre)}
	case 'o':
		d.de = n > 0
		return []string{"De-emphasis changed to " + state(d.de)}
	case 'p':
		d.pl = n > 0
		return []string{"PL Filter changed to " + state(d.pl)}
	case 'q':
		d.invert = n > 0
		m := "Ground"
		if d.invert {
			m = "Open"
		}
		return []string{"PTT mode changed to " + m}
	case 'r':
		d.carrier = n
		return []string{"Carrier From changed to " + []string{"no", "N/A", "N/A", "usb", "usbinvert", "pp", "ppinvert"}[n]}
	case 's':
		d.ctcss = n
		return []string{"CTCSS From changed to " + []string{"no", "usb", "usbinvert", "N/A", "pp", "ppinvert"}[n]}
	case 't':
		d.rxon = n
		return []string{fmt.Sprintf("RX On Delay From changed to %d", n)}
	case 'u':
		d.txoff = n
		return []string{fmt.Sprintf("TX Off Delay From changed to %d", n)}
	}
	return []string{"Unknown command"}
}

// fakeConfig serves files to the service.
type fakeConfig struct {
	mu    sync.Mutex
	files map[string]string
}

func (c *fakeConfig) ConfigList(context.Context) ([]helper.ConfigInfo, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []helper.ConfigInfo
	for p := range c.files {
		out = append(out, helper.ConfigInfo{Path: p})
	}
	return out, nil
}

func (c *fakeConfig) ConfigRead(_ context.Context, path string) (helper.ConfigContent, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.files[path]
	if !ok {
		return helper.ConfigContent{}, fmt.Errorf("%s", helper.ErrNoSuchFile)
	}
	return helper.ConfigContent{Path: path, Content: s}, nil
}

func (c *fakeConfig) set(p, s string) {
	c.mu.Lock()
	c.files[p] = s
	c.mu.Unlock()
}

type fakeRecorder struct {
	mu    sync.Mutex
	calls []string
	err   error
}

func (r *fakeRecorder) ConfigRecord(_ context.Context, path, msg, author string) (helper.ConfigApplyResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, path+"|"+msg+"|"+author)
	if r.err != nil {
		return helper.ConfigApplyResult{}, r.err
	}
	return helper.ConfigApplyResult{Commit: "abc1234"}, nil
}

func (f *fakeRadio) echoOn(dev string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.dev[dev].echo
}
