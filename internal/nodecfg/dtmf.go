package nodecfg

import (
	"fmt"
	"strings"

	"github.com/artw1/aslcp/internal/astconf"
)

// DTMFItem is one line of a DTMF function, telemetry or macro stanza.
type DTMFItem struct {
	// Code is the key: the digits typed after the prefix (functions, macros)
	// or the telemetry name.
	Code string `json:"code"`
	// Action is the raw value, shown as it is written.
	Action string `json:"action"`
	// Desc is the plain-language meaning, "" when this code is not known
	// (the raw line is shown then).
	Desc string `json:"desc,omitempty"`
	File string `json:"file"`
	Line int    `json:"line"`
	// Section is the stanza that holds the line (a template for inherited ones).
	Section   string `json:"section"`
	Inherited bool   `json:"inherited,omitempty"`
}

// DTMFGroup is one stanza a node points to.
type DTMFGroup struct {
	// Kind is the rpt.conf key: functions, link_functions, phone_functions,
	// telemetry or macro.
	Kind  string `json:"kind"`
	Title string `json:"title"`
	// Stanza is the section name ("" when the node does not name one).
	Stanza string `json:"stanza,omitempty"`
	// Assumed: the node does not set the key, the stanza name is app_rpt's
	// default (macro only).
	Assumed bool `json:"assumed,omitempty"`
	// SameAs names an earlier group that uses the same stanza (its items are
	// not repeated).
	SameAs string `json:"sameAs,omitempty"`
	File   string `json:"file,omitempty"`
	Line   int    `json:"line,omitempty"`
	// Note says why Items is empty or what to know (a refusal in words).
	Note  string     `json:"note,omitempty"`
	Items []DTMFItem `json:"items"`
}

// DTMF is the read-only view of a node's DTMF commands and macros.
type DTMF struct {
	Node   string      `json:"node"`
	Groups []DTMFGroup `json:"groups"`
}

var dtmfKinds = []struct{ key, title string }{
	{"functions", "Repeater functions"},
	{"link_functions", "Link functions"},
	{"phone_functions", "Phone functions"},
	{"telemetry", "Telemetry"},
	{"macro", "Macros"},
}

// DescribeAction gives the plain-language meaning of a function value such as
// "ilink,5" or "cop,6"; extra arguments after the number are ignored for the
// lookup. It returns "" for an unknown action.
func DescribeAction(v string) string {
	parts := strings.Split(v, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	cmd := strings.ToLower(parts[0])
	if cmd == "" {
		return ""
	}
	if len(parts) > 1 {
		k := cmd + "," + strings.ToLower(parts[1])
		if d, ok := dtmfCorpus[k]; ok {
			return d
		}
		if d, ok := dtmfExtra[k]; ok {
			return d
		}
	}
	return dtmfExtra[cmd]
}

// DTMFFor builds the DTMF view of a node. A stanza that cannot be resolved
// (missing, defined twice, spread over files) gets a note and no items:
// forms and this view refuse rather than guess.
func (w *World) DTMFFor(node string) (*DTMF, error) {
	tree := w.Trees[FileRpt]
	si := resolveStanza(tree, node, FileRpt)
	if si.refusal != nil {
		return nil, refuse(*si.refusal)
	}
	out := &DTMF{Node: node, Groups: []DTMFGroup{}}
	seen := map[string]string{}
	for _, k := range dtmfKinds {
		g := DTMFGroup{Kind: k.key, Title: k.title, Items: []DTMFItem{}}
		var name string
		for _, e := range si.eff {
			if strings.EqualFold(e.Key, k.key) {
				name = strings.TrimSpace(e.Value)
			}
		}
		if name == "" {
			if k.key != "macro" {
				g.Note = "This node does not set " + k.key + "."
				out.Groups = append(out.Groups, g)
				continue
			}
			name, g.Assumed = "macro", true
		}
		g.Stanza = name
		if first, ok := seen[name]; ok && k.key != "macro" {
			g.SameAs = first
			out.Groups = append(out.Groups, g)
			continue
		}
		seen[name] = k.key
		w.fillGroup(&g, name)
		out.Groups = append(out.Groups, g)
	}
	return out, nil
}

func (w *World) fillGroup(g *DTMFGroup, name string) {
	tree := w.Trees[FileRpt]
	defs := tree.Lookup(name)
	if len(defs) == 0 {
		g.Note = fmt.Sprintf("rpt.conf has no [%s] section.", name)
		return
	}
	g.File, g.Line = defs[0].File, defs[0].Line
	var entries []astconf.ResolvedEntry
	if g.Kind == "macro" {
		// A macro stanza may continue in custom/rpt/*.conf: every definition counts.
		for _, d := range defs {
			for _, e := range d.Entries {
				entries = append(entries, astconf.ResolvedEntry{Key: e.Key, Op: e.Op, Value: e.Value, File: e.File, Line: e.Line, Section: name})
			}
		}
	} else {
		if len(defs) > 1 {
			g.Note = fmt.Sprintf("[%s] is defined more than once, so it is shown in Advanced only.", name)
			return
		}
		eff, err := astconf.Effective(tree, name)
		if err != nil {
			g.Note = fmt.Sprintf("[%s] cannot be resolved (%v).", name, err)
			return
		}
		entries = eff
	}
	for _, e := range entries {
		it := DTMFItem{Code: e.Key, Action: e.Value, File: e.File, Line: e.Line, Section: e.Section, Inherited: e.Inherited}
		switch g.Kind {
		case "telemetry":
			it.Desc = dtmfTelemetry[strings.ToLower(e.Key)]
		case "macro":
		default:
			it.Desc = DescribeAction(e.Value)
		}
		g.Items = append(g.Items, it)
	}
	if len(g.Items) == 0 {
		g.Note = "No entries."
	}
}
