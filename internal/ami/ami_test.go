package ami_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/ami"
	"github.com/artw1/aslcp/internal/ami/amitest"
)

func cfgFor(s *amitest.Server) ami.Config {
	return ami.Config{
		Addr: s.Addr, Username: s.Username, Secret: s.Secret, Events: "call",
		DialTimeout: 2 * time.Second,
	}
}

func ctx5(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func dialOK(t *testing.T, s *amitest.Server) *ami.Conn {
	t.Helper()
	c, err := ami.Dial(ctx5(t), cfgFor(s))
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestLoginSuccess(t *testing.T) {
	s := amitest.New(t)
	c := dialOK(t, s)
	if c.Version() != "9.0.0" {
		t.Errorf("version = %q", c.Version())
	}
	if got := s.LoginEvents(); len(got) != 1 || got[0] != "call" {
		t.Errorf("login events = %v", got)
	}
}

func TestLoginFailureAndSecretScrub(t *testing.T) {
	s := amitest.New(t)
	s.EchoSecretOnFail(true)
	cfg := cfgFor(s)
	cfg.Secret = "wrong-secret-xyz"
	_, err := ami.Dial(ctx5(t), cfg)
	if !errors.Is(err, ami.ErrAuth) {
		t.Fatalf("want ErrAuth, got %v", err)
	}
	if strings.Contains(err.Error(), "wrong-secret-xyz") {
		t.Errorf("secret leaked in error: %v", err)
	}
}

func TestBadBanner(t *testing.T) {
	s := amitest.New(t)
	s.SetBanner("SSH-2.0-OpenSSH")
	_, err := ami.Dial(ctx5(t), cfgFor(s))
	if !errors.Is(err, ami.ErrBanner) {
		t.Fatalf("want ErrBanner, got %v", err)
	}
}

func TestDialRefusedErrorHasNoSecret(t *testing.T) {
	s := amitest.New(t)
	cfg := cfgFor(s)
	s.Close()
	_, err := ami.Dial(ctx5(t), cfg)
	if err == nil || strings.Contains(err.Error(), cfg.Secret) {
		t.Fatalf("err = %v", err)
	}
}

func TestActionErrorAndHeaders(t *testing.T) {
	s := amitest.New(t)
	s.Handle("Echo", func(_ *amitest.Session, r *amitest.Request) string {
		return amitest.Success(r, "Got", r.Get("Val"))
	})
	c := dialOK(t, s)
	m, err := c.Action(ctx5(t), "Echo", ami.H("Val", "hi"))
	if err != nil || m.Get("got") != "hi" {
		t.Fatalf("m=%v err=%v", m, err)
	}
	m, err = c.Action(ctx5(t), "Nope")
	var re *ami.ResponseError
	if !errors.As(err, &re) || m == nil || re.Msg != m || !strings.Contains(err.Error(), "Invalid/unknown") {
		t.Fatalf("err = %v", err)
	}
	// header injection is rejected
	if _, err := c.Action(ctx5(t), "Echo", ami.H("Val", "x\r\nAction: Logoff")); err == nil {
		t.Error("CRLF in header value accepted")
	}
	if _, err := c.Action(ctx5(t), "Echo\r\nX", nil...); err == nil {
		t.Error("CRLF in action name accepted")
	}
	if _, err := c.Command(ctx5(t), "core show\nuptime"); err == nil {
		t.Error("newline in command accepted")
	}
}

func TestConcurrentActionsCorrelate(t *testing.T) {
	s := amitest.New(t)
	s.Handle("Echo", func(ss *amitest.Session, r *amitest.Request) string {
		// A stale reply with a foreign ActionID must be ignored.
		ss.Write("Response: Success\r\nActionID: stale-id\r\nGot: WRONG\r\n\r\n")
		time.Sleep(time.Millisecond)
		return amitest.Success(r, "Got", r.Get("Val"))
	})
	c := dialOK(t, s)
	var wg sync.WaitGroup
	errs := make(chan error, 50)
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v := fmt.Sprint(i)
			m, err := c.Action(ctx5(t), "Echo", ami.H("Val", v))
			if err != nil {
				errs <- err
			} else if m.Get("Got") != v {
				errs <- fmt.Errorf("want %s got %s", v, m.Get("Got"))
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if c.Stray() < 50 {
		t.Errorf("stray = %d, want >= 50", c.Stray())
	}
}

func TestResponseWithoutActionID(t *testing.T) {
	s := amitest.New(t)
	s.Handle("RptLocalNodes", func(_ *amitest.Session, r *amitest.Request) string {
		return amitest.Raw(`<?xml version="1.0"?>`, `<nodes>`, `  <node>1999</node>`, `</nodes>`)
	})
	s.Handle("Echo", func(_ *amitest.Session, r *amitest.Request) string {
		return amitest.Success(r, "Got", "x")
	})
	c := dialOK(t, s)
	m, err := c.Action(ctx5(t), "RptLocalNodes")
	if err != nil {
		t.Fatal(err)
	}
	if m.Response() != "" || len(m.Body) != 4 || m.Body[2] != "  <node>1999</node>" {
		t.Fatalf("body = %q fields=%v", m.Body, m.Fields)
	}
	// The connection keeps working afterwards.
	if m, err := c.Action(ctx5(t), "Echo"); err != nil || m.Get("Got") != "x" {
		t.Fatalf("follow-up: %v %v", m, err)
	}
}

func TestCommandBothFormats(t *testing.T) {
	for _, follows := range []bool{false, true} {
		t.Run(fmt.Sprint("follows=", follows), func(t *testing.T) {
			s := amitest.New(t)
			s.HandleCommands(map[string][]string{
				"core show uptime seconds": {"System uptime: 100", "", "Last reload: 7"},
			}, follows)
			c := dialOK(t, s)
			out, err := c.Command(ctx5(t), "core show uptime seconds")
			if err != nil {
				t.Fatal(err)
			}
			want := []string{"System uptime: 100", "", "Last reload: 7"}
			if strings.Join(out, "|") != strings.Join(want, "|") {
				t.Fatalf("out = %q", out)
			}
		})
	}
}

func TestBareLFAndRepeatedKeys(t *testing.T) {
	s := amitest.New(t)
	s.Handle("Multi", func(_ *amitest.Session, r *amitest.Request) string {
		return "response: success\nactionid: " + r.ActionID + "\nConn: a\nconn: b\nCONN: c\n\n"
	})
	c := dialOK(t, s)
	m, err := c.Action(ctx5(t), "Multi")
	if err != nil {
		t.Fatal(err)
	}
	if got := m.All("Conn"); strings.Join(got, ",") != "a,b,c" {
		t.Fatalf("All = %v", got)
	}
	if m.Get("CONN") != "a" || len(m.Lines()) != 5 {
		t.Fatalf("Get/Lines wrong: %v", m.Lines())
	}
}

func TestEventsFanOut(t *testing.T) {
	s := amitest.New(t)
	c := dialOK(t, s)
	a, cancelA := c.Subscribe(8)
	b, cancelB := c.Subscribe(8)
	defer cancelB()
	s.PushEvent("RPT_RXKEYED", "Node", "1999", "EventValue", "1")
	for _, ch := range []<-chan *ami.Message{a, b} {
		select {
		case m := <-ch:
			if m.Event() != "RPT_RXKEYED" || m.Get("Node") != "1999" {
				t.Fatalf("bad event %v", m.Lines())
			}
		case <-time.After(2 * time.Second):
			t.Fatal("no event")
		}
	}
	cancelA()
	if _, ok := <-a; ok {
		t.Error("channel not closed after cancel")
	}
	cancelA() // idempotent
	s.PushEvent("X")
	if m := <-b; m.Event() != "X" {
		t.Error("b missing second event")
	}
}

func TestSlowSubscriberDoesNotBlock(t *testing.T) {
	s := amitest.New(t)
	s.Handle("Echo", func(_ *amitest.Session, r *amitest.Request) string { return amitest.Success(r) })
	c := dialOK(t, s)
	slow, cancel := c.Subscribe(2)
	defer cancel()
	fast, cancelF := c.Subscribe(1000)
	defer cancelF()
	for i := 0; i < 200; i++ {
		s.PushEvent("E", "N", fmt.Sprint(i))
	}
	// Actions still work while the slow subscriber is full.
	if _, err := c.Action(ctx5(t), "Echo"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for len(fast) < 200 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if len(fast) != 200 {
		t.Fatalf("fast got %d", len(fast))
	}
	if len(slow) != 2 {
		t.Errorf("slow buffered %d", len(slow))
	}
	if c.Dropped() < 198 {
		t.Errorf("dropped = %d", c.Dropped())
	}
}

func TestActionTimeoutThenLateReplyIsNotMisattributed(t *testing.T) {
	s := amitest.New(t)
	release := make(chan struct{})
	s.Handle("Slow", func(ss *amitest.Session, r *amitest.Request) string {
		go func() { <-release; ss.Write(amitest.Success(r, "Got", "slow")) }()
		return ""
	})
	s.Handle("Echo", func(_ *amitest.Session, r *amitest.Request) string {
		return amitest.Success(r, "Got", "echo")
	})
	c := dialOK(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.Action(ctx, "Slow"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	go func() { time.Sleep(100 * time.Millisecond); close(release) }()
	m, err := c.Action(ctx5(t), "Echo")
	if err != nil || m.Get("Got") != "echo" {
		t.Fatalf("m=%v err=%v", m, err)
	}
}

func TestKeepaliveFailureClosesConn(t *testing.T) {
	s := amitest.New(t)
	cfg := cfgFor(s)
	cfg.PingInterval = 100 * time.Millisecond
	c, err := ami.Dial(ctx5(t), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	// Healthy: pings are answered and the connection stays up.
	time.Sleep(450 * time.Millisecond)
	if s.Count("Ping") == 0 {
		t.Error("no pings sent")
	}
	select {
	case <-c.Done():
		t.Fatalf("closed while healthy: %v", c.Err())
	default:
	}
	s.IgnorePing(true)
	select {
	case <-c.Done():
		if c.Err() == nil || !strings.Contains(c.Err().Error(), "keepalive") {
			t.Errorf("err = %v", c.Err())
		}
	case <-time.After(6 * time.Second):
		t.Fatal("connection not closed after ping failure")
	}
	if _, err := c.Action(ctx5(t), "Echo"); err == nil {
		t.Error("action on closed conn succeeded")
	}
}

func TestServerDropClosesConn(t *testing.T) {
	s := amitest.New(t)
	c := dialOK(t, s)
	sub, cancel := c.Subscribe(1)
	defer cancel()
	s.DropConnections()
	select {
	case <-c.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("not closed")
	}
	if _, ok := <-sub; ok {
		t.Error("subscriber channel not closed")
	}
}

func TestCloseSendsLogoff(t *testing.T) {
	s := amitest.New(t)
	c := dialOK(t, s)
	c.Close()
	deadline := time.Now().Add(2 * time.Second)
	for s.Count("Logoff") == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if s.Count("Logoff") != 1 {
		t.Error("Logoff not sent")
	}
}

func waitState(t *testing.T, ch <-chan ami.State, want bool) ami.State {
	t.Helper()
	timeout := time.After(8 * time.Second)
	for {
		select {
		case st, ok := <-ch:
			if !ok {
				t.Fatal("state channel closed")
			}
			if st.Connected == want {
				return st
			}
		case <-timeout:
			t.Fatalf("timed out waiting for connected=%v", want)
		}
	}
}

func clientCfg(s *amitest.Server) ami.Config {
	c := cfgFor(s)
	c.ReconnectMin = 20 * time.Millisecond
	c.ReconnectMax = 80 * time.Millisecond
	return c
}

func TestClientReconnectAndEventsSurvive(t *testing.T) {
	s := amitest.New(t)
	s.Handle("Echo", func(_ *amitest.Session, r *amitest.Request) string { return amitest.Success(r, "Got", "1") })
	c := ami.NewClient(clientCfg(s))
	defer c.Close()
	states, cancelS := c.SubscribeState(16)
	defer cancelS()
	events, cancelE := c.Subscribe(16)
	defer cancelE()

	waitState(t, states, true)
	if _, err := c.Action(ctx5(t), "Echo"); err != nil {
		t.Fatal(err)
	}
	s.PushEvent("One")
	if m := <-events; m.Event() != "One" {
		t.Fatal("event 1")
	}

	s.DropConnections()
	st := waitState(t, states, false)
	if st.Err == nil {
		t.Error("disconnected state has no error")
	}
	waitState(t, states, true)
	s.PushEvent("Two")
	select {
	case m := <-events:
		if m.Event() != "Two" {
			t.Fatalf("got %s", m.Event())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no event after reconnect")
	}
	if _, err := c.Action(ctx5(t), "Echo"); err != nil {
		t.Fatalf("action after reconnect: %v", err)
	}
}

func TestClientBackoffWhileServerDown(t *testing.T) {
	s := amitest.New(t)
	c := ami.NewClient(clientCfg(s))
	defer c.Close()
	states, cancel := c.SubscribeState(64)
	defer cancel()
	waitState(t, states, true)
	s.Stop()
	waitState(t, states, false)
	if _, err := c.Action(ctx5(t), "Echo"); !errors.Is(err, ami.ErrNotConnected) {
		t.Errorf("err = %v", err)
	}
	time.Sleep(400 * time.Millisecond) // several failed attempts
	if st := c.State(); st.Connected || st.Err == nil || st.Attempts < 3 {
		t.Errorf("state = %+v", st)
	}
	s.Restart()
	if err := c.WaitConnected(ctx5(t)); err != nil {
		t.Fatal(err)
	}
}

func TestClientAuthFailureRetriesWithoutLeakingSecret(t *testing.T) {
	s := amitest.New(t)
	s.EchoSecretOnFail(true)
	cfg := clientCfg(s)
	cfg.Secret = "topsecret-abc"
	c := ami.NewClient(cfg)
	defer c.Close()
	deadline := time.Now().Add(3 * time.Second)
	for c.State().Attempts < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	st := c.State()
	if st.Attempts < 2 || !errors.Is(st.Err, ami.ErrAuth) {
		t.Fatalf("state = %+v", st)
	}
	if strings.Contains(st.Err.Error(), "topsecret-abc") {
		t.Errorf("secret leaked: %v", st.Err)
	}
}

func TestClientCloseLogsOffAndClosesChannels(t *testing.T) {
	s := amitest.New(t)
	c := ami.NewClient(clientCfg(s))
	if err := c.WaitConnected(ctx5(t)); err != nil {
		t.Fatal(err)
	}
	ev, _ := c.Subscribe(1)
	st, _ := c.SubscribeState(1)
	c.Close()
	c.Close() // idempotent
	deadline := time.Now().Add(2 * time.Second)
	for s.Count("Logoff") == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if s.Count("Logoff") != 1 {
		t.Errorf("logoff count = %d", s.Count("Logoff"))
	}
	if _, ok := <-ev; ok {
		t.Error("events not closed")
	}
	for range st { // drains then closes
	}
	if _, err := c.Command(ctx5(t), "x"); !errors.Is(err, ami.ErrClosed) {
		t.Errorf("err = %v", err)
	}
}

func TestClientCloseWhileServerDown(t *testing.T) {
	s := amitest.New(t)
	cfg := clientCfg(s)
	s.Close()
	c := ami.NewClient(cfg)
	time.Sleep(60 * time.Millisecond)
	done := make(chan struct{})
	go func() { c.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close hung")
	}
}

// Parser edge cases through a raw reader are covered via the public API; the
// helper below checks multi-line legacy bodies containing blank lines.
func TestFollowsBodyWithBlankLines(t *testing.T) {
	s := amitest.New(t)
	s.HandleCommands(map[string][]string{"x": {"a", "", "b: c", "d"}}, true)
	c := dialOK(t, s)
	out, err := c.Command(ctx5(t), "x")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(out, "|") != "a||b: c|d" {
		t.Fatalf("out = %q", out)
	}
	_ = bufio.ErrBufferFull
}

func TestConnectionLostAfterSendIsTyped(t *testing.T) {
	s := amitest.New(t)
	s.Handle("Command", func(ss *amitest.Session, _ *amitest.Request) string {
		ss.Close()
		return ""
	})
	c := dialOK(t, s)
	_, err := c.Command(ctx5(t), "core restart gracefully")
	if !errors.Is(err, ami.ErrLostAfterSend) || !errors.Is(err, io.EOF) {
		t.Fatalf("got %v", err)
	}
	// once closed, a new action fails before anything is written
	_, err = c.Command(ctx5(t), "core show uptime")
	if err == nil || errors.Is(err, ami.ErrLostAfterSend) {
		t.Fatalf("got %v", err)
	}
}

func TestClientSetLoginAndReconnect(t *testing.T) {
	s := amitest.New(t)
	c := ami.NewClient(clientCfg(s))
	defer c.Close()
	states, cancel := c.SubscribeState(64)
	defer cancel()
	waitState(t, states, true)

	// the server's secret changes: the open connection stays, a new login fails
	s.SetSecret("new-secret")
	c.SetLogin("admin", "new-secret")
	if !c.State().Connected {
		t.Fatal("SetLogin must not drop the connection")
	}
	c.Reconnect()
	waitState(t, states, false)
	waitState(t, states, true)
	if st := c.State(); !st.Connected {
		t.Fatalf("state = %+v", st)
	}
	// wrong login keeps retrying without connecting
	c.SetLogin("admin", "wrong")
	c.Reconnect()
	waitState(t, states, false)
	time.Sleep(200 * time.Millisecond)
	if c.State().Connected {
		t.Fatal("connected with a wrong secret")
	}
	c.SetLogin("admin", "new-secret")
	if err := c.WaitConnected(ctx5(t)); err != nil {
		t.Fatal(err)
	}
}
