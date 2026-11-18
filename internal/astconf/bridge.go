package astconf

import (
	"fmt"
	"strings"
)

// Helpers of the DVSwitch private bridge node (docs/DVSWITCH.md, M2): the
// rpt.conf stanza and [nodes] line of the USRP node and the chan_usrp line of
// modules.conf. They are pure text-to-text functions so that the plan shown
// to the operator is exactly what is written, and the uninstall can revert
// precisely what was added.

// ModuleUSRP is the chan_usrp module as modules.conf names it.
const ModuleUSRP = "chan_usrp.so"

// BridgeSpec describes the node to add.
type BridgeSpec struct {
	Node string
	// RxPort and TxPort are Analog_Bridge's [USRP] rxPort and txPort. The
	// rpt.conf channel is USRP/127.0.0.1:<RxPort>:<TxPort>: Asterisk sends to
	// the first (Analog_Bridge's rxPort) and listens on the second.
	RxPort, TxPort int
	// Local is the local node whose settings the stanza inherits.
	Local string
}

// BridgeLayout says how the stanza was built.
const (
	LayoutTemplate = "template" // [node](template) of the local node
	LayoutFlat     = "flat"     // the local node has no template: its stanza names are copied
	LayoutMinimal  = "minimal"  // no local node: a minimal self-contained stanza
	LayoutExisting = "existing" // the node already had a USRP stanza: only rxchannel and duplex set
)

// BridgeResult is the outcome of AddBridgeNode.
type BridgeResult struct {
	Out    []byte
	Layout string
	// StanzaAdded is false when the stanza existed (LayoutExisting).
	StanzaAdded bool
	// StanzaText is the stanza as it now stands (header and entries, no
	// trailing blank line): the uninstall removes it only if it still matches.
	StanzaText string
	// NodesLineAdded / NodesLine: the [nodes] entry, as written.
	NodesLineAdded bool
	NodesLine      string
	// BlankAdded: AddSection put a blank line before the stanza (pass it to
	// RemoveBridgeNode to restore the file exactly).
	BlankAdded bool
}

// BridgeChannel is the rxchannel value of the node.
func BridgeChannel(rx, tx int) string { return fmt.Sprintf("USRP/127.0.0.1:%d:%d", rx, tx) }

// stanza names that a flat stanza copies from the local node (the controls
// the bridge node needs to behave like a node).
var flatCopy = []string{"functions", "link_functions", "phone_functions", "telemetry", "morse", "wait_times", "context"}

// neutralKeys are written to every new bridge stanza so that the node does not
// inherit the local node's ID, telemetry, courtesy tones or hang time, which
// would otherwise be played out to DMR/YSF. Sources: app_rpt
// (github.com/AllStarLink/app_rpt, master as read 2026-11-18).
//
//   - hangtime/althangtime: load_rpt_vars() in apps/app_rpt/rpt_config.c
//     (RPT_CONFIG_VAR_INT_MIN_FLOOR) floors them at 1 ms, so 0 means 1 ms.
//   - idtime = 0: load_rpt_vars() reads it with retrieve_astcfgint(min -60000),
//     where a negative min means "zero is valid" (rpt_config.c,
//     retrieve_astcfgint), and apps/app_rpt.c (queue_id, the scheduler in
//     rpt_master) only IDs when p.idtime is non-zero ("ID time must be
//     non-zero"). That stops the periodic ID and the talkover ID, which is
//     only started from queue_id and while an ID is playing. An empty
//     idrecording would not do: the value is then "" (non-NULL) and is played.
//   - tailmessagetime = 0: no tail messages (app_rpt.c tests p.tailmessagetime).
//   - statpost_url is deliberately not written: RPT_CONFIG_VAR keeps an empty
//     value as a non-NULL "" and statpost()/do_link_post() in app_rpt.c only
//     skip posting when the pointer is NULL, so an empty value would post to
//     "?node=...". It cannot be unset from a child stanza. The stock
//     [node-main] has it commented out; if the operator enabled it there, the
//     bridge node posts too (docs/DVSWITCH.md).
var neutralKeys = [][2]string{
	{"hangtime", "0"},
	{"althangtime", "0"},
	{"holdofftelem", "1"},
	{"telemdefault", "0"},
	{"telemdynamic", "0"},
	{"nounkeyct", "1"},
	{"linktolink", "yes"},
	{"idtime", "0"},
	{"tailmessagetime", "0"},
}

// sectionText returns the section's text without trailing blank lines.
func sectionText(s *Section) string {
	ls := s.Lines()
	for len(ls) > 0 && ls[len(ls)-1].Kind == KindBlank {
		ls = ls[:len(ls)-1]
	}
	var b strings.Builder
	for _, l := range ls {
		b.WriteString(l.Raw)
	}
	t := b.String()
	return strings.TrimRight(t, "\r\n")
}

func nodesSection(f *File) *Section {
	ss := f.Section("nodes")
	if len(ss) == 0 {
		return nil
	}
	return ss[len(ss)-1]
}

// AddBridgeNode adds the bridge node to rpt.conf: its stanza (inheriting the
// local node's template, or a flat stanza when there is none) and its
// [nodes] line `<node> = radio@127.0.0.1/<node>,NONE`. Duplex is 0.
func AddBridgeNode(rpt []byte, spec BridgeSpec) (BridgeResult, error) {
	var res BridgeResult
	f, err := Parse("rpt.conf", rpt)
	if err != nil {
		return res, err
	}
	ch := BridgeChannel(spec.RxPort, spec.TxPort)
	if ss := f.Section(spec.Node); len(ss) > 0 {
		s := ss[len(ss)-1]
		if es := s.Get("rxchannel"); len(es) == 0 || !strings.HasPrefix(strings.ToLower(strings.TrimSpace(es[len(es)-1].Value)), "usrp/") {
			return res, fmt.Errorf("node %s already exists and is not a USRP node", spec.Node)
		}
		s.Set("rxchannel", ch)
		s.Set("duplex", "0")
		res.Layout = LayoutExisting
		res.StanzaText = sectionText(s)
	} else {
		var parents []string
		var local *Section
		if ls := f.Section(spec.Local); spec.Local != "" && len(ls) > 0 {
			local = ls[len(ls)-1]
		}
		switch {
		case local != nil && len(local.Parents) > 0:
			parents = []string{local.Parents[0]}
			res.Layout = LayoutTemplate
		case local != nil:
			res.Layout = LayoutFlat
		default:
			res.Layout = LayoutMinimal
		}
		ls := f.Lines()
		res.BlankAdded = len(ls) > 0 && ls[len(ls)-1].Kind != KindBlank
		s := f.AddSection(spec.Node, false, parents)
		s.Add("rxchannel", "", ch)
		s.Add("duplex", "", "0")
		s.Add("callerid", "", fmt.Sprintf("\"DVSwitch %s\" <0000000000>", spec.Node))
		for _, kv := range neutralKeys {
			s.Add(kv[0], "", kv[1])
		}
		if res.Layout == LayoutMinimal {
			s.Add("context", "", "radio")
		}
		if res.Layout == LayoutFlat {
			for _, k := range flatCopy {
				if es := local.Get(k); len(es) > 0 {
					s.Add(k, "", es[len(es)-1].Value)
				}
			}
		}
		res.StanzaAdded = true
		res.StanzaText = sectionText(s)
	}
	if ns := nodesSection(f); ns == nil {
		ns = f.AddSection("nodes", false, nil)
		res.NodesLine, res.NodesLineAdded = addNodesLine(ns, spec.Node)
	} else if len(ns.Get(spec.Node)) == 0 {
		res.NodesLine, res.NodesLineAdded = addNodesLine(ns, spec.Node)
	} else {
		res.NodesLine = strings.TrimRight(ns.Get(spec.Node)[0].Raw, "\r\n")
	}
	res.Out = f.Bytes()
	return res, nil
}

func addNodesLine(ns *Section, node string) (string, bool) {
	e := ns.Add(node, "", fmt.Sprintf("radio@127.0.0.1/%s,NONE", node))
	if e == nil {
		return "", false
	}
	return strings.TrimRight(e.Raw, "\r\n"), true
}

// RemoveBridgeNode reverts AddBridgeNode: the stanza is removed only if the
// section still reads exactly as recorded, the [nodes] line only if it still
// reads exactly as recorded. Each refused step is described in problems; the
// other step is still done.
func RemoveBridgeNode(rpt []byte, node, stanza, nodesLine string, blankAdded bool) (out []byte, removedStanza, removedLine bool, problems []string) {
	f, err := Parse("rpt.conf", rpt)
	if err != nil {
		return rpt, false, false, []string{err.Error()}
	}
	if stanza != "" {
		ss := f.Section(node)
		switch {
		case len(ss) == 0:
			problems = append(problems, fmt.Sprintf("the stanza [%s] is already gone from rpt.conf", node))
		case len(ss) > 1 || sectionText(ss[0]) != stanza:
			problems = append(problems, fmt.Sprintf("the stanza [%s] in rpt.conf was changed by hand and is left as it is", node))
		default:
			removedStanza = f.RemoveSection(ss[0], blankAdded)
		}
	}
	if nodesLine != "" {
		found := false
		if ns := nodesSection(f); ns != nil {
			for _, e := range ns.Get(node) {
				if strings.TrimRight(e.Raw, "\r\n") == nodesLine {
					removedLine = ns.Delete(e)
					found = true
					break
				}
			}
			if !found && len(ns.Get(node)) > 0 {
				found = true
				problems = append(problems, fmt.Sprintf("the [nodes] line of %s was changed by hand and is left as it is", node))
			}
		}
		if !found {
			problems = append(problems, fmt.Sprintf("the [nodes] line of %s is already gone", node))
		}
	}
	return f.Bytes(), removedStanza, removedLine, problems
}

// Module states reported by SetModuleLoad.
const (
	ModNoload = "noload"
	ModLoad   = "load"
	ModAbsent = "absent"
)

func moduleEntry(f *File, key, mod string) *Entry {
	for _, s := range f.Section("modules") {
		for _, e := range s.Get(key) {
			if strings.EqualFold(strings.TrimSpace(e.Value), mod) {
				return e
			}
		}
	}
	return nil
}

// SetModuleLoad makes modules.conf load the module: `noload = mod` becomes
// `load = mod` in place (comment and column kept), an absent module gets a
// `load = mod` line in [modules]. was is the state before the edit.
func SetModuleLoad(data []byte, mod string) (out []byte, was string, err error) {
	f, err := Parse("modules.conf", data)
	if err != nil {
		return data, "", err
	}
	if moduleEntry(f, "load", mod) != nil && moduleEntry(f, "noload", mod) == nil {
		return data, ModLoad, nil
	}
	if e := moduleEntry(f, "noload", mod); e != nil {
		e.SetKey("load")
		return f.Bytes(), ModNoload, nil
	}
	ss := f.Section("modules")
	var s *Section
	if len(ss) > 0 {
		s = ss[len(ss)-1]
	} else {
		s = f.AddSection("modules", false, nil)
	}
	s.Add("load", "", mod)
	return f.Bytes(), ModAbsent, nil
}

// RevertModuleLoad undoes SetModuleLoad given the state it reported:
// ModNoload turns `load = mod` back into `noload = mod`, ModAbsent removes the
// line that was added. changed is false when the file no longer has the line.
func RevertModuleLoad(data []byte, mod, was string) (out []byte, changed bool, err error) {
	f, err := Parse("modules.conf", data)
	if err != nil {
		return data, false, err
	}
	e := moduleEntry(f, "load", mod)
	if e == nil || was == ModLoad || was == "" {
		return data, false, nil
	}
	if was == ModNoload {
		e.SetKey("noload")
		return f.Bytes(), true, nil
	}
	for _, s := range f.Section("modules") {
		if s.Delete(e) {
			return f.Bytes(), true, nil
		}
	}
	return data, false, nil
}
