// Package dv is the digital voice modes: BrandMeister, TGIF, other DMR
// Homebrew masters, YSF, P25, NXDN, M17 and the local echo test. Each is a
// mode.Mode over one internal/dv/network client, with the radio's audio
// encoded to the network's codec (AMBE through md380-emu, IMBE or Codec2
// in Go) and the network's voice decoded for the radio.
package dv

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/artw1/aslcp/internal/dv/ctl"
	"github.com/artw1/aslcp/internal/native/mode"

	// The networks register themselves with internal/dv/network.
	_ "github.com/artw1/aslcp/internal/dv/network/bm"
	_ "github.com/artw1/aslcp/internal/dv/network/homebrew"
	_ "github.com/artw1/aslcp/internal/dv/network/loopback"
	_ "github.com/artw1/aslcp/internal/dv/network/m17"
	_ "github.com/artw1/aslcp/internal/dv/network/nxdn"
	_ "github.com/artw1/aslcp/internal/dv/network/p25"
	_ "github.com/artw1/aslcp/internal/dv/network/ysf"
)

// Setting keys with a meaning of their own. Every other field of a DV
// mode is passed to the network as an option of the same name.
const (
	KeyDMRID    = "dmr_id"
	KeyPassword = "password"
	KeyAddress  = "address" // host or host:port of the master or default reflector
	KeyOptions  = "options" // extra network options, "key=value, key=value"
)

// targetKind says how a Target ID maps to the network's ctl.Target.
type targetKind int

const (
	targetTG        targetKind = iota // a talkgroup number
	targetReflector                   // a reflector name, resolved through Settings.Reflectors or host:port
	targetM17                         // "M17-XXX B": reflector and module
	targetNone                        // the echo test: no target
)

// def is one DV mode.
type def struct {
	name, title string
	ctlMode     string
	login       bool // logs in to a master (DMR networks)
	target      targetKind
	needsHost   bool // a target must resolve to a host (P25)
	label, help string
	fields      []mode.Field
	check       func(mode.Settings) error
}

var dmrIDField = mode.Field{Key: KeyDMRID, Label: "DMR ID", Kind: mode.FieldNumber, Required: true,
	Help: "Your 7-digit DMR radio ID."}

func advanced(key, label string, kind mode.FieldKind, def, help string) mode.Field {
	return mode.Field{Key: key, Label: label, Kind: kind, Default: def, Help: help, Advanced: true}
}

var optionsField = advanced(KeyOptions, "Extra network options", mode.FieldText, "",
	"Comma-separated key=value options passed to the network client.")

var slotField = mode.Field{Key: "slot", Label: "Timeslot", Kind: mode.FieldSelect, Options: []string{"1", "2"},
	Default: "2", Advanced: true}

var defs = []def{
	{
		name: mode.BM, title: "BrandMeister", ctlMode: ctl.ModeBM, login: true, target: targetTG,
		label: "Talkgroup", help: "A BrandMeister talkgroup number. 9990 is the Parrot echo test.",
		fields: []mode.Field{
			dmrIDField,
			{Key: KeyPassword, Label: "Hotspot password", Kind: mode.FieldPassword, Required: true, Secret: true,
				Help: "The hotspot security password from your BrandMeister self-care page."},
			{Key: KeyAddress, Label: "Master server", Kind: mode.FieldText, Required: true,
				Help: "The BrandMeister master, host or host:port."},
			advanced("keepalive_ms", "Keepalive (ms)", mode.FieldNumber, "", ""),
			advanced("timeout_ms", "Master timeout (ms)", mode.FieldNumber, "", ""),
			advanced("login_timeout_ms", "Login timeout (ms)", mode.FieldNumber, "", ""),
			advanced("rx_timeout_ms", "Receive timeout (ms)", mode.FieldNumber, "", ""),
			optionsField,
		},
	},
	{
		name: mode.TGIF, title: "TGIF", ctlMode: ctl.ModeTGIF, login: true, target: targetTG,
		label: "Talkgroup", help: "A TGIF talkgroup number.",
		fields: []mode.Field{
			dmrIDField,
			{Key: KeyPassword, Label: "Hotspot password", Kind: mode.FieldPassword, Secret: true,
				Help: "The hotspot security password from your TGIF profile."},
			advanced(KeyAddress, "Master server", mode.FieldText, "", "Default: the TGIF master."),
			slotField,
			advanced("repeater_id", "Hotspot ID", mode.FieldNumber, "", "Default: the DMR ID."),
			optionsField,
		},
	},
	{
		name: mode.DMR, title: "DMR", ctlMode: ctl.ModeDMR, login: true, target: targetTG,
		label: "Talkgroup", help: "A talkgroup number on the master.",
		fields: []mode.Field{
			dmrIDField,
			{Key: KeyPassword, Label: "Master password", Kind: mode.FieldPassword, Secret: true},
			{Key: KeyAddress, Label: "Master server", Kind: mode.FieldText, Required: true,
				Help: "A Homebrew (MMDVM) master, host or host:port."},
			slotField,
			advanced("repeater_id", "Hotspot ID", mode.FieldNumber, "", "Default: the DMR ID."),
			advanced("colour_code", "Color code", mode.FieldNumber, "1", ""),
			advanced("options", "Extra network options", mode.FieldText, "", "Comma-separated key=value options, such as the master's OPTIONS line."),
		},
	},
	{
		name: mode.YSF, title: "System Fusion", ctlMode: ctl.ModeYSF, target: targetReflector,
		label: "Reflector", help: "A reflector from your list, or host:port.",
		fields: []mode.Field{
			advanced("poll_ms", "Poll interval (ms)", mode.FieldNumber, "", ""),
			advanced("timeout_ms", "Reflector timeout (ms)", mode.FieldNumber, "", ""),
			optionsField,
		},
	},
	{
		name: mode.P25, title: "P25", ctlMode: ctl.ModeP25, target: targetTG, needsHost: true,
		label: "Talkgroup", help: "A P25 talkgroup. The reflector list maps talkgroups to reflector addresses.",
		fields: []mode.Field{
			{Key: KeyDMRID, Label: "Radio ID", Kind: mode.FieldNumber, Help: "Your DMR ID, used as the P25 source ID."},
			{Key: KeyAddress, Label: "Default reflector", Kind: mode.FieldText,
				Help: "host or host:port used for talkgroups not in the reflector list."},
			optionsField,
		},
	},
	{
		name: mode.NXDN, title: "NXDN", ctlMode: ctl.ModeNXDN, target: targetTG,
		label: "Talkgroup", help: "An NXDN talkgroup. The reflector list maps talkgroups to reflector addresses.",
		fields: []mode.Field{
			{Key: "nxdn_id", Label: "NXDN ID", Kind: mode.FieldNumber, Required: true},
			{Key: KeyAddress, Label: "Default reflector", Kind: mode.FieldText,
				Help: "host or host:port used for talkgroups not in the reflector list."},
			advanced("nxdn_ran", "RAN", mode.FieldNumber, "", ""),
			optionsField,
		},
	},
	{
		name: mode.M17, title: "M17", ctlMode: ctl.ModeM17, target: targetM17,
		label: "Reflector", help: `A reflector and module, such as "M17-XXX B".`,
		fields: []mode.Field{
			{Key: KeyAddress, Label: "Default reflector address", Kind: mode.FieldText,
				Help: "host or host:port used for reflectors not in the reflector list."},
			advanced("suffix", "Callsign suffix", mode.FieldText, "", "Added to the station callsign, such as H."),
			advanced("can", "Channel access number", mode.FieldNumber, "", ""),
			optionsField,
		},
	},
	{
		name: mode.EchoTest, title: "Echo test", ctlMode: ctl.ModeLoop, target: targetNone,
		label: "", help: "Plays your transmission back through the DMR vocoder.",
		fields: []mode.Field{
			advanced("playback_delay_ms", "Playback delay (ms)", mode.FieldNumber, "", ""),
		},
	},
}

func init() {
	for _, d := range defs {
		mode.Register(d.spec())
	}
}

func (d def) spec() mode.Spec {
	return mode.Spec{
		Name: d.name, Title: d.title, TargetLabel: d.label, TargetHelp: d.help,
		Fields: d.fields, NeedsHost: d.needsHost || d.target == targetReflector,
		Validate: func(s mode.Settings) error { return d.validate(s) },
		New: func(env mode.Env, s mode.Settings) (mode.Mode, error) {
			return New(d.name, env, s, Options{})
		},
	}
}

func lookupDef(name string) (def, bool) {
	for _, d := range defs {
		if d.name == name {
			return d, true
		}
	}
	return def{}, false
}

func (d def) validate(s mode.Settings) error {
	var errs []error
	if v := s.Get(KeyDMRID, ""); v != "" {
		if n, err := strconv.ParseUint(v, 10, 32); err != nil || n == 0 || n > 0xFFFFFF {
			errs = append(errs, fmt.Errorf("DMR ID %q is not valid", v))
		}
	}
	if v := s.Get(KeyAddress, ""); v != "" {
		if _, _, err := splitAddr(v); err != nil {
			errs = append(errs, err)
		}
	}
	if _, err := parseOptions(s.Get(KeyOptions, "")); err != nil {
		errs = append(errs, err)
	}
	for _, r := range s.Reflectors {
		if strings.TrimSpace(r.ID) == "" || strings.TrimSpace(r.Host) == "" {
			errs = append(errs, fmt.Errorf("reflector list entry %q needs a name and a host", r.ID))
		}
	}
	return errors.Join(errs...)
}

// splitAddr parses "host" or "host:port".
func splitAddr(s string) (string, int, error) {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, ":"); i > 0 && !strings.Contains(s[:i], ":") {
		p, err := strconv.Atoi(s[i+1:])
		if err != nil || p <= 0 || p > 65535 {
			return "", 0, fmt.Errorf("address %q has a bad port", s)
		}
		return s[:i], p, nil
	}
	if s == "" {
		return "", 0, errors.New("empty address")
	}
	return s, 0, nil
}

// parseOptions parses "key=value, key=value".
func parseOptions(s string) (map[string]string, error) {
	out := map[string]string{}
	for _, kv := range strings.Split(s, ",") {
		kv = strings.TrimSpace(kv)
		if kv == "" {
			continue
		}
		k, v, ok := strings.Cut(kv, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("network option %q is not key=value", kv)
		}
		out[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return out, nil
}

// netConfig builds the network's config and identity from the settings.
func (d def) netConfig(env mode.Env, s mode.Settings) (ctl.NetConfig, ctl.Identity, error) {
	opts, err := parseOptions(s.Get(KeyOptions, ""))
	if err != nil {
		return ctl.NetConfig{}, ctl.Identity{}, err
	}
	cfg := ctl.NetConfig{Mode: d.ctlMode, Enabled: true, Password: s.Get(KeyPassword, ""), Options: opts}
	if a := s.Get(KeyAddress, ""); a != "" {
		h, p, err := splitAddr(a)
		if err != nil {
			return ctl.NetConfig{}, ctl.Identity{}, err
		}
		cfg.Address, cfg.Port = h, p
	}
	for _, f := range d.fields {
		switch f.Key {
		case KeyDMRID, KeyPassword, KeyAddress, KeyOptions:
			continue
		}
		if v := s.Get(f.Key, f.Default); v != "" {
			cfg.Options[f.Key] = v
		}
	}
	id := ctl.Identity{Callsign: strings.ToUpper(env.Callsign)}
	if v := s.Get(KeyDMRID, ""); v != "" {
		n, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			return ctl.NetConfig{}, ctl.Identity{}, fmt.Errorf("DMR ID %q is not valid", v)
		}
		id.DMRID = uint32(n)
	}
	if d.ctlMode == ctl.ModeYSF || d.ctlMode == ctl.ModeM17 || d.ctlMode == ctl.ModeNXDN || d.ctlMode == ctl.ModeP25 {
		if id.Callsign == "" {
			return ctl.NetConfig{}, ctl.Identity{}, errors.New("set the station callsign first")
		}
	}
	return cfg, id, nil
}

// netTarget maps a mode target to the network's target.
func (d def) netTarget(s mode.Settings, cfg ctl.NetConfig, t mode.Target) (ctl.Target, error) {
	id := strings.TrimSpace(t.ID)
	nt := ctl.Target{Name: t.Name, Host: t.Host, Port: t.Port, Slot: t.Slot, Private: t.Private}
	if nt.Host == "" {
		if r, ok := s.Reflector(id); ok {
			nt.Host, nt.Port = r.Host, r.Port
			if nt.Name == "" {
				nt.Name = r.Name
			}
		} else if d.target == targetM17 {
			if r, ok := s.Reflector(strings.Fields(id + " ")[0]); ok && id != "" {
				nt.Host, nt.Port = r.Host, r.Port
			}
		}
	}
	switch d.target {
	case targetNone:
		return nt, nil
	case targetTG:
		n, err := strconv.ParseUint(id, 10, 32)
		if err != nil || n == 0 || n > 0xFFFFFF {
			return ctl.Target{}, fmt.Errorf("%q is not a talkgroup number", t.ID)
		}
		nt.TG = uint32(n)
		if nt.Host == "" && cfg.Address != "" && (d.ctlMode == ctl.ModeP25 || d.ctlMode == ctl.ModeNXDN) {
			nt.Host, nt.Port = cfg.Address, cfg.Port
		}
		if d.needsHost && nt.Host == "" {
			return ctl.Target{}, fmt.Errorf("talkgroup %d has no reflector address: add it to the reflector list", n)
		}
	case targetReflector:
		if id == "" && nt.Host == "" {
			return ctl.Target{}, errors.New("no reflector given")
		}
		if nt.Host == "" {
			h, p, err := splitAddr(id)
			if err != nil || p == 0 {
				return ctl.Target{}, fmt.Errorf("reflector %q is not in the reflector list and is not host:port", t.ID)
			}
			nt.Host, nt.Port = h, p
		}
		nt.Room = id
	case targetM17:
		f := strings.Fields(strings.ToUpper(id))
		if len(f) != 2 || len(f[1]) != 1 || f[1][0] < 'A' || f[1][0] > 'Z' {
			return ctl.Target{}, fmt.Errorf(`%q is not a reflector and module such as "M17-XXX B"`, t.ID)
		}
		nt.Room = f[0] + " " + f[1]
	}
	return nt, nil
}
