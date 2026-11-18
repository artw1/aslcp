package helper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/artw1/aslcp/internal/astconf"
)

// The private bridge node (docs/DVSWITCH.md, M2): the helper side is the
// typed USRP op (Analog_Bridge's [USRP] address and ports, through the same
// allowlist and history as every other ini edit), the record of what ASLCP
// changed in the Asterisk config, and the uninstall step that reverts exactly
// that.

const (
	OpDVSwitchUSRP    = "dvswitch-usrp"    // [USRP] address, rxPort, txPort of Analog_Bridge.ini
	OpDVSwitchPrivate = "dvswitch-private" // get, set or clear the record of the private node

	dvPrivateFile  = "privatenode.json"
	StepPrivateRev = "private-revert"
	// dvUSRPAddress is the only address the helper writes: chan_usrp is local.
	dvUSRPAddress = "127.0.0.1"
)

// DVUSRP is the argument of dvswitch-usrp. Zero ports are left alone.
type DVUSRP struct {
	Address string `json:"address,omitempty"`
	RxPort  int    `json:"rxPort,omitempty"`
	TxPort  int    `json:"txPort,omitempty"`
	// Restore allows equal ports: the stock Analog_Bridge.ini has 31001 for
	// both, which a new setup never writes but a removal puts back.
	Restore bool `json:"restore,omitempty"`
}

// DVStockUSRP is the stock [USRP] of Analog_Bridge.ini (docs/DVSWITCH.md,
// package facts): what a removal restores when the record has no earlier
// values (records written before they were kept).
var DVStockUSRP = DVUSRP{Address: dvUSRPAddress, RxPort: 31001, TxPort: 31001}

// DVPrivateRecord is what ASLCP changed for the private node. The uninstall
// reverts exactly this and nothing else.
type DVPrivateRecord struct {
	Version int    `json:"version"`
	Node    string `json:"node"`
	// StanzaAdded: ASLCP created the stanza [Node] (false: it was there
	// before and ASLCP only edited rxchannel and duplex, which is left alone).
	StanzaAdded bool   `json:"stanzaAdded"`
	StanzaText  string `json:"stanzaText,omitempty"`
	BlankAdded  bool   `json:"blankAdded,omitempty"`
	// NodesLineAdded: ASLCP added the line to [nodes].
	NodesLineAdded bool   `json:"nodesLineAdded"`
	NodesLine      string `json:"nodesLine,omitempty"`
	// ChanUsrpWas: the state of chan_usrp.so in modules.conf before ASLCP
	// changed it: "noload", "load" or "absent".
	ChanUsrpWas string `json:"chanUsrpWas"`
	// UsrpWas: Analog_Bridge.ini's [USRP] address and ports before ASLCP
	// changed them (nil with UsrpUntouched false: a record from before they
	// were kept, the removal falls back to DVStockUSRP). UsrpUntouched: ASLCP
	// did not change the ini (the values already matched), nothing to restore.
	UsrpWas       *DVUSRP `json:"usrpWas,omitempty"`
	UsrpUntouched bool    `json:"usrpUntouched,omitempty"`
	// LinkMode and LocalNode are informational (the settings are in the store).
	LinkMode  string `json:"linkMode,omitempty"`
	LocalNode string `json:"localNode,omitempty"`
	At        string `json:"at,omitempty"`
}

// DVPrivate is the argument of dvswitch-private. Action is "get", "set" (with
// Record) or "clear".
type DVPrivate struct {
	Action string           `json:"action"`
	Record *DVPrivateRecord `json:"record,omitempty"`
}

// DVPrivateReply is the reply: the record, nil when there is none.
type DVPrivateReply struct {
	Record *DVPrivateRecord `json:"record"`
}

var (
	reDVNodeNum = regexp.MustCompile(`^[0-9]{1,7}$`)
)

func validDVPort(p int) bool { return p >= 1024 && p <= 65535 }

func (o *Ops) dvPrivatePath() string { return filepath.Join(o.dvStateDir(), dvPrivateFile) }

// ReadDVPrivate reads the record, nil when there is none.
func ReadDVPrivate(path string) (*DVPrivateRecord, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 64<<10))
	if err != nil {
		return nil, err
	}
	var r DVPrivateRecord
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil || r.Version != 1 {
		return nil, errors.New("the private node record is unreadable")
	}
	return &r, nil
}

func (r *DVPrivateRecord) valid() bool {
	if r == nil || r.Version != 1 || !reDVNodeNum.MatchString(r.Node) {
		return false
	}
	if n, _ := strconv.Atoi(r.Node); n < 1 || n >= 2000 {
		return false
	}
	switch r.ChanUsrpWas {
	case "noload", "load", "absent":
	default:
		return false
	}
	if len(r.StanzaText) > 4096 || len(r.NodesLine) > 256 || len(r.LinkMode) > 16 || len(r.LocalNode) > 16 || len(r.At) > 40 {
		return false
	}
	if w := r.UsrpWas; w != nil {
		if w.Restore || w.Address != "" && w.Address != dvUSRPAddress || !validDVPort(w.RxPort) || !validDVPort(w.TxPort) {
			return false
		}
	}
	if r.StanzaAdded && !strings.HasPrefix(r.StanzaText, "["+r.Node+"]") {
		return false
	}
	if r.NodesLineAdded && !strings.HasPrefix(r.NodesLine, r.Node) {
		return false
	}
	return true
}

// DVSwitchUSRP writes Analog_Bridge's [USRP] address and ports.
func (o *Ops) DVSwitchUSRP(ctx context.Context, req *DVSwitchRequest) (DVApplyResult, error) {
	req, err := o.dvArgs(req)
	if err != nil {
		return DVApplyResult{}, err
	}
	u := req.USRP
	if u == nil || req.Identity != nil || req.Credentials != nil || req.Modes != nil || req.Startup != nil || req.Tune != nil || req.Private != nil {
		return DVApplyResult{}, errors.New("invalid request: usrp only")
	}
	var ch []dvChange
	add := func(k, v string) { ch = append(ch, dvChange{DVFileAnalogBridge, "USRP", k, v}) }
	if u.Address != "" {
		add("address", u.Address)
	}
	if u.RxPort != 0 {
		add("rxPort", strconv.Itoa(u.RxPort))
	}
	if u.TxPort != 0 {
		add("txPort", strconv.Itoa(u.TxPort))
	}
	if len(ch) == 0 {
		return dvNoChange(), nil
	}
	if u.RxPort != 0 && u.RxPort == u.TxPort && !u.Restore {
		return DVApplyResult{}, dvInvalid("usrp/ports")
	}
	return o.dvApply(ctx, ch, req.Base)
}

// DVSwitchPrivate reads, replaces or clears the record of the private node.
func (o *Ops) DVSwitchPrivate(ctx context.Context, req *DVSwitchRequest) (DVPrivateReply, error) {
	req, err := o.dvArgs(req)
	if err != nil {
		return DVPrivateReply{}, err
	}
	p := req.Private
	if p == nil {
		return DVPrivateReply{}, errors.New("invalid request: private only")
	}
	o.applyMu.Lock()
	defer o.applyMu.Unlock()
	switch p.Action {
	case "get":
	case "set":
		if !p.Record.valid() {
			return DVPrivateReply{}, dvInvalid("private/record")
		}
		r := *p.Record
		if r.At == "" {
			r.At = o.now().UTC().Format(time.RFC3339)
		}
		b, _ := json.MarshalIndent(r, "", "  ")
		if err := os.MkdirAll(o.dvStateDir(), 0o700); err != nil {
			return DVPrivateReply{}, err
		}
		if err := writeStateFile(o.dvPrivatePath(), append(b, '\n'), 0o600); err != nil {
			return DVPrivateReply{}, fmt.Errorf("cannot write the private node record: %w", err)
		}
	case "clear":
		if err := os.Remove(o.dvPrivatePath()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return DVPrivateReply{}, err
		}
	default:
		return DVPrivateReply{}, dvInvalid("private/action")
	}
	r, err := ReadDVPrivate(o.dvPrivatePath())
	return DVPrivateReply{Record: r}, err
}

func writeStateFile(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// ---- revert (the uninstall step and the standalone removal)

// DVRevert is what undoing a record does to rpt.conf and modules.conf. Rpt and
// Modules are the new contents, nil when that file stays as it is.
type DVRevert struct {
	Rpt, Modules *string
	// RemovedStanza, RemovedLine, RevertedModule say which items changed.
	RemovedStanza, RemovedLine, RevertedModule bool
	// Log are the progress lines of the uninstall step.
	Log []string
	// Problems are items that were changed by hand or could not be read (they
	// stay, and the record is kept). Gone are items that are no longer there.
	Problems, Gone []string
}

// PlanPrivateRevert computes the revert of rec: the stanza and [nodes] line
// ASLCP added (only while they still read as recorded) and chan_usrp.so in
// modules.conf (only if ASLCP changed it and the line is still there). rpt and
// modules are the current contents; nil means the file could not be read.
// Pure: the uninstall step writes the result, the API shows it as a plan.
func PlanPrivateRevert(rec *DVPrivateRecord, rpt, modules *string) DVRevert {
	var r DVRevert
	if rec.StanzaAdded || rec.NodesLineAdded {
		if rpt == nil {
			r.Problems = append(r.Problems, "cannot read rpt.conf")
		} else {
			stanza, line := "", ""
			if rec.StanzaAdded {
				stanza = rec.StanzaText
			}
			if rec.NodesLineAdded {
				line = rec.NodesLine
			}
			out, rs, rl, probs := astconf.RevertBridgeNode([]byte(*rpt), rec.Node, stanza, line, rec.BlankAdded)
			for _, p := range probs {
				if p.Gone {
					r.Gone = append(r.Gone, p.Msg)
				} else {
					r.Problems = append(r.Problems, p.Msg)
				}
			}
			if rs || rl {
				o := string(out)
				r.Rpt = &o
				r.RemovedStanza, r.RemovedLine = rs, rl
				if rs {
					r.Log = append(r.Log, fmt.Sprintf("removing the node stanza [%s] from rpt.conf", rec.Node))
				}
				if rl {
					r.Log = append(r.Log, fmt.Sprintf("removing the [nodes] line of %s from rpt.conf", rec.Node))
				}
			}
		}
	}
	if rec.ChanUsrpWas == "noload" || rec.ChanUsrpWas == "absent" {
		if modules == nil {
			r.Problems = append(r.Problems, "cannot read modules.conf")
		} else if out, ch, err := astconf.RevertModuleLoad([]byte(*modules), astconf.ModuleUSRP, rec.ChanUsrpWas); err != nil {
			r.Problems = append(r.Problems, "modules.conf: "+err.Error())
		} else if ch {
			o := string(out)
			r.Modules, r.RevertedModule = &o, true
			r.Log = append(r.Log, "restoring chan_usrp.so in modules.conf to what it was before")
		} else {
			r.Gone = append(r.Gone, "the chan_usrp.so line of modules.conf was changed by hand and is left as it is")
		}
	}
	return r
}

// ---- uninstall step

// dvPrivateRevert is the uninstall's revert of the private node: the stanza
// and the [nodes] line ASLCP added (only while they still read as recorded),
// and chan_usrp.so back to what it was. It restarts Asterisk only when it
// changed something. A refused item is reported and keeps the record, so a
// later uninstall can try again; it never fails the unit.
func (o *Ops) dvPrivateRevert(ctx context.Context, w io.Writer) error {
	rec, err := ReadDVPrivate(o.dvPrivatePath())
	if err != nil {
		fmt.Fprintln(w, "warning:", err, "- the Asterisk config was not touched")
		return nil
	}
	if rec == nil {
		fmt.Fprintln(w, "no private bridge node was set up by ASLCP: the Asterisk config stays as it is")
		return nil
	}
	var pre []string
	read := func(name string, need bool) *string {
		if !need {
			return nil
		}
		cur, err := o.ConfigRead(name)
		if err != nil {
			pre = append(pre, "cannot read "+name+": "+err.Error())
			return nil
		}
		return &cur.Content
	}
	rptC := read("rpt.conf", rec.StanzaAdded || rec.NodesLineAdded)
	modC := read("modules.conf", rec.ChanUsrpWas == "noload" || rec.ChanUsrpWas == "absent")
	rv := PlanPrivateRevert(rec, rptC, modC)
	// the read errors above carry the reason; the plan's own "cannot read"
	// lines would only repeat them
	problems := append([]string(nil), pre...)
	for _, p := range rv.Problems {
		if !strings.HasPrefix(p, "cannot read ") {
			problems = append(problems, p)
		}
	}
	problems = append(problems, rv.Gone...)
	for _, l := range rv.Log {
		fmt.Fprintln(w, l)
	}
	var files []ConfigFile
	if rv.Rpt != nil {
		cur, _ := o.ConfigRead("rpt.conf")
		files = append(files, ConfigFile{Path: "rpt.conf", Content: *rv.Rpt, Base: cur.SHA256})
	}
	if rv.Modules != nil {
		cur, _ := o.ConfigRead("modules.conf")
		files = append(files, ConfigFile{Path: "modules.conf", Content: *rv.Modules, Base: cur.SHA256})
	}
	if len(files) > 0 {
		if _, err := o.ConfigApply(ctx, &ConfigRequest{Files: files, Author: "aslcp-uninstall", Message: "DVSwitch uninstall: revert the private node"}); err != nil {
			problems = append(problems, "cannot write the Asterisk config: "+err.Error())
			files = nil
		}
	}
	for _, p := range problems {
		fmt.Fprintln(w, "warning:", p)
	}
	if len(files) > 0 {
		fmt.Fprintln(w, "restarting Asterisk")
		if _, err := o.AsteriskRestart(ctx); err != nil {
			fmt.Fprintln(w, "warning: cannot restart Asterisk:", err)
		}
	}
	if len(problems) == 0 {
		_ = os.Remove(o.dvPrivatePath())
		return nil
	}
	fmt.Fprintln(w, "the private node record is kept: check the items above and uninstall again")
	return nil
}
