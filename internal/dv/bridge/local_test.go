package bridge_test

import (
	"context"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/dv/bridge"
	"github.com/artw1/aslcp/internal/dv/ctl"
	"github.com/artw1/aslcp/internal/dv/vocoder"
	"github.com/artw1/aslcp/internal/dv/vocoder/fake"
)

// TestLocalEndpointLoopback runs the router on an in-process endpoint: audio
// pushed by the host goes out on the loopback network, comes back as a
// received call, and the host pulls it in order.
func TestLocalEndpointLoopback(t *testing.T) {
	srv, err := fake.New("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	md := vocoder.NewMD380(srv.Addr(), 300*time.Millisecond)
	defer md.Close()
	ep := bridge.NewLocalEndpoint()
	cfg := ctl.Config{
		Version:  1,
		Identity: ctl.Identity{Callsign: "N0CALL", DMRID: 1234567},
		Networks: []ctl.NetConfig{{Mode: ctl.ModeLoop, Enabled: true, Options: map[string]string{"playback_delay_ms": "10"}}},
	}
	cfg.Vocoder.MD380 = srv.Addr()
	ev := &events{}
	r := bridge.New(bridge.Options{USRP: ep, AMBE: md, Config: cfg, Version: "test", Emit: ev.add, InitialMode: ctl.ModeLoop})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	const n = 30
	for k := 0; k < n; k++ {
		var f [160]int16
		for i := range f {
			f[i] = int16(1000 + k)
		}
		ep.Push(&f)
		if !ep.Keyed() {
			t.Fatal("not keyed after Push")
		}
		time.Sleep(20 * time.Millisecond)
	}
	ep.Unkey()
	if ep.Keyed() {
		t.Fatal("keyed after Unkey")
	}

	var got []int16
	sawActive := false
	talker := ""
	end := time.Now().Add(5 * time.Second)
	for time.Now().Before(end) && len(got) < n {
		if f, ok := ep.Pull(); ok {
			got = append(got, f[0])
		}
		if ep.Active() {
			sawActive = true
			if tk := ep.Talker(); tk != "" {
				talker = tk
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(got) != n {
		t.Fatalf("pulled %d of %d frames", len(got), n)
	}
	for k, v := range got {
		if v != int16(1000+k) {
			t.Fatalf("frame %d = %d", k, v)
		}
	}
	if !sawActive || talker == "" {
		t.Fatalf("active %v talker %q", sawActive, talker)
	}
	eventually(t, 3*time.Second, func() bool {
		ep.Pull()
		return !ep.Active()
	})
	if ev.find(ctl.EvTalkEnd, "tx") == nil || ev.find(ctl.EvTalkEnd, "rx") == nil {
		t.Fatalf("talks %v", ev.talks())
	}
	if s := ep.Stats(); s.RxFrames != n || s.TxFrames != n {
		t.Fatalf("stats %+v", s)
	}
}

func TestLocalEndpointQueue(t *testing.T) {
	ep := bridge.NewLocalEndpoint()
	if _, ok := ep.Pull(); ok || ep.Active() {
		t.Fatal("idle endpoint has audio")
	}
	ep.Unkey() // not keyed: no packet
	select {
	case p := <-ep.Rx():
		t.Fatalf("unexpected packet %+v", p)
	default:
	}
	_ = ep.SendText([]byte{0x08, 6, 'N', '0', 'C', 'A', 'L', 'L'})
	for i := 0; i < 60; i++ {
		var f [160]int16
		f[0] = int16(i)
		ep.SendVoice(f)
	}
	ep.EndTX()
	if !ep.Active() || ep.Talker() != "N0CALL" {
		t.Fatalf("active %v talker %q", ep.Active(), ep.Talker())
	}
	first, ok := ep.Pull()
	if !ok || first[0] != 10 { // the 10 oldest were dropped (cap 50)
		t.Fatalf("first %d %v", first[0], ok)
	}
	for i := 0; i < 49; i++ {
		if _, ok := ep.Pull(); !ok {
			t.Fatalf("frame %d missing", i)
		}
	}
	if _, ok := ep.Pull(); ok || ep.Active() || ep.Talker() != "" {
		t.Fatal("end marker not applied")
	}
	if s := ep.Stats(); s.TxDropped != 10 {
		t.Fatalf("dropped %d", s.TxDropped)
	}
}
