package nodecfg

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/artw1/aslcp/internal/astconf"
	"github.com/artw1/aslcp/internal/configsvc"
	"github.com/artw1/aslcp/internal/schema"
)

// Modes of a form change.
const (
	// ModeOverride writes the value on the node's own stanza (the default).
	ModeOverride = "override"
	// ModeReset removes the node's own line so the template value applies.
	ModeReset = "reset"
	// ModeTemplate edits the template the node inherits from ("change for
	// all nodes").
	ModeTemplate = "template"
)

// Change is one setting to write.
type Change struct {
	ID    string `json:"id"`
	Value string `json:"value"`
}

// RegChange sets how the node registers. Method is "http", "iax" or "none".
// An empty Password keeps the one already in the files.
type RegChange struct {
	Method   string `json:"method"`
	Host     string `json:"host,omitempty"`
	Password string `json:"password,omitempty"`
}

// RadioChange sets the radio interface: "simpleusb" or "hub".
type RadioChange struct {
	Type string `json:"type"`
}

// Request is a form change for one node.
type Request struct {
	Node         string       `json:"node"`
	Mode         string       `json:"mode,omitempty"`
	Preset       string       `json:"preset,omitempty"`
	Changes      []Change     `json:"changes,omitempty"`
	Callsign     *string      `json:"callsign,omitempty"`
	Registration *RegChange   `json:"registration,omitempty"`
	Radio        *RadioChange `json:"radio,omitempty"`
	// Link makes echolink.conf point at this node (and loads the driver).
	Link *bool `json:"link,omitempty"`
	// Modules to make sure modules.conf loads (chan_simpleusb.so,
	// chan_echolink.so).
	Modules []string `json:"modules,omitempty"`

	// to is the new number when the plan renumbers the node (setup.go).
	to string
}

// Applied is one edit of the plan, as shown to the operator. Secret values
// are masked.
type Applied struct {
	ID      string `json:"id,omitempty"`
	Key     string `json:"key"`
	File    string `json:"file"`
	Section string `json:"section"`
	// Action: set, add, remove, unchanged.
	Action string `json:"action"`
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
	Note   string `json:"note,omitempty"`
}

// PlanFile is one file of a plan.
type PlanFile struct {
	Path    string `json:"path"`
	Base    string `json:"base"`
	Diff    string `json:"diff"`
	Changed bool   `json:"changed"`
}

// Plan is the result of planning a Request.
type Plan struct {
	Node  string     `json:"node"`
	Mode  string     `json:"mode"`
	Files []PlanFile `json:"files"`
	// Base maps each changed file to the sha256 the plan is based on.
	Base    map[string]string `json:"base"`
	Changes []Applied         `json:"changes"`
	// Errors are checks the change would newly fail: the plan cannot be
	// applied. Warnings are everything else worth reading.
	Errors   []Finding `json:"errors"`
	Warnings []Finding `json:"warnings"`
	// Reload and Restart: what applying needs. RestartReasons says why.
	Reload         []configsvc.ReloadStep `json:"reload"`
	Restart        bool                   `json:"restart"`
	RestartReasons []string               `json:"restartReasons"`
	Noop           bool                   `json:"noop"`
	// Notes explain what the plan deliberately leaves as it is (shown on
	// the review page).
	Notes []string `json:"notes"`

	contents map[string]string
	bases    map[string]string
}

// Blocked reports whether the plan has errors and cannot be applied.
func (p *Plan) Blocked() bool { return len(p.Errors) > 0 }

// ConfigFiles returns the changed files as configsvc input.
func (p *Plan) ConfigFiles() []configsvc.File {
	var out []configsvc.File
	for _, f := range p.Files {
		if f.Changed {
			out = append(out, configsvc.File{Path: f.Path, Content: p.contents[f.Path], Base: p.bases[f.Path]})
		}
	}
	return out
}

// InvalidError is a request that cannot be honoured as written.
type InvalidError struct{ Msg string }

func (e *InvalidError) Error() string { return e.Msg }

// RefuseError means the configuration is too unusual for a form to edit
// safely. The UI shows it with a link to File in the raw editor.
type RefuseError struct{ Refusal }

func (e *RefuseError) Error() string { return e.Message }

func invalid(f string, a ...any) error { return &InvalidError{fmt.Sprintf(f, a...)} }

func refuse(r Refusal) error { return &RefuseError{r} }

// ---------------------------------------------------------------- editor

type editor struct {
	w       *World
	files   map[string]*astconf.File
	touched map[string]bool
	out     []Applied
}

func (ed *editor) file(name string) (*astconf.File, error) {
	if f := ed.files[name]; f != nil {
		return f, nil
	}
	txt, ok := ed.w.Content(name)
	if !ok {
		return nil, refuse(Refusal{Code: "no_file", Message: name + " does not exist.", File: name})
	}
	f, err := astconf.Parse(name, []byte(txt))
	if err != nil {
		return nil, refuse(Refusal{Code: "unreadable", Message: name + " cannot be parsed: " + err.Error(), File: name})
	}
	ed.files[name] = f
	return f, nil
}

// section finds the editable copy of a logical section.
func (ed *editor) section(ls *astconf.LogicalSection) (*astconf.Section, error) {
	f, err := ed.file(ls.File)
	if err != nil {
		return nil, err
	}
	for _, s := range f.Section(ls.Name) {
		if s.Header != nil && s.Header.Num == ls.Line {
			return s, nil
		}
	}
	return nil, refuse(Refusal{Code: "section_moved", Message: fmt.Sprintf("[%s] was not found at %s:%d.", ls.Name, ls.File, ls.Line), File: ls.File, Line: ls.Line})
}

func maskIf(secret bool, v string) string {
	if secret && v != "" {
		return Masked
	}
	return v
}

// write sets key=value in the section (value "" removes the line).
func (ed *editor) write(id string, ls *astconf.LogicalSection, key, value string, secret bool, note string) error {
	sec, err := ed.section(ls)
	if err != nil {
		return err
	}
	a := Applied{ID: id, Key: key, File: ls.File, Section: ls.Name, Note: note}
	es := sec.Get(key)
	if len(es) > 1 {
		return refuse(Refusal{Code: "repeated_key", Message: fmt.Sprintf("%s is set %d times in [%s]. Edit it in Advanced.", key, len(es), ls.Name), File: ls.File, Line: es[0].Num})
	}
	switch {
	case value == "" && len(es) == 1:
		a.Action, a.From = "remove", maskIf(secret, es[0].Value)
		sec.Delete(es[0])
		ed.touched[ls.File] = true
	case value == "":
		a.Action = "unchanged"
	case len(es) == 1 && es[0].Value == value:
		a.Action, a.From, a.To = "unchanged", maskIf(secret, value), maskIf(secret, value)
	case len(es) == 1:
		a.Action, a.From, a.To = "set", maskIf(secret, es[0].Value), maskIf(secret, value)
		es[0].SetValue(value)
		ed.touched[ls.File] = true
	default:
		if sec.Add(key, "", value) == nil {
			return invalid("%s cannot be written as a line", key)
		}
		a.Action, a.To = "add", maskIf(secret, value)
		ed.touched[ls.File] = true
	}
	ed.out = append(ed.out, a)
	return nil
}

// reset removes the node's own line of a templated key.
func (ed *editor) reset(id string, si *stanzaInfo, k schema.Key) error {
	if n := countKey(si.own, k.Name); n == 0 {
		ed.out = append(ed.out, Applied{ID: id, Key: k.Name, File: si.sec.File, Section: si.name, Action: "unchanged", Note: "not set on the node"})
		return nil
	}
	return ed.write(id, logical(si), k.Name, "", k.Secret, "reset to the template value")
}

func logical(si *stanzaInfo) *astconf.LogicalSection { return si.sec }

func curValue(si *stanzaInfo, k schema.Key) (string, bool) {
	if e := lastEntry(si.eff, k.Name); e != nil {
		return e.Value, true
	}
	return k.Default, false
}

// applyKey applies one change of a templated key (node or device stanza).
func (ed *editor) applyKey(mode string, si *stanzaInfo, k schema.Key, value string, warn func(Finding)) error {
	if si.refusal != nil {
		return refuse(*si.refusal)
	}
	switch mode {
	case ModeReset:
		return ed.reset(k.ID, si, k)
	case ModeOverride:
		cur, _ := curValue(si, k)
		own := countKey(si.own, k.Name)
		if own == 0 && cur == value {
			ed.out = append(ed.out, Applied{ID: k.ID, Key: k.Name, File: si.sec.File, Section: si.name, Action: "unchanged",
				To: maskIf(k.Secret, value), Note: "already the value in effect"})
			return nil
		}
		if value == "" && own == 0 {
			return invalid("%s is set by a template; use \"Change for all nodes\" to clear it", k.Name)
		}
		return ed.write(k.ID, logical(si), k.Name, value, k.Secret, "")
	case ModeTemplate:
		t, why := si.templateTarget(k.Name)
		if t == nil {
			return refuse(Refusal{Code: "no_template", Message: fmt.Sprintf("%s cannot be changed for all nodes: %s.", k.Name, why), File: si.sec.File, Line: si.sec.Line})
		}
		if value == "" && countKey(t.Entries, k.Name) == 0 {
			ed.out = append(ed.out, Applied{ID: k.ID, Key: k.Name, File: t.File, Section: t.Name, Action: "unchanged"})
			return nil
		}
		if countKey(si.own, k.Name) > 0 {
			warn(Finding{Level: LevelWarning, Code: "node_overrides", Node: si.name, File: si.sec.File,
				Message: fmt.Sprintf("Node %s sets %s itself, so it keeps its own value; reset it to use the template's.", si.name, k.Name)})
		}
		return ed.write(k.ID, t, k.Name, value, k.Secret, "template "+t.Name)
	}
	return invalid("unknown mode %q", mode)
}

// applyDirect applies one change of a key in a plain section.
func (ed *editor) applyDirect(mode string, k schema.Key, value string) error {
	if mode != ModeOverride {
		return invalid("%s is a single shared setting; reset and \"change for all nodes\" do not apply to it", k.Name)
	}
	t := ed.w.Trees[k.File]
	if t == nil {
		return refuse(Refusal{Code: "no_file", Message: k.File + " does not exist.", File: k.File})
	}
	defs := t.Lookup(k.Scope)
	switch {
	case len(defs) == 0:
		return refuse(Refusal{Code: "no_stanza", Message: fmt.Sprintf("%s has no [%s] section.", k.File, k.Scope), File: k.File})
	case len(defs) > 1:
		return refuse(Refusal{Code: "multiple_stanzas", Message: fmt.Sprintf("[%s] is defined more than once in %s.", k.Scope, k.File), File: k.File, Line: defs[0].Line})
	}
	for _, e := range defs[0].Entries {
		if e.File != defs[0].File {
			return refuse(Refusal{Code: "split_stanza", Message: fmt.Sprintf("[%s] continues in an included file.", k.Scope), File: e.File, Line: e.Line})
		}
	}
	if value == "" {
		return ed.write(k.ID, defs[0], k.Name, "", k.Secret, "")
	}
	return ed.write(k.ID, defs[0], k.Name, value, k.Secret, "")
}

// ensureModule makes modules.conf load mod.
func (ed *editor) ensureModule(mod string) error {
	f, err := ed.file(FileModules)
	if err != nil {
		return err
	}
	st, err := astconf.ModuleState(f.Bytes(), mod)
	if err != nil {
		return err
	}
	if st == astconf.ModExplicit || st == astconf.ModAuto {
		return nil
	}
	out, _, err := astconf.SetModuleLoad(f.Bytes(), mod)
	if err != nil {
		return err
	}
	nf, err := astconf.Parse(FileModules, out)
	if err != nil {
		return err
	}
	ed.files[FileModules] = nf
	ed.touched[FileModules] = true
	note := "was not loaded"
	if st == astconf.ModNoload {
		note = "was set to noload"
	}
	ed.out = append(ed.out, Applied{Key: "load", File: FileModules, Section: "modules", Action: "set", To: mod, Note: mod + " " + note})
	return nil
}

var allowedModules = map[string]bool{"chan_simpleusb.so": true, "chan_echolink.so": true}

var (
	reRegHost = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
	reRegPass = regexp.MustCompile(`^[^\s@;:\\\x00-\x1f]{1,64}$`)
)

// registerEntries returns the node's register lines of a file's [general]
// section (and the section itself).
func (ed *editor) registerEntries(file, node string) (*astconf.Section, []*astconf.Entry, *astconf.Entry, error) {
	f, err := ed.file(file)
	if err != nil {
		return nil, nil, nil, err
	}
	var sec *astconf.Section
	if ss := f.Section("general"); len(ss) == 1 {
		sec = ss[0]
	} else if len(ss) > 1 {
		return nil, nil, nil, refuse(Refusal{Code: "multiple_stanzas", Message: "[general] is defined more than once in " + file + ".", File: file})
	}
	var mine []*astconf.Entry
	var placeholder *astconf.Entry
	for _, s := range f.Sections() {
		for _, e := range s.Get("register") {
			n, p, _, ok := parseRegister(e.Value)
			switch {
			case !ok:
			case n == node:
				mine = append(mine, e)
			case n == "1234" && p == "abcdef":
				placeholder = e
			}
		}
	}
	if len(mine) > 1 {
		return nil, nil, nil, refuse(Refusal{Code: "repeated_key", Message: fmt.Sprintf("%s registers node %s %d times. Edit it in Advanced.", file, node, len(mine)), File: file, Line: mine[0].Num})
	}
	return sec, mine, placeholder, nil
}

func (ed *editor) register(node string, rc RegChange) error {
	method, host := rc.Method, rc.Host
	switch method {
	case RegHTTP, RegIAX, RegNone:
	default:
		return invalid("registration method must be http, iax or none")
	}
	if host != "" && !reRegHost.MatchString(host) {
		return invalid("registration host %q is not a host name", host)
	}
	if rc.Password != "" && !reRegPass.MatchString(rc.Password) {
		return invalid("the registration password must be 1 to 64 characters without spaces, @, :, ; or \\")
	}
	type target struct{ file, method string }
	ts := []target{{FileHTTPReg, RegHTTP}, {FileIAX, RegIAX}}
	// existing lines, to carry password and host over
	var oldPass, oldHost string
	for _, t := range ts {
		if !ed.w.Has(t.file) {
			continue
		}
		_, mine, _, err := ed.registerEntries(t.file, node)
		if err != nil {
			return err
		}
		if len(mine) == 1 {
			_, p, h, _ := parseRegister(mine[0].Value)
			if oldPass == "" || t.method == method {
				oldPass, oldHost = p, h
			}
		}
	}
	for _, t := range ts {
		if !ed.w.Has(t.file) {
			if t.method == method {
				return refuse(Refusal{Code: "no_file", Message: t.file + " does not exist. Create it in Advanced first.", File: t.file})
			}
			continue
		}
		sec, mine, ph, err := ed.registerEntries(t.file, node)
		if err != nil {
			return err
		}
		if t.method != method {
			if len(mine) == 1 {
				ed.out = append(ed.out, Applied{Key: "register", File: t.file, Section: "general", Action: "remove",
					From: node + ":" + Masked + "@" + hostOf(mine[0].Value), Note: "registration moved to " + method})
				for _, s := range ed.files[t.file].Sections() {
					if s.Delete(mine[0]) {
						break
					}
				}
				ed.touched[t.file] = true
			}
			continue
		}
		pass := rc.Password
		if pass == "" {
			pass = oldPass
		}
		if pass == "" {
			return invalid("a registration password is required")
		}
		h := host
		if h == "" {
			h = oldHost
		}
		if h == "" {
			h = DefaultRegHost
		}
		val := formatRegister(node, pass, h)
		shown := formatRegister(node, Masked, h)
		switch {
		case len(mine) == 1 && mine[0].Value == val:
			ed.out = append(ed.out, Applied{Key: "register", File: t.file, Section: "general", Action: "unchanged", To: shown})
		case len(mine) == 1:
			ed.out = append(ed.out, Applied{Key: "register", File: t.file, Section: "general", Action: "set", From: node + ":" + Masked + "@" + hostOf(mine[0].Value), To: shown})
			mine[0].SetValue(val)
			ed.touched[t.file] = true
		case ph != nil:
			ed.out = append(ed.out, Applied{Key: "register", File: t.file, Section: "general", Action: "set", From: formatRegister("1234", Masked, hostOf(ph.Value)), To: shown, Note: "replaces the sample line"})
			ph.SetValue(val)
			ed.touched[t.file] = true
		default:
			if sec == nil {
				sec = ed.files[t.file].AddSection("general", false, nil)
			}
			if sec.Add("register", "=>", val) == nil {
				return invalid("the registration line cannot be written")
			}
			ed.out = append(ed.out, Applied{Key: "register", File: t.file, Section: "general", Action: "add", To: shown})
			ed.touched[t.file] = true
		}
	}
	if method == RegNone && oldPass == "" && len(ed.out) == 0 {
		ed.out = append(ed.out, Applied{Key: "register", File: FileHTTPReg, Section: "general", Action: "unchanged", Note: "not registered"})
	}
	return nil
}

func hostOf(v string) string {
	_, _, h, _ := parseRegister(v)
	return h
}

// ---------------------------------------------------------------- plan

func findingKey(f Finding) string { return f.Code + "|" + f.Node + "|" + f.File + "|" + f.Message }

func (w *World) allFindings(node string) []Finding {
	si := resolveStanza(w.Trees[FileRpt], node, FileRpt)
	out := w.nodeFindings(node, si)
	for _, g := range w.globalFindings(w.LocalNodes()) {
		out = append(out, g)
	}
	return out
}

// Plan plans a form change. Nothing is written. A change the schema rejects
// returns *InvalidError; a configuration too unusual to edit returns
// *RefuseError.
func (w *World) Plan(req Request) (*Plan, error) {
	if w.Trees[FileRpt] == nil {
		return nil, refuse(Refusal{Code: "no_file", Message: "rpt.conf does not exist.", File: FileRpt})
	}
	mode := req.Mode
	if mode == "" {
		mode = ModeOverride
	}
	if mode != ModeOverride && mode != ModeReset && mode != ModeTemplate {
		return nil, invalid("mode must be override, reset or template")
	}
	known := false
	for _, n := range w.LocalNodes() {
		known = known || n == req.Node
	}
	if !known {
		return nil, invalid("node %q has no stanza in rpt.conf", req.Node)
	}
	si := resolveStanza(w.Trees[FileRpt], req.Node, FileRpt)
	if si.refusal != nil {
		return nil, refuse(*si.refusal)
	}
	p := w.newPlan(req.Node, mode)
	ed := w.newEditor()
	if err := w.planRequest(p, ed, si, req, mode); err != nil {
		return nil, err
	}
	p.Changes = ed.out
	w.finishPlan(p, ed, req.Node, req.Node)
	return p, nil
}

func (w *World) newPlan(node, mode string) *Plan {
	return &Plan{Node: node, Mode: mode, Files: []PlanFile{}, Base: map[string]string{}, Changes: []Applied{}, Errors: []Finding{}, Warnings: []Finding{},
		Reload: []configsvc.ReloadStep{}, RestartReasons: []string{}, Notes: []string{}, contents: map[string]string{}, bases: map[string]string{}}
}

func (w *World) newEditor() *editor {
	return &editor{w: w, files: map[string]*astconf.File{}, touched: map[string]bool{}}
}

// planRequest applies the settings of a Request to the editor. Nothing is
// written: the edits are collected in ed. When req.to is set the node is
// being renumbered in the same plan: everything that carries the node number
// (registration, device name, EchoLink link) uses req.to, while si still is
// the stanza as it is now (the stanza itself is renamed by ed.renumber, last).
func (w *World) planRequest(p *Plan, ed *editor, si *stanzaInfo, req Request, mode string) error {
	target := req.Node
	if req.to != "" {
		target = req.to
	}
	warn := func(f Finding) { p.Warnings = append(p.Warnings, f) }

	// the changes: preset first, explicit changes win
	changes := map[string]string{}
	var order []string
	add := func(id, v string) {
		if _, ok := changes[id]; !ok {
			order = append(order, id)
		}
		changes[id] = v
	}
	hub := false
	if req.Preset != "" {
		pr, ok := PresetByName(req.Preset)
		if !ok {
			return invalid("unknown preset %q", req.Preset)
		}
		if mode != ModeOverride {
			return invalid("a preset is applied to the node itself")
		}
		keys := make([]string, 0, len(pr.Values))
		for id := range pr.Values {
			keys = append(keys, id)
		}
		sort.Strings(keys)
		for _, id := range keys {
			add(id, pr.Values[id])
		}
		hub = pr.Hub
	}
	seen := map[string]bool{}
	for _, c := range req.Changes {
		if seen[c.ID] {
			return invalid("%s appears twice", c.ID)
		}
		seen[c.ID] = true
		add(c.ID, c.Value)
	}
	if hub && req.Radio == nil {
		req.Radio = &RadioChange{Type: RadioHub}
	}
	if req.Radio != nil && mode != ModeOverride {
		return invalid("the radio type is set on the node itself")
	}
	if req.Callsign != nil && mode != ModeOverride {
		return invalid("the callsign is set on the node itself")
	}

	// validate before touching anything
	for _, id := range order {
		k, ok := schema.Lookup(id)
		if !ok {
			return invalid("%s is not a setting forms can change; use Advanced", id)
		}
		if mode != ModeReset {
			if err := k.Validate(changes[id]); err != nil {
				return &InvalidError{err.Error()}
			}
		}
		if mode == ModeReset && (k.Scope != schema.ScopeNode && k.Scope != schema.ScopeDevice) {
			return invalid("%s has no template to reset to", k.Name)
		}
	}

	// rxchannel as a plain setting is held to the same rule as the radio
	// type: a USBRadio, Voter or other interface is never edited by forms.
	for _, id := range order {
		if id != "rpt.conf:node:rxchannel" || req.Radio != nil {
			continue
		}
		switch cur := w.radio(si); cur.Type {
		case RadioSimpleUSB, RadioHub, RadioNone:
		default:
			return refuse(radioUnsupported(cur))
		}
	}

	var radioDevice string
	// radio first, so the device stanza exists for later steps
	if req.Radio != nil {
		cur := w.radio(si)
		switch cur.Type {
		case RadioSimpleUSB, RadioHub, RadioNone:
		default:
			return refuse(radioUnsupported(cur))
		}
		switch req.Radio.Type {
		case RadioHub:
			k, _ := schema.Find(FileRpt, schema.ScopeNode, "rxchannel")
			if err := ed.applyKey(ModeOverride, si, k, "Local/pseudo", warn); err != nil {
				return err
			}
			if cur.Type == RadioSimpleUSB {
				p.Notes = append(p.Notes, w.hubNotes(req.Node, cur)...)
			}
		case RadioSimpleUSB:
			dev := target
			if cur.Type == RadioSimpleUSB && cur.Device != "" && cur.Device != req.Node {
				dev = cur.Device
			}
			radioDevice = dev
			k, _ := schema.Find(FileRpt, schema.ScopeNode, "rxchannel")
			if err := ed.applyKey(ModeOverride, si, k, "SimpleUSB/"+dev, warn); err != nil {
				return err
			}
			if err := ed.ensureModule("chan_simpleusb.so"); err != nil {
				return err
			}
			// a renumbered node renames its device section instead
			if !(req.to != "" && cur.DeviceStanza && cur.Device == req.Node) {
				if err := ed.ensureDevice(dev); err != nil {
					return err
				}
			}
			if cur.Type != RadioSimpleUSB && req.to == "" {
				for _, n := range w.otherSimpleUSBNodes(req.Node) {
					warn(Finding{Level: LevelWarning, Code: "second_simpleusb", Node: target, File: FileSimple,
						Message: fmt.Sprintf("Node %s also uses a SimpleUSB adapter. Two nodes need two adapters: set the USB device path or serial number of each in its device section, or both nodes may pick the same one.", n)})
					break
				}
			}
		default:
			return invalid("the radio type must be simpleusb or hub; USBRadio and other interfaces are edited in Advanced")
		}
	}
	if req.Callsign != nil {
		cs := strings.ToUpper(strings.TrimSpace(*req.Callsign))
		if !ValidCallsign(cs) {
			return invalid("%q does not look like a callsign (letters and digits, for example N0CALL)", *req.Callsign)
		}
		if e := lastEntry(si.eff, "idrecording"); e != nil && !strings.HasPrefix(strings.TrimSpace(e.Value), "|i") {
			return refuse(Refusal{Code: "idrecording_custom", Message: "idrecording is a recording name, not a Morse ID, so the callsign is not changed here.", File: e.File, Line: e.Line})
		}
		k, _ := schema.Find(FileRpt, schema.ScopeNode, "idrecording")
		if err := ed.applyKey(ModeOverride, si, k, "|i"+cs, warn); err != nil {
			return err
		}
	}
	for _, id := range order {
		k, _ := schema.Lookup(id)
		v := changes[id]
		if k.ID == "rpt.conf:node:rxchannel" && req.Radio != nil {
			continue
		}
		var err error
		switch {
		case k.File == FileRpt && k.Scope == schema.ScopeNode:
			err = ed.applyKey(mode, si, k, v, warn)
		case k.Scope == schema.ScopeDevice:
			if radioDevice != "" {
				return invalid("set the radio type first, then its device settings")
			}
			r := w.radio(si)
			if r.Type != RadioSimpleUSB || !r.DeviceStanza {
				return refuse(Refusal{Code: "no_device_stanza", Message: "The node has no SimpleUSB device section to edit.", File: FileSimple})
			}
			dsi := resolveStanza(w.Trees[FileSimple], r.Device, FileSimple)
			err = ed.applyKey(mode, dsi, k, v, warn)
		default:
			err = ed.applyDirect(mode, k, v)
		}
		if err != nil {
			return err
		}
	}
	if req.Link != nil && *req.Link {
		k, _ := schema.Find(FileEcho, "el0", "astnode")
		if err := ed.applyDirect(ModeOverride, k, target); err != nil {
			return err
		}
		if err := ed.ensureModule("chan_echolink.so"); err != nil {
			return err
		}
	}
	for _, m := range req.Modules {
		if !allowedModules[m] {
			return invalid("module %q cannot be changed here", m)
		}
		if err := ed.ensureModule(m); err != nil {
			return err
		}
	}
	if req.Registration != nil {
		if err := ed.register(target, *req.Registration); err != nil {
			return err
		}
	}
	return nil
}

func (w *World) finishPlan(p *Plan, ed *editor, nodeBefore, nodeAfter string) {
	// new contents
	var changed []string
	for name := range ed.touched {
		f := ed.files[name]
		now := string(f.Bytes())
		old, _ := w.Content(name)
		if now == old {
			continue
		}
		p.contents[name] = now
		p.bases[name] = w.SHA(name)
		p.Base[name] = p.bases[name]
		changed = append(changed, name)
	}
	sort.Strings(changed)
	for _, name := range changed {
		old, _ := w.Content(name)
		p.Files = append(p.Files, PlanFile{Path: name, Base: p.bases[name], Changed: true,
			Diff: configsvc.MaskDiff(configsvc.UnifiedDiff(name, old, p.contents[name], false))})
	}
	p.Noop = len(changed) == 0
	if p.Noop {
		return
	}

	// after-state checks
	w2 := w.With(p.contents)
	for name, err := range w2.Errors {
		if w.Errors[name] == nil {
			p.Errors = append(p.Errors, Finding{Level: LevelError, Code: "unreadable_after", File: name,
				Message: fmt.Sprintf("%s could not be read after the change: %v.", name, err)})
		}
	}
	before := map[string]bool{}
	for _, f := range w.allFindings(nodeBefore) {
		before[renameKey(findingKey(f), nodeBefore, nodeAfter)] = true
	}
	for _, f := range w2.allFindings(nodeAfter) {
		switch {
		case f.Level == LevelError && !before[findingKey(f)]:
			p.Errors = append(p.Errors, f)
		case f.Level == LevelError:
			f.Level = LevelWarning
			f.Message += " (already the case before this change)"
			p.Warnings = append(p.Warnings, f)
		case !before[findingKey(f)]:
			p.Warnings = append(p.Warnings, f)
		}
	}

	// manager interface checks: only when manager.conf changes
	if _, ok := p.contents[FileManager]; ok {
		beforeAMI := map[string]bool{}
		for _, f := range w.amiFindings() {
			beforeAMI[f.Code] = true
		}
		for _, f := range w2.amiFindings() {
			switch {
			case f.Level == LevelError:
				p.Errors = append(p.Errors, f)
			case !beforeAMI[f.Code]:
				p.Warnings = append(p.Warnings, f)
			}
		}
		if a, b := w.ami(), w2.ami(); a.Port != b.Port {
			p.Warnings = append(p.Warnings, Finding{Level: LevelWarning, Code: CodeAMIPort, File: FileManager,
				Message: "The manager port changes. ASLCP and any other tool must use the new port; ASLCP follows the password but not the port, so edit aslcp.conf if it connects to this port."})
		}
	}

	// reload plan
	steps, restart := configsvc.PlanReload(changed)
	p.Reload = steps
	if p.Reload == nil {
		p.Reload = []configsvc.ReloadStep{}
	}
	p.Restart = restart
	for _, name := range changed {
		if _, ok := configsvc.ReloadCommandFor(name); !ok {
			p.RestartReasons = append(p.RestartReasons, name+" has no reload command")
		}
	}
	for _, a := range p.Changes {
		if a.Action == "unchanged" || a.ID == "" {
			continue
		}
		if k, ok := schema.Lookup(a.ID); ok && k.Reload == schema.ReloadRestart {
			p.Restart = true
			p.RestartReasons = append(p.RestartReasons, k.Name+" needs an Asterisk restart")
		}
	}
	sort.Strings(p.RestartReasons)
	p.RestartReasons = dedupe(p.RestartReasons)
}

func dedupe(in []string) []string {
	out := in[:0]
	for i, s := range in {
		if i == 0 || s != in[i-1] {
			out = append(out, s)
		}
	}
	return out
}

// ensureDevice makes sure simpleusb.conf has a stanza for the device.
func (ed *editor) ensureDevice(dev string) error {
	t := ed.w.Trees[FileSimple]
	if t == nil {
		return refuse(Refusal{Code: "no_file", Message: FileSimple + " does not exist.", File: FileSimple})
	}
	defs := t.Lookup(dev)
	if len(defs) > 1 {
		return refuse(Refusal{Code: "multiple_stanzas", Message: fmt.Sprintf("[%s] is defined more than once in %s.", dev, FileSimple), File: FileSimple, Line: defs[0].Line})
	}
	if len(defs) == 1 {
		if defs[0].Template {
			return refuse(Refusal{Code: "is_template", Message: fmt.Sprintf("[%s] in %s is a template.", dev, FileSimple), File: FileSimple, Line: defs[0].Line})
		}
		return nil
	}
	f, err := ed.file(FileSimple)
	if err != nil {
		return err
	}
	var parents []string
	for _, s := range t.Sections {
		if s.Template {
			parents = []string{s.Name}
		}
	}
	f.AddSection(dev, false, parents)
	ed.touched[FileSimple] = true
	ed.out = append(ed.out, Applied{Key: "[" + dev + "]", File: FileSimple, Section: dev, Action: "add",
		Note: "new device section" + map[bool]string{true: " based on " + strings.Join(parents, ""), false: ""}[len(parents) > 0]})
	return nil
}

// radioUnsupported is the refusal for a node whose current radio interface
// forms do not edit (USBRadio, Voter, USRP, other). Changing it to SimpleUSB or
// a hub would drop a setup that cannot be tested here.
func radioUnsupported(cur Radio) Refusal {
	file := cur.AdvancedFile
	if file == "" {
		file = cur.DeviceFile
	}
	if file == "" {
		file = FileRpt
	}
	return Refusal{Code: "radio_unsupported", File: file,
		Message: "This node uses a radio interface (" + cur.Type + ") that forms do not change. Edit rxchannel and the device file in Advanced (" + file + ")."}
}

// renameKey maps the key of a finding made for the node before a renumbering
// onto the node's new number, so a problem that was there before is not
// reported as new.
func renameKey(k, from, to string) string {
	if from == to {
		return k
	}
	return strings.ReplaceAll(k, from, to)
}
