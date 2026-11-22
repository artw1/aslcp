package dvpanel

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
)

// Per-network settings of the digital modes (docs/DV.md): which modes this
// build carries natively, which fields and options each mode takes, and the
// validation of what the operator enters. One table drives the helper (which
// stores and renders), the API (which validates and exposes the table) and
// the web forms (which are built from it).
//
// A new network is one more entry in NativeModes and in NetSpecs (plus its
// blank import in cmd/aslcp-dv); nothing else in the plumbing changes.

// NativeModes are the modes this build implements, in display order. It is the
// static fallback of the snapshot's availability when the engine is down; the
// engine's own Status.Networks[].Available wins whenever it answers.
var NativeModes = []string{ModeLoop, ModeBM}

// NativeMode reports whether mode is in NativeModes.
func NativeMode(mode string) bool {
	for _, m := range NativeModes {
		if m == mode {
			return true
		}
	}
	return false
}

// Option kinds.
const (
	KindText = "text"
	KindInt  = "int"
	KindBool = "bool"
)

// OptSpec is one network option (ctl.NetConfig.Options key). Values travel as
// strings; Default is what the engine uses when the key is absent (shown as a
// placeholder, never written unless the operator changes it).
type OptSpec struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Kind     string `json:"kind"` // KindText, KindInt or KindBool
	Default  string `json:"default"`
	Help     string `json:"help,omitempty"`
	Min      int64  `json:"min,omitempty"` // KindInt
	Max      int64  `json:"max,omitempty"`
	Advanced bool   `json:"advanced,omitempty"`
}

// NetSpec describes the settings of one network mode.
type NetSpec struct {
	Mode  string `json:"mode"`
	Label string `json:"label"`
	// Address, Port and Password: the mode takes these fields.
	Address  bool `json:"address"`
	Port     bool `json:"port"`
	Password bool `json:"password"`
	// DefaultPort is the master's port when none is set (0: none).
	DefaultPort int `json:"defaultPort,omitempty"`
	// NeedsIdentity: the callsign and DMR ID must be set before it can be
	// enabled.
	NeedsIdentity bool   `json:"needsIdentity"`
	Help          string `json:"help,omitempty"`
}

// NetOptions is the allowlist of option keys per mode.
var NetOptions = map[string][]OptSpec{
	ModeBM: {
		{Key: "client_id", Label: "Application ID", Kind: KindInt, Min: 1, Max: 4294967295,
			Help: "The Rewind application ID BrandMeister registered for this client. Default: the repeater ID."},
		{Key: "repeater_id", Label: "Repeater ID", Kind: KindInt, Min: 1, Max: 999999999,
			Help: "Default for the application ID. Default: the repeater ID of the identity, else the DMR ID followed by 01."},
		{Key: "slot", Label: "Timeslot", Kind: KindInt, Default: "2", Min: 1, Max: 2,
			Help: "Rewind has no timeslots; the number is only shown with calls."},
		{Key: "colour_code", Label: "Colour code", Kind: KindInt, Default: "1", Min: 0, Max: 15, Advanced: true,
			Help: "Not carried by Rewind; kept for compatibility."},
		{Key: "super_header", Label: "Send caller names", Kind: KindBool, Default: "true", Advanced: true,
			Help: "Ask the master for the callsigns of the talkers."},
		{Key: "client_version", Label: "Software description", Kind: KindText, Default: "aslcp-dv", Advanced: true,
			Help: "Sent to the master with every keep-alive."},
		{Key: "keepalive_ms", Label: "Keep-alive period (ms)", Kind: KindInt, Default: "5000", Min: 500, Max: 60000, Advanced: true},
		{Key: "timeout_ms", Label: "Session timeout (ms)", Kind: KindInt, Default: "15000", Min: 1000, Max: 120000, Advanced: true,
			Help: "The session is considered lost after this silence."},
		{Key: "login_timeout_ms", Label: "Login attempt timeout (ms)", Kind: KindInt, Default: "5000", Min: 1000, Max: 60000, Advanced: true},
		{Key: "backoff_min_ms", Label: "First retry delay (ms)", Kind: KindInt, Default: "1000", Min: 100, Max: 60000, Advanced: true},
		{Key: "backoff_max_ms", Label: "Longest retry delay (ms)", Kind: KindInt, Default: "60000", Min: 1000, Max: 600000, Advanced: true},
		{Key: "rx_timeout_ms", Label: "Call end timeout (ms)", Kind: KindInt, Default: "1500", Min: 200, Max: 10000, Advanced: true,
			Help: "End a received call that has no terminator after this silence."},
	},
	ModeTGIF: {},
	ModeDMR:  {},
}

// NetSpecs lists the modes with settings, in display order. The fields of TGIF
// and DMR - Other are those of 1.4.0 (their networks are not in this build
// yet; their settings are kept).
var NetSpecs = []NetSpec{
	{Mode: ModeBM, Label: "BrandMeister", Address: true, Port: true, Password: true, DefaultPort: 54005, NeedsIdentity: true,
		Help: "BrandMeister requires a registered application ID for this client; until one is set in Options, logins may be refused."},
	{Mode: ModeTGIF, Label: "TGIF", Password: true, NeedsIdentity: true},
	{Mode: ModeDMR, Label: "DMR - Other", Address: true, Port: true, Password: true, NeedsIdentity: true},
}

// NetSpecFor finds the spec of mode.
func NetSpecFor(mode string) (NetSpec, bool) {
	for _, s := range NetSpecs {
		if s.Mode == mode {
			return s, true
		}
	}
	return NetSpec{}, false
}

// OptSpecs returns the option specs of mode (never nil).
func OptSpecs(mode string) []OptSpec {
	if o := NetOptions[mode]; o != nil {
		return o
	}
	return []OptSpec{}
}

func optSpec(mode, key string) (OptSpec, bool) {
	for _, o := range NetOptions[mode] {
		if o.Key == key {
			return o, true
		}
	}
	return OptSpec{}, false
}

var (
	reCallsign = regexp.MustCompile(`^[A-Z0-9]{3,8}$`)
	reLabel    = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)
)

// Identity limits.
const (
	MinDMRID      = 1000000
	MaxDMRID      = 9999999
	MaxRepeaterID = 999999999
)

// ValidCallsign reports whether s is a callsign: 3 to 8 letters and digits,
// upper case (NormCallsign upper-cases it first).
func ValidCallsign(s string) bool { return reCallsign.MatchString(s) }

// NormCallsign trims and upper-cases a callsign.
func NormCallsign(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }

// ValidDMRID: a 7 digit DMR ID.
func ValidDMRID(n uint32) bool { return n >= MinDMRID && n <= MaxDMRID }

// ValidRepeaterID: 0 (derived) or 1000000 to 999999999.
func ValidRepeaterID(n uint32) bool { return n == 0 || n >= MinDMRID && n <= MaxRepeaterID }

// ValidNetPort: a master port, 1 to 65535.
func ValidNetPort(p int) bool { return p >= 1 && p <= 65535 }

// ValidAddress reports whether s is an IP address or a host name (labels of
// letters, digits and hyphens, at most 253 characters).
func ValidAddress(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	if net.ParseIP(s) != nil {
		return true
	}
	if strings.Contains(s, ":") { // not an IPv6 address either
		return false
	}
	for _, l := range strings.Split(strings.TrimSuffix(s, "."), ".") {
		if !reLabel.MatchString(l) {
			return false
		}
	}
	return true
}

// ValidPassword: 1 to 64 printable ASCII characters, no blanks.
func ValidPassword(s string) bool {
	if len(s) < 1 || len(s) > 64 {
		return false
	}
	for _, c := range []byte(s) {
		if c < 0x21 || c > 0x7e {
			return false
		}
	}
	return true
}

// FieldError names the field a refused value belongs to.
type FieldError struct{ Field, Msg string }

func (e *FieldError) Error() string { return e.Field + ": " + e.Msg }

func fieldErr(field, f string, a ...any) error {
	return &FieldError{Field: field, Msg: fmt.Sprintf(f, a...)}
}

// CleanOptions validates opts against the allowlist of mode and returns the
// values to store: keys the mode does not know are refused, an empty value
// drops the key (the default applies).
func CleanOptions(mode string, opts map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for k, v := range opts {
		sp, ok := optSpec(mode, k)
		if !ok {
			return nil, fieldErr("options."+k, "unknown option")
		}
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		switch sp.Kind {
		case KindInt:
			if len(v) > 10 {
				return nil, fieldErr("options."+k, "not a number in range %d..%d", sp.Min, sp.Max)
			}
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || strings.HasPrefix(v, "+") || n < sp.Min || n > sp.Max {
				return nil, fieldErr("options."+k, "not a number in range %d..%d", sp.Min, sp.Max)
			}
			v = strconv.FormatInt(n, 10)
		case KindBool:
			v = strings.ToLower(v)
			if v != "true" && v != "false" {
				return nil, fieldErr("options."+k, "must be true or false")
			}
		default:
			if len(v) > 32 {
				return nil, fieldErr("options."+k, "at most 32 characters")
			}
			for _, c := range []byte(v) {
				if c < 0x20 || c > 0x7e {
					return nil, fieldErr("options."+k, "printable ASCII only")
				}
			}
		}
		out[k] = v
	}
	return out, nil
}

// NetState is the stored settings of one network, as validation sees them
// (the password only as "is one set").
type NetState struct {
	Enabled     bool
	Address     string
	Port        int
	PasswordSet bool
}

// IdentityState is the stored identity.
type IdentityState struct {
	Callsign   string `json:"callsign"`
	DMRID      uint32 `json:"dmrId"`
	RepeaterID uint32 `json:"repeaterId,omitempty"`
}

// ValidateIdentity checks and normalises an identity. A zero identity (both
// callsign and DMR ID empty) clears it.
func ValidateIdentity(id IdentityState) (IdentityState, error) {
	id.Callsign = NormCallsign(id.Callsign)
	if id.Callsign == "" && id.DMRID == 0 && id.RepeaterID == 0 {
		return id, nil
	}
	if !ValidCallsign(id.Callsign) {
		return id, fieldErr("callsign", "3 to 8 letters and digits")
	}
	if !ValidDMRID(id.DMRID) {
		return id, fieldErr("dmrId", "a 7 digit DMR ID (%d to %d)", MinDMRID, MaxDMRID)
	}
	if !ValidRepeaterID(id.RepeaterID) {
		return id, fieldErr("repeaterId", "0 or a DMR ID with up to 9 digits")
	}
	return id, nil
}

// EnableBlock says why a network cannot be enabled with these settings ("" when
// it can).
func EnableBlock(sp NetSpec, st NetState, id IdentityState) string {
	switch {
	case sp.Address && st.Address == "":
		return "set the master address first"
	case sp.Password && !st.PasswordSet:
		return "set the password first"
	case sp.NeedsIdentity && (id.Callsign == "" || id.DMRID == 0):
		return "set the callsign and DMR ID in the identity first"
	}
	return ""
}

// NetUpdate is a change to one network; a nil field is left as it is.
type NetUpdate struct {
	Enabled       *bool
	Address       *string
	Port          *int // 0 clears (the default port applies)
	Password      *string
	ClearPassword bool
	Options       map[string]string // non-nil replaces every option
}

// CheckUpdate validates u against the spec of mode and returns the cleaned
// update (address and options normalised). It does not look at the stored
// state: EnableBlock does that.
func CheckUpdate(mode string, u NetUpdate) (NetUpdate, error) {
	sp, ok := NetSpecFor(mode)
	if !ok {
		return u, fieldErr("mode", "no settings for this mode")
	}
	if u.Address != nil {
		a := strings.TrimSpace(*u.Address)
		switch {
		case !sp.Address:
			return u, fieldErr("address", "this network has no address setting")
		case a != "" && !ValidAddress(a):
			return u, fieldErr("address", "not a host name or IP address")
		}
		u.Address = &a
	}
	if u.Port != nil {
		switch {
		case !sp.Port:
			return u, fieldErr("port", "this network has no port setting")
		case *u.Port != 0 && !ValidNetPort(*u.Port):
			return u, fieldErr("port", "1 to 65535")
		}
	}
	if u.Password != nil && *u.Password != "" {
		if !sp.Password {
			return u, fieldErr("password", "this network has no password setting")
		}
		if !ValidPassword(*u.Password) {
			return u, fieldErr("password", "1 to 64 printable characters, no spaces")
		}
	}
	if u.ClearPassword && !sp.Password {
		return u, fieldErr("password", "this network has no password setting")
	}
	if u.Options != nil {
		o, err := CleanOptions(mode, u.Options)
		if err != nil {
			return u, err
		}
		u.Options = o
	}
	return u, nil
}
