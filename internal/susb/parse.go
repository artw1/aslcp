// Package susb tunes SimpleUSB audio interfaces (chan_simpleusb) through the
// Asterisk CLI over AMI: it lists the devices, reads and sets the audio
// parameters, reads the receive level meter, runs the echo mode and saves
// the result. See docs/SIMPLEUSB_TUNE.md.
//
// Every command that reaches Asterisk is built here from a closed set of fixed
// formats (see Command); nothing the client sends is passed through.
package susb

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Frame is the length of one driver frame in milliseconds. The receive-on
// and transmit-off delays are stored in frames.
const Frame = 20

// Limits of the settings.
const (
	MaxLevel       = 999  // rx / tx A / tx B levels
	MaxDelayFrames = 3000 // the driver clamps here (60 s)
	MaxDelayMs     = MaxDelayFrames * Frame
	ParamFields    = 16 // fields of "menu-support 0+4"
)

// Params are the tunable settings of one device, in the units the UI uses.
type Params struct {
	RxVoice      int    `json:"rxVoice"`
	TxA          int    `json:"txA"`
	TxB          int    `json:"txB"`
	Echo         bool   `json:"echo"`
	RxBoost      bool   `json:"rxBoost"`
	PreEmphasis  bool   `json:"preEmphasis"`
	DeEmphasis   bool   `json:"deEmphasis"`
	PlFilter     bool   `json:"plFilter"`
	PttInvert    bool   `json:"pttInvert"`
	CarrierFrom  string `json:"carrierFrom"`
	CtcssFrom    string `json:"ctcssFrom"`
	RxOnDelayMs  int    `json:"rxOnDelayMs"`
	TxOffDelayMs int    `json:"txOffDelayMs"`
	MicPlayMax   int    `json:"micPlayMax"`
	SpkrMax      int    `json:"spkrMax"`
	MicMax       int    `json:"micMax"`
	// CarrierOther and CtcssOther: the driver reports a code outside the
	// whitelist (the name is then "other" and is never written back).
	CarrierOther bool `json:"carrierOther,omitempty"`
	CtcssOther   bool `json:"ctcssOther,omitempty"`
}

// Signal sources. The driver's internal codes are not contiguous (the
// "N/A" slots are not valid modes), so only these are ever sent.
var (
	carrierCodes = map[string]int{"no": 0, "usb": 3, "usbinvert": 4, "pp": 5, "ppinvert": 6}
	ctcssCodes   = map[string]int{"no": 0, "usb": 1, "usbinvert": 2, "pp": 4, "ppinvert": 5}
	// SignalNames are the allowed values of carrierFrom and ctcssFrom.
	SignalNames = []string{"no", "usb", "usbinvert", "pp", "ppinvert"}
)

// CarrierCode returns the driver code of a carrierfrom name.
func CarrierCode(name string) (int, bool) { c, ok := carrierCodes[name]; return c, ok }

// CtcssCode returns the driver code of a ctcssfrom name.
func CtcssCode(name string) (int, bool) { c, ok := ctcssCodes[name]; return c, ok }

func nameOf(m map[string]int, code int) (string, bool) {
	for n, c := range m {
		if c == code {
			return n, true
		}
	}
	return "", false
}

// MsToFrames converts milliseconds to 20 ms frames (rounded to the nearest).
func MsToFrames(ms int) (int, error) {
	if ms < 0 || ms > MaxDelayMs {
		return 0, fmt.Errorf("delay must be between 0 and %d ms", MaxDelayMs)
	}
	return (ms + Frame/2) / Frame, nil
}

// FramesToMs converts frames to milliseconds.
func FramesToMs(frames int) int { return frames * Frame }

// Error is a failure with a stable code (the API maps codes to statuses).
type Error struct {
	Code string
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

// Error codes.
const (
	CodeInvalid       = "invalid"
	CodeUnknownDevice = "unknown_device"
	CodeNoDriver      = "no_chan_simpleusb"
	CodeUnsupported   = "unsupported_version"
	CodeUnexpected    = "radio_unexpected_output"
	CodeDeviceMissing = "device_not_found"
	CodeCommandFailed = "radio_command_failed"
	CodeUnavailable   = "radio_unavailable"
	CodeEchoOn        = "echo_on"
	CodeNoBaseline    = "no_baseline"
)

func errf(code, format string, a ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, a...)}
}

// ErrUnsupported is returned when the 0+4 block does not have 16 fields.
var ErrUnsupported = &Error{Code: CodeUnsupported, Msg: "unsupported ASL3 version: the tuning parameter block does not have 16 fields"}

// clean strips carriage returns and surrounding space and drops empty lines.
func clean(lines []string) []string {
	var out []string
	for _, l := range lines {
		for _, p := range strings.Split(strings.ReplaceAll(l, "\r", "\n"), "\n") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// first returns the first line, shortened for messages.
func first(lines []string) string {
	if len(lines) == 0 {
		return "(no output)"
	}
	s := lines[0]
	if len(s) > 160 {
		s = s[:160] + "..."
	}
	return s
}

func unexpected(what string, lines []string) *Error {
	return errf(CodeUnexpected, "unexpected reply to %s: %s", what, first(lines))
}

var (
	reDevName   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)
	reParamLine = regexp.MustCompile(`^-?\d+(,-?\d+)+$`)
	reNoCmd     = regexp.MustCompile(`(?i)no such command|unknown command|command .* not found|usage:`)
	reNotFound  = regexp.MustCompile(`(?i)was not found|not found|no such device`)
)

// ValidDevice reports whether name has the form of a device stanza name.
func ValidDevice(name string) bool { return reDevName.MatchString(name) }

// ParseDevices parses the reply of "menu-support 1": a comma-separated list
// of device names; an empty reply means no devices.
func ParseDevices(lines []string) ([]string, error) {
	lines = clean(lines)
	if len(lines) == 0 {
		return nil, nil
	}
	if reNoCmd.MatchString(lines[0]) {
		return nil, errf(CodeNoDriver, "chan_simpleusb is not loaded (%s)", first(lines))
	}
	var out []string
	for _, n := range strings.Split(lines[0], ",") {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		if !reDevName.MatchString(n) {
			return nil, unexpected("the device list", lines)
		}
		out = append(out, n)
	}
	return out, nil
}

// ParseParams parses the 16 comma-separated integers of "menu-support 0+4".
// Any other field count is ErrUnsupported.
func ParseParams(lines []string) (Params, error) {
	lines = clean(lines)
	var line string
	for _, l := range lines {
		if reParamLine.MatchString(l) {
			line = l
			break
		}
	}
	if line == "" {
		if len(lines) > 0 && reNotFound.MatchString(lines[0]) {
			return Params{}, errf(CodeDeviceMissing, "the radio interface was not found: %s", first(lines))
		}
		return Params{}, unexpected("the parameter query", lines)
	}
	f := strings.Split(line, ",")
	if len(f) != ParamFields {
		return Params{}, ErrUnsupported
	}
	n := make([]int, len(f))
	for i, s := range f {
		v, err := strconv.Atoi(s)
		if err != nil {
			return Params{}, unexpected("the parameter query", lines)
		}
		n[i] = v
	}
	for _, i := range []int{0, 1, 12} {
		if n[i] < 0 || n[i] > MaxLevel {
			return Params{}, unexpected("the parameter query", lines)
		}
	}
	for _, i := range []int{2, 3, 4, 5, 6, 7} {
		if n[i] != 0 && n[i] != 1 {
			return Params{}, unexpected("the parameter query", lines)
		}
	}
	if n[10] < 0 || n[10] > MaxDelayFrames || n[11] < 0 || n[11] > MaxDelayFrames {
		return Params{}, unexpected("the parameter query", lines)
	}
	p := Params{
		TxA: n[0], TxB: n[1], Echo: n[2] == 1, RxBoost: n[3] == 1, PreEmphasis: n[4] == 1,
		DeEmphasis: n[5] == 1, PlFilter: n[6] == 1, PttInvert: n[7] == 1,
		RxOnDelayMs: FramesToMs(n[10]), TxOffDelayMs: FramesToMs(n[11]),
		RxVoice: n[12], MicPlayMax: n[13], SpkrMax: n[14], MicMax: n[15],
	}
	var ok bool
	if p.CarrierFrom, ok = nameOf(carrierCodes, n[8]); !ok {
		p.CarrierFrom, p.CarrierOther = "other", true
	}
	if p.CtcssFrom, ok = nameOf(ctcssCodes, n[9]); !ok {
		p.CtcssFrom, p.CtcssOther = "other", true
	}
	return p, nil
}

// Meter is one reading of the receive audio statistics ("menu-support Y").
// The values are dBFS (relative to digital full scale), measured on the USB
// capture stream after the hardware input mixer: they follow the Rx level,
// not the deviation.
type Meter struct {
	Peak float64 `json:"pk"`
	Avg  int     `json:"avg"`
	Min  int     `json:"min"`
	Max  int     `json:"max"`
	Clip uint    `json:"clip"`
}

var reMeter = regexp.MustCompile(`AudioStats:\s*Pk\s+(-?\d+(?:\.\d+)?)\s+Avg Pwr\s+(-?\d+)\s+Min\s+(-?\d+)\s+Max\s+(-?\d+)\s+dBFS\s+ClipCnt\s+(\d+)`)

// ParseMeter parses the reply of "menu-support Y".
func ParseMeter(lines []string) (Meter, error) {
	lines = clean(lines)
	for _, l := range lines {
		if m := reMeter.FindStringSubmatch(l); m != nil {
			pk, _ := strconv.ParseFloat(m[1], 64)
			avg, _ := strconv.Atoi(m[2])
			mn, _ := strconv.Atoi(m[3])
			mx, _ := strconv.Atoi(m[4])
			clip, err := strconv.ParseUint(m[5], 10, 32)
			if err != nil {
				break
			}
			return Meter{Peak: pk, Avg: avg, Min: mn, Max: mx, Clip: uint(clip)}, nil
		}
	}
	return Meter{}, unexpected("the level meter", lines)
}

var (
	reActive    = regexp.MustCompile(`^Active Simple USB Radio device is \[([^\]]*)\]`)
	reSetActive = regexp.MustCompile(`^Active \(command\) Simple USB Radio device set to \[([^\]]*)\]`)
	reIdent     = regexp.MustCompile(`^Active radio interface is \[([^\]]*)\]`)
	reAssigned  = regexp.MustCompile(`^Device \[([^\]]+)\] is assigned to device=(\S*)\s+card=(-?\d+)`)
)

// ParseActive parses the reply of "susb active".
func ParseActive(lines []string) (string, error) {
	lines = clean(lines)
	for _, l := range lines {
		if m := reActive.FindStringSubmatch(l); m != nil {
			return m[1], nil
		}
	}
	return "", unexpected("the active device query", lines)
}

// VerifySetActive checks the reply of "susb active <dev>".
func VerifySetActive(dev string, lines []string) error {
	lines = clean(lines)
	for _, l := range lines {
		if m := reSetActive.FindStringSubmatch(l); m != nil {
			if m[1] != dev {
				return errf(CodeUnexpected, "the active device is %q, not %q", m[1], dev)
			}
			return nil
		}
		if strings.HasPrefix(l, "No device") {
			return errf(CodeUnknownDevice, "%s", l)
		}
	}
	return unexpected("selecting the device", lines)
}

// Identity is what "menu-support 2" says about the active device.
type Identity struct {
	Device string `json:"device"`
	String string `json:"usbString,omitempty"`
	Serial string `json:"serial,omitempty"`
	Card   int    `json:"card"`
}

// ParseIdentity parses "menu-support 2" and checks that it describes dev.
// A device whose USB interface is missing is CodeDeviceMissing.
func ParseIdentity(dev string, lines []string) (Identity, error) {
	lines = clean(lines)
	id := Identity{Card: -1}
	seen := false
	for _, l := range lines {
		if reNotFound.MatchString(l) && !strings.HasPrefix(l, "Device String is") {
			return id, errf(CodeDeviceMissing, "the USB interface of %s was not found: %s", dev, first([]string{l}))
		}
		if m := reIdent.FindStringSubmatch(l); m != nil {
			seen = true
			if m[1] != dev {
				return id, errf(CodeUnexpected, "the active device is %q, not %q", m[1], dev)
			}
			id.Device = m[1]
		} else if v, ok := strings.CutPrefix(l, "Device String is "); ok {
			if v != "unassigned" {
				id.String = v
			}
		} else if v, ok := strings.CutPrefix(l, "Device Serial is "); ok {
			id.Serial = v
		} else if v, ok := strings.CutPrefix(l, "Card is "); ok {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
				id.Card = n
			}
		}
	}
	if !seen {
		return id, unexpected("the device query", lines)
	}
	return id, nil
}

// Assignment is a line of "susb active show".
type Assignment struct {
	Device string
	Path   string
	Card   int
}

// ParseAssignments parses "susb active show"; unrecognised lines are ignored
// (the output is only used to label devices).
func ParseAssignments(lines []string) []Assignment {
	var out []Assignment
	for _, l := range clean(lines) {
		if m := reAssigned.FindStringSubmatch(l); m != nil {
			c, _ := strconv.Atoi(m[3])
			out = append(out, Assignment{Device: m[1], Path: m[2], Card: c})
		}
	}
	return out
}

// ---------------------------------------------------------------- commands

// Fixed command strings. These are the only commands the package sends.
const (
	cmdActive     = "susb active"
	cmdActiveShow = "susb active show"
	cmdDevices    = "susb tune menu-support 1"
	cmdIdentity   = "susb tune menu-support 2"
	cmdParams     = "susb tune menu-support 0+4"
	cmdMeter      = "susb tune menu-support Y"
	cmdSave       = "susb tune save"
)

// allowed is the closed set of commands. rxcap, txcap, nocap, load, swap,
// rxd, the live displays (b, v, y, z), l and the keyed fK/gK forms are not in
// it.
var reAllowed = regexp.MustCompile(`^(` +
	`susb active( show| [A-Za-z0-9_-]{1,32})?` +
	`|susb tune save` +
	`|susb tune menu-support (0\+4|1|2|Y|[cfg][0-9]{1,3}|[kmnopq][01]|r[03456]|s[01245]|[tu][0-9]{1,4})` +
	`)$`)

// Allowed reports whether cmd is in the closed set of commands.
func Allowed(cmd string) bool { return reAllowed.MatchString(cmd) }

// setField describes one settable field: the command letter, how to check
// the reply.
type setSpec struct {
	field string
	cmd   string
	reply *regexp.Regexp // must match one reply line; %s replaced by the device
	want  string         // expected captured value ("" = do not compare)
}

func boolArg(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

func enabled(v bool) string {
	if v {
		return "Enabled"
	}
	return "Disabled"
}

func levelSpec(dev, field, letter, label string, v int) (setSpec, error) {
	if v < 0 || v > MaxLevel {
		return setSpec{}, errf(CodeInvalid, "%s must be between 0 and %d", field, MaxLevel)
	}
	return setSpec{field: field, cmd: "susb tune menu-support " + letter + strconv.Itoa(v),
		reply: regexp.MustCompile(`^Channel ` + regexp.QuoteMeta(dev) + `: Changed setting on ` + label + ` to (\d+)$`),
		want:  strconv.Itoa(v)}, nil
}

func boolSpec(field, letter, label string, v bool) setSpec {
	return setSpec{field: field, cmd: "susb tune menu-support " + letter + boolArg(v),
		reply: regexp.MustCompile(`^` + regexp.QuoteMeta(label) + ` changed to (Enabled|Disabled)$`), want: enabled(v)}
}

// Patch is a partial update. Nil fields are left alone.
type Patch struct {
	RxVoice      *int    `json:"rxVoice,omitempty"`
	TxA          *int    `json:"txA,omitempty"`
	TxB          *int    `json:"txB,omitempty"`
	RxBoost      *bool   `json:"rxBoost,omitempty"`
	PreEmphasis  *bool   `json:"preEmphasis,omitempty"`
	DeEmphasis   *bool   `json:"deEmphasis,omitempty"`
	PlFilter     *bool   `json:"plFilter,omitempty"`
	PttInvert    *bool   `json:"pttInvert,omitempty"`
	CarrierFrom  *string `json:"carrierFrom,omitempty"`
	CtcssFrom    *string `json:"ctcssFrom,omitempty"`
	RxOnDelayMs  *int    `json:"rxOnDelayMs,omitempty"`
	TxOffDelayMs *int    `json:"txOffDelayMs,omitempty"`
}

// Empty reports whether nothing is set.
func (p Patch) Empty() bool { return p == Patch{} }

// Specs validates the patch and returns the commands to run, in a fixed
// order (receive side, then transmit side). Any invalid field is an error
// and nothing is returned.
func (p Patch) Specs(dev string) ([]setSpec, error) {
	var out []setSpec
	add := func(s setSpec, err error) error {
		if err != nil {
			return err
		}
		out = append(out, s)
		return nil
	}
	if p.RxVoice != nil {
		if err := add(levelSpec(dev, "rxVoice", "c", "RX Channel", *p.RxVoice)); err != nil {
			return nil, err
		}
	}
	if p.RxBoost != nil {
		out = append(out, boolSpec("rxBoost", "m", "RxBoost", *p.RxBoost))
	}
	if p.DeEmphasis != nil {
		out = append(out, boolSpec("deEmphasis", "o", "De-emphasis", *p.DeEmphasis))
	}
	if p.PlFilter != nil {
		out = append(out, boolSpec("plFilter", "p", "PL Filter", *p.PlFilter))
	}
	if p.CarrierFrom != nil {
		code, ok := carrierCodes[*p.CarrierFrom]
		if !ok {
			return nil, errf(CodeInvalid, "carrierFrom must be one of %s", strings.Join(SignalNames, ", "))
		}
		out = append(out, setSpec{field: "carrierFrom", cmd: "susb tune menu-support r" + strconv.Itoa(code),
			reply: regexp.MustCompile(`^Carrier From changed to (\S+)$`), want: *p.CarrierFrom})
	}
	if p.CtcssFrom != nil {
		code, ok := ctcssCodes[*p.CtcssFrom]
		if !ok {
			return nil, errf(CodeInvalid, "ctcssFrom must be one of %s", strings.Join(SignalNames, ", "))
		}
		out = append(out, setSpec{field: "ctcssFrom", cmd: "susb tune menu-support s" + strconv.Itoa(code),
			reply: regexp.MustCompile(`^CTCSS From changed to (\S+)$`), want: *p.CtcssFrom})
	}
	if p.RxOnDelayMs != nil {
		f, err := MsToFrames(*p.RxOnDelayMs)
		if err != nil {
			return nil, errf(CodeInvalid, "rxOnDelayMs: %v", err)
		}
		out = append(out, setSpec{field: "rxOnDelayMs", cmd: "susb tune menu-support t" + strconv.Itoa(f),
			reply: regexp.MustCompile(`^RX On Delay From changed to (\d+)$`), want: strconv.Itoa(f)})
	}
	if p.TxA != nil {
		if err := add(levelSpec(dev, "txA", "f", "TX Channel A", *p.TxA)); err != nil {
			return nil, err
		}
	}
	if p.TxB != nil {
		if err := add(levelSpec(dev, "txB", "g", "TX Channel B", *p.TxB)); err != nil {
			return nil, err
		}
	}
	if p.PreEmphasis != nil {
		out = append(out, boolSpec("preEmphasis", "n", "Pre-emphasis", *p.PreEmphasis))
	}
	if p.PttInvert != nil {
		mode := "Ground"
		if *p.PttInvert {
			mode = "Open"
		}
		out = append(out, setSpec{field: "pttInvert", cmd: "susb tune menu-support q" + boolArg(*p.PttInvert),
			reply: regexp.MustCompile(`^PTT mode changed to (Open|Ground)$`), want: mode})
	}
	if p.TxOffDelayMs != nil {
		f, err := MsToFrames(*p.TxOffDelayMs)
		if err != nil {
			return nil, errf(CodeInvalid, "txOffDelayMs: %v", err)
		}
		out = append(out, setSpec{field: "txOffDelayMs", cmd: "susb tune menu-support u" + strconv.Itoa(f),
			reply: regexp.MustCompile(`^TX Off Delay From changed to (\d+)$`), want: strconv.Itoa(f)})
	}
	return out, nil
}

// echoSpec is the command for echo mode.
func echoSpec(on bool) setSpec {
	return boolSpec("echo", "k", "Echo Mode", on)
}

var reEntryError = regexp.MustCompile(`(?i)entry error|not changed|invalid|unable|error`)

// VerifyReply checks the reply of one set command. A reply that carries the
// device name (the level commands) must name dev. It returns the matching
// reply line.
func (s setSpec) VerifyReply(lines []string) (string, error) {
	lines = clean(lines)
	for _, l := range lines {
		if m := s.reply.FindStringSubmatch(l); m != nil {
			if s.want != "" && !strings.EqualFold(m[len(m)-1], s.want) {
				return l, errf(CodeUnexpected, "%s: the radio answered %q, expected %q", s.field, m[len(m)-1], s.want)
			}
			return l, nil
		}
	}
	for _, l := range lines {
		if reEntryError.MatchString(l) {
			return l, errf(CodeCommandFailed, "%s: %s", s.field, l)
		}
	}
	return first(lines), unexpected(s.field, lines)
}

// Diff lists the fields (JSON names) in which a and b differ. The echo
// mode and the hardware maximums are not tuning values and are ignored.
func Diff(a, b Params) []string {
	var d []string
	chk := func(diff bool, name string) {
		if diff {
			d = append(d, name)
		}
	}
	chk(a.RxVoice != b.RxVoice, "rxVoice")
	chk(a.RxBoost != b.RxBoost, "rxBoost")
	chk(a.DeEmphasis != b.DeEmphasis, "deEmphasis")
	chk(a.PlFilter != b.PlFilter, "plFilter")
	chk(a.CarrierFrom != b.CarrierFrom, "carrierFrom")
	chk(a.CtcssFrom != b.CtcssFrom, "ctcssFrom")
	chk(a.RxOnDelayMs != b.RxOnDelayMs, "rxOnDelayMs")
	chk(a.TxA != b.TxA, "txA")
	chk(a.TxB != b.TxB, "txB")
	chk(a.PreEmphasis != b.PreEmphasis, "preEmphasis")
	chk(a.PttInvert != b.PttInvert, "pttInvert")
	chk(a.TxOffDelayMs != b.TxOffDelayMs, "txOffDelayMs")
	return d
}

// PatchFrom builds a patch that sets every tuning value of p (used by
// Revert).
func PatchFrom(p Params) Patch {
	q := Patch{RxVoice: &p.RxVoice, TxA: &p.TxA, TxB: &p.TxB, RxBoost: &p.RxBoost,
		PreEmphasis: &p.PreEmphasis, DeEmphasis: &p.DeEmphasis, PlFilter: &p.PlFilter, PttInvert: &p.PttInvert,
		RxOnDelayMs: &p.RxOnDelayMs, TxOffDelayMs: &p.TxOffDelayMs}
	if !p.CarrierOther {
		q.CarrierFrom = &p.CarrierFrom
	}
	if !p.CtcssOther {
		q.CtcssFrom = &p.CtcssFrom
	}
	return q
}
