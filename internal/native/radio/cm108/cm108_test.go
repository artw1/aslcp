package cm108

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestGPIOBit(t *testing.T) {
	tests := []struct {
		n    int
		want byte
		err  bool
	}{{1, 1, false}, {2, 2, false}, {3, 4, false}, {4, 8, false}, {0, 0, true}, {5, 0, true}}
	for _, tc := range tests {
		got, err := GPIOBit(tc.n)
		if (err != nil) != tc.err || got != tc.want {
			t.Errorf("GPIOBit(%d) = %d, %v", tc.n, got, err)
		}
	}
}

func TestOutputReport(t *testing.T) {
	got := OutputReport(0x04, 0x0c)
	want := [5]byte{0, 0, 0x04, 0x0c, 0}
	if got != want {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestParseInputAndCarrier(t *testing.T) {
	tests := []struct {
		name      string
		report    []byte
		activeLow bool
		wantByte  byte
		carrier   bool
		err       bool
	}{
		{"4 bytes, volume-down high, usbinvert", []byte{0x02, 0, 0, 0}, false, 0x02, true, false},
		{"4 bytes, volume-down low, usbinvert", []byte{0x00, 0, 0, 0}, false, 0x00, false, false},
		{"4 bytes, volume-down low, usb", []byte{0x00, 0, 0, 0}, true, 0x00, true, false},
		{"4 bytes, volume-down high, usb", []byte{0x02, 0, 0, 0}, true, 0x02, false, false},
		{"5 bytes with report id", []byte{0, 0x02, 0, 0, 0}, false, 0x02, true, false},
		{"other bits ignored", []byte{0x0d, 0, 0, 0}, false, 0x0d, false, false},
		{"empty", nil, false, 0, false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, err := ParseInput(tc.report)
			if (err != nil) != tc.err {
				t.Fatalf("err = %v", err)
			}
			if tc.err {
				if !errors.Is(err, ErrShortReport) {
					t.Fatalf("err = %v", err)
				}
				return
			}
			if b != tc.wantByte {
				t.Errorf("byte = %#x, want %#x", b, tc.wantByte)
			}
			if got := Carrier(b, tc.activeLow); got != tc.carrier {
				t.Errorf("carrier = %v, want %v", got, tc.carrier)
			}
		})
	}
}

func TestDebouncer(t *testing.T) {
	t0 := time.Unix(1000, 0)
	at := func(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }
	type step struct {
		ms      int
		raw     bool
		state   bool
		changed bool
	}
	tests := []struct {
		name  string
		start bool
		steps []step
	}{
		{"accepts after window", false, []step{
			{0, true, false, false}, {20, true, false, false}, {30, true, true, true}, {40, true, true, false},
		}},
		{"glitch is ignored", false, []step{
			{0, true, false, false}, {10, false, false, false}, {50, false, false, false},
		}},
		{"restart on flip-flop", false, []step{
			{0, true, false, false}, {20, false, false, false}, {25, true, false, false}, {50, true, false, false}, {55, true, true, true},
		}},
		{"falling edge", true, []step{
			{0, false, true, false}, {30, false, false, true},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			d := NewDebouncer(30*time.Millisecond, tc.start)
			for i, s := range tc.steps {
				st, ch := d.Update(at(s.ms), s.raw)
				if st != s.state || ch != s.changed {
					t.Fatalf("step %d: got (%v,%v), want (%v,%v)", i, st, ch, s.state, s.changed)
				}
			}
		})
	}
}

func fakeSys(t *testing.T, withHID, withCard bool) string {
	t.Helper()
	root := t.TempDir()
	dev := filepath.Join(root, "bus", "usb", "devices", "1-1.2")
	if withCard {
		os.MkdirAll(filepath.Join(dev, "1-1.2:1.0", "sound", "card2"), 0o755)
	}
	if withHID {
		os.MkdirAll(filepath.Join(dev, "1-1.2:1.3", "0003:0D8C:0012.0004", "hidraw", "hidraw3"), 0o755)
	}
	os.MkdirAll(dev, 0o755)
	os.WriteFile(filepath.Join(dev, "idVendor"), []byte("0d8c\n"), 0o644)
	os.WriteFile(filepath.Join(dev, "idProduct"), []byte("0012\n"), 0o644)
	return root
}

func TestDiscover(t *testing.T) {
	tests := []struct {
		name     string
		hid, snd bool
		bus      string
		wantErr  bool
	}{
		{"complete", true, true, "", false},
		{"bus path", true, true, "1-1.2", false},
		{"wrong bus path", true, true, "9-9", true},
		{"no hidraw", false, true, "", true},
		{"no card", true, false, "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			info, err := Discover(fakeSys(t, tc.hid, tc.snd), "/dev", tc.bus)
			if tc.wantErr {
				if !errors.Is(err, ErrNotFound) {
					t.Fatalf("err = %v, want ErrNotFound", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if info.Hidraw != "/dev/hidraw3" || info.Card != 2 || info.BusPath != "1-1.2" || info.ID.Product != 0x12 {
				t.Errorf("info = %+v", info)
			}
		})
	}
}

func TestSupportedIDs(t *testing.T) {
	want := map[uint16]bool{0x000c: true, 0x0012: true, 0x013c: true, 0x013a: true, 0x0008: true}
	have := map[uint16]bool{}
	for _, id := range SupportedIDs {
		if id.Vendor == 0x0d8c {
			have[id.Product] = true
		}
	}
	for p := range want {
		if !have[p] {
			t.Errorf("product %04x missing", p)
		}
	}
}

type fakeHID struct {
	mu     sync.Mutex
	writes [][]byte
	in     byte
	failW  bool
	closed bool
}

func (f *fakeHID) WriteReport(r []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failW {
		return errors.New("write failed")
	}
	f.writes = append(f.writes, append([]byte(nil), r...))
	return nil
}
func (f *fakeHID) Inputs() (byte, error) { f.mu.Lock(); defer f.mu.Unlock(); return f.in, nil }
func (f *fakeHID) Close() error          { f.mu.Lock(); f.closed = true; f.mu.Unlock(); return nil }
func (f *fakeHID) set(b byte)            { f.mu.Lock(); f.in = b; f.mu.Unlock() }
func (f *fakeHID) last() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.writes) == 0 {
		return nil
	}
	return f.writes[len(f.writes)-1]
}

// manualClock is a Clock whose ticker never fires; tests call step directly.
type manualClock struct{ now time.Time }

func (c *manualClock) Now() time.Time { return c.now }
func (c *manualClock) NewTicker(time.Duration) (<-chan time.Time, func()) {
	return make(chan time.Time), func() {}
}

func TestPTTReports(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		on   bool
		want []byte
	}{
		{"default on", Config{}, true, []byte{0, 0, 0x04, 0x0c, 0}},
		{"default off", Config{}, false, []byte{0, 0, 0x00, 0x0c, 0}},
		{"gpio4", Config{PTTGPIO: 4}, true, []byte{0, 0, 0x08, 0x0c, 0}},
		{"gpio1 adds direction", Config{PTTGPIO: 1}, true, []byte{0, 0, 0x01, 0x0d, 0}},
		{"active low on", Config{PTTActiveLow: true}, true, []byte{0, 0, 0x00, 0x0c, 0}},
		{"active low off", Config{PTTActiveLow: true}, false, []byte{0, 0, 0x04, 0x0c, 0}},
		{"custom mask", Config{OutputMask: 0x02}, true, []byte{0, 0, 0x04, 0x06, 0}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := &fakeHID{}
			tc.cfg.Clock = &manualClock{}
			d, err := NewDevice(Info{}, h, tc.cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			if err := d.SetPTT(tc.on); err != nil {
				t.Fatal(err)
			}
			if got := h.last(); string(got) != string(tc.want) {
				t.Errorf("report = %v, want %v", got, tc.want)
			}
			if d.PTT() != tc.on {
				t.Errorf("PTT() = %v", d.PTT())
			}
		})
	}
	if _, err := NewDevice(Info{}, &fakeHID{}, Config{PTTGPIO: 7, Clock: &manualClock{}}); err == nil {
		t.Error("bad GPIO accepted")
	}
}

func TestPTTWriteError(t *testing.T) {
	h := &fakeHID{failW: true}
	if _, err := NewDevice(Info{}, h, Config{Clock: &manualClock{}}); err == nil {
		t.Fatal("expected init error")
	}
}

func TestCloseReleasesPTT(t *testing.T) {
	h := &fakeHID{}
	d, err := NewDevice(Info{}, h, Config{Clock: &manualClock{}})
	if err != nil {
		t.Fatal(err)
	}
	d.SetPTT(true)
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if got := h.last(); got[2] != 0 {
		t.Errorf("PTT still keyed after Close: %v", got)
	}
	if !h.closed {
		t.Error("hid not closed")
	}
	d.Close() // idempotent
}

func TestCOSStepAndChannel(t *testing.T) {
	h := &fakeHID{}
	clk := &manualClock{now: time.Unix(0, 0)}
	d, err := NewDevice(Info{}, h, Config{Clock: clk, Debounce: 30 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	at := func(ms int) time.Time { return time.Unix(0, 0).Add(time.Duration(ms) * time.Millisecond) }
	d.step(at(0), true)
	d.step(at(10), true)
	if d.COS() {
		t.Fatal("COS accepted before debounce")
	}
	d.step(at(30), true)
	if !d.COS() {
		t.Fatal("COS not accepted")
	}
	select {
	case v := <-d.Changes():
		if !v {
			t.Fatal("expected carrier on")
		}
	default:
		t.Fatal("no change delivered")
	}
	d.step(at(100), false)
	d.step(at(130), false)
	if v := <-d.Changes(); v {
		t.Fatal("expected carrier off")
	}
}

func TestCOSInitialStateFromHID(t *testing.T) {
	h := &fakeHID{in: InVolumeDown}
	d, err := NewDevice(Info{}, h, Config{Clock: &manualClock{}})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if !d.COS() {
		t.Error("carrier already present at open should read true")
	}
	select {
	case <-d.Changes():
		t.Error("initial state must not be reported as a change")
	default:
	}
}

func TestPollLoopWithRealTicker(t *testing.T) {
	h := &fakeHID{}
	d, err := NewDevice(Info{}, h, Config{Debounce: 5 * time.Millisecond, PollInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	h.set(InVolumeDown)
	select {
	case v := <-d.Changes():
		if !v {
			t.Fatal("want carrier on")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no COS change from poll loop")
	}
	h.set(0)
	select {
	case v := <-d.Changes():
		if v {
			t.Fatal("want carrier off")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no COS release from poll loop")
	}
}
