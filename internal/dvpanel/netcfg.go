package dvpanel

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"

	"github.com/artw1/aslcp/internal/dv/ctl"
	"github.com/artw1/aslcp/internal/dv/m17"
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
var NativeModes = []string{ModeLoop, ModeBM, ModeTGIF, ModeDMR, ModeYSF, ModeP25, ModeNXDN, ModeM17}

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
	MaxLen   int    `json:"maxLen,omitempty"` // KindText: longest value (0: 32)
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
	// DefaultAddress is the master used when no address is set; a network with
	// one can be enabled without an address.
	DefaultAddress string `json:"defaultAddress,omitempty"`
	// DefaultPassword is the login password used when none is stored (DMR -
	// Other); a network with one can be enabled without a password.
	DefaultPassword string `json:"defaultPassword,omitempty"`
	// NeedsIdentity: the callsign and DMR ID must be set before it can be
	// enabled.
	NeedsIdentity bool   `json:"needsIdentity"`
	Help          string `json:"help,omitempty"`
}

// NetOptions is the allowlist of option keys per mode.
var NetOptions = map[string][]OptSpec{
	ModeBM: {
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
	// TGIF always uses timeslot 2 and sends no master options, so it has neither field.
	ModeTGIF: hbOptions(""),
	ModeDMR:  hbOptions("Sent to the master after login (RPTO), for example static talkgroups on masters that read them. Leave empty if unsure."),
	ModeYSF: {
		{Key: "poll_ms", Label: "Poll period (ms)", Kind: KindInt, Default: "5000", Min: 1000, Max: 60000, Advanced: true,
			Help: "How often the reflector is polled to keep the link up."},
		{Key: "timeout_ms", Label: "Link timeout (ms)", Kind: KindInt, Default: "15000", Min: 2000, Max: 120000, Advanced: true,
			Help: "The link is considered lost after this silence from the reflector."},
		{Key: "rx_timeout_ms", Label: "Call end timeout (ms)", Kind: KindInt, Default: "1500", Min: 200, Max: 10000, Advanced: true,
			Help: "End a received call that has no terminator after this silence."},
	},
	ModeP25: {
		{Key: "poll_ms", Label: "Poll period (ms)", Kind: KindInt, Default: "5000", Min: 1000, Max: 60000, Advanced: true,
			Help: "How often the reflector is polled to keep the link up."},
		{Key: "timeout_ms", Label: "Link timeout (ms)", Kind: KindInt, Default: "15000", Min: 2000, Max: 120000, Advanced: true,
			Help: "The link is considered lost after this silence from the reflector."},
		{Key: "rx_timeout_ms", Label: "Call end timeout (ms)", Kind: KindInt, Default: "1500", Min: 200, Max: 10000, Advanced: true,
			Help: "End a received call that has no terminator after this silence."},
	},
	ModeNXDN: {
		{Key: "nxdn_id", Label: "NXDN ID", Kind: KindInt, Min: 1, Max: 65535,
			Help: "Your NXDN ID. Required while NXDN is enabled."},
		{Key: "nxdn_ran", Label: "RAN", Kind: KindInt, Default: "0", Min: 0, Max: 63, Advanced: true,
			Help: "Radio access number sent with your transmissions."},
		{Key: "listen", Label: "Local address", Kind: KindText, Default: "0.0.0.0:0", Advanced: true,
			Help: "Local UDP address to bind, host:port. Port 0 picks one."},
		{Key: "poll_interval_ms", Label: "Poll period (ms)", Kind: KindInt, Default: "5000", Min: 1000, Max: 60000, Advanced: true},
		{Key: "missed_polls", Label: "Missed polls", Kind: KindInt, Default: "3", Min: 1, Max: 20, Advanced: true,
			Help: "The reflector is considered gone after this many silent poll periods."},
		{Key: "backoff_min_ms", Label: "First retry delay (ms)", Kind: KindInt, Default: "1000", Min: 100, Max: 60000, Advanced: true},
		{Key: "backoff_max_ms", Label: "Longest retry delay (ms)", Kind: KindInt, Default: "30000", Min: 1000, Max: 600000, Advanced: true},
		{Key: "rx_timeout_ms", Label: "Call end timeout (ms)", Kind: KindInt, Default: "1500", Min: 200, Max: 10000, Advanced: true,
			Help: "End a received call that has no terminator after this silence."},
	},
	ModeM17: {
		{Key: "suffix", Label: "Callsign suffix", Kind: KindText, MaxLen: 3,
			Help: "A letter after your callsign that marks this station, for example H (hotspot) or R (repeater). Leave empty to send your plain callsign."},
		{Key: "can", Label: "Channel access number", Kind: KindInt, Default: "0", Min: 0, Max: 15, Advanced: true,
			Help: "Leave at 0 unless your reflector asks for another value."},
		{Key: "ack_timeout_ms", Label: "Link retry delay (ms)", Kind: KindInt, Default: "2000", Min: 500, Max: 60000, Advanced: true,
			Help: "How long to wait for the reflector to accept the link before asking again."},
		{Key: "timeout_ms", Label: "Link timeout (ms)", Kind: KindInt, Default: "30000", Min: 5000, Max: 120000, Advanced: true,
			Help: "The link is considered lost after this silence from the reflector."},
		{Key: "rx_timeout_ms", Label: "Call end timeout (ms)", Kind: KindInt, Default: "1500", Min: 200, Max: 10000, Advanced: true,
			Help: "End a received call that has no terminator after this silence."},
	},
}

// RetiredAddress lists the modes that stored a reflector address (and port)
// before they became enable-only; a stored one is dropped on load.
var RetiredAddress = map[string]bool{ModeYSF: true, ModeP25: true, ModeTGIF: true}

// RetiredOptions are option keys a mode used to take; a stored one is dropped
// on load instead of making the settings unreadable.
var RetiredOptions = map[string]map[string]bool{
	ModeP25:  {"p25_id": true},
	ModeTGIF: {"options": true, "slot": true},
}

// hbOptions are the options of the Homebrew client, shared by TGIF and
// DMR - Other (internal/dv/network/homebrew). The master options and the
// timeslot are only for DMR - Other: an empty optionsHelp (TGIF) leaves both out.
func hbOptions(optionsHelp string) []OptSpec {
	var lead []OptSpec
	if optionsHelp != "" {
		lead = []OptSpec{
			{Key: "options", Label: "Master options", Kind: KindText, MaxLen: 200, Help: optionsHelp},
			{Key: "slot", Label: "Timeslot", Kind: KindInt, Default: "2", Min: 1, Max: 2,
				Help: "The timeslot of your calls. Hotspot style masters use slot 2."},
		}
	}
	return append(lead, []OptSpec{
		{Key: "repeater_id", Label: "Repeater ID", Kind: KindInt, Min: 1, Max: MaxRepeaterID, Advanced: true,
			Help: "Default: your DMR ID followed by the ESSID of the identity, else the DMR ID."},
		{Key: "colour_code", Label: "Colour code", Kind: KindInt, Default: "1", Min: 0, Max: 15, Advanced: true},
		{Key: "tune_kerchunk", Label: "Link by short key-up", Kind: KindBool, Default: "true", Advanced: true,
			Help: "Tuning a talkgroup sends a short transmission to it, which links it on most masters."},
		{Key: "unlink_tg", Label: "Unlink talkgroup", Kind: KindInt, Default: "4000", Min: 0, Max: 16777215, Advanced: true,
			Help: "Unlink sends a short transmission to this talkgroup. 0: send nothing."},
		{Key: "rx_freq", Label: "RX frequency (Hz)", Kind: KindInt, Default: "0", Min: 0, Max: 999999999, Advanced: true,
			Help: "Reported to the master with your repeater details."},
		{Key: "tx_freq", Label: "TX frequency (Hz)", Kind: KindInt, Default: "0", Min: 0, Max: 999999999, Advanced: true},
		{Key: "location", Label: "Location", Kind: KindText, MaxLen: 20, Advanced: true, Help: "Shown by the master."},
		{Key: "description", Label: "Description", Kind: KindText, MaxLen: 19, Advanced: true, Help: "Shown by the master."},
		{Key: "url", Label: "URL", Kind: KindText, MaxLen: 124, Advanced: true, Help: "Shown by the master."},
		{Key: "ping_interval_ms", Label: "Keep-alive period (ms)", Kind: KindInt, Default: "5000", Min: 1000, Max: 60000, Advanced: true},
		{Key: "missed_pongs", Label: "Missed keep-alives", Kind: KindInt, Default: "3", Min: 1, Max: 20, Advanced: true,
			Help: "The session is considered lost after this many unanswered keep-alives."},
		{Key: "login_timeout_ms", Label: "Login step timeout (ms)", Kind: KindInt, Default: "5000", Min: 1000, Max: 60000, Advanced: true},
		{Key: "backoff_min_ms", Label: "First retry delay (ms)", Kind: KindInt, Default: "1000", Min: 100, Max: 60000, Advanced: true},
		{Key: "backoff_max_ms", Label: "Longest retry delay (ms)", Kind: KindInt, Default: "60000", Min: 1000, Max: 600000, Advanced: true},
		{Key: "rx_timeout_ms", Label: "Call end timeout (ms)", Kind: KindInt, Default: "1500", Min: 200, Max: 10000, Advanced: true,
			Help: "End a received call that has no terminator after this silence."},
	}...)
}

// NetSpecs lists the modes with settings, in display order.
var NetSpecs = []NetSpec{
	{Mode: ModeBM, Label: "BrandMeister", Address: true, Password: true, NeedsIdentity: true,
		Help: "Logs in as an Open DMR Terminal with your DMR ID and the password set in your BrandMeister SelfCare (hotspot security). Pick the master closest to you."},
	{Mode: ModeTGIF, Label: "TGIF", Password: true, NeedsIdentity: true,
		Help: "The password is the hotspot security password of your TGIF profile. The master follows the DMR host list (the first TGIF entry), else tgif.network:62031."},
	{Mode: ModeDMR, Label: "DMR - Other", Address: true, Port: true, Password: true, DefaultPort: 62031, DefaultPassword: ctl.DefaultHomebrewPassword, NeedsIdentity: true,
		Help: "A Homebrew (MMDVM) master such as FreeDMR, DMR+ or an hblink3 server. Pick one from the list (BrandMeister and TGIF have their own cards), or choose Other and enter its address and port. The password is only needed when the master asks for a different one than passw0rd."},
	{Mode: ModeYSF, Label: "System Fusion (YSF)", NeedsIdentity: true,
		Help: "Enable it, then tune a reflector on the dashboard."},
	{Mode: ModeP25, Label: "P25", NeedsIdentity: true,
		Help: "Enable it, then tune a talkgroup on the dashboard."},
	{Mode: ModeNXDN, Label: "NXDN", NeedsIdentity: true,
		Help: "NXDN has no login: tune a reflector talkgroup on the dashboard. The callsign of the identity is sent to the reflector; set your NXDN ID in Options (required to enable NXDN)."},
	{Mode: ModeM17, Label: "M17", NeedsIdentity: true,
		Help: "M17 has no login: tune a reflector and module (for example M17-XXX C) on the dashboard. Your callsign (plus the optional suffix) identifies the node on the reflector."},
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
			switch strings.ToLower(v) {
			case "true", "1":
				v = "true"
			case "false", "0":
				v = "false"
			default:
				return nil, fieldErr("options."+k, "must be true or false")
			}
		default:
			max := sp.MaxLen
			if max == 0 {
				max = 32
			}
			if len(v) > max {
				return nil, fieldErr("options."+k, "at most %d characters", max)
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
	Options     map[string]string
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
	case sp.Address && st.Address == "" && sp.DefaultAddress == "":
		return "set the master address first"
	case sp.Password && sp.DefaultPassword == "" && !st.PasswordSet:
		return "set the password first"
	case sp.NeedsIdentity && (id.Callsign == "" || id.DMRID == 0):
		return "set the callsign and DMR ID in the identity first"
	case sp.Mode == ModeNXDN && strings.TrimSpace(st.Options["nxdn_id"]) == "":
		return "set the NXDN ID first"
	case sp.Mode == ModeM17:
		if _, err := m17.NodeCallsign(id.Callsign, st.Options["suffix"]); err != nil {
			return "the callsign (with the suffix) cannot be used on M17"
		}
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
		if mode == ModeM17 {
			// the suffix must encode next to the longest callsign
			if _, err := m17.NodeCallsign("N0CALL", o["suffix"]); err != nil {
				return u, fieldErr("options.suffix", "not valid on M17 (up to 3 letters, digits, - / or .)")
			}
		}
		u.Options = o
	}
	return u, nil
}
