//go:build linux

package helper

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// These tests run term-host in-process on a real PTY with /bin/sh. Opening a
// PTY and a session needs no privilege in a test directory, but the cgroup
// and utmp lookups are replaced, so they run for any uid.

type hostRun struct {
	dir    string
	id     string
	sock   string
	done   chan struct{}
	reason string
	err    error
	cancel context.CancelFunc
}

func startHost(t *testing.T, id string, owner int64, mut func(*termHostCfg)) *hostRun {
	t.Helper()
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	dir, err := os.MkdirTemp("", "th")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	h := &hostRun{dir: dir, id: id, sock: filepath.Join(dir, id+".sock"), done: make(chan struct{})}
	cfg := termHostCfg{
		termHostArgs: termHostArgs{ID: id, Sock: h.sock, Rows: 24, Cols: 80, Owner: owner},
		Dir:          dir,
		Argv:         []string{"/bin/sh"},
		Env:          []string{"PATH=/usr/bin:/bin", "TERM=dumb", "PS1=$ "},
		Busy:         func() bool { return false },
		User:         func(string) string { return "" },
		Tick:         10 * time.Millisecond,
	}
	if mut != nil {
		mut(&cfg)
	}
	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel
	t.Cleanup(func() { cancel(); <-h.done })
	go func() {
		defer close(h.done)
		h.reason, h.err = runTermHost(ctx, cfg)
	}()
	// wait for the socket
	for i := 0; i < 500; i++ {
		if c, err := net.Dial("unix", h.sock); err == nil {
			c.Close()
			if _, err := os.Stat(filepath.Join(dir, id+".json")); err == nil {
				return h
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("term-host did not listen")
	return nil
}

func (h *hostRun) attach(t *testing.T) net.Conn {
	t.Helper()
	c, err := net.Dial("unix", h.sock)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteResize(c, 24, 80); err != nil {
		t.Fatal(err)
	}
	return c
}

// readUntil collects data frames until want appears.
func readUntil(t *testing.T, c net.Conn, want string) string {
	t.Helper()
	var got bytes.Buffer
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(got.String(), want) {
		_ = c.SetReadDeadline(deadline)
		typ, p, err := ReadFrame(c)
		if err != nil {
			t.Fatalf("waiting for %q: %v (got %q)", want, err, got.String())
		}
		if typ == FrameData {
			got.Write(p)
		}
	}
	return got.String()
}

func (h *hostRun) info(t *testing.T) TerminalInfo {
	t.Helper()
	var info TerminalInfo
	data, err := os.ReadFile(filepath.Join(h.dir, h.id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatal(err)
	}
	return info
}

func (h *hostRun) waitInfo(t *testing.T, ok func(TerminalInfo) bool) {
	t.Helper()
	for i := 0; i < 500; i++ {
		var info TerminalInfo
		if data, err := os.ReadFile(filepath.Join(h.dir, h.id+".json")); err == nil && json.Unmarshal(data, &info) == nil && ok(info) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("state file never reached the expected state")
}

func (h *hostRun) waitEnd(t *testing.T) TerminalEnded {
	t.Helper()
	select {
	case <-h.done:
	case <-time.After(15 * time.Second):
		t.Fatal("term-host did not end")
	}
	var rec TerminalEnded
	data, err := os.ReadFile(filepath.Join(h.dir, h.id+".ended.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &rec); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(h.sock); err == nil {
		t.Fatal("socket left behind")
	}
	if _, err := os.Stat(filepath.Join(h.dir, h.id+".json")); err == nil {
		t.Fatal("state file left behind")
	}
	return rec
}

func TestTermHostSessionDetachReplayAndEnd(t *testing.T) {
	h := startHost(t, "00000000000000e1", 7, nil)
	st, err := os.Stat(h.sock)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode: %v %v", st, err)
	}
	if st, _ := os.Stat(h.dir); st.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %v", st.Mode())
	}
	if info := h.info(t); info.Attached || info.Owner != 7 {
		t.Fatalf("%+v", info)
	}

	c := h.attach(t)
	h.waitInfo(t, func(i TerminalInfo) bool { return i.Attached })
	if err := WriteData(c, []byte("echo MARK$((1+2))\n")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, c, "MARK3")

	// a second client is refused while one is attached
	c2 := h.attach(t)
	_ = c2.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, _, err := ReadFrame(c2); err == nil {
		t.Fatal("second client was served")
	}
	c2.Close()
	if !h.info(t).Attached {
		t.Fatal("refused client detached the first")
	}

	// transport loss: close without an end frame
	c.Close()
	h.waitInfo(t, func(i TerminalInfo) bool { return !i.Attached && i.DetachedSince > 0 })
	select {
	case <-h.done:
		t.Fatal("session ended on a lost transport")
	default:
	}

	// output while detached lands in the ring and is replayed
	// (shell is still alive; feed it through a new attach, detach, attach)
	c = h.attach(t)
	readUntil(t, c, "MARK3") // replay of the earlier output
	if err := WriteData(c, []byte("echo SECOND$((2+2))\n")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, c, "SECOND4")
	c.Close()
	h.waitInfo(t, func(i TerminalInfo) bool { return !i.Attached })
	c = h.attach(t)
	out := readUntil(t, c, "SECOND4")
	if !strings.Contains(out, "MARK3") {
		t.Fatalf("replay lost the older output: %q", out)
	}

	// explicit end
	if err := WriteFrame(c, FrameEnd, nil); err != nil {
		t.Fatal(err)
	}
	rec := h.waitEnd(t)
	if rec.Reason != EndRequested || rec.Owner != 7 || rec.Detached {
		t.Fatalf("%+v", rec)
	}
	// the client sees the connection close
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		if _, _, err := ReadFrame(c); err != nil {
			break
		}
	}
}

func TestTermHostShellExit(t *testing.T) {
	h := startHost(t, "00000000000000e2", 1, nil)
	c := h.attach(t)
	defer c.Close()
	if err := WriteData(c, []byte("exit\n")); err != nil {
		t.Fatal(err)
	}
	rec := h.waitEnd(t)
	if rec.Reason != EndExited || rec.Detached {
		t.Fatalf("%+v", rec)
	}
}

func TestTermHostGraceEndsDetachedSession(t *testing.T) {
	h := startHost(t, "00000000000000e3", 1, func(c *termHostCfg) { c.Grace = 150 * time.Millisecond })
	c := h.attach(t)
	h.waitInfo(t, func(i TerminalInfo) bool { return i.Attached })
	c.Close()
	rec := h.waitEnd(t)
	if rec.Reason != EndGrace || !rec.Detached {
		t.Fatalf("%+v", rec)
	}
}

func TestTermHostWaitsForPackageManager(t *testing.T) {
	var busy atomic.Bool
	busy.Store(true)
	h := startHost(t, "00000000000000e4", 1, func(c *termHostCfg) {
		c.Grace = 100 * time.Millisecond
		c.Poll = 30 * time.Millisecond
		c.Busy = busy.Load
	})
	c := h.attach(t)
	h.waitInfo(t, func(i TerminalInfo) bool { return i.Attached })
	c.Close()
	select {
	case <-h.done:
		t.Fatal("session ended while a package manager ran")
	case <-time.After(700 * time.Millisecond):
	}
	// a reattach in the middle of the wait works
	c = h.attach(t)
	h.waitInfo(t, func(i TerminalInfo) bool { return i.Attached })
	c.Close()
	h.waitInfo(t, func(i TerminalInfo) bool { return !i.Attached })
	select {
	case <-h.done:
		t.Fatal("session ended while a package manager ran")
	case <-time.After(500 * time.Millisecond):
	}
	busy.Store(false)
	rec := h.waitEnd(t)
	if rec.Reason != EndAptFinished || !rec.Detached {
		t.Fatalf("%+v", rec)
	}
}

func TestTermHostHardCap(t *testing.T) {
	h := startHost(t, "00000000000000e5", 1, func(c *termHostCfg) {
		c.Grace = 50 * time.Millisecond
		c.Poll = 20 * time.Millisecond
		c.Cap = 400 * time.Millisecond
		c.Busy = func() bool { return true }
	})
	rec := h.waitEnd(t) // never attached: detached from the start
	if rec.Reason != EndGrace {
		t.Fatalf("%+v", rec)
	}
}

func TestTermHostProbeIsNotAnAttach(t *testing.T) {
	h := startHost(t, "00000000000000e6", 1, nil)
	for i := 0; i < 3; i++ {
		c, err := net.Dial("unix", h.sock)
		if err != nil {
			t.Fatal(err)
		}
		c.Close()
	}
	time.Sleep(50 * time.Millisecond)
	if h.info(t).Attached {
		t.Fatal("a probe counted as an attach")
	}
	// bad first frames and bad sizes never attach either
	c, _ := net.Dial("unix", h.sock)
	WriteData(c, []byte("x"))
	c.Close()
	c, _ = net.Dial("unix", h.sock)
	WriteResize(c, 0, 0)
	c.Close()
	c, _ = net.Dial("unix", h.sock)
	WriteResize(c, 999, 80)
	c.Close()
	time.Sleep(50 * time.Millisecond)
	if h.info(t).Attached {
		t.Fatal("an invalid client attached")
	}
	// and a real attach still works
	c = h.attach(t)
	defer c.Close()
	h.waitInfo(t, func(i TerminalInfo) bool { return i.Attached })
}

func TestTermHostStopsOnSignalContext(t *testing.T) {
	h := startHost(t, "00000000000000e7", 1, nil)
	h.cancel()
	rec := h.waitEnd(t)
	if rec.Reason != EndStopped {
		t.Fatalf("%+v", rec)
	}
}

// The backend against real term-host sessions: ownership, list, adoption.
func TestTermBackendAttachListAdopt(t *testing.T) {
	h := startHost(t, "00000000000000f1", 5, nil)
	units := "aslcp-term-00000000000000f1.service loaded active running ASLCP web terminal\n" +
		"aslcp-term-00000000000000f2.service loaded active running ASLCP web terminal\n" +
		"aslcp-term-zz.service loaded active running not ours\n"
	run := &fakeRunner{rules: map[string]resp{
		"systemctl list-units": {out: units},
		"systemctl stop":       {},
	}}
	b := &termBackend{run: run, dir: h.dir, ready: time.Second}

	// listing shows the live session and removes the stale one
	stale := filepath.Join(h.dir, "00000000000000f3.json")
	data, _ := json.Marshal(TerminalInfo{ID: "00000000000000f3", Owner: 5})
	if err := os.WriteFile(stale, data, 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := b.List(context.Background())
	if err != nil || len(l.Sessions) != 1 || l.Sessions[0].ID != h.id || l.Sessions[0].Owner != 5 {
		t.Fatalf("%+v %v", l, err)
	}
	if _, err := os.Stat(stale); err == nil {
		t.Fatal("stale state file kept")
	}

	// ownership
	if _, err := b.Attach(context.Background(), h.id, 6, 24, 80); err == nil || !strings.Contains(err.Error(), "not your session") {
		t.Fatalf("another user attached: %v", err)
	}
	if _, err := b.Attach(context.Background(), "../x", 5, 24, 80); err == nil {
		t.Fatal("invalid id accepted")
	}
	if _, err := b.Attach(context.Background(), "00000000000000ff", 5, 24, 80); err == nil {
		t.Fatal("unknown session accepted")
	}
	c, err := b.Attach(context.Background(), h.id, 5, 30, 100)
	if err != nil {
		t.Fatal(err)
	}
	h.waitInfo(t, func(i TerminalInfo) bool { return i.Attached })
	// in use
	if _, err := b.Attach(context.Background(), h.id, 5, 24, 80); err == nil || !strings.Contains(err.Error(), "in use") {
		t.Fatalf("double attach: %v", err)
	}
	c.Close()
	h.waitInfo(t, func(i TerminalInfo) bool { return !i.Attached })

	// adoption: the reachable session stays, the other unit is stopped, the
	// malformed name is ignored
	adopted, stopped, err := b.Adopt(context.Background())
	if err != nil || adopted != 1 || stopped != 1 {
		t.Fatalf("adopted %d stopped %d err %v", adopted, stopped, err)
	}
	if run.called("systemctl stop aslcp-term-00000000000000f2.service") != 1 || run.called("systemctl stop aslcp-term-00000000000000f1.service") != 0 {
		t.Fatalf("calls: %v", run.calls)
	}

	// ended records are listed once and forgotten on ack
	rec, _ := json.Marshal(TerminalEnded{ID: "00000000000000f4", Owner: 5, Ended: time.Now().Unix(), Reason: EndGrace, Detached: true})
	if err := os.WriteFile(filepath.Join(h.dir, "00000000000000f4.ended.json"), rec, 0o600); err != nil {
		t.Fatal(err)
	}
	l, _ = b.List(context.Background())
	if len(l.Ended) != 1 || l.Ended[0].Reason != EndGrace {
		t.Fatalf("%+v", l)
	}
	b.Ack([]string{"00000000000000f4", "../../etc/passwd"})
	l, _ = b.List(context.Background())
	if len(l.Ended) != 0 {
		t.Fatalf("ended record kept after ack: %+v", l)
	}
}

func TestTermBackendOpenRunsUnitThenConnects(t *testing.T) {
	dir, err := os.MkdirTemp("", "tb")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	var gotArgv string
	run := &fakeRunner{rules: map[string]resp{"systemd-run": {}, "systemctl stop": {}}}
	b := &termBackend{run: run, self: "/usr/libexec/aslcp/aslcp-helper", dir: dir, ready: 300 * time.Millisecond}
	// nothing listens: the open fails and the unit is stopped again
	_, _, err = b.Open(context.Background(), 24, 80, 3)
	if err == nil || !strings.Contains(err.Error(), "did not start") {
		t.Fatalf("err = %v", err)
	}
	run.mu.Lock()
	for _, c := range run.calls {
		if strings.HasPrefix(c, "systemd-run") {
			gotArgv = c
		}
	}
	run.mu.Unlock()
	if !strings.Contains(gotArgv, "--service-type=exec") || !strings.Contains(gotArgv, "term-host") || !strings.Contains(gotArgv, "--owner=3") {
		t.Fatalf("argv %q", gotArgv)
	}
	if run.called("systemctl stop aslcp-term-") != 1 {
		t.Fatalf("unit not stopped: %v", run.calls)
	}
	// systemd-run failing is reported without a connect attempt
	run = &fakeRunner{rules: map[string]resp{"systemd-run": {out: "Failed to start\n", code: 1}}}
	b.run = run
	if _, _, err = b.Open(context.Background(), 24, 80, 3); err == nil || !strings.Contains(err.Error(), "systemd-run failed") {
		t.Fatalf("err = %v", err)
	}
}
