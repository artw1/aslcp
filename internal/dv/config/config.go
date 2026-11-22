// Package config loads and validates the aslcp-dv config file (ctl.Config).
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"

	"github.com/artw1/aslcp/internal/dv/ctl"
)

const maxSize = 1 << 20

// Default values when a field or the whole file is absent.
const (
	DefaultListen    = "127.0.0.1:34001"
	DefaultPeer      = "127.0.0.1:32001"
	DefaultMD380     = "127.0.0.1:2470"
	DefaultTimeoutMS = 60
)

var knownModes = map[string]bool{
	ctl.ModeLoop: true, ctl.ModeBM: true, ctl.ModeTGIF: true, ctl.ModeDMR: true,
	ctl.ModeYSF: true, ctl.ModeP25: true, ctl.ModeNXDN: true,
}

// Default is the config used when no file exists: the standard USRP ports,
// md380-emu on loopback and the parrot network.
func Default() ctl.Config {
	c := ctl.Config{Version: ctl.ConfigVersion}
	applyDefaults(&c)
	return c
}

func applyDefaults(c *ctl.Config) {
	if c.USRP.Listen == "" {
		c.USRP.Listen = DefaultListen
	}
	if c.USRP.Peer == "" {
		c.USRP.Peer = DefaultPeer
	}
	if c.Vocoder.MD380 == "" {
		c.Vocoder.MD380 = DefaultMD380
	}
	if c.Vocoder.TimeoutMS == 0 {
		c.Vocoder.TimeoutMS = DefaultTimeoutMS
	}
	if len(c.Networks) == 0 {
		c.Networks = []ctl.NetConfig{{Mode: ctl.ModeLoop, Enabled: true}}
	}
}

// Load reads path. A missing file yields Default() and a nil error; the
// returned bool tells whether a file was read.
func Load(path string) (ctl.Config, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Default(), false, nil
		}
		return ctl.Config{}, false, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxSize+1))
	if err != nil {
		return ctl.Config{}, false, err
	}
	if len(b) > maxSize {
		return ctl.Config{}, false, errors.New("config too large")
	}
	c, err := Parse(b)
	return c, err == nil, err
}

// Parse decodes and validates a config document strictly: unknown fields and
// trailing data are errors.
func Parse(b []byte) (ctl.Config, error) {
	var c ctl.Config
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return ctl.Config{}, fmt.Errorf("config: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return ctl.Config{}, errors.New("config: trailing data")
	}
	if err := Validate(&c); err != nil {
		return ctl.Config{}, err
	}
	return c, nil
}

// Validate checks c and fills defaults in place.
func Validate(c *ctl.Config) error {
	if c.Version != ctl.ConfigVersion {
		return fmt.Errorf("config: unsupported version %d (want %d)", c.Version, ctl.ConfigVersion)
	}
	applyDefaults(c)
	for _, a := range []struct{ name, v string }{
		{"usrp.listen", c.USRP.Listen}, {"usrp.peer", c.USRP.Peer}, {"vocoder.md380", c.Vocoder.MD380},
	} {
		if err := hostPort(a.v); err != nil {
			return fmt.Errorf("config: %s: %w", a.name, err)
		}
	}
	if c.Vocoder.TimeoutMS < 1 || c.Vocoder.TimeoutMS > 1000 {
		return fmt.Errorf("config: vocoder.timeoutMs %d out of range 1..1000", c.Vocoder.TimeoutMS)
	}
	if c.USRP.GainRx < -20 || c.USRP.GainRx > 20 || c.USRP.GainTx < -20 || c.USRP.GainTx > 20 {
		return errors.New("config: usrp gain out of range -20..20")
	}
	seen := map[string]bool{}
	for i, n := range c.Networks {
		if !knownModes[n.Mode] {
			return fmt.Errorf("config: networks[%d]: unknown mode %q", i, n.Mode)
		}
		if seen[n.Mode] {
			return fmt.Errorf("config: networks[%d]: duplicate mode %q", i, n.Mode)
		}
		seen[n.Mode] = true
		if n.Port < 0 || n.Port > 65535 {
			return fmt.Errorf("config: networks[%d]: bad port %d", i, n.Port)
		}
	}
	for _, u := range c.Ctl.AllowUIDs {
		if u < 0 {
			return errors.New("config: ctl.allowUids: negative uid")
		}
	}
	return nil
}

func hostPort(s string) error {
	h, p, err := net.SplitHostPort(s)
	if err != nil {
		return err
	}
	if h == "" {
		return errors.New("empty host")
	}
	n, err := strconv.Atoi(p)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("bad port %q", p)
	}
	return nil
}
