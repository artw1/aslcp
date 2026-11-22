package bm

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/dv/ctl"
	"github.com/artw1/aslcp/internal/dv/dmr"
	"github.com/artw1/aslcp/internal/dv/network"
)

const (
	testPass = "example-pass"
	testID   = 1234567
)

// fakeMaster is an in-process Rewind master.
type fakeMaster struct {
	t    *testing.T
	conn *net.UDPConn

	mu      sync.Mutex
	pass    string
	silent  bool
	parrot  bool
	salt    []byte
	authed  map[string]bool
	clients []time.Time // first sight of each client address
	seen    map[string]bool
	pkts    []packet // all packets received from clients (after sign check)
	last    *net.UDPAddr
	rec     []packet // current call recording for the parrot
	events  chan packet
}

func newMaster(t *testing.T) *fakeMaster {
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	m := &fakeMaster{t: t, conn: c, pass: testPass, salt: []byte{1, 2, 3, 4},
		authed: map[string]bool{}, seen: map[string]bool{}, events: make(chan packet, 256)}
	go m.loop()
	t.Cleanup(func() { c.Close() })
	return m
}

func (m *fakeMaster) port() int { return m.conn.LocalAddr().(*net.UDPAddr).Port }

func (m *fakeMaster) reply(to *net.UDPAddr, typ, flags uint16, payload []byte) {
	m.conn.WriteToUDP(marshal(typ, flags, 0, payload), to)
}

func (m *fakeMaster) loop() {
	buf := make([]byte, 2048)
	for {
		n, from, err := m.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		p, err := unmarshal(buf[:n])
		if err != nil {
			continue
		}
		key := from.String()
		m.mu.Lock()
		if !m.seen[key] {
			m.seen[key] = true
			m.clients = append(m.clients, time.Now())
		}
		if m.silent {
			m.mu.Unlock()
			continue
		}
		m.pkts = append(m.pkts, p)
		m.last = from
		switch p.typ {
		case typKeepAlive:
			if m.authed[key] {
				m.reply(from, typKeepAlive, 0, nil)
			} else {
				m.reply(from, typChallenge, 0, m.salt)
			}
		case typAuth:
			d := sha256.Sum256(append(append([]byte(nil), m.salt...), m.pass...))
			if string(p.payload) == string(d[:]) {
				m.authed[key] = true
				m.reply(from, typKeepAlive, 0, nil)
			} else {
				m.reply(from, typChallenge, 0, m.salt)
			}
		case typConfig:
			if m.authed[key] {
				m.reply(from, typConfig, 0, nil)
			}
		case typSubscription, typCancelling:
			m.reply(from, p.typ, 0, nil)
		case typSuperHeader, typAudio, typTerminator:
			if m.parrot && (len(m.rec) > 0 || p.typ == typSuperHeader) && (len(m.rec) == 0 || p.seq != m.rec[len(m.rec)-1].seq || p.typ != m.rec[len(m.rec)-1].typ) {
				m.rec = append(m.rec, p)
				if p.typ == typTerminator {
					rec := m.rec
					m.rec = nil
					go m.echo(from, rec)
				}
			}
		}
		m.mu.Unlock()
		select {
		case m.events <- p:
		default:
		}
	}
}

func (m *fakeMaster) echo(to *net.UDPAddr, rec []packet) {
	sh, _ := parseSuper(rec[0].payload)
	m.sendCall(to, sh.src, sh.dst, sh.srcCall, sh.typ == sessionPrivate, audioOf(rec), true)
}

func audioOf(rec []packet) [][]byte {
	var a [][]byte
	for _, p := range rec {
		if p.typ == typAudio {
			a = append(a, p.payload)
		}
	}
	return a
}

// sendCall plays a call to the client: super header, LC header, audio, terminator.
func (m *fakeMaster) sendCall(to *net.UDPAddr, src, dst uint32, call string, private bool, audio [][]byte, term bool) {
	typ := sessionGroup
	if private {
		typ = sessionPrivate
	}
	m.reply(to, typSuperHeader, flagRealTime1, superHeader{typ: typ, src: src, dst: dst, srcCall: call}.marshal())
	m.reply(to, typVoiceHeader, flagRealTime1, voiceHeaderLC(private, dst, src))
	for _, a := range audio {
		m.reply(to, typAudio, flagRealTime1, a)
	}
	if term {
		m.reply(to, typTerminator, flagRealTime1, nil)
	}
}

func (m *fakeMaster) setSilent(v bool) { m.mu.Lock(); m.silent = v; m.mu.Unlock() }
func (m *fakeMaster) lastAddr() *net.UDPAddr {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.last
}

func (m *fakeMaster) packets(typ uint16) []packet {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []packet
	for _, p := range m.pkts {
		if p.typ == typ {
			out = append(out, p)
		}
	}
	return out
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

// testSink records everything.
type testSink struct {
	mu     sync.Mutex
	logins []ctl.LoginState
	hdrs   []network.CallHeader
	frames []network.VoiceFrame
	ends   []uint64
}

func (s *testSink) Header(h network.CallHeader) {
	s.mu.Lock()
	s.hdrs = append(s.hdrs, h)
	s.mu.Unlock()
}
func (s *testSink) Voice(f network.VoiceFrame) {
	f.Data = append([]byte(nil), f.Data...)
	s.mu.Lock()
	s.frames = append(s.frames, f)
	s.mu.Unlock()
}
func (s *testSink) End(c uint64) { s.mu.Lock(); s.ends = append(s.ends, c); s.mu.Unlock() }
func (s *testSink) Login(st ctl.LoginState, _ string) {
	s.mu.Lock()
	s.logins = append(s.logins, st)
	s.mu.Unlock()
}
func (s *testSink) loginIs(st ctl.LoginState) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.logins) > 0 && s.logins[len(s.logins)-1] == st
}
func (s *testSink) count(st ctl.LoginState) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := 0
	for _, l := range s.logins {
		if l == st {
			c++
		}
	}
	return c
}
func (s *testSink) nHdr() int    { s.mu.Lock(); defer s.mu.Unlock(); return len(s.hdrs) }
func (s *testSink) nFrames() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.frames) }
func (s *testSink) nEnds() int   { s.mu.Lock(); defer s.mu.Unlock(); return len(s.ends) }

var fastOpts = map[string]string{
	"keepalive_ms": "30", "timeout_ms": "150", "login_timeout_ms": "200",
	"backoff_min_ms": "20", "backoff_max_ms": "100", "rx_timeout_ms": "80",
}

func start(t *testing.T, m *fakeMaster, pass string, extra map[string]string) (*Net, *testSink) {
	t.Helper()
	opts := map[string]string{}
	for k, v := range fastOpts {
		opts[k] = v
	}
	for k, v := range extra {
		opts[k] = v
	}
	n, err := New(ctl.NetConfig{Mode: ctl.ModeBM, Enabled: true, Address: "127.0.0.1", Port: m.port(),
		Password: pass, Options: opts}, ctl.Identity{Callsign: "N0CALL", DMRID: testID})
	if err != nil {
		t.Fatal(err)
	}
	s := &testSink{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { n.Run(ctx, s); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return n, s
}

func login(t *testing.T) (*fakeMaster, *Net, *testSink) {
	m := newMaster(t)
	n, s := start(t, m, testPass, map[string]string{"client_version": "test 1.0"})
	waitFor(t, "login", func() bool { return s.loginIs(ctl.LoginOK) })
	return m, n, s
}

func TestLoginSuccess(t *testing.T) {
	m, n, s := login(t)
	if st := n.Status(); st.Login != ctl.LoginOK || !st.Available || st.Mode != ctl.ModeBM {
		t.Fatalf("status %+v", st)
	}
	ka := m.packets(typKeepAlive)[0].payload
	if binary.LittleEndian.Uint32(ka) != testID || ka[4] != 0x21 || string(ka[5:]) != "test 1.0" {
		t.Fatalf("keepalive payload % x", ka)
	}
	cfg := m.packets(typConfig)[0].payload
	if binary.LittleEndian.Uint32(cfg) != optSuperHeader {
		t.Fatalf("config % x", cfg)
	}
	if s.count(ctl.LoginPending) == 0 {
		t.Fatal("never pending")
	}
}

func TestBadPassword(t *testing.T) {
	m := newMaster(t)
	n, s := start(t, m, "wrong", nil)
	waitFor(t, "failed", func() bool { return s.loginIs(ctl.LoginFailed) })
	if n.Status().Login != ctl.LoginFailed {
		t.Fatal(n.Status())
	}
	if len(m.packets(typConfig)) != 0 {
		t.Fatal("config sent without auth")
	}
}

func TestStatusHasNoPassword(t *testing.T) {
	_, n, _ := login(t)
	if d := n.Status().Detail; d != "" {
		t.Fatalf("detail %q", d)
	}
}

func TestLoginTimeoutBackoff(t *testing.T) {
	m := newMaster(t)
	m.setSilent(true)
	_, s := start(t, m, testPass, map[string]string{"login_timeout_ms": "30", "backoff_min_ms": "30", "backoff_max_ms": "120"})
	waitFor(t, "4 attempts", func() bool { m.mu.Lock(); defer m.mu.Unlock(); return len(m.clients) >= 4 })
	m.mu.Lock()
	g1 := m.clients[1].Sub(m.clients[0])
	g3 := m.clients[3].Sub(m.clients[2])
	m.mu.Unlock()
	if g3 < g1 {
		t.Fatalf("backoff not growing: %v then %v", g1, g3)
	}
	if s.loginIs(ctl.LoginOK) {
		t.Fatal("logged in")
	}
	// the master wakes up: the client gets in.
	m.setSilent(false)
	waitFor(t, "login", func() bool { return s.loginIs(ctl.LoginOK) })
}

func TestBackoffSequence(t *testing.T) {
	d := time.Second
	var got []time.Duration
	for i := 0; i < 8; i++ {
		got = append(got, d)
		d = nextBackoff(d, 60*time.Second)
	}
	want := []time.Duration{1, 2, 4, 8, 16, 32, 60, 60}
	for i := range want {
		if got[i] != want[i]*time.Second {
			t.Fatalf("%v", got)
		}
	}
	if DefaultBackoffMin != time.Second || DefaultBackoffMax != 60*time.Second {
		t.Fatal("defaults")
	}
}

func TestKeepAliveAndReconnect(t *testing.T) {
	m, n, s := login(t)
	if err := n.Tune(context.Background(), ctl.Target{TG: 1999}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "keepalives", func() bool { return len(m.packets(typKeepAlive)) >= 4 })
	m.setSilent(true)
	waitFor(t, "pending after loss", func() bool { return s.loginIs(ctl.LoginPending) && s.count(ctl.LoginOK) == 1 })
	if st := n.Status(); st.Login != ctl.LoginPending {
		t.Fatal(st)
	}
	before := len(m.packets(typSubscription))
	m.setSilent(false)
	waitFor(t, "relogin", func() bool { return s.count(ctl.LoginOK) == 2 })
	waitFor(t, "resubscribe", func() bool { return len(m.packets(typSubscription)) > before })
}

func TestTuneUnlink(t *testing.T) {
	m, n, _ := login(t)
	ctx := context.Background()
	sub := func(i int) (uint32, uint32) {
		p := m.packets(typSubscription)[i].payload
		return binary.LittleEndian.Uint32(p), binary.LittleEndian.Uint32(p[4:])
	}
	n.Tune(ctx, ctl.Target{TG: 1999})
	waitFor(t, "sub", func() bool { return len(m.packets(typSubscription)) == 1 })
	if a, b := sub(0); a != sessionGroup || b != 1999 {
		t.Fatal(a, b)
	}
	if d := n.Status().Detail; d != "TG 1999" {
		t.Fatalf("detail %q", d)
	}
	n.Tune(ctx, ctl.Target{TG: 1998})
	waitFor(t, "retune", func() bool { return len(m.packets(typSubscription)) == 2 })
	c := m.packets(typCancelling)
	if len(c) != 1 || binary.LittleEndian.Uint32(c[0].payload[4:]) != 1999 {
		t.Fatalf("cancel %+v", c)
	}
	n.Tune(ctx, ctl.Target{TG: ParrotTG})
	waitFor(t, "parrot sub", func() bool { return len(m.packets(typSubscription)) == 3 })
	if a, b := sub(2); a != sessionPrivate || b != testID {
		t.Fatal(a, b)
	}
	if err := n.Unlink(ctx); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "cancel all", func() bool { return len(m.packets(typCancelling)) == 3 })
	if last := m.packets(typCancelling)[2]; len(last.payload) != 0 {
		t.Fatal("cancel-all must be empty")
	}
	if n.Status().Detail != "" {
		t.Fatal(n.Status())
	}
	if err := n.Tune(ctx, ctl.Target{}); err == nil {
		t.Fatal("tg 0 accepted")
	}
}

func TestTuneBeforeLogin(t *testing.T) {
	m := newMaster(t)
	n, s := start(t, m, testPass, nil)
	n.Tune(context.Background(), ctl.Target{TG: 1997})
	waitFor(t, "login", func() bool { return s.loginIs(ctl.LoginOK) })
	waitFor(t, "sub", func() bool { return len(m.packets(typSubscription)) >= 1 })
}

func frames(n int, base byte) [][]byte {
	var out [][]byte
	for i := 0; i < n; i++ {
		f := make([]byte, 7)
		for j := range f {
			f[j] = base + byte(i*7+j)
		}
		f[6] &= 0x80 // 49 bits: only the top bit of the last byte is used
		out = append(out, f)
	}
	return out
}

func cat(fs [][]byte) []byte {
	var b []byte
	for _, f := range fs {
		b = append(b, f...)
	}
	return b
}

func TestRXCall(t *testing.T) {
	m, n, s := login(t)
	n.Tune(context.Background(), ctl.Target{TG: 1999})
	fs := frames(6, 1)
	m.sendCall(m.lastAddr(), 7654321, 1999, "N0CALL", false, [][]byte{cat(fs[:3]), cat(fs[3:])}, true)
	waitFor(t, "end", func() bool { return s.nEnds() == 1 })
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.hdrs[0]
	if len(s.hdrs) != 1 || h.Src != "N0CALL" || h.SrcID != 7654321 || h.TG != 1999 || h.Private || h.Slot != 2 {
		t.Fatalf("header %+v", h)
	}
	if len(s.frames) != 6 {
		t.Fatalf("%d frames", len(s.frames))
	}
	for i, f := range s.frames {
		if f.Call != h.Call || string(f.Data) != string(fs[i]) || f.Seq != uint32(i) {
			t.Fatalf("frame %d %+v", i, f)
		}
	}
	if s.ends[0] != h.Call {
		t.Fatal("end call id")
	}
}

func TestRXWithoutSuperHeader(t *testing.T) {
	m, _, s := login(t)
	to := m.lastAddr()
	m.reply(to, typVoiceHeader, flagRealTime1, voiceHeaderLC(true, testID, 7654321))
	m.reply(to, typAudio, flagRealTime1, cat(frames(3, 9)))
	m.reply(to, typTerminator, flagRealTime1, nil)
	waitFor(t, "end", func() bool { return s.nEnds() == 1 })
	h := s.hdrs[0]
	if h.Src != "" || h.SrcID != 7654321 || h.TG != testID || !h.Private || s.nFrames() != 3 {
		t.Fatalf("%+v", h)
	}
}

func TestRXLostEnd(t *testing.T) {
	m, _, s := login(t)
	m.sendCall(m.lastAddr(), 7654321, 1999, "N0CALL", false, [][]byte{cat(frames(3, 1))}, false)
	waitFor(t, "timeout end", func() bool { return s.nEnds() == 1 })
	if s.nFrames() != 3 {
		t.Fatal(s.nFrames())
	}
	// a repeated terminator and stray audio after the end are ignored.
	m.reply(m.lastAddr(), typTerminator, flagRealTime1, nil)
	m.reply(m.lastAddr(), typAudio, flagRealTime1, cat(frames(3, 1)))
	time.Sleep(30 * time.Millisecond)
	if s.nEnds() != 1 || s.nFrames() != 3 || s.nHdr() != 1 {
		t.Fatal("stray packets produced events")
	}
}

// RX framing is unconfirmed: 27 byte 72-bit audio and 21 byte linear audio
// are both accepted and delivered as 49-bit frames.
func TestRXBothFramings(t *testing.T) {
	m, _, s := login(t)
	fs := frames(3, 1)
	var p72 []byte
	for _, f := range fs {
		var a dmr.AMBE
		copy(a[:], f)
		e := dmr.EncodeAMBE(a)
		p72 = append(p72, e[:]...)
	}
	if len(p72) != 27 {
		t.Fatal(len(p72))
	}
	m.sendCall(m.lastAddr(), 7654321, 1999, "N0CALL", false, [][]byte{p72, cat(fs), make([]byte, 5)}, true)
	waitFor(t, "end", func() bool { return s.nEnds() == 1 })
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.frames) != 6 {
		t.Fatalf("%d frames", len(s.frames))
	}
	for i, f := range s.frames {
		want := fs[i%3]
		// the AMBE frame goes through Golay: the 49 bits come back unchanged
		// (a, b parts are protected, c, d are carried as is).
		if string(f.Data) != string(want) {
			t.Fatalf("frame %d: % x want % x", i, f.Data, want)
		}
	}
}

func TestTXFraming(t *testing.T) {
	m, n, _ := login(t)
	n.Tune(context.Background(), ctl.Target{TG: 1999})
	if err := n.StartTX(network.CallHeader{Call: 5, Src: "N0CALL", SrcID: testID}); err != nil {
		t.Fatal(err)
	}
	fs := frames(7, 40)
	for i, f := range fs {
		if err := n.WriteVoice(network.VoiceFrame{Call: 5, Seq: uint32(i), Data: f}); err != nil {
			t.Fatal(err)
		}
	}
	if err := n.EndTX(5); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "terminators", func() bool { return len(m.packets(typTerminator)) == 3 })
	sh := m.packets(typSuperHeader)
	if len(sh) != 3 {
		t.Fatalf("%d super headers", len(sh))
	}
	for _, p := range sh {
		if p.flags != flagRealTime1 || p.seq != sh[0].seq {
			t.Fatalf("header copies differ: %+v", p)
		}
	}
	h, _ := parseSuper(sh[0].payload)
	if h.typ != sessionGroup || h.src != testID || h.dst != 1999 || h.srcCall != "N0CALL" {
		t.Fatalf("%+v", h)
	}
	lc := m.packets(typVoiceHeader)
	if len(lc) != 3 {
		t.Fatalf("%d LC headers", len(lc))
	}
	if pr, dst, src, _ := parseLC(lc[0].payload); pr || dst != 1999 || src != testID {
		t.Fatal(pr, dst, src)
	}
	au := m.packets(typAudio)
	if len(au) != 3 {
		t.Fatalf("%d audio packets", len(au))
	}
	var want []byte
	for _, f := range fs {
		var a dmr.AMBE
		copy(a[:], f)
		e := dmr.EncodeAMBE(a)
		want = append(want, e[:]...)
	}
	want = append(want, dmr.SilenceAMBE72[:]...) // pad with silence
	want = append(want, dmr.SilenceAMBE72[:]...)
	if string(cat([][]byte{au[0].payload, au[1].payload, au[2].payload})) != string(want) {
		t.Fatal("audio mismatch")
	}
	for i, p := range au {
		if p.flags != flagRealTime1 || len(p.payload) != 27 || p.seq != lc[0].seq+1+uint32(i) {
			t.Fatalf("audio %d: %+v", i, p)
		}
	}
	tm := m.packets(typTerminator)
	if tm[0].seq != au[2].seq+1 || tm[1].seq != tm[0].seq || tm[0].flags != flagRealTime1 || len(tm[0].payload) != 0 {
		t.Fatalf("terminator %+v", tm)
	}
	// non-real-time packets use their own counter.
	if ka := m.packets(typConfig)[0]; ka.flags != 0 {
		t.Fatal(ka)
	}
}

func TestTXErrors(t *testing.T) {
	m := newMaster(t)
	n, s := start(t, m, "wrong", nil)
	if err := n.StartTX(network.CallHeader{Call: 1, TG: 1999}); err == nil {
		t.Fatal("tx before login")
	}
	waitFor(t, "failed", func() bool { return s.loginIs(ctl.LoginFailed) })
	_, n2, _ := login(t)
	if err := n2.StartTX(network.CallHeader{Call: 1}); err == nil {
		t.Fatal("tx without target")
	}
	if err := n2.WriteVoice(network.VoiceFrame{Call: 1, Data: make([]byte, 3)}); err == nil {
		t.Fatal("short frame accepted")
	}
}

func TestParrotRoundTrip(t *testing.T) {
	m, n, s := login(t)
	m.mu.Lock()
	m.parrot = true
	m.mu.Unlock()
	n.Tune(context.Background(), ctl.Target{TG: ParrotTG})
	n.StartTX(network.CallHeader{Call: 9, Src: "N0CALL", SrcID: testID})
	fs := frames(6, 70)
	for i, f := range fs {
		n.WriteVoice(network.VoiceFrame{Call: 9, Seq: uint32(i), Data: f})
	}
	n.EndTX(9)
	waitFor(t, "echo end", func() bool { return s.nEnds() == 1 })
	s.mu.Lock()
	defer s.mu.Unlock()
	h := s.hdrs[0]
	if h.TG != ParrotTG || !h.Private || h.Src != "N0CALL" {
		t.Fatalf("%+v", h)
	}
	if len(s.frames) != 6 {
		t.Fatal(len(s.frames))
	}
	for i, f := range s.frames {
		if string(f.Data) != string(fs[i]) {
			t.Fatalf("frame %d", i)
		}
	}
	// the TX was a private call to 9990.
	sh, _ := parseSuper(m.packets(typSuperHeader)[0].payload)
	if sh.typ != sessionPrivate || sh.dst != ParrotTG {
		t.Fatalf("%+v", sh)
	}
}

func TestFactoryAndOptions(t *testing.T) {
	id := ctl.Identity{Callsign: "N0CALL", DMRID: testID}
	if _, err := New(ctl.NetConfig{}, id); err == nil {
		t.Fatal("empty address accepted")
	}
	if !network.Available(ctl.ModeBM) {
		t.Fatal("not registered")
	}
	n, err := New(ctl.NetConfig{Address: "node2001.example.net"}, id)
	if err != nil {
		t.Fatal(err)
	}
	if n.addr != "node2001.example.net:54006" || n.slot != 2 || n.version == "" {
		t.Fatalf("%+v", n)
	}
	if n.Mode() != ctl.ModeBM || n.Codec() != network.CodecAMBE {
		t.Fatal("mode/codec")
	}
	n, _ = New(ctl.NetConfig{Address: "192.0.2.1", Port: 1234, Options: map[string]string{
		"slot": "1", "colour_code": "3", "super_header": "false"}}, id)
	if n.addr != "192.0.2.1:1234" || n.slot != 1 || n.options != 0 {
		t.Fatalf("%+v", n)
	}
	if _, err := New(ctl.NetConfig{Address: "192.0.2.1"}, ctl.Identity{Callsign: "N0CALL"}); err == nil {
		t.Fatal("DMR ID 0 accepted")
	}
	for _, bad := range []map[string]string{{"slot": "3"}, {"colour_code": "16"},
		{"keepalive_ms": "0"}} {
		if _, err := New(ctl.NetConfig{Address: "192.0.2.1", Options: bad}, id); err == nil {
			t.Fatalf("accepted %v", bad)
		}
	}
	_ = strconv.Itoa
}

func TestWire(t *testing.T) {
	b := marshal(typKeepAlive, 0, 1, []byte{1, 2})
	want := append([]byte("REWIND01"), 0, 0, 0, 0, 1, 0, 0, 0, 2, 0, 1, 2)
	if string(b) != string(want) {
		t.Fatalf("% x", b)
	}
	if _, err := unmarshal(b[:len(b)-1]); err == nil {
		t.Fatal("truncated accepted")
	}
	if _, err := unmarshal(append([]byte("REWIND02"), b[8:]...)); err == nil {
		t.Fatal("foreign sign accepted")
	}
	sh := superHeader{typ: 7, src: 1, dst: 2, srcCall: "N0CALL"}
	g, ok := parseSuper(sh.marshal())
	if !ok || g != sh || len(sh.marshal()) != 32 {
		t.Fatal(g)
	}
	pr, dst, src, _ := parseLC(voiceHeaderLC(true, 9990, 1234567))
	if !pr || dst != 9990 || src != 1234567 {
		t.Fatal(pr, dst, src)
	}
}
