// Package schema is the hand-curated key schema of docs/DESIGN.md 4.2: for
// the settings that matter on an ASL3 node it records the type, allowed
// values or range, default, what the setting does on the air, whether a
// change needs a reload or an Asterisk restart, and whether the value is a
// secret. Keys it does not know are never rejected or dropped: forms show
// them read-only and the raw editor stays the way to change them.
package schema

import (
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Type is the value type of a key.
type Type string

// Value types.
const (
	TypeString Type = "string"
	TypeInt    Type = "int"
	TypeYesNo  Type = "yesno" // yes / no
	TypeFlag   Type = "flag"  // 1 / 0
	TypeEnum   Type = "enum"
	TypeNode   Type = "node" // an AllStar node number
)

// Reload says what a change of the key needs. Per-key behaviour of app_rpt
// has not been verified on hardware (docs/CONFIG.md, "Reload and restart"):
// ReloadVerified is reserved for keys that have been, and no key has it yet.
type Reload string

// Reload classes.
const (
	// ReloadVerified: checked on hardware that the reload command in
	// configsvc.ReloadMap applies it.
	ReloadVerified Reload = "verified"
	// ReloadAssumed: the reload command is expected to apply it; not checked.
	ReloadAssumed Reload = "assumed"
	// ReloadRestart: affects channel or driver setup; offer an Asterisk
	// restart.
	ReloadRestart Reload = "restart"
)

// Scopes of a key besides a literal section name.
const (
	// ScopeNode: the key lives in each node's stanza of rpt.conf and may be
	// inherited from a template.
	ScopeNode = "node"
	// ScopeDevice: the key lives in the node's device stanza of the radio
	// interface file (simpleusb.conf).
	ScopeDevice = "device"
)

// Forms group the keys.
const (
	FormIdentity = "identity"
	FormDuplex   = "duplex"
	FormTimers   = "timers"
	FormLinking  = "linking"
	FormStatus   = "status"
	FormEchoLink = "echolink"
	FormAMI      = "ami"
	FormRadio    = "radio"
	FormGeneral  = "general"
)

// Key describes one setting.
type Key struct {
	// ID is "<file>:<scope>:<name>", e.g. "rpt.conf:node:hangtime".
	ID    string `json:"id"`
	File  string `json:"file"`
	Scope string `json:"scope"`
	Name  string `json:"name"`
	Label string `json:"label"`
	Form  string `json:"form"`
	Type  Type   `json:"type"`
	// Values are the allowed values of an enum; Labels their descriptions.
	Values []string `json:"values,omitempty"`
	Labels []string `json:"labels,omitempty"`
	// Min and Max bound an int (inclusive); nil: unbounded.
	Min *int64 `json:"min,omitempty"`
	Max *int64 `json:"max,omitempty"`
	// Unit: "ms", "s", "dB" or "".
	Unit    string `json:"unit,omitempty"`
	Default string `json:"default,omitempty"`
	// Desc says in plain words what the setting does on the air.
	Desc   string `json:"desc"`
	Reload Reload `json:"reload"`
	Secret bool   `json:"secret,omitempty"`
	// Optional: an empty value is allowed and means "not set" (the line is
	// removed instead of written empty).
	Optional bool `json:"optional,omitempty"`
}

func i64(n int64) *int64 { return &n }

func k(file, scope, name, label, form string, t Type, desc string) Key {
	return Key{ID: file + ":" + scope + ":" + name, File: file, Scope: scope, Name: name, Label: label,
		Form: form, Type: t, Desc: desc, Reload: ReloadAssumed}
}

func (x Key) rng(min, max int64) Key { x.Min, x.Max = i64(min), i64(max); return x }
func (x Key) unit(u string) Key      { x.Unit = u; return x }
func (x Key) def(d string) Key       { x.Default = d; return x }
func (x Key) restart() Key           { x.Reload = ReloadRestart; return x }
func (x Key) secret() Key            { x.Secret = true; return x }
func (x Key) opt() Key               { x.Optional = true; return x }
func (x Key) enum(vals []string, labels []string) Key {
	x.Type, x.Values, x.Labels = TypeEnum, vals, labels
	return x
}

const (
	rpt  = "rpt.conf"
	echo = "echolink.conf"
	susb = "simpleusb.conf"
)

var keys = []Key{
	// ---- Identity (rpt.conf, per node)
	k(rpt, ScopeNode, "idrecording", "Identification", FormIdentity, TypeString,
		"What the node sends to identify itself: `|i` followed by the callsign is sent in Morse code, anything else names a sound recording. Amateur radio rules usually require the callsign to be sent regularly.").def("|iNOTSET"),
	k(rpt, ScopeNode, "idtalkover", "Talkover identification", FormIdentity, TypeString,
		"What is sent instead of the normal identification while someone is talking, so the ID does not interrupt them. Leave empty for none.").opt(),
	k(rpt, ScopeNode, "idtime", "Identification interval", FormIdentity, TypeInt,
		"How often the node identifies itself while it is in use. 0 turns the periodic ID off.").rng(0, 9999999).unit("ms").def("300000"),
	k(rpt, ScopeNode, "politeid", "Polite ID window", FormIdentity, TypeInt,
		"How long before the ID is due the node tries to send it in a gap between transmissions rather than over a conversation.").rng(0, 9999999).unit("ms").def("30000"),
	k(rpt, ScopeNode, "callerid", "Caller ID", FormIdentity, TypeString,
		"The name and number shown to phone-style (autopatch) callers. Does not affect radio operation."),

	// ---- Radio interface (rpt.conf, per node)
	k(rpt, ScopeNode, "rxchannel", "Radio channel", FormRadio, TypeString,
		"Which interface carries the node's audio and radio signalling, for example SimpleUSB/<node> for a USB sound adapter, or Local/pseudo for a hub with no radio. The matching driver must be loaded in modules.conf.").restart(),

	// ---- Duplex and timers (rpt.conf, per node)
	k(rpt, ScopeNode, "duplex", "Duplex mode", FormDuplex, TypeEnum,
		"How the node's radio behaves. 0: half duplex, no telemetry tones or hang time (for a link radio or an external controller). 1: half duplex with tones and hang time, audio is not repeated (hotspot or simplex node). 2: full duplex with tones and hang time (repeater). 3: full duplex, audio is not repeated. 4: full duplex, audio is repeated only while no autopatch call is up.").
		enum([]string{"0", "1", "2", "3", "4"}, []string{"Half duplex, no tones", "Half duplex with tones", "Full duplex", "Full duplex, no repeated audio", "Full duplex, repeat unless autopatch"}).restart().def("1"),
	k(rpt, ScopeNode, "linktolink", "Link to link", FormDuplex, TypeYesNo,
		"With duplex 0, yes makes the node run full duplex (for a repeater controlled by an external controller). Otherwise yes disables forcing the radio to half duplex while keeping half duplex behaviour.").def("no"),
	k(rpt, ScopeNode, "hangtime", "Hang time", FormTimers, TypeInt,
		"How long the transmitter stays on after the last person stops talking, so replies are not clipped. Values below 1 ms count as 1 ms.").rng(0, 9999999).unit("ms").def("5000"),
	k(rpt, ScopeNode, "althangtime", "Alternate hang time", FormTimers, TypeInt,
		"A second, longer hang time that users can switch to with a DTMF function.").rng(0, 9999999).unit("ms"),
	k(rpt, ScopeNode, "totime", "Time-out timer", FormTimers, TypeInt,
		"The longest a single transmission may last before the transmitter is shut off. Protects the node from a stuck transmitter.").rng(0, 9999999).unit("ms").def("180000"),
	k(rpt, ScopeNode, "time_out_reset_unkey_interval", "Time-out reset gap", FormTimers, TypeInt,
		"After a time-out, how long the signal must be absent before the timer resets. Filters noise and picket fencing.").rng(0, 3000).unit("ms").def("0"),
	k(rpt, ScopeNode, "telemdefault", "Telemetry output", FormDuplex, TypeEnum,
		"Which beeps and voice messages (courtesy tones, connect messages) the node sends. 0: none. 1: all of them. 2: only for a short time after a command.").
		enum([]string{"0", "1", "2"}, []string{"Off", "On", "Timed"}).def("1"),
	k(rpt, ScopeNode, "telemdynamic", "Users may change telemetry", FormDuplex, TypeYesNo,
		"yes stops users from changing the telemetry setting with a DTMF command; no lets them.").def("no"),
	k(rpt, ScopeNode, "holdofftelem", "Hold off telemetry while receiving", FormDuplex, TypeFlag,
		"1 keeps beeps and messages from being sent while someone is being received.").def("0"),
	k(rpt, ScopeNode, "nounkeyct", "Disable courtesy tones", FormDuplex, TypeFlag,
		"1 turns the courtesy tone after each transmission off, together with the delay it adds.").def("0"),
	k(rpt, ScopeNode, "unlinkedct", "Courtesy tone, not linked", FormDuplex, TypeString,
		"Tone sent after a transmission while the node is not linked to another node. Name of an entry in the [telemetry] stanza.").opt(),
	k(rpt, ScopeNode, "remotect", "Courtesy tone, linked", FormDuplex, TypeString,
		"Tone sent after a transmission while a remote node is linked. Name of an entry in the [telemetry] stanza.").opt(),
	k(rpt, ScopeNode, "linkunkeyct", "Courtesy tone, link unkeys", FormDuplex, TypeString,
		"Tone sent when a transmission arriving over a link ends. Name of an entry in the [telemetry] stanza.").opt(),
	k(rpt, ScopeNode, "tailmessagetime", "Tail message interval", FormTimers, TypeInt,
		"How often a tail message (a recording played between transmissions) is sent. 0 turns tail messages off.").rng(0, 200000000).unit("ms").opt(),
	k(rpt, ScopeNode, "parrot", "Parrot mode", FormTimers, TypeEnum,
		"1 echoes everything the node receives back to the sender after a short wait. 0 leaves it off unless switched on with a DTMF command.").
		enum([]string{"0", "1"}, []string{"Off", "Always on"}).def("0"),

	// ---- Linking and telemetry (rpt.conf, per node)
	k(rpt, ScopeNode, "linkmongain", "Monitored link gain", FormLinking, TypeInt,
		"Volume change in dB for nodes that are only monitored. Negative is quieter.").rng(-60, 60).unit("dB").def("0"),
	k(rpt, ScopeNode, "erxgain", "EchoLink receive gain", FormLinking, TypeInt,
		"Volume change in dB for audio arriving from EchoLink.").rng(-60, 60).unit("dB").def("0"),
	k(rpt, ScopeNode, "etxgain", "EchoLink transmit gain", FormLinking, TypeInt,
		"Volume change in dB for audio sent to EchoLink.").rng(-60, 60).unit("dB").def("0"),
	k(rpt, ScopeNode, "context", "Dialplan context", FormLinking, TypeString,
		"Which part of extensions.conf handles calls to and from this node. Change only if you know the dialplan.").def("radio"),
	k(rpt, ScopeNode, "startup_macro", "Startup macro", FormLinking, TypeString,
		"DTMF commands run once when the node starts, for example to connect to a favourite node. Needs a restart of Asterisk to take effect at the next start.").opt(),

	k(rpt, ScopeNode, "telemwait", "Telemetry delay", FormLinking, TypeInt,
		"How long the node waits before sending most telemetry, so it does not step on the end of a transmission.").rng(0, 100000).unit("ms").opt(),
	k(rpt, ScopeNode, "telemnomdb", "Telemetry level", FormLinking, TypeInt,
		"Nominal level of telemetry sounds in dB. Negative is quieter.").rng(-60, 0).unit("dB").opt(),
	k(rpt, ScopeNode, "telemduckdb", "Telemetry ducking", FormLinking, TypeInt,
		"How much telemetry is turned down in dB while voice is being transmitted.").rng(-60, 0).unit("dB").opt(),
	k(rpt, ScopeNode, "parrottime", "Parrot wait", FormLinking, TypeInt,
		"How long the parrot waits after you stop talking before it plays your audio back.").rng(0, 100000).unit("ms").opt(),

	// ---- Status reporting (rpt.conf, per node)
	k(rpt, ScopeNode, "statpost_url", "Status reporting address", FormStatus, TypeString,
		"Where the node reports its status and links, for the public statistics pages. Empty: no reporting.").opt(),
	k(rpt, ScopeNode, "statpost_time", "Status reporting interval", FormStatus, TypeInt,
		"How often the node reports its connected links.").rng(30, 600).unit("s").def("60").opt(),

	// ---- General (rpt.conf [general])
	k(rpt, "general", "node_lookup_method", "Node lookup method", FormGeneral, TypeEnum,
		"How other nodes are found: dns asks the AllStar DNS, file uses a local list, both tries DNS first.").
		enum([]string{"dns", "both", "file"}, []string{"DNS only", "DNS, then file", "File only"}).def("dns"),

	// ---- AMI (manager.conf)
	k("manager.conf", "general", "enabled", "Manager interface enabled", FormAMI, TypeYesNo,
		"Whether Asterisk accepts management connections. ASLCP and other tools need this on.").def("no").restart(),
	k("manager.conf", "general", "bindaddr", "Manager listen address", FormAMI, TypeString,
		"Which address the management interface listens on. 127.0.0.1 allows this computer only.").def("0.0.0.0").restart(),
	k("manager.conf", "general", "port", "Manager port", FormAMI, TypeInt,
		"TCP port of the management interface.").rng(1, 65535).def("5038").restart(),
	k("manager.conf", "admin", "permit", "Allowed networks", FormAMI, TypeString,
		"Networks the admin login may connect from, as address/mask (for example 127.0.0.1/255.255.255.255). This sets one entry; if the file has several, edit them in Advanced.").opt(),
	k("manager.conf", "admin", "deny", "Denied networks", FormAMI, TypeString,
		"Networks the admin login may not connect from. Usually 0.0.0.0/0.0.0.0, which together with a permit line allows only the permitted networks.").opt(),
	k("manager.conf", "admin", "secret", "Manager password", FormAMI, TypeString,
		"Password of the admin management login. Changing it breaks every tool that still uses the old one, ASLCP included until it is told.").secret(),

	// ---- IAX (iax.conf [general])
	k("iax.conf", "general", "bindport", "IAX port", FormLinking, TypeInt,
		"UDP port nodes use to link to this node. Other nodes must be told if it is not 4569.").rng(1, 65535).def("4569").restart(),

	// ---- EchoLink (echolink.conf [el0])
	k(echo, "el0", "call", "EchoLink callsign", FormEchoLink, TypeString,
		"Your EchoLink callsign, with -L or -R for a link or repeater station."),
	k(echo, "el0", "pwd", "EchoLink password", FormEchoLink, TypeString,
		"Your EchoLink password.").secret(),
	k(echo, "el0", "name", "Name shown on EchoLink", FormEchoLink, TypeString,
		"The name other EchoLink users see."),
	k(echo, "el0", "qth", "Location shown on EchoLink", FormEchoLink, TypeString,
		"The location other EchoLink users see."),
	k(echo, "el0", "email", "Contact e-mail", FormEchoLink, TypeString,
		"Contact address sent to the EchoLink directory."),
	k(echo, "el0", "astnode", "AllStar node", FormEchoLink, TypeNode,
		"The node that EchoLink calls are connected to. Must be one of this server's nodes.").restart(),
	k(echo, "el0", "maxstns", "Most stations", FormEchoLink, TypeInt,
		"The most EchoLink stations that may be connected at once.").rng(1, 200).def("20"),
	k(echo, "el0", "freq", "Frequency", FormEchoLink, TypeString,
		"Frequency in MHz shown in the EchoLink directory. Not required."),
	k(echo, "el0", "tone", "CTCSS tone", FormEchoLink, TypeString,
		"CTCSS tone in Hz shown in the EchoLink directory. 0 for none."),
	k(echo, "el0", "power", "Power class", FormEchoLink, TypeInt,
		"Directory power class, 0 to 9 (0 W, 1 W, 4 W, 9 W and so on).").rng(0, 9),
	k(echo, "el0", "height", "Antenna height class", FormEchoLink, TypeInt,
		"Directory antenna height class, 0 to 9.").rng(0, 9),
	k(echo, "el0", "gain", "Antenna gain", FormEchoLink, TypeInt,
		"Directory antenna gain in dB, 0 to 9.").rng(0, 9),
	k(echo, "el0", "dir", "Antenna direction", FormEchoLink, TypeInt,
		"Directory antenna direction, 0 (omni) to 8.").rng(0, 8),

	// ---- SimpleUSB device (simpleusb.conf, the node's device stanza)
	k(susb, ScopeDevice, "carrierfrom", "Carrier detect from", FormRadio, TypeEnum,
		"Where the node learns that a signal is being received: nowhere (no), the USB adapter pin (usb) or the inverted pin (usbinvert).").
		enum([]string{"no", "usb", "usbinvert"}, []string{"Not used", "USB pin", "USB pin, inverted"}),
	k(susb, ScopeDevice, "ctcssfrom", "CTCSS detect from", FormRadio, TypeEnum,
		"Where the node learns that the right CTCSS tone is present: nowhere (no), the USB adapter pin (usb) or the inverted pin (usbinvert).").
		enum([]string{"no", "usb", "usbinvert"}, []string{"Not used", "USB pin", "USB pin, inverted"}),
	k(susb, ScopeDevice, "devstr", "USB device path", FormRadio, TypeString,
		"Which USB sound adapter this device uses, as the bus path (for example 1-1.4:1.0). Leave empty when there is only one adapter: it is found automatically.").opt(),
	k(susb, ScopeDevice, "serial", "USB serial number", FormRadio, TypeString,
		"Picks the adapter by its USB serial number instead of its port, so it keeps working when moved to another port. Leave empty when not needed.").opt(),
	k(susb, ScopeDevice, "invertptt", "Invert PTT", FormRadio, TypeYesNo,
		"yes if the radio transmits when the PTT line is open instead of closed.").def("no"),
	k(susb, ScopeDevice, "rxboost", "Receive boost", FormRadio, TypeYesNo,
		"yes removes the 20 dB attenuator on the receive audio input of the adapter.").def("no"),
	k(susb, ScopeDevice, "preemphasis", "Pre-emphasis", FormRadio, TypeYesNo,
		"yes applies the standard 6 dB per octave pre-emphasis to transmit audio.").def("no"),
	k(susb, ScopeDevice, "deemphasis", "De-emphasis", FormRadio, TypeYesNo,
		"yes applies de-emphasis to received audio, for a radio whose audio comes straight from the discriminator.").def("no"),
	k(susb, ScopeDevice, "plfilter", "PL filter", FormRadio, TypeYesNo,
		"yes filters the CTCSS tone out of received audio.").def("yes"),
	k(susb, ScopeDevice, "rxondelay", "Receive on delay", FormRadio, TypeInt,
		"Number of 20 ms audio frames the signal must be present before the node treats it as received. Use only on simplex nodes, to stop two nodes ping-ponging; leave 0 on repeaters.").rng(0, 50).def("0"),
}

var byID = func() map[string]Key {
	m := make(map[string]Key, len(keys))
	for _, x := range keys {
		if _, dup := m[x.ID]; dup {
			panic("schema: duplicate key " + x.ID)
		}
		m[x.ID] = x
	}
	return m
}()

// All returns every key, in a stable order.
func All() []Key { return append([]Key(nil), keys...) }

// Lookup returns the key with the given ID.
func Lookup(id string) (Key, bool) { k, ok := byID[id]; return k, ok }

// Find returns the key for a file, scope and name (name compares
// case-insensitively, as Asterisk does).
func Find(file, scope, name string) (Key, bool) {
	for _, x := range keys {
		if x.File == file && x.Scope == scope && strings.EqualFold(x.Name, name) {
			return x, true
		}
	}
	return Key{}, false
}

// ForForm returns the keys of a form, in order.
func ForForm(form string) []Key {
	var out []Key
	for _, x := range keys {
		if x.Form == form {
			out = append(out, x)
		}
	}
	return out
}

// Forms returns the form names that have keys, sorted.
func Forms() []string {
	seen := map[string]bool{}
	for _, x := range keys {
		seen[x.Form] = true
	}
	out := make([]string, 0, len(seen))
	for f := range seen {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// DefaultStatpostURL is the stock status reporting address: the commented
// example in the ASL3 rpt.conf (internal/astconf/testdata/corpus).
const DefaultStatpostURL = "http://stats.allstarlink.org/uhandler"

// ValidateError is a rejected value.
type ValidateError struct {
	Key string
	Msg string
}

func (e *ValidateError) Error() string { return e.Key + ": " + e.Msg }

var reNode = regexp.MustCompile(`^[1-9][0-9]{0,9}$`)

// Validate checks a value against the key's type, range and allowed values.
// An empty value is valid for an Optional key only.
func (x Key) Validate(v string) error {
	bad := func(f string, a ...any) error { return &ValidateError{Key: x.Name, Msg: fmt.Sprintf(f, a...)} }
	if v == "" {
		if x.Optional {
			return nil
		}
		return bad("a value is required")
	}
	if strings.ContainsAny(v, "\r\n\x00") {
		return bad("must be on one line")
	}
	if strings.ContainsRune(strings.ReplaceAll(v, `\;`, ""), ';') {
		return bad("must not contain ';' (it starts a comment); put a backslash before it for a literal one")
	}
	if strings.TrimSpace(v) != v {
		return bad("must not start or end with a space")
	}
	switch x.Type {
	case TypeString:
		if x.Name == "statpost_url" {
			u, err := url.Parse(v)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || strings.ContainsAny(v, " \t\"'`$\\") {
				return bad("must be a web address starting with http:// or https://, without a user name or password in it")
			}
		}
		return nil
	case TypeInt:
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return bad("must be a whole number")
		}
		if x.Min != nil && n < *x.Min {
			return bad("must be at least %d", *x.Min)
		}
		if x.Max != nil && n > *x.Max {
			return bad("must be at most %d", *x.Max)
		}
	case TypeYesNo:
		if v != "yes" && v != "no" {
			return bad("must be yes or no")
		}
	case TypeFlag:
		if v != "0" && v != "1" {
			return bad("must be 0 or 1")
		}
	case TypeEnum:
		for _, a := range x.Values {
			if a == v {
				return nil
			}
		}
		return bad("must be one of %s", strings.Join(x.Values, ", "))
	case TypeNode:
		if !reNode.MatchString(v) {
			return bad("must be a node number")
		}
	}
	return nil
}
