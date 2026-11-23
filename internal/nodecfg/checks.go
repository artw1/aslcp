package nodecfg

import (
	"fmt"
	"regexp"
	"strings"
)

// Finding levels.
const (
	LevelError   = "error"
	LevelWarning = "warning"
	LevelInfo    = "info"
)

// Finding codes of the cross-field checks.
const (
	CodeDriverNotLoaded = "driver_not_loaded"
	CodeDriverNoLoad    = "driver_noload"
	CodeNoDeviceStanza  = "no_device_stanza"
	CodeRegBoth         = "registered_both"
	CodePrivateRegister = "private_registered"
	CodePublicNoReg     = "public_not_registered"
	CodeRegMismatch     = "register_node_mismatch"
	CodeCallsign        = "callsign_format"
	CodeEchoNoModule    = "echolink_module"
	CodeNodeInNodes     = "node_not_in_nodes"
)

// reCallsign accepts amateur-style callsigns (letters and digits, at least one
// of each, optional /suffix), 3 to 12 characters.
var reCallsign = regexp.MustCompile(`^[A-Z0-9]{1,3}[0-9][A-Z0-9]{0,4}[A-Z](/[A-Z0-9]{1,4})?$`)

// ValidCallsign reports whether s looks like a callsign (upper case).
func ValidCallsign(s string) bool {
	return len(s) >= 3 && len(s) <= 12 && reCallsign.MatchString(s)
}

// nodeFindings are the cross-field checks of one node.
func (w *World) nodeFindings(n string, si *stanzaInfo) []Finding {
	var out []Finding
	add := func(level, code, file, format string, a ...any) {
		out = append(out, Finding{Level: level, Code: code, Node: n, File: file, Message: fmt.Sprintf(format, a...)})
	}
	if si != nil && si.refusal == nil {
		r := w.radio(si)
		if d, ok := drivers[strings.ToLower(splitType(r.Channel))]; ok && r.Module != "" {
			switch r.ModuleState {
			case "noload":
				add(LevelError, CodeDriverNoLoad, FileModules, "rxchannel %s needs %s, but modules.conf has it set to noload.", r.Channel, d.module)
			case "off":
				add(LevelError, CodeDriverNotLoaded, FileModules, "rxchannel %s needs %s, but modules.conf does not load it.", r.Channel, d.module)
			}
			if d.file != "" && w.Trees[d.file] != nil && !r.DeviceStanza && r.Device != "" {
				add(LevelError, CodeNoDeviceStanza, d.file, "rxchannel %s has no [%s] section in %s.", r.Channel, r.Device, d.file)
			}
		}
		if e := lastEntry(si.eff, "idrecording"); e != nil {
			if c := CallsignOf(e.Value); c != "" && !ValidCallsign(c) {
				add(LevelWarning, CodeCallsign, FileRpt, "The callsign %q does not look like a callsign (letters and digits, for example N0CALL).", c)
			}
		}
	}
	reg := w.registration(n)
	priv := IsPrivate(n)
	switch {
	case reg.Method == RegBoth:
		add(LevelError, CodeRegBoth, FileHTTPReg, "Node %s is registered both over HTTP and over IAX. Use one method.", n)
	case priv && reg.Method != RegNone:
		add(LevelWarning, CodePrivateRegister, FileIAX, "Node %s is a private node number (1 to 1999) but has a registration line; private nodes are not in the public directory.", n)
	case !priv && reg.Method == RegNone:
		add(LevelWarning, CodePublicNoReg, FileHTTPReg, "Node %s is a public node number but is not registered, so other nodes cannot find it.", n)
	}
	el := w.echolink(n)
	if el.Linked && el.Configured && !ModuleLoaded(el.ModuleState) && el.ModuleState != "unknown" {
		add(LevelError, CodeEchoNoModule, FileModules, "EchoLink is set up for node %s, but chan_echolink.so is not loaded in modules.conf.", n)
	}
	return out
}

func splitType(ch string) string { t, _ := SplitChannel(ch); return t }

// globalFindings are checks across nodes.
func (w *World) globalFindings(nodes []string) []Finding {
	var out []Finding
	set := map[string]bool{}
	for _, n := range nodes {
		set[n] = true
	}
	for _, t := range []string{FileHTTPReg, FileIAX} {
		for _, l := range regLines(w.Trees[t]) {
			if !set[l.Node] && !l.Placeholder {
				out = append(out, Finding{Level: LevelWarning, Code: CodeRegMismatch, File: t,
					Message: fmt.Sprintf("%s:%d registers node %s, which has no stanza in rpt.conf.", l.File, l.Line, l.Node)})
			}
		}
	}
	return out
}
