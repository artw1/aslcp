// Package system gathers host metrics and Asterisk status for the System
// strip. Paths are injectable for tests.
package system

import (
	"bufio"
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/artw1/aslcp/internal/ami"
	"github.com/artw1/aslcp/internal/rpt"
)

// Asterisk is the Asterisk part of System.
type Asterisk struct {
	Connected bool    `json:"connected"`
	Version   string  `json:"version"`
	Uptime    *int64  `json:"uptime"`
	Error     *string `json:"error"`
}

// System is the API object of GET /api/system.
type System struct {
	Hostname     string     `json:"hostname"`
	CPUTemp      *float64   `json:"cpuTemp"`
	Load         [3]float64 `json:"load"`
	MemTotal     uint64     `json:"memTotal"`
	MemAvailable uint64     `json:"memAvailable"`
	Uptime       int64      `json:"uptime"`
	Asterisk     Asterisk   `json:"asterisk"`
	Version      string     `json:"version"`
}

// AMI is what the collector needs from the AMI client.
type AMI interface {
	rpt.API
	State() ami.State
}

// Collector reads system information.
type Collector struct {
	Version  string
	ThermalP string // default /sys/class/thermal/thermal_zone0/temp
	LoadP    string // default /proc/loadavg
	MemP     string // default /proc/meminfo
	UptimeP  string // default /proc/uptime
	Hostname func() (string, error)
	AMI      AMI // nil means "no Asterisk" (demo without AMI)

	mu          sync.Mutex
	verSince    time.Time
	verCached   string
	verAttempts time.Time
}

// New returns a Collector with the real /proc and /sys paths.
func New(version string, a AMI) *Collector {
	return &Collector{
		Version:  version,
		ThermalP: "/sys/class/thermal/thermal_zone0/temp",
		LoadP:    "/proc/loadavg",
		MemP:     "/proc/meminfo",
		UptimeP:  "/proc/uptime",
		Hostname: os.Hostname,
		AMI:      a,
	}
}

// Collect gathers a full System snapshot. Missing files leave zero/null values.
func (c *Collector) Collect(ctx context.Context) System {
	s := System{Version: c.Version}
	if c.Hostname != nil {
		s.Hostname, _ = c.Hostname()
	}
	s.CPUTemp = ReadCPUTemp(c.ThermalP)
	s.Load, _ = ReadLoad(c.LoadP)
	s.MemTotal, s.MemAvailable, _ = ReadMem(c.MemP)
	s.Uptime, _ = ReadUptime(c.UptimeP)
	s.Asterisk = c.asterisk(ctx)
	return s
}

// ReadCPUTemp parses millidegrees C; nil if unavailable.
func ReadCPUTemp(path string) *float64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
	if err != nil {
		return nil
	}
	v := n / 1000
	if v < -50 || v > 200 {
		return nil
	}
	v = float64(int(v*10+0.5)) / 10
	return &v
}

// ReadLoad parses /proc/loadavg.
func ReadLoad(path string) ([3]float64, error) {
	var out [3]float64
	b, err := os.ReadFile(path)
	if err != nil {
		return out, err
	}
	f := strings.Fields(string(b))
	if len(f) < 3 {
		return out, errors.New("system: short loadavg")
	}
	for i := 0; i < 3; i++ {
		out[i], err = strconv.ParseFloat(f[i], 64)
		if err != nil {
			return [3]float64{}, err
		}
	}
	return out, nil
}

// ReadMem parses MemTotal and MemAvailable (kB in the file) into bytes.
func ReadMem(path string) (total, avail uint64, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	var gotT, gotA bool
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		fs := strings.Fields(v)
		if len(fs) == 0 {
			continue
		}
		n, perr := strconv.ParseUint(fs[0], 10, 64)
		if perr != nil {
			continue
		}
		switch k {
		case "MemTotal":
			total, gotT = n*1024, true
		case "MemAvailable":
			avail, gotA = n*1024, true
		}
	}
	if !gotT || !gotA {
		return total, avail, errors.New("system: meminfo incomplete")
	}
	return total, avail, sc.Err()
}

// ReadUptime parses whole seconds from /proc/uptime.
func ReadUptime(path string) (int64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return 0, errors.New("system: empty uptime")
	}
	v, err := strconv.ParseFloat(f[0], 64)
	return int64(v), err
}

// ParseVersion extracts the version from "core show version" output:
// "Asterisk 22.10.1+asl3-3.10.8-1.deb12 built by ...".
func ParseVersion(lines []string) string {
	for _, l := range lines {
		f := strings.Fields(l)
		if len(f) >= 2 && strings.EqualFold(f[0], "Asterisk") {
			return f[1]
		}
	}
	return ""
}

func (c *Collector) asterisk(ctx context.Context) Asterisk {
	if c.AMI == nil {
		e := "AMI not configured"
		return Asterisk{Error: &e}
	}
	st := c.AMI.State()
	if !st.Connected {
		a := Asterisk{}
		msg := "AMI disconnected"
		if st.Err != nil {
			msg += ": " + st.Err.Error()
		}
		a.Error = &msg
		return a
	}
	a := Asterisk{Connected: true}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if u, err := rpt.New(c.AMI).Uptime(cctx); err == nil {
		v := int64(u.System / time.Second)
		a.Uptime = &v
	} else {
		e := err.Error()
		a.Error = &e
	}
	a.Version = c.version(cctx, st)
	return a
}

// version caches `core show version` per AMI connection.
func (c *Collector) version(ctx context.Context, st ami.State) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.verSince.Equal(st.Since) && c.verCached != "" {
		return c.verCached
	}
	if c.verSince.Equal(st.Since) && time.Since(c.verAttempts) < time.Minute {
		return c.verCached
	}
	c.verSince, c.verAttempts, c.verCached = st.Since, time.Now(), ""
	lines, err := c.AMI.Command(ctx, "core show version")
	if err == nil {
		c.verCached = ParseVersion(lines)
	}
	return c.verCached
}

// AsteriskVersion returns the cached Asterisk version ("" when AMI is down
// or the version is unknown).
func (c *Collector) AsteriskVersion(ctx context.Context) string {
	if c.AMI == nil {
		return ""
	}
	st := c.AMI.State()
	if !st.Connected {
		return ""
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return c.version(cctx, st)
}
