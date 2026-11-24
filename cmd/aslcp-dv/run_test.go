package main

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/dv/ctl"
	"github.com/artw1/aslcp/internal/dv/usrp"
	"github.com/artw1/aslcp/internal/dv/vocoder/fake"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func TestRunBadConfigExits78(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "c.json")
	os.WriteFile(cfg, []byte(`{"version":1,"nonsense":true}`), 0o600)
	var eb syncBuf
	rc := run(context.Background(), []string{"-config", cfg, "-ctl", filepath.Join(dir, "s")}, &eb)
	if rc != 78 || !strings.Contains(eb.String(), "config") {
		t.Fatalf("rc=%d %s", rc, eb.String())
	}
}

func TestRunEndToEnd(t *testing.T) {
	srv, err := fake.New("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	peer, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	// Find a free UDP port for the engine.
	tmp, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	listen := tmp.LocalAddr().String()
	tmp.Close()

	dir, _ := os.MkdirTemp("", "dvrun")
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "ctl.sock")
	cfgPath := filepath.Join(dir, "c.json")
	write := func(timeoutMS string) {
		doc := `{"version":1,"usrp":{"agcRx":false,"listen":"` + listen + `","peer":"` + peer.LocalAddr().String() + `"},
		 "vocoder":{"md380":"` + srv.Addr() + `","timeoutMs":` + timeoutMS + `},
		 "identity":{"callsign":"N0CALL","dmrId":1234567},
		 "networks":[{"mode":"LOOP","enabled":true,"options":{"playback_delay_ms":"10"}}],
		 "ctl":{"allowUids":[` + itoa(os.Getuid()) + `]}}`
		os.WriteFile(cfgPath, []byte(doc), 0o600)
	}
	write("200")

	ctx, cancel := context.WithCancel(context.Background())
	var eb syncBuf
	done := make(chan int, 1)
	go func() { done <- run(ctx, []string{"-config", cfgPath, "-ctl", sock}, &eb) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("run did not stop")
		}
	}()

	c := ctl.NewClient(sock)
	var st ctl.Status
	deadline := time.Now().Add(5 * time.Second)
	for {
		st, err = c.Status(ctx)
		if err == nil && st.Version != "" && st.Networks != nil && len(st.Networks) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no status: %v %+v\n%s", err, st, eb.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if st.USRP.Listen != listen || len(st.Networks) != 1 || !st.Networks[0].Available {
		t.Fatalf("%+v", st)
	}
	if st, err = c.Tune(ctx, ctl.TuneArgs{Mode: ctl.ModeLoop, Target: ctl.Target{TG: 9990}}); err != nil || st.Mode != "LOOP" {
		t.Fatal(st, err)
	}

	// One call through the whole stack.
	dst, _ := net.ResolveUDPAddr("udp", listen)
	var pcm [160]int16
	for k := 0; k < 10; k++ {
		for i := range pcm {
			pcm[i] = int16(500 + k)
		}
		p := usrp.Packet{Seq: uint32(k), Keyup: true, PCM: &pcm}
		peer.WriteToUDP(p.Append(nil), dst)
		time.Sleep(20 * time.Millisecond)
	}
	peer.WriteToUDP((&usrp.Packet{Seq: 10}).Append(nil), dst)
	got := 0
	buf := make([]byte, 2048)
	peer.SetReadDeadline(time.Now().Add(3 * time.Second))
	for got < 10 {
		n, _, err := peer.ReadFromUDP(buf)
		if err != nil {
			t.Fatalf("got %d frames: %v", got, err)
		}
		if p, err := usrp.Parse(buf[:n]); err == nil && p.PCM != nil {
			if p.PCM[0] != int16(500+got) {
				t.Fatalf("frame %d = %d", got, p.PCM[0])
			}
			got++
		}
	}

	// Reload: a bad file keeps the old config; a good one is applied.
	os.WriteFile(cfgPath, []byte(`{"version":1,"zzz":1}`), 0o600)
	if _, err := c.Reload(ctx); err == nil {
		t.Fatal("bad reload accepted")
	} else if ce, ok := err.(*ctl.Error); !ok || ce.Code != ctl.CodeBadConfig {
		t.Fatalf("%v", err)
	}
	write("100")
	if _, err := c.Reload(ctx); err != nil {
		t.Fatal(err)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
