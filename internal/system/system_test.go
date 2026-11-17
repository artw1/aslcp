package system

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/ami"
)

func put(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

type fakeAMI struct {
	st    ami.State
	lines map[string][]string
	calls map[string]int
}

func (f *fakeAMI) State() ami.State { return f.st }
func (f *fakeAMI) Command(_ context.Context, c string) ([]string, error) {
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[c]++
	l, ok := f.lines[c]
	if !ok {
		return nil, errors.New("nope")
	}
	return l, nil
}
func (f *fakeAMI) Action(context.Context, string, ...ami.Header) (*ami.Message, error) {
	return nil, errors.New("unused")
}

func TestHostMetrics(t *testing.T) {
	d := t.TempDir()
	c := New("1.2.3", nil)
	c.ThermalP = put(t, d, "temp", "48234\n")
	c.LoadP = put(t, d, "load", "0.12 0.10 0.05 1/200 1234\n")
	c.MemP = put(t, d, "mem", "MemTotal:        1000 kB\nMemFree:  10 kB\nMemAvailable:    500 kB\n")
	c.UptimeP = put(t, d, "up", "120660.55 4000.1\n")
	c.Hostname = func() (string, error) { return "node1999", nil }
	s := c.Collect(context.Background())
	if s.CPUTemp == nil || *s.CPUTemp != 48.2 || s.Load != [3]float64{0.12, 0.10, 0.05} ||
		s.MemTotal != 1000*1024 || s.MemAvailable != 500*1024 || s.Uptime != 120660 || s.Hostname != "node1999" || s.Version != "1.2.3" {
		t.Fatalf("%+v", s)
	}
	if s.Asterisk.Connected || s.Asterisk.Error == nil {
		t.Fatalf("%+v", s.Asterisk)
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

func TestAsterisk(t *testing.T) {
	f := &fakeAMI{
		st: ami.State{Connected: true, Since: time.Now()},
		lines: map[string][]string{
			"core show uptime seconds": {"System uptime: 120660", "Last reload: 120660"},
			"core show version":        {"Asterisk 22.10.1+asl3-3.10.8-1.deb12 built by builder @ x on a aarch64 running Linux on 2026-10-11 15:31:58 UTC"},
		},
	}
	c := New("v", f)
	c.ThermalP, c.LoadP, c.MemP, c.UptimeP = "/x", "/x", "/x", "/x"
	for i := 0; i < 3; i++ {
		a := c.Collect(context.Background()).Asterisk
		if !a.Connected || a.Version != "22.10.1+asl3-3.10.8-1.deb12" || a.Uptime == nil || *a.Uptime != 120660 || a.Error != nil {
			t.Fatalf("%+v", a)
		}
	}
	if f.calls["core show version"] != 1 {
		t.Fatalf("version not cached: %v", f.calls)
	}
	f.st = ami.State{Err: errors.New("connection refused"), Since: time.Now()}
	a := c.Collect(context.Background()).Asterisk
	if a.Connected || a.Error == nil || *a.Error != "AMI disconnected: connection refused" {
		t.Fatalf("%+v", a)
	}
}
