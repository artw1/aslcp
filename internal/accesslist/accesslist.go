// Package accesslist reads and changes the node access lists of AllStarLink 3.
//
// Where they live (internal/astconf/testdata/corpus/asl3-dist/extensions.conf,
// [radio-secure] and [allstar-public]): not in rpt.conf but in the Asterisk
// database, in two families keyed by the local node and the remote one:
//
//	allowlist/<local>/<remote>
//	denylist/<local>/<remote>
//
// The dialplan checks them when a remote node connects. If the local node has
// any allowlist key, only the nodes listed there may connect (the denylist is
// not looked at). Otherwise a node on the denylist is refused. Otherwise
// everybody may connect. So there is no switch to turn the mode on or off:
// the mode follows from which lists have entries. Connections from the
// local host over IAX are always let through. The web transceiver
// ([allstar-public]) uses the same families keyed by callsign.
//
// Reading uses the CLI command "database show"; writing uses the AMI actions
// DBPut and DBDel. Both go through Asterisk's own database API as the
// manager user: no root helper and no file is involved. Every write is a
// fixed family, a local node and a remote node number, all digits.
package accesslist

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/artw1/aslcp/internal/ami"
)

// Family names, as the dialplan spells them.
const (
	Allow = "allowlist"
	Deny  = "denylist"
)

// MaxEntries is the most entries a node may have in one family.
const MaxEntries = 500

// MaxNote is the longest note kept with an entry.
const MaxNote = 40

// InvalidError is a request the package refuses before asking Asterisk.
type InvalidError struct{ Msg string }

func (e *InvalidError) Error() string { return e.Msg }

// IsInvalid reports whether err is an InvalidError.
func IsInvalid(err error) bool {
	var ie *InvalidError
	return errors.As(err, &ie)
}

func invalid(format string, a ...any) error { return &InvalidError{Msg: fmt.Sprintf(format, a...)} }

// AMI is the part of the manager client this package needs.
type AMI interface {
	Action(ctx context.Context, name string, headers ...ami.Header) (*ami.Message, error)
	Command(ctx context.Context, cmd string) ([]string, error)
}

// Entry is one key of a list.
type Entry struct {
	Family string `json:"family"`
	Local  string `json:"local"`
	Remote string `json:"remote"`
	// Web: the remote part is a callsign (a web transceiver entry). Such
	// entries are shown but not changed here.
	Web  bool   `json:"web,omitempty"`
	Note string `json:"note,omitempty"`
}

// Lists is what one node has.
type Lists struct {
	Node  string  `json:"node"`
	Mode  string  `json:"mode"` // "allow", "deny" or "open"
	Allow []Entry `json:"allow"`
	Deny  []Entry `json:"deny"`
}

var (
	reNode = regexp.MustCompile(`^[1-9][0-9]{0,6}$`)
	// reCall is what a web transceiver entry may look like (read only).
	reCall = regexp.MustCompile(`^[A-Za-z0-9]{1,12}(/[A-Za-z0-9]{1,6})?$`)
	reNote = regexp.MustCompile(`^[A-Za-z0-9 ._-]{0,40}$`)
	// reLine: "/allowlist/2001/1999        : 1"
	reLine = regexp.MustCompile(`^/([^/\s]+)/([^/\s]+)/([^/\s]+)\s*:\s?(.*)$`)
)

// ValidNode: 1 to 7 digits, no leading zero.
func ValidNode(s string) bool { return reNode.MatchString(s) }

// ValidFamily: one of the two fixed families.
func ValidFamily(s string) bool { return s == Allow || s == Deny }

// ValidNote: letters, digits, space, dot, underscore and hyphen, at most 40.
func ValidNote(s string) bool { return reNote.MatchString(s) }

// ParseShow turns the output of "database show <family>" into entries of the
// two families. Lines that are not a key of those families, and keys with a
// shape other than family/local/remote, are ignored.
func ParseShow(lines []string) []Entry {
	var out []Entry
	for _, raw := range lines {
		// A command result may carry several lines in one element.
		for _, l := range strings.Split(raw, "\n") {
			m := reLine.FindStringSubmatch(strings.TrimSpace(l))
			if m == nil || !ValidFamily(m[1]) || !ValidNode(m[2]) {
				continue
			}
			e := Entry{Family: m[1], Local: m[2], Remote: m[3], Note: cleanNote(m[4])}
			switch {
			case ValidNode(m[3]):
			case reCall.MatchString(m[3]):
				e.Web = true
			default:
				continue
			}
			out = append(out, e)
		}
	}
	return out
}

// cleanNote shows a stored value only when it is plain text; the value "1"
// (what the dialplan needs) is not a note.
func cleanNote(v string) string {
	v = strings.TrimSpace(v)
	if v == "1" || !ValidNote(v) {
		return ""
	}
	return v
}

// ModeOf says what the dialplan will do for a node with these entries.
func ModeOf(allow, deny []Entry) string {
	switch {
	case len(allow) > 0:
		return "allow"
	case len(deny) > 0:
		return "deny"
	}
	return "open"
}

// Read returns the lists of one local node.
func Read(ctx context.Context, a AMI, node string) (Lists, error) {
	if !ValidNode(node) {
		return Lists{}, errors.New("invalid node")
	}
	out := Lists{Node: node, Allow: []Entry{}, Deny: []Entry{}}
	for _, fam := range []string{Allow, Deny} {
		lines, err := a.Command(ctx, "database show "+fam)
		if err != nil {
			return Lists{}, err
		}
		for _, e := range ParseShow(lines) {
			if e.Local != node || e.Family != fam {
				continue
			}
			if fam == Allow {
				out.Allow = append(out.Allow, e)
			} else {
				out.Deny = append(out.Deny, e)
			}
		}
	}
	for _, l := range [][]Entry{out.Allow, out.Deny} {
		sort.Slice(l, func(i, j int) bool { return l[i].Remote < l[j].Remote })
	}
	out.Mode = ModeOf(out.Allow, out.Deny)
	return out, nil
}

// Enforced reports whether the dialplan text checks the lists at all.
func Enforced(extensions string) bool {
	return strings.Contains(extensions, "allowlist/") && strings.Contains(extensions, "denylist/")
}

// Put adds a node to a family of a local node. cur is what Read returned
// just before, for the size limit and the duplicate check.
func Put(ctx context.Context, a AMI, cur Lists, family, remote, note string) error {
	if !ValidFamily(family) {
		return invalid("the list must be allowlist or denylist")
	}
	if !ValidNode(remote) {
		return invalid("the node must be a number of 1 to 7 digits")
	}
	if !ValidNote(note) {
		return invalid("the note may only have letters, digits, space, . _ - (at most %d characters)", MaxNote)
	}
	if remote == cur.Node {
		return invalid("a node cannot be on its own list")
	}
	list := cur.Allow
	if family == Deny {
		list = cur.Deny
	}
	for _, e := range list {
		if e.Remote == remote {
			return invalid("that node is already on the list")
		}
	}
	if len(list) >= MaxEntries {
		return invalid("a list holds at most %d nodes", MaxEntries)
	}
	val := note
	if val == "" {
		val = "1"
	}
	return action(ctx, a, "DBPut", family, cur.Node, remote, ami.Header{Key: "Val", Value: val})
}

// Del removes a node from a family. Only a node that is on the list can be
// removed, so no other key can be reached through this call.
func Del(ctx context.Context, a AMI, cur Lists, family, remote string) error {
	if !ValidFamily(family) {
		return invalid("the list must be allowlist or denylist")
	}
	if !ValidNode(remote) && !reCall.MatchString(remote) {
		return invalid("invalid entry")
	}
	list := cur.Allow
	if family == Deny {
		list = cur.Deny
	}
	found := false
	for _, e := range list {
		if e.Remote == remote {
			if e.Web {
				return invalid("web transceiver entries are edited with the Asterisk command line")
			}
			found = true
		}
	}
	if !found {
		return invalid("that node is not on the list")
	}
	return action(ctx, a, "DBDel", family, cur.Node, remote)
}

func action(ctx context.Context, a AMI, name, family, local, remote string, extra ...ami.Header) error {
	hs := append([]ami.Header{{Key: "Family", Value: family}, {Key: "Key", Value: local + "/" + remote}}, extra...)
	msg, err := a.Action(ctx, name, hs...)
	if err != nil {
		return err
	}
	if msg != nil && !strings.EqualFold(msg.Get("Response"), "Success") {
		m := msg.Get("Message")
		if m == "" {
			m = "Asterisk refused the change"
		}
		return errors.New(m)
	}
	return nil
}
