package helper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/artw1/aslcp/internal/astconf"
)

// The private bridge node: the record of what ASLCP changed in the Asterisk
// config (rpt.conf stanza, [nodes] line, modules.conf), kept in the state
// directory, and the plan that removes exactly that.

const (
	// StepPrivateRev names the removal in progress lines.
	StepPrivateRev = "private-revert"
)

// DVUSRP is a USRP address and port pair as the private node record keeps
// it (records of earlier versions hold the old stack's values).
type DVUSRP struct {
	Address string `json:"address,omitempty"`
	RxPort  int    `json:"rxPort,omitempty"`
	TxPort  int    `json:"txPort,omitempty"`
	// Restore allows equal ports: the stock Analog_Bridge.ini has 31001 for
	// both, which a new setup never writes but a removal puts back.
	Restore bool `json:"restore,omitempty"`
}

// DVStockUSRP is the stock [USRP] of Analog_Bridge.ini (docs/DV.md,
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

// DVPrivateReply is the reply of dv-private: the record, nil when there is none.
type DVPrivateReply struct {
	Record *DVPrivateRecord `json:"record"`
}

func validDVPort(p int) bool { return p >= 1024 && p <= 65535 }

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
	data, err := io.ReadAll(io.LimitReader(f, dvPrivateMax))
	if err != nil {
		return nil, err
	}
	return parseDVPrivate(data)
}

// parseDVPrivate parses a record strictly: one JSON object, no unknown
// fields, version 1.
func parseDVPrivate(data []byte) (*DVPrivateRecord, error) {
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

// DVPrivate reads, replaces or clears the record of the private node: no dv
// field is a read, Private sets, Clear clears.
func (o *Ops) DVPrivate(ctx context.Context, req *DVRequest) (DVPrivateReply, error) {
	o.applyMu.Lock()
	defer o.applyMu.Unlock()
	switch {
	case req != nil && req.Private != nil && req.Clear:
		return DVPrivateReply{}, errors.New("invalid request: set or clear, not both")
	case req != nil && req.Private != nil:
		if !req.Private.valid() {
			return DVPrivateReply{}, errors.New("invalid request: private record")
		}
		r := *req.Private
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
	case req != nil && req.Clear:
		if err := os.Remove(o.dvPrivatePath()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return DVPrivateReply{}, err
		}
	}
	r, err := ReadDVPrivate(o.dvPrivatePath())
	return DVPrivateReply{Record: r}, err
}

// ---- removal plan

// DVRemoval is what undoing a record does to rpt.conf and modules.conf. Rpt and
// Modules are the new contents, nil when that file stays as it is.
type DVRemoval struct {
	Rpt, Modules *string
	// RemovedStanza, RemovedLine, RevertedModule say which items changed.
	RemovedStanza, RemovedLine, RevertedModule bool
	// Log are the progress lines of the removal.
	Log []string
	// Problems are items that were changed by hand or could not be read (they
	// stay, and the record is kept). Gone are items that are no longer there.
	Problems, Gone []string
}

// PlanPrivateRemove computes the removal of rec: the stanza and [nodes] line
// ASLCP added (only while they still read as recorded) and chan_usrp.so in
// modules.conf (only if ASLCP changed it and the line is still there). rpt and
// modules are the current contents; nil means the file could not be read.
// Pure: the API shows the result as a plan and writes it through the config op.
func PlanPrivateRemove(rec *DVPrivateRecord, rpt, modules *string) DVRemoval {
	var r DVRemoval
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
