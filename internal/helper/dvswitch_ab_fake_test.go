package helper

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeAB is an Analog_Bridge over real loopback UDP. Like the real one it
// moves its TLV listener when it gets rxport=N: the old socket is closed (so
// datagrams sent there are lost), N is opened after a delay and ABInfo is
// rewritten, now naming N. With freeze the ABInfo keeps naming the first port
// (the node whose ABInfo never updates).
type fakeAB struct {
	t     *testing.T
	o     *Ops
	delay time.Duration // listener move: close of the old port to open of the new
	park  int
	ports map[string][2]int // mode -> DVSwitch.ini TXPort (AB listens there), RXPort

	mu       sync.Mutex
	conn     net.PacketConn
	port     int
	infoPort int
	freeze   bool
	mode     string
	tune     string
	log      []string
	tmpl     string
}

// freePorts returns n distinct loopback UDP ports that are free right now.
func freePorts(t *testing.T, n int) []int {
	t.Helper()
	var pcs []net.PacketConn
	var out []int
	for i := 0; i < n; i++ {
		pc, err := net.ListenPacket("udp", "127.0.0.1:0")
		if err != nil {
			t.Skip("no loopback UDP:", err)
		}
		pcs = append(pcs, pc)
		out = append(out, pc.LocalAddr().(*net.UDPAddr).Port)
	}
	for _, pc := range pcs {
		pc.Close()
	}
	return out
}

var dvTestModes = []string{"DMR", "YSF", "P25", "NXDN"}

// newFakeAB starts a bridge in mode (an ABInfo mode such as YSFN) listening on
// a free port, writes ABInfo and DVSwitch.ini for DMR, YSF, P25 and NXDN with
// free ports, and sets the helper's timing to test speed.
func newFakeAB(t *testing.T, o *Ops, mode, lastTune string) *fakeAB {
	t.Helper()
	ps := freePorts(t, 2+2*len(dvTestModes))
	f := &fakeAB{t: t, o: o, delay: 15 * time.Millisecond, park: ps[0], port: ps[1], infoPort: ps[1], mode: mode, tune: lastTune, ports: map[string][2]int{}}
	good, err := os.ReadFile("testdata/ABInfo_31001.json")
	if err != nil {
		t.Fatal(err)
	}
	f.tmpl = string(good)
	var ini strings.Builder
	for i, m := range dvTestModes {
		tx, rx := ps[2+2*i], ps[3+2*i]
		f.ports[m] = [2]int{tx, rx}
		ini.WriteString("[" + m + "]\ntxPort=" + strconv.Itoa(tx) + "\nrxPort=" + strconv.Itoa(rx) + "\n\n")
	}
	p := filepath.Join(o.dvOptDir(), "MMDVM_Bridge", "DVSwitch.ini")
	if err := os.WriteFile(p, []byte(ini.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	o.DVTLVDelay, o.DVTLVSettle = time.Millisecond, 40*time.Millisecond
	o.DVPortPoll, o.DVPortWait, o.DVParkPort = 2*time.Millisecond, 500*time.Millisecond, f.park
	f.listen(f.port)
	f.mu.Lock()
	f.writeInfo()
	f.mu.Unlock()
	t.Cleanup(func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.conn != nil {
			f.conn.Close()
			f.conn = nil
		}
	})
	return f
}

func (f *fakeAB) listen(port int) {
	var pc net.PacketConn
	var err error
	for i := 0; i < 50; i++ {
		if pc, err = net.ListenPacket("udp", "127.0.0.1:"+strconv.Itoa(port)); err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil {
		f.t.Errorf("fake AB cannot listen on %d: %v", port, err)
		return
	}
	f.mu.Lock()
	f.conn, f.port = pc, port
	f.mu.Unlock()
	go f.serve(pc)
}

func (f *fakeAB) serve(pc net.PacketConn) {
	buf := make([]byte, 512)
	for {
		n, _, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		if n < 2 || buf[0] != dvTLVTag || int(buf[1]) != n-2 {
			f.record("BAD")
			continue
		}
		f.handle(pc, string(buf[2:n]))
	}
}

func (f *fakeAB) record(c string) {
	f.mu.Lock()
	f.log = append(f.log, c)
	f.mu.Unlock()
}

func (f *fakeAB) handle(pc net.PacketConn, c string) {
	f.mu.Lock()
	f.log = append(f.log, c)
	k, v, _ := strings.Cut(c, "=")
	switch k {
	case "rxport":
		if f.conn == pc {
			f.conn = nil
		}
		pc.Close()
		n, _ := strconv.Atoi(v)
		go func() {
			time.Sleep(f.delay)
			f.listen(n)
			f.mu.Lock()
			f.writeInfo()
			f.mu.Unlock()
		}()
	case "ambeMode":
		f.mode = v
		if v == "YSF" {
			f.mode = "YSFN"
		}
	case "txTg":
		f.tune = v
	case "info":
		f.writeInfo()
	}
	f.mu.Unlock()
}

// writeInfo rewrites ABInfo atomically; f.mu is held.
func (f *fakeAB) writeInfo() {
	if !f.freeze {
		f.infoPort = f.port
	}
	s := strings.Replace(f.tmpl, `"rx_port":"31100"`, `"rx_port":"`+strconv.Itoa(f.infoPort)+`"`, 1)
	s = strings.Replace(s, `"ambe_mode":"DMR"`, `"ambe_mode":"`+f.mode+`"`, 1)
	s = strings.Replace(s, `"last_tune":""`, `"last_tune":"`+f.tune+`"`, 1)
	dir := filepath.Join(f.o.DVProcDir, "4242", "root", "tmp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Error(err)
		return
	}
	tmp := filepath.Join(dir, "ab.part")
	if err := os.WriteFile(tmp, []byte(s), 0o644); err != nil {
		f.t.Error(err)
		return
	}
	if err := os.Rename(tmp, filepath.Join(dir, "ABInfo_31001.json")); err != nil {
		f.t.Error(err)
	}
}

// recv waits for the datagrams in flight, then returns and clears the
// commands received so far.
func (f *fakeAB) recv() []string {
	time.Sleep(60 * time.Millisecond)
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.log
	f.log = nil
	return out
}

func (f *fakeAB) state() (mode, tune string, port int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mode, f.tune, f.port
}

// switchCmds is the command sequence of a mode switch, joined with "|".
func (f *fakeAB) switchCmds(mode string) string {
	p := f.ports[mode]
	return strings.Join([]string{"rxport=" + strconv.Itoa(f.park), "ambeMode=" + mode, "txport=" + strconv.Itoa(p[1]), "rxport=" + strconv.Itoa(p[0]), "info"}, "|")
}

// freezeInfo makes ABInfo keep naming the current port from now on.
func (f *fakeAB) freezeInfo() {
	f.mu.Lock()
	f.freeze = true
	f.mu.Unlock()
}
