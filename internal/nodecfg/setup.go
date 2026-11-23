package nodecfg

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/mail"
	"regexp"
	"strings"
	"unicode"

	"github.com/artw1/aslcp/internal/schema"
)

// The setup wizard plan: one multi-file plan that turns the placeholder node
// of a fresh ASL3 install (or any local node) into the operator's node. It
// reuses the form planner (planRequest, finishPlan) and adds the renumbering
// of every place a node number appears (docs/DESIGN.md 4.3). Everything that
// is not an Asterisk .conf file (savenode.conf, allmon3.ini, ASLCP's own AMI
// login) is done by the API after the files, in one fixed order (see
// internal/api/handlers_nodesetup.go).

// SetupEchoLink is the optional EchoLink step.
type SetupEchoLink struct {
	Call     string `json:"call"` // CALL-L (link) or CALL-R (repeater)
	Password string `json:"password"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	QTH      string `json:"qth"`
}

// SetupRequest is the wizard's answers.
type SetupRequest struct {
	// OldNode is the node being set up (the overview's placeholder).
	OldNode string `json:"oldNode"`
	// Node is the new number: 1 to 1999 private (never registered), above
	// that public.
	Node     string `json:"node"`
	Password string `json:"password"`
	Callsign string `json:"callsign"`
	// Radio is "simpleusb" or "hub".
	Radio string `json:"radio"`
	// Preset is a duplex preset name (Presets()).
	Preset string `json:"preset"`
	// Registration: method http (default for public nodes), iax or none; the
	// host is optional. Private nodes only accept none.
	Registration *RegChange     `json:"registration,omitempty"`
	EchoLink     *SetupEchoLink `json:"echolink,omitempty"`
	// AMI asks for a new AMI secret; the API generates it and sets AMISecret.
	AMI       bool   `json:"ami"`
	AMISecret string `json:"-"`
}

// SetupStep is one step of the apply, as shown on the review page.
type SetupStep struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// Skipped is set when the step is not needed, with the reason.
	Skipped string `json:"skipped,omitempty"`
}

// SetupPlan is the result of PlanSetup.
type SetupPlan struct {
	*Plan
	OldNode string `json:"oldNode"`
	Public  bool   `json:"public"`
	// Renumber: the node number changes.
	Renumber bool        `json:"renumber"`
	Steps    []SetupStep `json:"steps"`
}

var (
	reSetupNode = regexp.MustCompile(`^[1-9][0-9]{0,6}$`)
	reELCall    = regexp.MustCompile(`^[A-Z0-9]{3,10}-[LR]$`)
)

// GenerateSecret returns a random secret for the AMI login (32 hex digits).
func GenerateSecret() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func plainText(s string) bool {
	for _, r := range s {
		if unicode.IsControl(r) || r == ';' || r == '\\' {
			return false
		}
	}
	return true
}

// PlanSetup plans the wizard. Nothing is written. Bad answers return
// *InvalidError; a configuration too unusual to edit returns *RefuseError.
func (w *World) PlanSetup(in SetupRequest) (*SetupPlan, error) {
	if w.Trees[FileRpt] == nil {
		return nil, refuse(Refusal{Code: "no_file", Message: "rpt.conf does not exist.", File: FileRpt})
	}
	old, node := in.OldNode, in.Node
	if !reSetupNode.MatchString(node) {
		return nil, invalid("the node number must be 1 to 7 digits")
	}
	local := map[string]bool{}
	for _, n := range w.LocalNodes() {
		local[n] = true
	}
	if !local[old] {
		return nil, invalid("node %q has no stanza in rpt.conf", old)
	}
	renumber := old != node
	if renumber && local[node] {
		return nil, invalid("rpt.conf already has a node %s", node)
	}
	si := resolveStanza(w.Trees[FileRpt], old, FileRpt)
	if si.refusal != nil {
		return nil, refuse(*si.refusal)
	}
	public := !IsPrivate(node)
	if !plainText(in.Password) || !regexp.MustCompile(`^[A-Za-z0-9._+=,:@%-]{1,64}$`).MatchString(in.Password) {
		return nil, invalid("the password must be 1 to 64 letters, digits or . _ + = , : @ %% -")
	}
	cs := strings.ToUpper(strings.TrimSpace(in.Callsign))
	if !ValidCallsign(cs) {
		return nil, invalid("%q does not look like a callsign (letters and digits, for example N0CALL)", in.Callsign)
	}
	if in.Radio != RadioSimpleUSB && in.Radio != RadioHub {
		return nil, invalid("the radio must be simpleusb or hub; USBRadio and other interfaces are edited in Advanced")
	}
	if in.Preset == "" {
		return nil, invalid("choose a duplex preset")
	}
	if pr, ok := PresetByName(in.Preset); !ok {
		return nil, invalid("unknown preset %q", in.Preset)
	} else if pr.Hub != (in.Radio == RadioHub) {
		return nil, invalid("the hub preset goes with the hub radio option, and the other presets with SimpleUSB")
	}

	rc := RegChange{Method: RegNone}
	if in.Registration != nil {
		rc = *in.Registration
	} else if public {
		rc.Method = RegHTTP
	}
	if rc.Method == "" {
		rc.Method = RegNone
	}
	switch {
	case !public && rc.Method != RegNone:
		return nil, invalid("private nodes (1 to 1999) are not registered; use a number above 1999 to register")
	case rc.Method != RegNone:
		rc.Password = in.Password
	default:
		rc.Password = ""
	}

	req := Request{Node: old, to: node, Preset: in.Preset, Callsign: &cs, Radio: &RadioChange{Type: in.Radio}, Registration: &rc}
	if el := in.EchoLink; el != nil {
		call := strings.ToUpper(strings.TrimSpace(el.Call))
		if !reELCall.MatchString(call) {
			return nil, invalid("the EchoLink callsign must end in -L or -R, for example N0CALL-L")
		}
		if el.Password == "" || !plainText(el.Password) || len(el.Password) > 64 {
			return nil, invalid("the EchoLink password is required (up to 64 characters, no ; or \\)")
		}
		if _, err := mail.ParseAddress(el.Email); err != nil || strings.ContainsAny(el.Email, " <>;") {
			return nil, invalid("%q is not an e-mail address", el.Email)
		}
		for _, f := range []string{el.Name, el.QTH} {
			if strings.TrimSpace(f) == "" || !plainText(f) || len(f) > 64 {
				return nil, invalid("the EchoLink name and location are required (up to 64 characters, no ; or \\)")
			}
		}
		link := true
		req.Link = &link
		req.Changes = []Change{
			{ID: "echolink.conf:el0:call", Value: call},
			{ID: "echolink.conf:el0:pwd", Value: el.Password},
			{ID: "echolink.conf:el0:name", Value: strings.TrimSpace(el.Name)},
			{ID: "echolink.conf:el0:qth", Value: strings.TrimSpace(el.QTH)},
			{ID: "echolink.conf:el0:email", Value: el.Email},
		}
	}

	p := w.newPlan(node, ModeOverride)
	ed := w.newEditor()
	if err := w.planRequest(p, ed, si, req, ModeOverride); err != nil {
		return nil, err
	}
	if in.AMI {
		if in.AMISecret == "" {
			return nil, invalid("no AMI secret was generated")
		}
		k, _ := schema.Find(FileManager, "admin", "secret")
		if err := ed.applyDirect(ModeOverride, k, in.AMISecret); err != nil {
			return nil, err
		}
	}
	if renumber {
		cur := w.radio(si)
		dev := ""
		if cur.Type == RadioSimpleUSB && cur.DeviceStanza && cur.Device == old && in.Radio == RadioSimpleUSB {
			dev = old
		}
		if err := ed.renumber(old, node, si, dev); err != nil {
			return nil, err
		}
		if dev == "" && cur.Device == old && cur.DeviceStanza {
			// hub: the device section is not used any more; left as it is
			ed.out = append(ed.out, Applied{Key: "[" + old + "]", File: FileSimple, Section: old, Action: "unchanged",
				Note: "the device section is not used by a hub; left in place"})
		}
	}
	p.Changes = ed.out
	w.finishPlan(p, ed, old, node)
	sp := &SetupPlan{Plan: p, OldNode: old, Public: public, Renumber: renumber}
	if !p.Noop && (renumber || in.AMI) {
		p.Restart = true
		if renumber {
			p.RestartReasons = append(p.RestartReasons, "the node number changed")
		}
		if in.AMI {
			p.RestartReasons = append(p.RestartReasons, "the AMI secret changed")
		}
		p.RestartReasons = dedupeSorted(p.RestartReasons)
	}
	sp.Steps = setupSteps(sp, in, rc)
	return sp, nil
}

func dedupeSorted(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func setupSteps(sp *SetupPlan, in SetupRequest, rc RegChange) []SetupStep {
	var st []SetupStep
	add := func(id, label, skip string) { st = append(st, SetupStep{ID: id, Label: label, Skipped: skip}) }
	add("files", fmt.Sprintf("Write %d configuration file(s) and save a restore point", len(sp.Files)), "")
	add("savenode", "Update savenode.conf (NODE, PASSWORD, ENABLE)", "")
	add("ami", "Set a new AMI secret and update ASLCP's login", map[bool]string{true: "", false: "not requested"}[in.AMI])
	add("restart", "Restart Asterisk", map[bool]string{true: "", false: "not needed"}[sp.Restart])
	add("allmon3", "Update Allmon3 (allmon3.ini) when it is installed", "")
	add("health", "Check that the node came back", "")
	add("registration", "Check the registration", map[bool]string{true: "", false: "not registered"}[rc.Method != RegNone])
	return st
}
