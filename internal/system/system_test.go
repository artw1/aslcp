package system

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func put(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestHostMetrics(t *testing.T) {
	d := t.TempDir()
	c := New("1.2.3", nil)
	c.ThermalP = put(t, d, "temp", "48234\n")
	c.LoadP = put(t, d, "load", "0.12 0.10 0.05 1/200 1234\n")
	c.MemP = put(t, d, "mem", "MemTotal:        1000 kB\nMemFree:  10 kB\nMemAvailable:    500 kB\n")
	c.UptimeP = put(t, d, "up", "120660.55 4000.1\n")
	c.Hostname = func() (string, error) { return "node", nil }
	s := c.Collect(context.Background())
	if s.CPUTemp == nil || *s.CPUTemp != 48.2 || s.Load != [3]float64{0.12, 0.10, 0.05} ||
		s.MemTotal != 1000*1024 || s.MemAvailable != 500*1024 || s.Uptime != 120660 || s.Hostname != "node" || s.Version != "1.2.3" {
		t.Fatalf("%+v", s)
	}
	if s.Node.Running || s.Node.Error == nil {
		t.Fatalf("%+v", s.Node)
	}
}

func TestMissingFiles(t *testing.T) {
	c := New("v", nil)
	c.ThermalP, c.LoadP, c.MemP, c.UptimeP = "/nonexistent/a", "/nonexistent/b", "/nonexistent/c", "/nonexistent/d"
	s := c.Collect(context.Background())
	if s.CPUTemp != nil || s.Uptime != 0 || s.MemTotal != 0 {
		t.Fatalf("%+v", s)
	}
	d := t.TempDir()
	if ReadCPUTemp(put(t, d, "t", "garbage")) != nil || ReadCPUTemp(put(t, d, "t2", "9999999")) != nil {
		t.Fatal("bad temp accepted")
	}
	if _, err := ReadLoad(put(t, d, "l", "1 2")); err == nil {
		t.Fatal("short loadavg accepted")
	}
	if _, _, err := ReadMem(put(t, d, "m", "MemTotal: 1 kB\n")); err == nil {
		t.Fatal("incomplete meminfo accepted")
	}
}

func TestNode(t *testing.T) {
	t0 := time.Unix(1_800_000_000, 0)
	cases := []struct {
		name string
		info NodeInfo
		err  error
		want func(n Node) bool
	}{
		{"running", NodeInfo{Started: t0.Add(-90 * time.Second), Mode: "ysf"}, nil, func(n Node) bool {
			return n.Running && n.Mode == "ysf" && n.Uptime != nil && *n.Uptime == 90 && n.Error == nil
		}},
		{"idle", NodeInfo{Started: t0}, nil, func(n Node) bool { return n.Running && n.Mode == "" && *n.Uptime == 0 }},
		{"down", NodeInfo{}, errors.New("the node is not running"), func(n Node) bool {
			return !n.Running && n.Uptime == nil && n.Error != nil && *n.Error == "the node is not running"
		}},
	}
	for _, c := range cases {
		cl := New("v", func(context.Context) (NodeInfo, error) { return c.info, c.err })
		cl.Now = func() time.Time { return t0 }
		cl.ThermalP, cl.LoadP, cl.MemP, cl.UptimeP = "/x", "/x", "/x", "/x"
		if n := cl.Collect(context.Background()).Node; !c.want(n) {
			t.Errorf("%s: %+v", c.name, n)
		}
	}
}
