package nodecfg

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/artw1/aslcp/internal/astconf"
	"github.com/artw1/aslcp/internal/schema"
)

// Masked stands for a secret value in every response.
const Masked = "********"

// Source kinds of a field's provenance.
const (
	SrcNode     = "node"     // set on this node's own stanza
	SrcTemplate = "template" // inherited from a template
	SrcSection  = "section"  // set in the file's section (keys that have no template)
	SrcDefault  = "default"  // not set: the schema's default applies
	SrcUnset    = "unset"    // not set and no known default
)

// Source says where a field's value comes from.
type Source struct {
	Kind    string `json:"kind"`
	Section string `json:"section,omitempty"`
	File    string `json:"file,omitempty"`
	Line    int    `json:"line,omitempty"`
}

// Inherited is the value a template gives to a key the node overrides.
type Inherited struct {
	Value   string `json:"value"`
	Section string `json:"section"`
}

// Field is one schema key of a node with its resolved value.
type Field struct {
	ID    string `json:"id"`
	Key   string `json:"key"`
	Value string `json:"value"`
	// Set: a value is written in the files (Source says where).
	Set    bool   `json:"set"`
	Source Source `json:"source"`
	// Overrides: the node sets the key and a template gives it a value too.
	Overrides *Inherited `json:"overrides,omitempty"`
	Masked    bool       `json:"masked,omitempty"`
	// CanSet: "Save" writes it (an override on the node, or the section).
	// CanReset: the node has an override that "Reset to template" removes.
	// CanTemplate: "Change for all nodes" can edit the template.
	CanSet      bool `json:"canSet"`
	CanReset    bool `json:"canReset"`
	CanTemplate bool `json:"canTemplate"`
	// Locked says why the key cannot be edited here ("" when it can).
	Locked string `json:"locked,omitempty"`
}

// OtherKey is a key of the node's effective stanza that the schema does not
// know. It is shown read-only and always preserved.
type OtherKey struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Source Source `json:"source"`
	Masked bool   `json:"masked,omitempty"`
}

// Stanza locates the node's stanza.
type Stanza struct {
	File    string   `json:"file"`
	Line    int      `json:"line"`
	Parents []string `json:"parents"`
	// Template is the template the stanza inherits from ("" when none).
	Template string `json:"template,omitempty"`
}

// Radio interface types.
const (
	RadioSimpleUSB = "simpleusb"
	RadioUSBRadio  = "usbradio"
	RadioVoter     = "voter"
	RadioUSRP      = "usrp"
	RadioHub       = "hub"
	RadioOther     = "other"
	RadioNone      = "none" // no rxchannel at all
)

// Radio describes the node's radio interface.
type Radio struct {
	Type    string `json:"type"`
	Channel string `json:"channel"`
	// Module is the channel driver as modules.conf names it ("" for a hub).
	Module string `json:"module,omitempty"`
	// ModuleState: "explicit", "auto", "noload", "off", or "unknown".
	ModuleState string `json:"moduleState,omitempty"`
	// Device is the device stanza name (the part after the slash).
	Device string `json:"device,omitempty"`
	// DeviceFile is the file of the device stanza.
	DeviceFile   string `json:"deviceFile,omitempty"`
	DeviceStanza bool   `json:"deviceStanza"`
	// Supported: forms and wizards may edit it (SimpleUSB and hub only).
	Supported bool `json:"supported"`
	// AdvancedFile: the file to edit in Advanced when Supported is false.
	AdvancedFile string `json:"advancedFile,omitempty"`
	Note         string `json:"note,omitempty"`
	// DeviceFields are the device stanza's schema keys (SimpleUSB only).
	DeviceFields []Field `json:"deviceFields,omitempty"`
}

// EchoLink is the node's EchoLink state.
type EchoLink struct {
	// Present: echolink.conf exists.
	Present bool `json:"present"`
	// Module state of chan_echolink.so ("explicit", "auto", "noload", "off", "unknown").
	ModuleState string `json:"moduleState"`
	// Linked: echolink.conf's astnode is this node.
	Linked bool `json:"linked"`
	// Configured: a callsign other than the stock placeholder is set.
	Configured bool `json:"configured"`
	// Enabled: linked, configured and the driver is loaded.
	Enabled bool    `json:"enabled"`
	Fields  []Field `json:"fields,omitempty"`
}

// Finding is a message about the configuration.
type Finding struct {
	// Level is "error" (blocks applying a change that introduces it),
	// "warning" or "info".
	Level   string `json:"level"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Node    string `json:"node,omitempty"`
	// File is the Advanced editor file that fixes it.
	File string `json:"file,omitempty"`
}

// Refusal explains why a node (or the whole model) cannot be edited by
// forms. The UI shows Message and a link to File in the raw editor.
type Refusal struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	File    string `json:"file,omitempty"`
	Line    int    `json:"line,omitempty"`
}

// Node is one local node.
type Node struct {
	Node string `json:"node"`
	// Private: node numbers 1 to 1999 are not in the public AllStar directory.
	Private bool `json:"private"`
	// Placeholder: the stock sample node of a fresh ASL3 install (1999 with
	// no callsign): the setup wizard is offered.
	Placeholder bool    `json:"placeholder"`
	Stanza      *Stanza `json:"stanza,omitempty"`
	// Callsign is read from idrecording (`|i<call>`); "" when unset or when
	// idrecording is not a Morse ID.
	Callsign     string       `json:"callsign"`
	Radio        Radio        `json:"radio"`
	Fields       []Field      `json:"fields"`
	Other        []OtherKey   `json:"other"`
	Registration Registration `json:"registration"`
	EchoLink     EchoLink     `json:"echolink"`
	Warnings     []Finding    `json:"warnings"`
	Refusals     []Refusal    `json:"refusals"`
	InNodes      bool         `json:"inNodes"`
}

// Overview is the whole read model.
type Overview struct {
	Nodes []Node `json:"nodes"`
	// Base holds the sha256 of every file the model read, for plans.
	Base map[string]string `json:"base"`
	// Placeholder is the stock sample node's number ("" when none).
	Placeholder string `json:"placeholder"`
	// Warnings are findings not tied to one node.
	Warnings []Finding `json:"warnings"`
	// Refusal is set when nothing can be edited (rpt.conf missing or
	// unusable).
	Refusal *Refusal `json:"refusal,omitempty"`
	// AMI is the manager interface card.
	AMI AMIInfo `json:"ami"`
}

var reNum = regexp.MustCompile(`^[1-9][0-9]{0,9}$`)

// PrivateMax is the highest private node number.
const PrivateMax = 1999

// IsPrivate reports whether a node number is private (1 to 1999).
func IsPrivate(node string) bool {
	n, err := strconv.Atoi(node)
	return err == nil && n >= 1 && n <= PrivateMax
}

// LocalNodes returns the numbers of the node stanzas of rpt.conf in order.
func (w *World) LocalNodes() []string {
	t := w.Trees[FileRpt]
	if t == nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, s := range t.Sections {
		if s.Template || !reNum.MatchString(s.Name) || seen[s.Name] {
			continue
		}
		seen[s.Name] = true
		out = append(out, s.Name)
	}
	return out
}

func lastEntry(es []astconf.ResolvedEntry, key string) *astconf.ResolvedEntry {
	for i := len(es) - 1; i >= 0; i-- {
		if strings.EqualFold(es[i].Key, key) {
			return &es[i]
		}
	}
	return nil
}

func countKey(es []astconf.ResolvedEntry, key string) int {
	n := 0
	for _, e := range es {
		if strings.EqualFold(e.Key, key) {
			n++
		}
	}
	return n
}

// nodeStanza finds the one stanza of a templated section ([node] or a device
// stanza) or says why it cannot be edited.
type stanzaInfo struct {
	tree *astconf.Tree
	name string
	sec  *astconf.LogicalSection
	// own are the section's own entries; eff the effective ones.
	own []astconf.ResolvedEntry
	eff []astconf.ResolvedEntry
	// inherited: key (lower case) -> entry given by the parents.
	inherited map[string]astconf.ResolvedEntry
	refusal   *Refusal
}

func resolveStanza(tree *astconf.Tree, name, file string) *stanzaInfo {
	si := &stanzaInfo{tree: tree, name: name, inherited: map[string]astconf.ResolvedEntry{}}
	if tree == nil {
		si.refusal = &Refusal{Code: "no_file", Message: file + " does not exist.", File: file}
		return si
	}
	var defs []*astconf.LogicalSection
	for _, s := range tree.Lookup(name) {
		defs = append(defs, s)
	}
	switch {
	case len(defs) == 0:
		si.refusal = &Refusal{Code: "no_stanza", Message: fmt.Sprintf("%s has no [%s] section.", file, name), File: file}
		return si
	case len(defs) > 1:
		var where []string
		for _, d := range defs {
			where = append(where, fmt.Sprintf("%s:%d", d.File, d.Line))
		}
		si.refusal = &Refusal{Code: "multiple_stanzas", Message: fmt.Sprintf("[%s] is defined more than once (%s). Forms do not guess which one counts.", name, strings.Join(where, ", ")), File: defs[0].File, Line: defs[0].Line}
		return si
	}
	si.sec = defs[0]
	if si.sec.Template {
		si.refusal = &Refusal{Code: "is_template", Message: fmt.Sprintf("[%s] is a template, not a node.", name), File: si.sec.File, Line: si.sec.Line}
		return si
	}
	for _, e := range si.sec.Entries {
		if e.File != si.sec.File {
			si.refusal = &Refusal{Code: "split_stanza", Message: fmt.Sprintf("[%s] continues in an included file (%s:%d), so its settings are spread over several files.", name, e.File, e.Line), File: e.File, Line: e.Line}
			return si
		}
	}
	eff, err := astconf.Effective(tree, name)
	if err != nil {
		si.refusal = &Refusal{Code: "broken_inheritance", Message: fmt.Sprintf("[%s] cannot be resolved: %v.", name, err), File: si.sec.File, Line: si.sec.Line}
		return si
	}
	si.eff = eff
	si.own = si.sec.Entries
	for _, p := range si.sec.Parents {
		pe, err := astconf.Effective(tree, p)
		if err != nil {
			continue
		}
		for _, e := range pe {
			si.inherited[strings.ToLower(e.Key)] = e
		}
	}
	return si
}

// lastParent is the section "Change for all nodes" writes new keys into: the
// stanza's last parent (the one that wins).
func (si *stanzaInfo) lastParent() string {
	if si.sec == nil || len(si.sec.Parents) == 0 {
		return ""
	}
	return si.sec.Parents[len(si.sec.Parents)-1]
}

// templateTarget returns the template section that "Change for all nodes"
// edits for the key, or why it cannot.
func (si *stanzaInfo) templateTarget(key string) (*astconf.LogicalSection, string) {
	if si.sec == nil || len(si.sec.Parents) == 0 {
		return nil, "this node does not inherit from a template"
	}
	name := si.lastParent()
	if inh, ok := si.inherited[strings.ToLower(key)]; ok {
		name = inh.Section
	}
	defs := si.tree.Lookup(name)
	switch {
	case len(defs) == 0:
		return nil, "the template [" + name + "] is not defined"
	case len(defs) > 1:
		return nil, "the template [" + name + "] is defined more than once"
	}
	t := defs[0]
	for _, e := range t.Entries {
		if e.File != t.File {
			return nil, "the template [" + name + "] is spread over several files"
		}
	}
	if countKey(t.Entries, key) > 1 {
		return nil, "the template sets " + key + " more than once"
	}
	return t, ""
}

func srcOf(e astconf.ResolvedEntry, node string) Source {
	k := SrcTemplate
	if !e.Inherited {
		k = SrcNode
	}
	return Source{Kind: k, Section: e.Section, File: e.File, Line: e.Line}
}

// fieldOf builds the field of a templated key (node stanza or device stanza).
func (si *stanzaInfo) fieldOf(k schema.Key) Field {
	f := Field{ID: k.ID, Key: k.Name, Source: Source{Kind: SrcUnset}, Masked: k.Secret}
	if k.Default != "" {
		f.Source.Kind = SrcDefault
		f.Value = k.Default
	}
	if si.refusal != nil {
		f.Locked = si.refusal.Message
		return f
	}
	if e := lastEntry(si.eff, k.Name); e != nil {
		f.Value, f.Set, f.Source = e.Value, true, srcOf(*e, si.name)
		if f.Source.Kind == SrcNode {
			if inh, ok := si.inherited[strings.ToLower(k.Name)]; ok {
				f.Overrides = &Inherited{Value: inh.Value, Section: inh.Section}
				if k.Secret {
					f.Overrides.Value = Masked
				}
			}
		}
	}
	if k.Secret && f.Set {
		f.Value = Masked
	}
	f.CanSet = true
	if n := countKey(si.own, k.Name); n > 1 {
		f.CanSet, f.Locked = false, fmt.Sprintf("%s is set %d times in [%s]; edit it in Advanced", k.Name, n, si.name)
	}
	f.CanReset = f.Set && f.Source.Kind == SrcNode && f.Locked == ""
	if t, _ := si.templateTarget(k.Name); t != nil && f.Locked == "" {
		f.CanTemplate = true
	}
	return f
}

// directField builds the field of a key in a plain section (no templates).
func directField(tree *astconf.Tree, k schema.Key) Field {
	f := Field{ID: k.ID, Key: k.Name, Source: Source{Kind: SrcUnset}, Masked: k.Secret}
	if k.Default != "" {
		f.Source.Kind, f.Value = SrcDefault, k.Default
	}
	if tree == nil {
		f.Locked = k.File + " does not exist"
		return f
	}
	defs := tree.Lookup(k.Scope)
	if len(defs) > 1 {
		f.Locked = fmt.Sprintf("[%s] is defined more than once in %s", k.Scope, k.File)
		return f
	}
	n := 0
	if len(defs) == 1 {
		for _, e := range defs[0].Entries {
			if strings.EqualFold(e.Key, k.Name) {
				n++
				f.Value, f.Set = e.Value, true
				f.Source = Source{Kind: SrcSection, Section: k.Scope, File: e.File, Line: e.Line}
			}
		}
		for _, e := range defs[0].Entries {
			if e.File != defs[0].File {
				f.Locked = fmt.Sprintf("[%s] continues in an included file", k.Scope)
			}
		}
	}
	if n > 1 {
		f.Locked = fmt.Sprintf("%s is set %d times in [%s]; edit it in Advanced", k.Name, n, k.Scope)
	}
	if k.Secret && f.Set {
		f.Value = Masked
	}
	f.CanSet = f.Locked == ""
	return f
}

var reMorseID = regexp.MustCompile(`^\|i(.*)$`)

// CallsignOf reads the callsign from an idrecording value; "" when it is a
// recording name or the stock placeholder.
func CallsignOf(idrecording string) string {
	m := reMorseID.FindStringSubmatch(strings.TrimSpace(idrecording))
	if m == nil {
		return ""
	}
	c := strings.TrimSpace(m[1])
	if strings.EqualFold(c, "NOTSET") {
		return ""
	}
	return c
}

// moduleState says how modules.conf treats a module.
func (w *World) moduleState(mod string) string {
	t := w.Trees[FileModules]
	if t == nil {
		return "unknown"
	}
	load, noload, auto := false, false, false
	for _, s := range t.Sections {
		if s.Name != "modules" {
			continue
		}
		for _, e := range s.Entries {
			v := strings.TrimSpace(e.Value)
			switch strings.ToLower(e.Key) {
			case "load", "require", "preload":
				if strings.EqualFold(v, mod) {
					load = true
				}
			case "noload":
				if strings.EqualFold(v, mod) {
					noload = true
				}
			case "autoload":
				switch strings.ToLower(v) {
				case "yes", "true", "y", "t", "on", "1":
					auto = true
				default:
					auto = false
				}
			}
		}
	}
	switch {
	case noload:
		return "noload"
	case load:
		return "explicit"
	case auto:
		return "auto"
	}
	return "off"
}

// ModuleLoaded reports whether a module state means the driver is loaded.
func ModuleLoaded(state string) bool { return state == "explicit" || state == "auto" }

type driver struct {
	typ, module, file string
}

// drivers maps the channel type of rxchannel (lower case) to its driver.
var drivers = map[string]driver{
	"simpleusb": {RadioSimpleUSB, "chan_simpleusb.so", FileSimple},
	"radio":     {RadioUSBRadio, "chan_usbradio.so", FileUSBRadio},
	"usbradio":  {RadioUSBRadio, "chan_usbradio.so", FileUSBRadio},
	"voter":     {RadioVoter, "chan_voter.so", FileVoter},
	"usrp":      {RadioUSRP, "chan_usrp.so", ""},
}

// SplitChannel splits an rxchannel value into its type and argument.
func SplitChannel(ch string) (typ, arg string) {
	ch = strings.TrimSpace(ch)
	if i := strings.IndexByte(ch, '/'); i >= 0 {
		return ch[:i], ch[i+1:]
	}
	return ch, ""
}

func (w *World) radio(si *stanzaInfo) Radio {
	r := Radio{Type: RadioNone}
	if si.refusal != nil {
		return r
	}
	e := lastEntry(si.eff, "rxchannel")
	if e == nil {
		r.Note = "No radio channel is set."
		return r
	}
	r.Channel = e.Value
	typ, arg := SplitChannel(e.Value)
	switch d, ok := drivers[strings.ToLower(typ)]; {
	case strings.EqualFold(typ, "local") && strings.EqualFold(arg, "pseudo"):
		r.Type, r.Supported = RadioHub, true
	case ok:
		r.Type, r.Module, r.Device, r.DeviceFile = d.typ, d.module, arg, d.file
		r.ModuleState = w.moduleState(d.module)
		if d.file != "" {
			if t := w.Trees[d.file]; t != nil {
				for _, s := range t.Lookup(arg) {
					if !s.Template {
						r.DeviceStanza = true
					}
				}
			}
		}
		switch d.typ {
		case RadioSimpleUSB:
			r.Supported = true
		case RadioUSBRadio:
			r.AdvancedFile = FileUSBRadio
			r.Note = "USBRadio is edited in Advanced (usbradio.conf); forms and wizards do not support it."
		case RadioVoter:
			r.AdvancedFile = FileVoter
			r.Note = "Voter is edited in Advanced (voter.conf)."
		case RadioUSRP:
			r.Note = "A digital modes bridge node; see the Digital modes page."
		}
	default:
		r.Type = RadioOther
		r.Note = "This radio channel is edited in Advanced (rpt.conf)."
		r.AdvancedFile = FileRpt
	}
	if r.Type == RadioSimpleUSB && r.DeviceStanza {
		dsi := resolveStanza(w.Trees[FileSimple], r.Device, FileSimple)
		if dsi.refusal == nil {
			for _, k := range schema.ForForm(schema.FormRadio) {
				if k.File == FileSimple {
					r.DeviceFields = append(r.DeviceFields, dsi.fieldOf(k))
				}
			}
		}
	}
	return r
}

func (w *World) echolink(node string) EchoLink {
	el := EchoLink{ModuleState: w.moduleState("chan_echolink.so")}
	t := w.Trees[FileEcho]
	if t == nil {
		return el
	}
	el.Present = true
	for _, k := range schema.ForForm(schema.FormEchoLink) {
		f := directField(t, k)
		if k.Name == "astnode" && f.Value == node {
			el.Linked = true
		}
		if k.Name == "call" && f.Set && !strings.EqualFold(f.Value, "INVALID") && f.Value != "" {
			el.Configured = true
		}
		el.Fields = append(el.Fields, f)
	}
	el.Enabled = el.Linked && el.Configured && ModuleLoaded(el.ModuleState)
	return el
}

func isKnownKey(file, scope, key string) bool {
	_, ok := schema.Find(file, scope, key)
	return ok
}

// Analyze builds the read model.
func (w *World) Analyze() *Overview {
	ov := &Overview{Nodes: []Node{}, Base: map[string]string{}, Warnings: []Finding{}}
	for _, f := range modelFiles {
		if t := w.Trees[f]; t != nil {
			for name := range t.Files {
				ov.Base[name] = w.SHA(name)
			}
		}
	}
	if err := w.Errors[FileRpt]; err != nil {
		ov.Refusal = &Refusal{Code: "unreadable", Message: "rpt.conf cannot be read as a whole: " + err.Error() + ".", File: FileRpt}
		return ov
	}
	t := w.Trees[FileRpt]
	if t == nil {
		ov.Refusal = &Refusal{Code: "no_file", Message: "rpt.conf does not exist.", File: FileRpt}
		return ov
	}
	for name, err := range w.Errors {
		ov.Warnings = append(ov.Warnings, Finding{Level: "warning", Code: "unreadable_file", File: name,
			Message: fmt.Sprintf("%s cannot be read as a whole (%v); settings from it are not shown.", name, err)})
	}
	for _, x := range t.Execs {
		ov.Warnings = append(ov.Warnings, Finding{Level: "info", Code: "exec_directive", File: x.File,
			Message: fmt.Sprintf("%s:%d has an #exec line; ASLCP never runs it and cannot see what it adds.", x.File, x.Line)})
	}
	nodes := w.LocalNodes()
	if len(nodes) == 0 {
		ov.Warnings = append(ov.Warnings, Finding{Level: "warning", Code: "no_nodes", File: FileRpt,
			Message: "rpt.conf has no node stanza."})
	}
	for _, n := range nodes {
		node := w.analyzeNode(n)
		if node.Placeholder && ov.Placeholder == "" {
			ov.Placeholder = n
		}
		ov.Nodes = append(ov.Nodes, node)
	}
	ov.Warnings = append(ov.Warnings, w.globalFindings(nodes)...)
	ov.AMI = w.ami()
	if t := w.Trees[FileManager]; t != nil {
		for _, k := range schema.ForForm(schema.FormAMI) {
			if si := resolveStanza(t, k.Scope, FileManager); si.refusal == nil {
				ov.AMI.Fields = append(ov.AMI.Fields, si.fieldOf(k))
			}
		}
	}
	ov.Warnings = append(ov.Warnings, w.amiFindings()...)
	return ov
}

func (w *World) analyzeNode(n string) Node {
	si := resolveStanza(w.Trees[FileRpt], n, FileRpt)
	node := Node{Node: n, Private: IsPrivate(n), Fields: []Field{}, Other: []OtherKey{}, Warnings: []Finding{}, Refusals: []Refusal{}}
	node.Registration = w.registration(n)
	node.EchoLink = w.echolink(n)
	if ns := w.Trees[FileRpt].Lookup("nodes"); len(ns) > 0 {
		for _, s := range ns {
			for _, e := range s.Entries {
				if strings.TrimSpace(e.Key) == n {
					node.InNodes = true
				}
			}
		}
	}
	if si.refusal != nil {
		node.Refusals = append(node.Refusals, *si.refusal)
		node.Radio = w.radio(si)
		node.Warnings = append(node.Warnings, w.nodeFindings(n, si)...)
		return node
	}
	node.Stanza = &Stanza{File: si.sec.File, Line: si.sec.Line, Parents: append([]string{}, si.sec.Parents...)}
	for i := len(si.sec.Parents) - 1; i >= 0; i-- {
		if defs := si.tree.Lookup(si.sec.Parents[i]); len(defs) > 0 && defs[0].Template {
			node.Stanza.Template = si.sec.Parents[i]
			break
		}
	}
	for _, k := range schema.All() {
		if k.File == FileRpt && k.Scope == schema.ScopeNode {
			node.Fields = append(node.Fields, si.fieldOf(k))
		}
	}
	for _, e := range si.eff {
		if isKnownKey(FileRpt, schema.ScopeNode, e.Key) {
			continue
		}
		o := OtherKey{Key: e.Key, Value: e.Value, Source: srcOf(e, n)}
		if secretish(e.Key) {
			o.Value, o.Masked = Masked, true
		}
		node.Other = append(node.Other, o)
	}
	node.Radio = w.radio(si)
	if id := lastEntry(si.eff, "idrecording"); id != nil {
		node.Callsign = CallsignOf(id.Value)
	}
	node.Placeholder = n == "1999" && node.Callsign == ""
	if node.Placeholder {
		if id := lastEntry(si.eff, "idrecording"); id != nil && !strings.Contains(strings.ToUpper(id.Value), "NOTSET") {
			node.Placeholder = false
		}
	}
	node.Warnings = append(node.Warnings, w.nodeFindings(n, si)...)
	return node
}

func secretish(key string) bool {
	k := strings.ToLower(key)
	return strings.Contains(k, "secret") || strings.Contains(k, "password") || k == "pass" || k == "passwd" || k == "pwd"
}

// Local nodes sorted numerically, for stable output elsewhere.
func sortedNodes(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for n := range m {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool {
		a, _ := strconv.Atoi(out[i])
		b, _ := strconv.Atoi(out[j])
		return a < b
	})
	return out
}
