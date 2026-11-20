package helper

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func waitClosed(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal(what)
	}
}

func readFrameTimeout(t *testing.T, c net.Conn) (byte, []byte, error) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	return ReadFrame(c)
}

type relayRun struct {
	web, host net.Conn
	stops     *int32
	done      chan struct{}
	mu        sync.Mutex
	result    string
}

func (r *relayRun) res() string { r.mu.Lock(); defer r.mu.Unlock(); return r.result }

// startRelay runs relayTerminal between a "web server" pipe and a "term-host"
// pipe; the test holds the far ends.
func startRelay(ctx context.Context) *relayRun {
	cl, web := net.Pipe()
	h, host := net.Pipe()
	r := &relayRun{web: web, host: host, stops: new(int32), done: make(chan struct{})}
	go func() {
		defer close(r.done)
		res := relayTerminal(ctx, cl, cl, h, func() { atomic.AddInt32(r.stops, 1) })
		r.mu.Lock()
		r.result = res
		r.mu.Unlock()
	}()
	return r
}

func TestRelayBothWaysAndResize(t *testing.T) {
	r := startRelay(context.Background())
	defer r.host.Close()
	go WriteData(r.host, []byte("login: "))
	typ, p, err := readFrameTimeout(t, r.web)
	if err != nil || typ != FrameData || string(p) != "login: " {
		t.Fatalf("%d %q %v", typ, p, err)
	}
	go WriteFrame(r.host, FrameUser, []byte("alice"))
	if typ, p, _ := readFrameTimeout(t, r.web); typ != FrameUser || string(p) != "alice" {
		t.Fatalf("%d %q", typ, p)
	}
	go WriteData(r.web, []byte("x"))
	if typ, p, _ := readFrameTimeout(t, r.host); typ != FrameData || string(p) != "x" {
		t.Fatalf("%d %q", typ, p)
	}
	go WriteResize(r.web, 900, 0)
	typ, p, _ = readFrameTimeout(t, r.host)
	rows, cols, _ := ParseResize(p)
	if typ != FrameResize || rows != MaxRows || cols != DefaultCols {
		t.Fatalf("%d %d %d", typ, rows, cols)
	}
	r.web.Close()
	waitClosed(t, r.done, "relay did not end")
}

// A client that goes away without an end frame is a transport loss: the relay
// only closes the term-host connection and never stops the unit.
func TestRelayTransportLossDetaches(t *testing.T) {
	r := startRelay(context.Background())
	r.web.Close()
	waitClosed(t, r.done, "relay did not end")
	if r.res() != "detached" || atomic.LoadInt32(r.stops) != 0 {
		t.Fatalf("result %q, stops %d", r.res(), *r.stops)
	}
	_ = r.host.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, _, err := ReadFrame(r.host); err == nil {
		t.Fatal("term-host connection still open")
	}
}

func TestRelayExplicitEndStopsSession(t *testing.T) {
	r := startRelay(context.Background())
	defer r.host.Close()
	go WriteFrame(r.web, FrameEnd, nil)
	waitClosed(t, r.done, "relay did not end")
	if r.res() != "ended" || atomic.LoadInt32(r.stops) != 1 {
		t.Fatalf("result %q, stops %d", r.res(), *r.stops)
	}
}

// The helper stopping detaches; it never stops the session.
func TestRelayHelperShutdownDetaches(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	r := startRelay(ctx)
	cancel()
	waitClosed(t, r.done, "relay did not end")
	if r.res() != "detached" || atomic.LoadInt32(r.stops) != 0 {
		t.Fatalf("result %q, stops %d", r.res(), *r.stops)
	}
	// the client sees a bare close, not an end frame
	_ = r.web.SetReadDeadline(time.Now().Add(5 * time.Second))
	if typ, _, err := ReadFrame(r.web); err == nil {
		t.Fatalf("got a frame of type %d on shutdown", typ)
	}
	r.host.Close()
}

// term-host closing its end means the session is over: the client is told.
func TestRelaySessionOverSendsEnd(t *testing.T) {
	r := startRelay(context.Background())
	r.host.Close()
	typ, _, err := readFrameTimeout(t, r.web)
	if err != nil || typ != FrameEnd {
		t.Fatalf("%d %v", typ, err)
	}
	waitClosed(t, r.done, "relay did not end")
	if r.res() != "session over" || atomic.LoadInt32(r.stops) != 0 {
		t.Fatalf("result %q, stops %d", r.res(), *r.stops)
	}
}

func TestRelayBadFramesEnd(t *testing.T) {
	r := startRelay(context.Background())
	defer r.host.Close()
	go r.web.Write([]byte{7, 0, 0})
	waitClosed(t, r.done, "bad frame did not end the relay")
	// FrameUser never flows towards term-host
	r2 := startRelay(context.Background())
	defer r2.host.Close()
	go WriteFrame(r2.web, FrameUser, []byte("root"))
	waitClosed(t, r2.done, "user frame from the client did not end the relay")
}

// fakeBackend is a TermBackend for the server tests.
type fakeBackend struct {
	mu      sync.Mutex
	hosts   []net.Conn // far ends of the sessions opened or attached
	opened  []string
	attach  map[string]int64 // id -> owner of a detached session
	ended   []string
	acked   []string
	list    TerminalList
	openErr error
}

func (f *fakeBackend) Open(_ context.Context, rows, cols uint16, owner int64) (string, net.Conn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.openErr != nil {
		return "", nil, f.openErr
	}
	h, far := net.Pipe()
	f.hosts = append(f.hosts, far)
	id := "00000000000000a1"
	f.opened = append(f.opened, id)
	return id, h, nil
}

func (f *fakeBackend) Attach(_ context.Context, id string, owner int64, rows, cols uint16) (net.Conn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	o, ok := f.attach[id]
	if !ok {
		return nil, errors.New("no such session")
	}
	if o != owner {
		return nil, errors.New("not your session")
	}
	h, far := net.Pipe()
	f.hosts = append(f.hosts, far)
	return h, nil
}

func (f *fakeBackend) List(context.Context) (TerminalList, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.list, nil
}

func (f *fakeBackend) Ack(ids []string) {
	f.mu.Lock()
	f.acked = append(f.acked, ids...)
	f.mu.Unlock()
}

func (f *fakeBackend) End(_ context.Context, id string) error {
	f.mu.Lock()
	f.ended = append(f.ended, id)
	f.mu.Unlock()
	return nil
}

func (f *fakeBackend) Adopt(context.Context) (int, int, error) { return 0, 0, nil }

func (f *fakeBackend) host(i int) net.Conn {
	for n := 0; n < 250; n++ {
		f.mu.Lock()
		if len(f.hosts) > i {
			h := f.hosts[i]
			f.mu.Unlock()
			return h
		}
		f.mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	return nil
}

func (f *fakeBackend) endedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.ended)
}

func termServer(t *testing.T, b TermBackend) *Client {
	t.Helper()
	uid := uint32(os.Getuid())
	ops := &Ops{Run: &fakeRunner{}}
	c, srv := startServer(t, ops, func(net.Conn) (uint32, error) { return uid, nil }, []uint32{uid})
	srv.Term = b
	return c
}

func TestTerminalOpenOverSocket(t *testing.T) {
	b := &fakeBackend{}
	c := termServer(t, b)
	s, id, err := c.OpenTerminal(context.Background(), 30, 100, 7)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if id != "00000000000000a1" {
		t.Fatalf("id %q", id)
	}
	host := b.host(0)
	go WriteData(host, []byte("hi"))
	typ, d, err := ReadFrame(s)
	if err != nil || typ != FrameData || string(d) != "hi" {
		t.Fatalf("%d %q %v", typ, d, err)
	}
	go WriteData(s, []byte("x"))
	if typ, d, err := readFrameTimeout(t, host); err != nil || typ != FrameData || string(d) != "x" {
		t.Fatalf("%d %q %v", typ, d, err)
	}
	// closing the stream without an end only detaches: no stop
	s.Close()
	_ = host.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, _, err := ReadFrame(host); err == nil {
		t.Fatal("term-host connection not closed after the client left")
	}
	time.Sleep(100 * time.Millisecond)
	if n := b.endedCount(); n != 0 {
		t.Fatalf("a lost transport ended the session (%d)", n)
	}
}

func TestTerminalExplicitEndOverSocket(t *testing.T) {
	b := &fakeBackend{}
	c := termServer(t, b)
	s, id, err := c.OpenTerminal(context.Background(), 0, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := WriteFrame(s, FrameEnd, nil); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 250 && b.endedCount() == 0; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.ended) != 1 || b.ended[0] != id {
		t.Fatalf("ended %v", b.ended)
	}
}

func TestTerminalAttachOwnership(t *testing.T) {
	id := "00000000000000b2"
	b := &fakeBackend{attach: map[string]int64{id: 5}}
	c := termServer(t, b)
	if _, err := c.AttachTerminal(context.Background(), id, 6, 24, 80); err == nil || !strings.Contains(err.Error(), "not your session") {
		t.Fatalf("another user attached: %v", err)
	}
	if _, err := c.AttachTerminal(context.Background(), "00000000000000ff", 5, 24, 80); err == nil {
		t.Fatal("attached to a session that does not exist")
	}
	s, err := c.AttachTerminal(context.Background(), id, 5, 24, 80)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
}

func TestTerminalListEndAndRefusals(t *testing.T) {
	b := &fakeBackend{list: TerminalList{
		Sessions: []TerminalInfo{{ID: "00000000000000c3", Owner: 2, Started: 100}},
		Ended:    []TerminalEnded{{ID: "00000000000000d4", Reason: EndGrace, Detached: true}},
	}}
	c := termServer(t, b)
	l, err := c.ListTerminals(context.Background(), []string{"00000000000000d4"})
	if err != nil || len(l.Sessions) != 1 || l.Sessions[0].Owner != 2 || len(l.Ended) != 1 {
		t.Fatalf("%+v %v", l, err)
	}
	if len(b.acked) != 1 {
		t.Fatalf("acked %v", b.acked)
	}
	if err := c.EndTerminal(context.Background(), "00000000000000c3"); err != nil || b.endedCount() != 1 {
		t.Fatalf("%v %d", err, b.endedCount())
	}
	if err := c.EndTerminal(context.Background(), "nope"); err == nil {
		t.Fatal("invalid id accepted")
	}
	b.openErr = errors.New("terminal not available: login not found")
	if _, _, err := c.OpenTerminal(context.Background(), 0, 0, 1); err == nil || !strings.Contains(err.Error(), "login not found") {
		t.Fatalf("err = %v", err)
	}
	b.openErr = nil
	if _, _, err := c.OpenTerminal(context.Background(), 600, 80, 1); err == nil || !strings.Contains(err.Error(), "invalid size") {
		t.Fatalf("err = %v", err)
	}
	if len(b.opened) != 0 {
		t.Fatal("invalid size reached the backend")
	}
	c = termServer(t, nil)
	if _, _, err := c.OpenTerminal(context.Background(), 0, 0, 1); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("err = %v", err)
	}
}

// Adoption runs before the first connection is accepted.
func TestServeAdoptsBeforeAccepting(t *testing.T) {
	ln, err := net.Listen("unix", filepath.Join(t.TempDir(), "s"))
	if err != nil {
		t.Skip(err)
	}
	defer ln.Close()
	adopted := false
	srv := &Server{AdoptTerminals: func(context.Context) { adopted = true }}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = srv.Serve(ctx, ln)
	if !adopted {
		t.Fatal("no adoption at start")
	}
}
