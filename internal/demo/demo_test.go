package demo

import (
	"context"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/ami"
	"github.com/artw1/aslcp/internal/rpt"
)

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestSimulatedNode(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sim, err := Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); sim.Wait() }()
	client := ami.NewClient(ami.Config{Addr: sim.Addr, Username: Username, Secret: Secret, Events: "call"})
	defer client.Close()
	wctx, wc := context.WithTimeout(ctx, 5*time.Second)
	defer wc()
	if err := client.WaitConnected(wctx); err != nil {
		t.Fatal(err)
	}
	rc := rpt.New(client)
	nodes, err := rc.LocalNodes(ctx)
	if err != nil || len(nodes) != 2 || nodes[0] != "2006" || nodes[1] != "2007" {
		t.Fatalf("%v %v", nodes, err)
	}
	if up, err := rc.Uptime(ctx); err != nil || up.System < time.Hour {
		t.Fatal(up, err)
	}

	mon := rpt.NewMonitor(client, rpt.MonitorConfig{Nodes: nodes, Interval: 100 * time.Millisecond})
	go mon.Run(ctx)
	waitFor(t, "initial poll", func() bool {
		s, _ := mon.Status("2006")
		return !s.Stale && len(s.Links) == 1
	})
	s, _ := mon.Status("2006")
	if s.Links[0].Node != "50241" || s.Links[0].Direction != rpt.DirOut || s.Links[0].Mode != rpt.ModeTransceive || len(s.Network) < 25 {
		t.Fatalf("%+v", s)
	}
	if s2, _ := mon.Status("2007"); len(s2.Links) != 0 || s2.Stale {
		t.Fatalf("2007: %+v", s2)
	}

	// links key on and off by themselves
	waitFor(t, "a link to key", func() bool {
		s, _ := mon.Status("2006")
		return len(s.Links) > 0 && s.Links[0].Keyed && s.TxKeyed
	})

	// connect / disconnect
	if err := rc.Connect(ctx, "2007", "2000", rpt.ConnTransceive, false); err != nil {
		t.Fatal(err)
	}
	mon.Refresh("2007")
	waitFor(t, "link to 2000", func() bool {
		s, _ := mon.Status("2007")
		return len(s.Links) == 1 && s.Links[0].Node == "2000"
	})
	waitFor(t, "link established with network", func() bool {
		s, _ := mon.Status("2007")
		return len(s.Links) == 1 && s.Links[0].State == rpt.StateEstablished && len(s.Network) == 6
	})
	if err := rc.Disconnect(ctx, "2007", "2000", false); err != nil {
		t.Fatal(err)
	}
	mon.Refresh("2007")
	waitFor(t, "link removed", func() bool {
		s, _ := mon.Status("2007")
		return len(s.Links) == 0 && len(s.Network) == 0
	})
	if err := rc.Connect(ctx, "9999", "2000", rpt.ConnTransceive, false); err == nil {
		t.Fatal("unknown node accepted")
	}
	if err := rc.DisconnectAll(ctx, "2006"); err != nil {
		t.Fatal(err)
	}
	mon.Refresh("2006")
	waitFor(t, "disconnect-all", func() bool {
		s, _ := mon.Status("2006")
		return len(s.Links) == 0
	})
}

func TestNodeList(t *testing.T) {
	l := NodeList()
	seen := map[string]bool{}
	for _, n := range l {
		if seen[n.Node] {
			t.Fatalf("duplicate %s", n.Node)
		}
		seen[n.Node] = true
		if n.Callsign == "" || n.Callsign[:2] != "N0" {
			t.Fatalf("non-fake callsign %+v", n)
		}
	}
	for _, n := range []string{"2006", "2007", "50241", "2000", "1999", "1021"} {
		if !seen[n] {
			t.Errorf("missing %s", n)
		}
	}
}
