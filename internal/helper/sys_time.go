package helper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Hostname, time zone, NTP and power ops of the System tab (plan 5.1).
const (
	OpHostStatus      = "host-status"       // read-only
	OpHostSetHostname = "host-set-hostname" // SysRequest.Hostname
	OpHostSetTimezone = "host-set-timezone" // SysRequest.Timezone
	OpHostSetNTP      = "host-set-ntp"      // SysRequest.NTP
	OpHostTimezones   = "host-timezones"    // read-only: the valid zone names
	OpHostPoweroff    = "host-poweroff"     // systemctl poweroff
)

func init() {
	registerSysOp(OpHostStatus, func(o *Ops, ctx context.Context, _ *SysRequest) (any, error) { return o.HostStatus(ctx), nil })
	registerSysOp(OpHostSetHostname, func(o *Ops, ctx context.Context, r *SysRequest) (any, error) {
		return o.HostSetHostname(ctx, r.Hostname)
	})
	registerSysOp(OpHostSetTimezone, func(o *Ops, ctx context.Context, r *SysRequest) (any, error) {
		return o.HostSetTimezone(ctx, r.Timezone)
	})
	registerSysOp(OpHostSetNTP, func(o *Ops, ctx context.Context, r *SysRequest) (any, error) {
		if r.NTP == nil {
			return nil, errors.New("invalid ntp setting")
		}
		return o.HostSetNTP(ctx, *r.NTP)
	})
	registerSysOp(OpHostTimezones, func(o *Ops, ctx context.Context, _ *SysRequest) (any, error) { return o.HostTimezones(ctx) })
	registerSysOp(OpHostPoweroff, func(o *Ops, ctx context.Context, _ *SysRequest) (any, error) { return o.HostPoweroff(ctx) })
}

// HostStatus is the response of host-status. A tool that is not installed
// (or systemd is not running) shows as an unavailable group, not an error.
type HostStatus struct {
	Hostname       string `json:"hostname"`
	PrettyHostname string `json:"prettyHostname"`
	OS             string `json:"os"`
	HostnameAvail  bool   `json:"hostnameAvailable"` // a hostname was read, by hostnamectl or by the fallback
	// HostnamectlAvail: hostnamectl works or is installed, so Set hostname can
	// be used. HostnamectlMissing: it is not installed (not found).
	HostnamectlAvail   bool   `json:"hostnamectlAvailable"`
	HostnamectlMissing bool   `json:"hostnamectlMissing"`
	Timezone           string `json:"timezone"`
	NTP                bool   `json:"ntp"`
	NTPSynchronized    bool   `json:"ntpSynchronized"`
	CanNTP             bool   `json:"canNtp"`
	LocalRTC           bool   `json:"localRtc"`
	TimeAvail          bool   `json:"timeAvailable"`
	Now                string `json:"now"`   // RFC 3339, UTC, the helper's clock
	Clock              string `json:"clock"` // timedatectl's TimeUSec text
}

// TimezoneList is the response of host-timezones.
type TimezoneList struct {
	Timezones []string `json:"timezones"`
}

// DefaultHostnameFile holds the static hostname.
const DefaultHostnameFile = "/etc/hostname"

var hostnameRE = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

// ValidHostname checks an RFC 1123 label (1 to 63 letters, digits and
// hyphens, not starting or ending with a hyphen) and returns it lowercased.
func ValidHostname(s string) (string, error) {
	if !hostnameRE.MatchString(s) {
		return "", errors.New("invalid hostname: use 1 to 63 letters, digits and hyphens, not starting or ending with a hyphen")
	}
	return strings.ToLower(s), nil
}

var tzSyntaxRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_+./-]{0,63}$`)

// ValidTimezoneSyntax is the cheap pre-check before the zone is looked up in
// the list timedatectl reports.
func ValidTimezoneSyntax(s string) bool {
	return tzSyntaxRE.MatchString(s) && !strings.Contains(s, "..")
}

// parseHostnamectlJSON reads the output of "hostnamectl --json=short"
// (systemd 250 and later). Missing fields and nulls give empty strings.
func parseHostnamectlJSON(text string) (hostname, pretty, osName string, err error) {
	var v struct {
		Hostname       *string `json:"Hostname"`
		StaticHostname *string `json:"StaticHostname"`
		PrettyHostname *string `json:"PrettyHostname"`
		OSPrettyName   *string `json:"OperatingSystemPrettyName"`
	}
	if err = json.Unmarshal([]byte(text), &v); err != nil {
		return "", "", "", err
	}
	str := func(p *string) string {
		if p == nil {
			return ""
		}
		return strings.TrimSpace(*p)
	}
	hostname = str(v.Hostname)
	if hostname == "" {
		hostname = str(v.StaticHostname)
	}
	return hostname, str(v.PrettyHostname), str(v.OSPrettyName), nil
}

// parseOSReleasePretty returns PRETTY_NAME from os-release content: quoted
// (single or double) and unquoted values; comments and junk lines ignored.
func parseOSReleasePretty(text string) string {
	val := ""
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(k) != "PRETTY_NAME" {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
			if line[strings.Index(line, "=")+1:][0] == '"' {
				v = strings.NewReplacer(`\"`, `"`, `\\`, `\`).Replace(v)
			}
		}
		val = v
	}
	return val
}

// parseTimedatectl reads the output of "timedatectl show".
func parseTimedatectl(text string, h *HostStatus) {
	m := parseKV(text)
	h.Timezone = m["Timezone"]
	h.NTP = m["NTP"] == "yes"
	h.NTPSynchronized = m["NTPSynchronized"] == "yes"
	h.CanNTP = m["CanNTP"] == "yes"
	h.LocalRTC = m["LocalRTC"] == "yes"
	h.Clock = m["TimeUSec"]
}

// HostStatus reads hostname and time settings; it never fails. The hostname
// comes from "hostnamectl --json=short" (systemd 250 and later); when that
// fails it falls back to the kernel hostname, /etc/hostname and os-release.
func (o *Ops) HostStatus(ctx context.Context) HostStatus {
	h := HostStatus{}
	if out, code, err := o.Run.Run(ctx, "hostnamectl", "--json=short"); err == nil && code == 0 {
		if name, pretty, osName, perr := parseHostnamectlJSON(out); perr == nil {
			h.Hostname, h.PrettyHostname, h.OS = name, pretty, osName
			h.HostnamectlAvail = true
		}
	}
	if !h.HostnamectlAvail {
		lp := o.LookPath
		if lp == nil {
			lp = exec.LookPath
		}
		if _, err := lp("hostnamectl"); err == nil {
			h.HostnamectlAvail = true
		} else {
			h.HostnamectlMissing = true
		}
	}
	if h.Hostname == "" {
		gh := o.GetHostname
		if gh == nil {
			gh = os.Hostname
		}
		if n, err := gh(); err == nil {
			h.Hostname = strings.TrimSpace(n)
		}
	}
	if h.Hostname == "" {
		if b, err := os.ReadFile(orDefault(o.HostnameFile, DefaultHostnameFile)); err == nil {
			h.Hostname = strings.TrimSpace(strings.SplitN(string(b), "\n", 2)[0])
		}
	}
	if h.OS == "" {
		paths := []string{o.osRelease()}
		if o.OSRelease == "" {
			paths = append(paths, "/usr/lib/os-release")
		}
		for _, p := range paths {
			if b, err := os.ReadFile(p); err == nil {
				if h.OS = parseOSReleasePretty(string(b)); h.OS != "" {
					break
				}
			}
		}
	}
	h.HostnameAvail = h.Hostname != ""
	if out, code, err := o.Run.Run(ctx, "timedatectl", "show"); err == nil && code == 0 {
		parseTimedatectl(out, &h)
		h.TimeAvail = true
	}
	h.Now = o.now().UTC().Format(time.RFC3339)
	return h
}

// HostSetHostname validates name as an RFC 1123 label and sets it.
func (o *Ops) HostSetHostname(ctx context.Context, name string) (HostStatus, error) {
	name, err := ValidHostname(name)
	if err != nil {
		return HostStatus{}, err
	}
	if err := o.sysRun(ctx, "hostnamectl", "set-hostname", name); err != nil {
		return HostStatus{}, err
	}
	return o.HostStatus(ctx), nil
}

// HostTimezones lists the zone names timedatectl accepts.
func (o *Ops) HostTimezones(ctx context.Context) (TimezoneList, error) {
	out, code, err := o.Run.Run(ctx, "timedatectl", "list-timezones")
	if err != nil || code != 0 {
		return TimezoneList{Timezones: []string{}}, errors.New("timedatectl is not available")
	}
	return TimezoneList{Timezones: parseTimezones(out)}, nil
}

func parseTimezones(text string) []string {
	zones := []string{}
	for _, l := range strings.Split(text, "\n") {
		if l = strings.TrimSpace(l); l != "" && ValidTimezoneSyntax(l) {
			zones = append(zones, l)
		}
	}
	return zones
}

// HostSetTimezone sets tz, which must be one of the names timedatectl lists.
func (o *Ops) HostSetTimezone(ctx context.Context, tz string) (HostStatus, error) {
	if !ValidTimezoneSyntax(tz) {
		return HostStatus{}, errors.New("invalid timezone")
	}
	list, err := o.HostTimezones(ctx)
	if err != nil {
		return HostStatus{}, err
	}
	found := false
	for _, z := range list.Timezones {
		if z == tz {
			found = true
			break
		}
	}
	if !found {
		return HostStatus{}, errors.New("invalid timezone")
	}
	if err := o.sysRun(ctx, "timedatectl", "set-timezone", tz); err != nil {
		return HostStatus{}, err
	}
	return o.HostStatus(ctx), nil
}

// HostSetNTP switches network time synchronisation on or off.
func (o *Ops) HostSetNTP(ctx context.Context, on bool) (HostStatus, error) {
	v := "false"
	if on {
		v = "true"
	}
	if err := o.sysRun(ctx, "timedatectl", "set-ntp", v); err != nil {
		return HostStatus{}, err
	}
	return o.HostStatus(ctx), nil
}

// HostPoweroff powers the machine off (the counterpart of reboot).
func (o *Ops) HostPoweroff(ctx context.Context) (OKResult, error) {
	o.applyMu.Lock()
	defer o.applyMu.Unlock()
	if o.aptJobRunning(ctx) {
		return OKResult{}, ErrBusy
	}
	if err := o.sysRun(ctx, "systemctl", "--no-block", "poweroff"); err != nil {
		return OKResult{}, err
	}
	return OKResult{OK: true}, nil
}

// sysRun runs one fixed command line and turns a failure into an error.
func (o *Ops) sysRun(ctx context.Context, argv ...string) error {
	out, code, err := o.Run.Run(ctx, argv...)
	if err != nil {
		return fmt.Errorf("%s is not available", argv[0])
	}
	if code != 0 {
		return fmt.Errorf("%s failed: %s", argv[0], LastLines(out, 3))
	}
	return nil
}
