package nodecfg

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/artw1/aslcp/internal/astconf"
)

// The add-node and change-node-number wizards (docs/CONFIG.md, "Add a node",
// "Change the node number"). Both are plans of the same shape as the setup
// wizard (SetupPlan): the form planner's edits, finishPlan's checks, and a
// list of the steps the API runs after the files (see
// internal/api/nodewizard_run.go).

var reSecretText = regexp.MustCompile(`^[A-Za-z0-9._+=,:@%-]{1,64}$`)

// normalizeAnswers validates the answers the setup and add-node wizards share
// and returns the upper-cased callsign, the registration change (password set
// only for a registered node) and whether the number is public.
func normalizeAnswers(node, password, callsign, radio, preset string, reg *RegChange) (cs string, rc RegChange, public bool, err error) {
	if !reSetupNode.MatchString(node) {
		return "", rc, false, invalid("the node number must be 1 to 7 digits")
	}
	public = !IsPrivate(node)
	if !plainText(password) || !reSecretText.MatchString(password) {
		return "", rc, false, invalid("the password must be 1 to 64 letters, digits or . _ + = , : @ %% -")
	}
	cs = strings.ToUpper(strings.TrimSpace(callsign))
	if !ValidCallsign(cs) {
		return "", rc, false, invalid("%q does not look like a callsign (letters and digits, for example N0CALL)", callsign)
	}
	if radio != RadioSimpleUSB && radio != RadioHub {
		return "", rc, false, invalid("the radio must be simpleusb or hub; USBRadio and other interfaces are edited in Advanced")
	}
	if preset == "" {
		return "", rc, false, invalid("choose a duplex preset")
	}
	if pr, ok := PresetByName(preset); !ok {
		return "", rc, false, invalid("unknown preset %q", preset)
	} else if pr.Hub != (radio == RadioHub) {
		return "", rc, false, invalid("the hub preset goes with the hub radio option, and the other presets with SimpleUSB")
	}
	rc = RegChange{Method: RegNone}
	if reg != nil {
		rc = *reg
	} else if public {
		rc.Method = RegHTTP
	}
	if rc.Method == "" {
		rc.Method = RegNone
	}
	switch {
	case !public && rc.Method != RegNone:
		return "", rc, false, invalid("private nodes (1 to 1999) are not registered; use a number above 1999 to register")
	case rc.Method != RegNone:
		rc.Password = password
	default:
		rc.Password = ""
	}
	return cs, rc, public, nil
}

// existsAnywhere says where a node number is already used ("" when it is
// not): a wizard never creates or renames onto an existing number.
func (w *World) existsAnywhere(node string) string {
	if t := w.Trees[FileRpt]; t != nil {
		if len(t.Lookup(node)) > 0 {
			return "rpt.conf has a section [" + node + "]"
		}
		for _, d := range t.Lookup("nodes") {
			if lastEntry(d.Entries, node) != nil {
				return "rpt.conf [nodes] has a line for node " + node
			}
		}
	}
	for _, f := range []string{FileSimple, FileUSBRadio, FileVoter} {
		if t := w.Trees[f]; t != nil && len(t.Lookup(node)) > 0 {
			return f + " has a section [" + node + "]"
		}
	}
	for _, f := range []string{FileHTTPReg, FileIAX} {
		for _, l := range regLines(w.Trees[f]) {
			if l.Node == node {
				return f + " registers node " + node
			}
		}
	}
	if t := w.Trees[FileEcho]; t != nil {
		for _, d := range t.Lookup("el0") {
			if e := lastEntry(d.Entries, "astnode"); e != nil && e.Value == node {
				return "echolink.conf points at node " + node
			}
		}
	}
	if t := w.Trees[FileExt]; t != nil {
		for _, d := range t.Lookup("globals") {
			if e := lastEntry(d.Entries, "NODE"); e != nil && e.Value == node {
				return "extensions.conf sets NODE to " + node
			}
		}
	}
	return ""
}

// otherSimpleUSBNodes lists the other local nodes that use SimpleUSB.
func (w *World) otherSimpleUSBNodes(node string) []string {
	var out []string
	for _, n := range w.LocalNodes() {
		if n == node {
			continue
		}
		si := resolveStanza(w.Trees[FileRpt], n, FileRpt)
		if si.refusal == nil && w.radio(si).Type == RadioSimpleUSB {
			out = append(out, n)
		}
	}
	return out
}

// hubNotes explain what switching a SimpleUSB node to a hub leaves in place.
func (w *World) hubNotes(node string, cur Radio) []string {
	var out []string
	if cur.DeviceStanza {
		out = append(out, fmt.Sprintf("The SimpleUSB device section [%s] stays in %s with all its settings, so you can switch back to SimpleUSB without losing them. A hub does not use it.", cur.Device, FileSimple))
	}
	if others := w.otherSimpleUSBNodes(node); len(others) > 0 {
		out = append(out, fmt.Sprintf("chan_simpleusb.so stays loaded in modules.conf because node %s still uses SimpleUSB.", others[0]))
	} else {
		out = append(out, "chan_simpleusb.so stays loaded in modules.conf. It does no harm, and switching back needs no change there.")
	}
	return out
}

// AddNodeRequest is the add-node wizard's answers.
type AddNodeRequest struct {
	Node     string `json:"node"`
	Password string `json:"password"`
	Callsign string `json:"callsign"`
	// Radio is "simpleusb" or "hub"; Preset a duplex preset name.
	Radio  string `json:"radio"`
	Preset string `json:"preset"`
	// Registration: method http (default for public nodes), iax or none.
	Registration *RegChange `json:"registration,omitempty"`
	// From is the node whose template the new one inherits (default: the
	// first local node).
	From string `json:"from,omitempty"`
}

// PlanAddNode plans a second local node: a stanza that inherits the
// existing template (or is flat when there is none), its [nodes] line, the
// registration, the radio and a duplex preset. The new stanza is built
// first and the rest is planned against it, so every setting goes through the
// same code as the forms.
func (w *World) PlanAddNode(in AddNodeRequest) (*SetupPlan, error) {
	if w.Trees[FileRpt] == nil {
		return nil, refuse(Refusal{Code: "no_file", Message: "rpt.conf does not exist.", File: FileRpt})
	}
	cs, rc, public, err := normalizeAnswers(in.Node, in.Password, in.Callsign, in.Radio, in.Preset, in.Registration)
	if err != nil {
		return nil, err
	}
	node := in.Node
	if where := w.existsAnywhere(node); where != "" {
		return nil, invalid("node %s already exists: %s", node, where)
	}
	locals := w.LocalNodes()
	if len(locals) == 0 {
		return nil, refuse(Refusal{Code: "no_nodes", Message: "rpt.conf has no node to base the new one on. Add a node section in Advanced first.", File: FileRpt})
	}
	src := in.From
	if src == "" {
		src = locals[0]
	}
	known := false
	for _, n := range locals {
		known = known || n == src
	}
	if !known {
		return nil, invalid("node %q has no stanza in rpt.conf", src)
	}
	ssi := resolveStanza(w.Trees[FileRpt], src, FileRpt)
	if ssi.refusal != nil {
		return nil, refuse(*ssi.refusal)
	}

	// 1. the new stanza and its [nodes] line
	ed1 := w.newEditor()
	f, err := ed1.file(FileRpt)
	if err != nil {
		return nil, err
	}
	var parents []string
	var notes []string
	if len(ssi.sec.Parents) > 0 {
		parents = []string{ssi.sec.Parents[0]}
	}
	sec := f.AddSection(node, false, parents)
	if len(parents) == 0 {
		var copied []string
		for _, k := range astconf.FlatCopyKeys() {
			if e := lastEntry(ssi.eff, k); e != nil {
				sec.Add(k, "", e.Value)
				copied = append(copied, k)
			}
		}
		notes = append(notes, fmt.Sprintf("Node %s has no template, so the new section is a flat copy of its basics (%s). Everything else uses Asterisk's defaults.", src, strings.Join(copied, ", ")))
	}
	ed1.touched[FileRpt] = true
	how := "inherits " + strings.Join(parents, "")
	if len(parents) == 0 {
		how = "flat section"
	}
	ed1.out = append(ed1.out, Applied{Key: "[node]", File: FileRpt, Section: node, Action: "add", To: "[" + node + "]", Note: "new node section (" + how + ")"})
	if line := astconf.AddLocalNodesLine(f, node); line != "" {
		ed1.out = append(ed1.out, Applied{Key: node, File: FileRpt, Section: "nodes", Action: "add", To: strings.TrimSpace(line), Note: "[nodes] entry"})
	}

	// 2. everything else, planned against the world that has the stanza
	w2 := w.With(map[string]string{FileRpt: string(f.Bytes())})
	si := resolveStanza(w2.Trees[FileRpt], node, FileRpt)
	if si.refusal != nil {
		return nil, refuse(*si.refusal)
	}
	p := w2.newPlan(node, ModeOverride)
	ed2 := w2.newEditor()
	req := Request{Node: node, Preset: in.Preset, Callsign: &cs, Radio: &RadioChange{Type: in.Radio}, Registration: &rc}
	if err := w2.planRequest(p, ed2, si, req, ModeOverride); err != nil {
		return nil, err
	}
	// merge into one editor over the real files, so the diffs are against what is on disk
	ed := w.newEditor()
	if _, err := ed2.file(FileRpt); err != nil {
		return nil, err
	}
	for name, ef := range ed2.files {
		ed.files[name] = ef
	}
	for name := range ed2.touched {
		ed.touched[name] = true
	}
	ed.touched[FileRpt] = true
	ed.out = append(ed1.out, ed2.out...)
	p.Changes = ed.out
	p.Notes = append(p.Notes, notes...)
	w.finishPlan(p, ed, node, node)
	// Warnings of the second pass (planRequest) were collected on p before
	// finishPlan; finishPlan appends its own.
	if !p.Noop {
		p.Restart = true
		p.RestartReasons = dedupeSorted(append(p.RestartReasons, "a new node starts when Asterisk restarts"))
	}
	sp := &SetupPlan{Plan: p, OldNode: node, Public: public, Manual: []string{}}
	sp.Manual = append(sp.Manual, "Allmon3 (allmon3.ini) is not changed: add the new node there yourself if you use Allmon3.")
	if in.Radio == RadioSimpleUSB {
		sp.Manual = append(sp.Manual, "Set the USB device path or serial number of the new radio's adapter in its SimpleUSB device section if more than one adapter is plugged in, then tune its levels under Audio Tuning.")
	}
	sp.Steps = []SetupStep{
		{ID: "files", Label: fmt.Sprintf("Write %d configuration file(s) and save a restore point", len(p.Files))},
		{ID: "savenode", Label: "Update savenode.conf", Skipped: "not needed for an added node"},
		{ID: "ami", Label: "Set a new AMI secret", Skipped: "not needed"},
		{ID: "restart", Label: "Restart Asterisk", Skipped: map[bool]string{true: "", false: "not needed"}[p.Restart]},
		{ID: "allmon3", Label: "Update Allmon3", Skipped: "not needed for an added node"},
		{ID: "health", Label: "Check that the new node loaded"},
		{ID: "registration", Label: "Check the registration", Skipped: map[bool]string{true: "", false: "not registered"}[rc.Method != RegNone]},
	}
	return sp, nil
}

// RenumberRequest is the change-node-number wizard's answers.
type RenumberRequest struct {
	OldNode string `json:"oldNode"`
	Node    string `json:"node"`
	// Password is the new node's AllStarLink password. Needed to register a
	// public node and to write savenode.conf.
	Password string `json:"password,omitempty"`
	// Savenode: this is the node savenode.conf holds; NODE and PASSWORD are
	// updated there too (through the root helper).
	Savenode bool `json:"savenode"`
}

// renumberRegistration moves the node's register lines to the new number
// (password and host kept unless a new password is given); a private target
// is not registered, so the lines are removed.
func (ed *editor) renumberRegistration(old, node string, public bool, password string) error {
	for _, t := range []string{FileHTTPReg, FileIAX} {
		if !ed.w.Has(t) {
			continue
		}
		_, mine, _, err := ed.registerEntries(t, old)
		if err != nil {
			return err
		}
		if len(mine) != 1 {
			continue
		}
		_, pass, host, _ := parseRegister(mine[0].Value)
		if !public {
			ed.out = append(ed.out, Applied{Key: "register", File: t, Section: "general", Action: "remove",
				From: old + ":" + Masked + "@" + host, Note: "private nodes are not registered"})
			for _, s := range ed.files[t].Sections() {
				if s.Delete(mine[0]) {
					break
				}
			}
			ed.touched[t] = true
			continue
		}
		if password != "" {
			pass = password
		}
		ed.out = append(ed.out, Applied{Key: "register", File: t, Section: "general", Action: "set",
			From: old + ":" + Masked + "@" + host, To: formatRegister(node, Masked, host), Note: "registration moved to the new number"})
		mine[0].SetValue(formatRegister(node, pass, host))
		ed.touched[t] = true
	}
	return nil
}

// PlanRenumber plans the change of a node's number: every place in the
// DESIGN.md 4.3 table that the planner owns, the helper steps for savenode.conf
// and allmon3.ini, and a list of what it cannot change.
func (w *World) PlanRenumber(in RenumberRequest) (*SetupPlan, error) {
	if w.Trees[FileRpt] == nil {
		return nil, refuse(Refusal{Code: "no_file", Message: "rpt.conf does not exist.", File: FileRpt})
	}
	old, node := in.OldNode, in.Node
	if !reSetupNode.MatchString(node) {
		return nil, invalid("the node number must be 1 to 7 digits")
	}
	if old == node {
		return nil, invalid("the new number is the same as the old one")
	}
	known := false
	for _, n := range w.LocalNodes() {
		known = known || n == old
	}
	if !known {
		return nil, invalid("node %q has no stanza in rpt.conf", old)
	}
	if where := w.existsAnywhere(node); where != "" {
		return nil, invalid("node %s already exists: %s", node, where)
	}
	si := resolveStanza(w.Trees[FileRpt], old, FileRpt)
	if si.refusal != nil {
		return nil, refuse(*si.refusal)
	}
	cur := w.radio(si)
	switch cur.Type {
	case RadioSimpleUSB, RadioHub, RadioNone:
	default:
		return nil, refuse(radioUnsupported(cur))
	}
	public := !IsPrivate(node)
	if in.Password != "" && (!plainText(in.Password) || !reSecretText.MatchString(in.Password)) {
		return nil, invalid("the password must be 1 to 64 letters, digits or . _ + = , : @ %% -")
	}
	regd := w.registration(old).Method != RegNone
	if regd && public && in.Password == "" {
		return nil, invalid("the new node's AllStarLink password is needed to register node %s", node)
	}
	if in.Savenode && in.Password == "" {
		return nil, invalid("the password is needed to update savenode.conf")
	}

	req := Request{Node: old, to: node}
	dev := ""
	var warns []Finding
	if cur.Type == RadioSimpleUSB && cur.Device == old {
		if cur.DeviceStanza {
			dev = old
			req.Radio = &RadioChange{Type: RadioSimpleUSB}
		} else {
			warns = append(warns, Finding{Level: LevelWarning, Code: "no_device_stanza", Node: node, File: FileSimple,
				Message: fmt.Sprintf("The node's radio is SimpleUSB/%s but %s has no [%s] section. rxchannel is renamed; add the device section in Advanced.", old, FileSimple, old)})
		}
	}
	p := w.newPlan(node, ModeOverride)
	ed := w.newEditor()
	if err := w.planRequest(p, ed, si, req, ModeOverride); err != nil {
		return nil, err
	}
	if cur.Type == RadioSimpleUSB && cur.Device == old && !cur.DeviceStanza {
		// planRequest left rxchannel alone: rename it with the node
		if e := lastEntry(si.own, "rxchannel"); e != nil {
			if err := ed.write("rpt.conf:node:rxchannel", logical(si), "rxchannel", "SimpleUSB/"+node, false, ""); err != nil {
				return nil, err
			}
		}
	}
	if err := ed.renumberRegistration(old, node, public, in.Password); err != nil {
		return nil, err
	}
	if err := ed.renumber(old, node, si, dev); err != nil {
		return nil, err
	}
	p.Changes = ed.out
	w.finishPlan(p, ed, old, node)
	p.Warnings = append(p.Warnings, warns...)
	if p.Noop {
		return &SetupPlan{Plan: p, OldNode: old, Public: public, Renumber: true, Manual: []string{}, Steps: []SetupStep{}}, nil
	}
	p.Restart = true
	p.RestartReasons = dedupeSorted(append(p.RestartReasons, "the node number changed"))
	if cur.Type != RadioSimpleUSB && w.Trees[FileSimple] != nil && len(w.Trees[FileSimple].Lookup(old)) > 0 {
		p.Notes = append(p.Notes, fmt.Sprintf("The SimpleUSB device section [%s] is not used by a hub and is left in place.", old))
	}
	if regd && public {
		p.Notes = append(p.Notes, "The node is registered under the new number with the password you gave. Check the registration after the restart.")
	}
	if regd && !public {
		p.Notes = append(p.Notes, "A private number is not registered with AllStarLink, so the registration line is removed.")
	}
	p.Warnings = append(p.Warnings, w.otherReferences(p, old)...)

	sp := &SetupPlan{Plan: p, OldNode: old, Public: public, Renumber: true}
	sp.Manual = []string{
		"voter.conf: voter and client sections that use the old number (edit in Advanced).",
		"allmon.ini.php (Allmon2 and Supermon): the node's section and any [" + old + "] references (edit that file on disk).",
		"The Asterisk database (favorites, access lists and similar entries are keyed by node number); they do not move on their own.",
		"Other nodes and other computers that link to node " + old + ": tell their operators the new number.",
	}
	savenodeSkip := ""
	if !in.Savenode {
		savenodeSkip = "not requested"
	}
	sp.Steps = []SetupStep{
		{ID: "files", Label: fmt.Sprintf("Write %d configuration file(s) and save a restore point", len(p.Files))},
		{ID: "savenode", Label: "Update savenode.conf (NODE, PASSWORD, ENABLE)", Skipped: savenodeSkip},
		{ID: "ami", Label: "Set a new AMI secret", Skipped: "not needed"},
		{ID: "restart", Label: "Restart Asterisk"},
		{ID: "allmon3", Label: "Update Allmon3 (allmon3.ini) when it is installed"},
		{ID: "health", Label: "Check that the node came back"},
		{ID: "registration", Label: "Check the registration", Skipped: map[bool]string{true: "", false: "not registered"}[regd && public]},
	}
	return sp, nil
}

// otherReferences lists the lines of the files after the change that still
// mention the old node number outside comments (other nodes' settings, dialplan
// contexts, voter.conf). They are reported, never rewritten: a number can mean
// something else there.
func (w *World) otherReferences(p *Plan, old string) []Finding {
	re := regexp.MustCompile(`(^|[^0-9A-Za-z.])` + regexp.QuoteMeta(old) + `([^0-9A-Za-z.]|$)`)
	var out []Finding
	files := append([]string(nil), modelFiles...)
	sort.Strings(files)
	for _, name := range files {
		txt, ok := p.contents[name]
		if !ok {
			if txt, ok = w.Content(name); !ok {
				continue
			}
		}
		var lines []int
		for i, l := range strings.Split(txt, "\n") {
			t := strings.TrimSpace(l)
			if t == "" || strings.HasPrefix(t, ";") {
				continue
			}
			if j := strings.Index(t, ";"); j >= 0 {
				t = t[:j]
			}
			if re.MatchString(t) {
				lines = append(lines, i+1)
			}
		}
		if len(lines) == 0 {
			continue
		}
		shown := lines
		more := ""
		if len(shown) > 6 {
			shown, more = shown[:6], fmt.Sprintf(" and %d more", len(lines)-6)
		}
		var nums []string
		for _, n := range shown {
			nums = append(nums, fmt.Sprint(n))
		}
		out = append(out, Finding{Level: LevelWarning, Code: "renumber_reference", File: name,
			Message: fmt.Sprintf("%s still mentions the old number %s on line %s%s. It was not changed because it may mean something else there; check it in Advanced.", name, old, strings.Join(nums, ", "), more)})
	}
	return out
}
