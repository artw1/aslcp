package rpt

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/ami"
	"github.com/artw1/aslcp/internal/ami/amitest"
)

func ctxT(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func connect(t *testing.T, f *fakeNode) *Client {
	t.Helper()
	conn, err := ami.Dial(ctxT(t), f.cfg())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	c := New(conn)
	c.MinGap = 5 * time.Millisecond // pacing is covered in pace_test.go
	return c
}

func TestLocalNodesXML(t *testing.T) {
	f := newFakeNode(t, "1999")
	nodes, err := connect(t, f).LocalNodes(ctxT(t))
	if err != nil || !reflect.DeepEqual(nodes, []string{"1999"}) {
		t.Fatalf("%v %v", nodes, err)
	}
	if f.Count("RptStatus") != 0 {
		t.Error("fell back unnecessarily")
	}
}

func TestLocalNodesFallsBackToRptStat(t *testing.T) {
	f := newFakeNode(t, "1999")
	// Unknown action: server answers Response: Error.
	f.Handle("RptLocalNodes", func(_ *amitest.Session, r *amitest.Request) string {
		return amitest.Error(r, "Invalid/unknown command")
	})
	nodes, err := connect(t, f).LocalNodes(ctxT(t))
	if err != nil || !reflect.DeepEqual(nodes, []string{"1999"}) {
		t.Fatalf("%v %v", nodes, err)
	}
	// Unparseable XML also falls back.
	f.Handle("RptLocalNodes", func(_ *amitest.Session, r *amitest.Request) string {
		return amitest.Raw("garbage <")
	})
	nodes, err = connect(t, f).LocalNodes(ctxT(t))
	if err != nil || len(nodes) != 1 {
		t.Fatalf("%v %v", nodes, err)
	}
}

func TestXStatSawStatNodeStatus(t *testing.T) {
	f := newFakeNode(t, "1999")
	f.set(func(f *fakeNode) {
		f.tx = true
		f.links = []string{"2000", "55555"}
		f.keyed["2000"] = true
	})
	c := connect(t, f)
	st, err := c.NodeStatus(ctxT(t), "1999")
	if err != nil {
		t.Fatal(err)
	}
	if !st.TxKeyed || st.RxKeyed || len(st.Links) != 2 || !st.Links[0].Keyed || st.Links[1].Keyed ||
		st.Links[0].SinceKey == nil || *st.Links[0].SinceKey != 3*time.Second || st.Links[1].SinceKey != nil ||
		st.Links[0].Mode != ModeTransceive || st.Links[0].Connected != time.Minute || len(st.Network) != 2 {
		t.Fatalf("status = %+v", st)
	}
	reqs := f.Requests()
	var sawNode bool
	for _, r := range reqs {
		if r.Action == "RptStatus" && r.Get("Command") == "XStat" && r.Get("Node") == "1999" {
			sawNode = true
		}
	}
	if !sawNode {
		t.Error("XStat request malformed")
	}

	if _, err := c.NodeStatus(ctxT(t), "4242"); err == nil {
		t.Error("unknown node did not error")
	} else {
		var re *ami.ResponseError
		if !errors.As(err, &re) {
			t.Errorf("want ResponseError, got %v", err)
		}
	}
	for _, bad := range []string{"", "19 99", "1999\r\nAction: Logoff", "abc", "12345678901"} {
		if _, err := c.XStat(ctxT(t), bad); err == nil {
			t.Errorf("node %q accepted", bad)
		}
		if _, err := c.SawStat(ctxT(t), bad); err == nil {
			t.Errorf("node %q accepted by SawStat", bad)
		}
	}
}

func TestUptime(t *testing.T) {
	f := newFakeNode(t, "1999")
	f.set(func(f *fakeNode) {
		f.cmdOut["core show uptime seconds"] = []string{"System uptime: 90", "Last reload: 30"}
	})
	u, err := connect(t, f).Uptime(ctxT(t))
	if err != nil || u.System != 90*time.Second || u.LastReload != 30*time.Second {
		t.Fatalf("%+v %v", u, err)
	}
}

func TestLinkControlCommands(t *testing.T) {
	f := newFakeNode(t, "1999")
	c := connect(t, f)
	ctx := ctxT(t)
	steps := []struct {
		do   func() error
		want string
	}{
		{func() error { return c.Connect(ctx, "1999", "2000", ConnTransceive, false) }, "rpt cmd 1999 ilink 3 2000"},
		{func() error { return c.Connect(ctx, "1999", "2000", ConnMonitor, false) }, "rpt cmd 1999 ilink 2 2000"},
		{func() error { return c.Connect(ctx, "1999", "2000", ConnLocalMonitor, false) }, "rpt cmd 1999 ilink 8 2000"},
		{func() error { return c.Connect(ctx, "1999", "2000", ConnTransceive, true) }, "rpt cmd 1999 ilink 13 2000"},
		{func() error { return c.Connect(ctx, "1999", "2000", ConnMonitor, true) }, "rpt cmd 1999 ilink 12 2000"},
		{func() error { return c.Connect(ctx, "1999", "2000", ConnLocalMonitor, true) }, "rpt cmd 1999 ilink 18 2000"},
		{func() error { return c.Connect(ctx, "1999", "3141592", ConnTransceive, false) }, "rpt cmd 1999 ilink 3 3141592"},
		{func() error { return c.Disconnect(ctx, "1999", "2000", false) }, "rpt cmd 1999 ilink 1 2000"},
		{func() error { return c.Disconnect(ctx, "1999", "2000", true) }, "rpt cmd 1999 ilink 11 2000"},
		{func() error { return c.Disconnect(ctx, "1999", "N0CALL", false) }, "rpt cmd 1999 ilink 1 N0CALL"},
		{func() error { return c.Disconnect(ctx, "1999", "W1AW-P", false) }, "rpt cmd 1999 ilink 1 W1AW-P"},
		{func() error { return c.DisconnectAll(ctx, "1999") }, "rpt cmd 1999 ilink 6"},
	}
	for _, s := range steps {
		if err := s.do(); err != nil {
			t.Fatalf("%s: %v", s.want, err)
		}
	}
	var want []string
	for _, s := range steps {
		want = append(want, s.want)
	}
	if got := f.cmds(); !reflect.DeepEqual(got, want) {
		t.Errorf("commands:\n got %q\nwant %q", got, want)
	}
}

func TestLinkControlValidation(t *testing.T) {
	f := newFakeNode(t, "1999")
	c := connect(t, f)
	ctx := ctxT(t)
	evil := []string{"", "0", "01", "2000 2001", "2000;core restart now", "2000\nrpt cmd 1999 ilink 6",
		"2000\r\n", "-1", "2000-P", "N0CALL", "$(x)", "2000,2001", "12345678901", " 2000"}
	for _, e := range evil {
		if err := c.Connect(ctx, "1999", e, ConnTransceive, false); err == nil {
			t.Errorf("Connect target %q accepted", e)
		}
	}
	for _, e := range evil {
		if e == "2000-P" || e == "N0CALL" || e == "12345678901" {
			continue // valid or checked separately for disconnect
		}
		if err := c.Disconnect(ctx, "1999", e, false); err == nil {
			t.Errorf("Disconnect target %q accepted", e)
		}
	}
	for _, e := range []string{"a b", "x;y", "-abc", "abc\n", strings.Repeat("A", 21), "0abc", "a/b", "a|b", "a`b"} {
		if err := c.Disconnect(ctx, "1999", e, false); err == nil {
			t.Errorf("Disconnect target %q accepted", e)
		}
	}
	for _, n := range []string{"", "abc", "19 99", "1999\n", "1;2", "-1"} {
		if c.Connect(ctx, n, "2000", ConnMonitor, false) == nil || c.Disconnect(ctx, n, "2000", false) == nil || c.DisconnectAll(ctx, n) == nil {
			t.Errorf("node %q accepted", n)
		}
	}
	if c.Connect(ctx, "1999", "1999", ConnMonitor, false) == nil {
		t.Error("self connect accepted")
	}
	if c.Connect(ctx, "1999", "2000", ConnectMode(99), false) == nil {
		t.Error("invalid mode accepted")
	}
	if got := f.cmds(); len(got) != 0 {
		t.Errorf("invalid input reached the server: %q", got)
	}
}

func TestLinkControlFailureOutput(t *testing.T) {
	f := newFakeNode(t, "1999")
	f.set(func(f *fakeNode) {
		f.cmdOut["rpt cmd 1999 ilink 3 2000"] = []string{"Node 1999 is not ready."}
		f.cmdOut["rpt cmd 4242 ilink 1 2000"] = []string{"Unknown node number 4242."}
		f.cmdOut["rpt cmd 1999 ilink 1 2001"] = []string{"Usage: rpt cmd <nodename> <cmd-name> <cmd-index> <cmd-args>"}
	})
	c := connect(t, f)
	ctx := ctxT(t)
	if err := c.Connect(ctx, "1999", "2000", ConnTransceive, false); err == nil || !strings.Contains(err.Error(), "not ready") {
		t.Errorf("err = %v", err)
	}
	if err := c.Disconnect(ctx, "4242", "2000", false); err == nil || !strings.Contains(err.Error(), "Unknown node") {
		t.Errorf("err = %v", err)
	}
	if err := c.Disconnect(ctx, "1999", "2001", false); err == nil {
		t.Error("usage output not treated as failure")
	}
}

func TestClientOverReconnectingAMIClient(t *testing.T) {
	// *ami.Client satisfies API and Source.
	f := newFakeNode(t, "1999")
	ac := ami.NewClient(f.cfg())
	defer ac.Close()
	var _ Source = ac
	if err := ac.WaitConnected(ctxT(t)); err != nil {
		t.Fatal(err)
	}
	nodes, err := New(ac).LocalNodes(ctxT(t))
	if err != nil || len(nodes) != 1 {
		t.Fatalf("%v %v", nodes, err)
	}
}

func TestRestartAsteriskConnectionDropAfterWriteIsSuccess(t *testing.T) {
	f := newFakeNode(t, "1999")
	f.Handle("Command", func(s *amitest.Session, r *amitest.Request) string {
		f.mu.Lock()
		f.commands = append(f.commands, r.Get("Command"))
		f.mu.Unlock()
		s.Close() // an idle Asterisk restarts and hangs up without a reply
		return ""
	})
	c := connect(t, f)
	if err := c.RestartAsterisk(ctxT(t)); err != nil {
		t.Fatalf("a drop after the command was sent is a restart: %v", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.commands) != 1 || f.commands[0] != "core restart gracefully" {
		t.Fatalf("commands %v", f.commands)
	}
}

func TestRestartAsteriskErrorBeforeWriteIsError(t *testing.T) {
	f := newFakeNode(t, "1999")
	conn, err := ami.Dial(ctxT(t), f.cfg())
	if err != nil {
		t.Fatal(err)
	}
	c := New(conn)
	conn.Close() // closed before the command is written
	err = c.RestartAsterisk(ctxT(t))
	if err == nil || errors.Is(err, ami.ErrLostAfterSend) {
		t.Fatalf("want a plain closed error, got %v", err)
	}
	// Which close error wins (ErrClosed or the read loop's EOF) is a race;
	// either is fine as long as it is not "lost after send".
	if n := f.Count("Command"); n != 0 {
		t.Fatalf("command was sent: %d", n)
	}
}

func TestRestartAsteriskNotConnectedIsError(t *testing.T) {
	cl := ami.NewClient(ami.Config{Addr: "127.0.0.1:1", Username: "u", Secret: "s", ReconnectMin: time.Hour, ReconnectMax: time.Hour})
	t.Cleanup(func() { cl.Close() })
	if err := New(cl).RestartAsterisk(ctxT(t)); !errors.Is(err, ami.ErrNotConnected) {
		t.Fatalf("got %v", err)
	}
}

func TestRestartAsteriskCommandErrorIsError(t *testing.T) {
	f := newFakeNode(t, "1999")
	f.Handle("Command", func(_ *amitest.Session, r *amitest.Request) string { return amitest.Error(r, "denied") })
	if err := connect(t, f).RestartAsterisk(ctxT(t)); err == nil {
		t.Fatal("a rejected command is an error")
	}
}
