package helper

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFrameRoundTrip(t *testing.T) {
	var b bytes.Buffer
	if err := WriteData(&b, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := WriteResize(&b, 40, 132); err != nil {
		t.Fatal(err)
	}
	if err := WriteFrame(&b, FrameUser, []byte("alice")); err != nil {
		t.Fatal(err)
	}
	// wire format: type, two byte big-endian length, payload
	if got := b.Bytes()[:8]; !bytes.Equal(got, []byte{0, 0, 5, 'h', 'e', 'l', 'l', 'o'}) {
		t.Fatalf("data frame bytes % x", got)
	}
	typ, p, err := ReadFrame(&b)
	if err != nil || typ != FrameData || string(p) != "hello" {
		t.Fatalf("%d %q %v", typ, p, err)
	}
	typ, p, err = ReadFrame(&b)
	if err != nil || typ != FrameResize {
		t.Fatalf("%d %v", typ, err)
	}
	if r, c, err := ParseResize(p); err != nil || r != 40 || c != 132 {
		t.Fatalf("%d %d %v", r, c, err)
	}
	typ, p, err = ReadFrame(&b)
	if err != nil || typ != FrameUser || string(p) != "alice" {
		t.Fatalf("%d %q %v", typ, p, err)
	}
	if _, _, err = ReadFrame(&b); err != io.EOF {
		t.Fatalf("want EOF, got %v", err)
	}
}

func TestResizeIsNotData(t *testing.T) {
	// A resize frame and a data frame with the same four payload bytes differ
	// by type only, so input that looks like a resize stays input.
	var a, b bytes.Buffer
	_ = WriteResize(&a, 24, 80)
	var raw [4]byte
	binary.BigEndian.PutUint16(raw[0:2], 24)
	binary.BigEndian.PutUint16(raw[2:4], 80)
	_ = WriteData(&b, raw[:])
	ta, _, _ := ReadFrame(&a)
	tb, _, _ := ReadFrame(&b)
	if ta == tb {
		t.Fatal("resize and data frames have the same type")
	}
	if _, _, err := ParseResize([]byte{1, 2, 3}); !errors.Is(err, ErrBadFrame) {
		t.Fatalf("short resize accepted: %v", err)
	}
}

func TestFrameLimitsAndErrors(t *testing.T) {
	if err := WriteFrame(io.Discard, FrameData, make([]byte, MaxFramePayload+1)); !errors.Is(err, ErrBadFrame) {
		t.Fatalf("oversized frame written: %v", err)
	}
	// WriteData splits large input
	var b bytes.Buffer
	if err := WriteData(&b, make([]byte, 40<<10)); err != nil {
		t.Fatal(err)
	}
	n := 0
	for {
		_, p, err := ReadFrame(&b)
		if err != nil {
			break
		}
		if len(p) > dataChunk {
			t.Fatalf("chunk of %d", len(p))
		}
		n += len(p)
	}
	if n != 40<<10 {
		t.Fatalf("got %d bytes", n)
	}
	if _, _, err := ReadFrame(bytes.NewReader([]byte{9, 0, 0})); !errors.Is(err, ErrBadFrame) {
		t.Fatalf("unknown type: %v", err)
	}
	if _, _, err := ReadFrame(bytes.NewReader([]byte{0, 0, 5, 'a'})); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated: %v", err)
	}
}

func TestSizes(t *testing.T) {
	for _, c := range []struct {
		r, c uint16
		ok   bool
	}{{24, 80, true}, {1, 1, true}, {500, 500, true}, {0, 80, false}, {24, 0, false}, {501, 80, false}, {24, 501, false}} {
		if ValidSize(c.r, c.c) != c.ok {
			t.Errorf("ValidSize(%d,%d) != %v", c.r, c.c, c.ok)
		}
	}
	if r, c := ClampSize(0, 0); r != 24 || c != 80 {
		t.Fatalf("%d %d", r, c)
	}
	if r, c := ClampSize(9999, 7); r != 500 || c != 7 {
		t.Fatalf("%d %d", r, c)
	}
}

func TestParseProcStat(t *testing.T) {
	// the name may hold spaces and parentheses
	pid, sid, st, ok := parseProcStat("812 (tmux: (server) x) S 1 812 812 0 -1 4194560 100")
	if !ok || pid != 812 || sid != 812 || st != 'S' {
		t.Fatalf("%d %d %c %v", pid, sid, st, ok)
	}
	pid, sid, st, ok = parseProcStat("1999 (bash) S 1998 1999 4242 34816 1999 4194304")
	if !ok || pid != 1999 || sid != 4242 {
		t.Fatalf("%d %d %v", pid, sid, ok)
	}
	for _, bad := range []string{"", "x (a) S 1", "5 (a)", "5 a S 1 2 3 4"} {
		if _, _, _, ok := parseProcStat(bad); ok {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestParseProcUID(t *testing.T) {
	st := "Name:\tbash\nUmask:\t0022\nState:\tS (sleeping)\nUid:\t1001\t1001\t1001\t1001\nGid:\t1001\t1001\t1001\t1001\n"
	if uid, ok := parseProcUID(st); !ok || uid != 1001 {
		t.Fatalf("%d %v", uid, ok)
	}
	if _, ok := parseProcUID("Name:\tx\n"); ok {
		t.Fatal("no Uid line accepted")
	}
}

func utmpRec(typ int16, line, user string) []byte {
	b := make([]byte, utmpRecSize)
	binary.LittleEndian.PutUint16(b[0:2], uint16(typ))
	copy(b[utmpLineOff:], line)
	copy(b[utmpUserOff:], user)
	return b
}

func TestUtmpUser(t *testing.T) {
	var data []byte
	data = append(data, utmpRec(2, "~", "reboot")...)
	data = append(data, utmpRec(8, "pts/3", "dead")...)
	data = append(data, utmpRec(7, "pts/2", "other")...)
	data = append(data, utmpRec(7, "pts/3", "example")...)
	if got := utmpUser(data, "pts/3"); got != "example" {
		t.Fatalf("got %q", got)
	}
	if got := utmpUser(data, "pts/9"); got != "" {
		t.Fatalf("got %q", got)
	}
	if got := utmpUser(data[:100], "pts/3"); got != "" {
		t.Fatalf("short data: %q", got)
	}
}

// fakePTY is a PTY backed by pipes: what the test writes to out is the
// program's output, what the relay writes is read from in.
type fakePTY struct {
	outR, outW *os.File // program output
	inR, inW   *os.File // keyboard input
	mu         sync.Mutex
	sizes      [][2]uint16
	user       string
	closed     chan struct{}
	exited     chan struct{}
	once       sync.Once
}

func newFakePTY(t *testing.T) *fakePTY {
	t.Helper()
	f := &fakePTY{closed: make(chan struct{}), exited: make(chan struct{})}
	var err error
	if f.outR, f.outW, err = os.Pipe(); err != nil {
		t.Fatal(err)
	}
	if f.inR, f.inW, err = os.Pipe(); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fakePTY) Read(b []byte) (int, error)  { return f.outR.Read(b) }
func (f *fakePTY) Write(b []byte) (int, error) { return f.inW.Write(b) }
func (f *fakePTY) Done() <-chan struct{}       { return f.exited }
func (f *fakePTY) Resize(r, c uint16) error {
	f.mu.Lock()
	f.sizes = append(f.sizes, [2]uint16{r, c})
	f.mu.Unlock()
	return nil
}
func (f *fakePTY) LinuxUser() string { f.mu.Lock(); defer f.mu.Unlock(); return f.user }
func (f *fakePTY) Close() error {
	f.once.Do(func() {
		close(f.closed)
		f.outR.Close()
		f.inW.Close()
	})
	return nil
}
func (f *fakePTY) exit() { close(f.exited) }

// relayEnv runs relayTerminal over a socket pair and returns the "web server"
// end.
func relayEnv(t *testing.T, p PTY) (net.Conn, chan struct{}) {
	t.Helper()
	return relayEnvPoll(t, p, time.Hour)
}

func relayEnvPoll(t *testing.T, p PTY, poll time.Duration) (net.Conn, chan struct{}) {
	t.Helper()
	a, b := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		relayTerminal(context.Background(), a, a, p, poll)
	}()
	t.Cleanup(func() { b.Close() })
	return b, done
}

func waitClosed(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timeout: %s", what)
	}
}

func TestRelayBothWaysAndResize(t *testing.T) {
	p := newFakePTY(t)
	web, done := relayEnv(t, p)

	// output to the web server
	go p.outW.Write([]byte("login: "))
	typ, data, err := ReadFrame(web)
	if err != nil || typ != FrameData || string(data) != "login: " {
		t.Fatalf("%d %q %v", typ, data, err)
	}
	// input from the web server, resize in between; a resize is never typed
	if err := WriteData(web, []byte("al")); err != nil {
		t.Fatal(err)
	}
	if err := WriteResize(web, 50, 200); err != nil {
		t.Fatal(err)
	}
	if err := WriteResize(web, 9999, 0); err != nil { // clamped, not rejected
		t.Fatal(err)
	}
	if err := WriteData(web, []byte("ice")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 5)
	if _, err := io.ReadFull(p.inR, got); err != nil || string(got) != "alice" {
		t.Fatalf("%q %v", got, err)
	}
	p.mu.Lock()
	sizes := append([][2]uint16(nil), p.sizes...)
	p.mu.Unlock()
	if len(sizes) != 2 || sizes[0] != [2]uint16{50, 200} || sizes[1] != [2]uint16{500, 80} {
		t.Fatalf("sizes %v", sizes)
	}
	// closing the web side ends the session: PTY closed, relay returns
	web.Close()
	waitClosed(t, done, "relay did not end")
	select {
	case <-p.closed:
	default:
		t.Fatal("PTY not closed")
	}
}

func TestRelayEndsWhenLoginExits(t *testing.T) {
	p := newFakePTY(t)
	web, done := relayEnv(t, p)
	go func() {
		p.outW.Write([]byte("bye\r\n"))
		p.exit()
	}()
	// the last output still arrives, then the stream ends
	var all []byte
	for {
		typ, d, err := ReadFrame(web)
		if err != nil {
			break
		}
		if typ == FrameData {
			all = append(all, d...)
		}
	}
	if string(all) != "bye\r\n" {
		t.Fatalf("output %q", all)
	}
	waitClosed(t, done, "relay did not end")
	<-p.closed
}

func TestRelayBadFrameEndsSession(t *testing.T) {
	p := newFakePTY(t)
	web, done := relayEnv(t, p)
	// a user frame only flows towards the web server
	if err := WriteFrame(web, FrameUser, []byte("x")); err != nil {
		t.Fatal(err)
	}
	waitClosed(t, done, "relay accepted a user frame")
	web.Close()
}

func TestRelayContextCancel(t *testing.T) {
	p := newFakePTY(t)
	a, b := net.Pipe()
	defer b.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); relayTerminal(ctx, a, a, p, time.Hour) }()
	cancel()
	waitClosed(t, done, "relay ignored the context")
	<-p.closed
}

func TestRelayUserFrame(t *testing.T) {
	p := newFakePTY(t)
	p.mu.Lock()
	p.user = "example"
	p.mu.Unlock()
	web, done := relayEnvPoll(t, p, 10*time.Millisecond)
	typ, d, err := ReadFrame(web)
	if err != nil || typ != FrameUser || string(d) != "example" {
		t.Fatalf("%d %q %v", typ, d, err)
	}
	web.Close()
	waitClosed(t, done, "relay did not end")
}

// startTerminalServer runs a helper Server whose terminal sessions are fake.
func startTerminalServer(t *testing.T, start StartPTYFunc) *Client {
	t.Helper()
	dir, err := os.MkdirTemp("", "hlp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "h.sock")
	ln, err := ListenUnix(sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{Ops: &Ops{Run: &fakeRunner{}}, Peer: PeerUID, AllowedUIDs: func() []uint32 { return []uint32{0, uint32(os.Getuid())} }, StartTerminal: start}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = srv.Serve(ctx, ln) }()
	t.Cleanup(func() { cancel(); ln.Close(); <-done })
	return NewClient(sock)
}

func TestTerminalOpenOverSocket(t *testing.T) {
	p := newFakePTY(t)
	var gotRows, gotCols uint16
	c := startTerminalServer(t, func(rows, cols uint16) (PTY, error) {
		gotRows, gotCols = rows, cols
		return p, nil
	})
	s, err := c.OpenTerminal(context.Background(), 30, 100)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if gotRows != 30 || gotCols != 100 {
		t.Fatalf("size %dx%d", gotRows, gotCols)
	}
	go p.outW.Write([]byte("hi"))
	typ, d, err := ReadFrame(s)
	if err != nil || typ != FrameData || string(d) != "hi" {
		t.Fatalf("%d %q %v", typ, d, err)
	}
	if err := WriteData(s, []byte("x")); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 1)
	if _, err := io.ReadFull(p.inR, b); err != nil || b[0] != 'x' {
		t.Fatalf("%q %v", b, err)
	}
	// the web server going away hangs the session up
	s.Close()
	select {
	case <-p.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("PTY not closed after the client left")
	}
}

func TestTerminalOpenRefusals(t *testing.T) {
	c := startTerminalServer(t, func(rows, cols uint16) (PTY, error) {
		return nil, errors.New("terminal not available: login not found")
	})
	if _, err := c.OpenTerminal(context.Background(), 0, 0); err == nil || !strings.Contains(err.Error(), "login not found") {
		t.Fatalf("err = %v", err)
	}
	// an invalid size never reaches the starter
	called := false
	c = startTerminalServer(t, func(rows, cols uint16) (PTY, error) { called = true; return nil, errors.New("x") })
	if _, err := c.OpenTerminal(context.Background(), 600, 80); err == nil || !strings.Contains(err.Error(), "invalid size") || called {
		t.Fatalf("err = %v, called %v", err, called)
	}
	// no starter configured
	c = startTerminalServer(t, nil)
	if _, err := c.OpenTerminal(context.Background(), 0, 0); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("err = %v", err)
	}
	// the terminal field is refused for any other op
	if _, err := (&Ops{Run: &fakeRunner{}}).HandleRequest(context.Background(), Request{Op: OpNetStatus, Terminal: &TerminalRequest{}}); err == nil {
		t.Fatal("terminal field accepted on another op")
	}
}
