package dvpanel

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/artw1/aslcp/internal/astconf"
)

// Pure rules of the private bridge node (docs/DV.md, "Private node and
// USRP"): which node numbers and ports the setup accepts.

const (
	// Stock USRP ports: Asterisk sends to DefaultRxPort (the engine listens
	// there) and listens on DefaultTxPort.
	DefaultRxPort = 34001
	DefaultTxPort = 32001
	// MaxPrivateNode: private nodes are below 2000; public ones start there.
	MaxPrivateNode = 1999
	suggestFirst   = 1900
	suggestLast    = 1997
)

// ErrInvalidNode is returned for a node that is not a private node number.
var ErrInvalidNode = errors.New("the bridge node must be a private node number from 1 to 1999")

var reNodeNum = regexp.MustCompile(`^[1-9][0-9]{0,3}$`)

// ValidNode checks a bridge node number (1 to 1999).
func ValidNode(s string) (int, error) {
	if !reNodeNum.MatchString(s) {
		return 0, ErrInvalidNode
	}
	n, _ := strconv.Atoi(s)
	if n > MaxPrivateNode {
		return 0, ErrInvalidNode
	}
	return n, nil
}

// SuggestNode returns the first node of 1900-1997 that is not in used, "" when
// all are taken.
func SuggestNode(used map[string]bool) string {
	for n := suggestFirst; n <= suggestLast; n++ {
		if !used[strconv.Itoa(n)] {
			return strconv.Itoa(n)
		}
	}
	return ""
}

// PortConflict says which port clashes with what.
type PortConflict struct {
	Port int
	With string
}

func (c *PortConflict) Error() string {
	return fmt.Sprintf("port %d is already used by %s", c.Port, c.With)
}

// PortRange: ports the helper accepts for USRP (unprivileged).
func ValidPort(p int) bool { return p >= 1024 && p <= 65535 }

// CheckPorts validates the USRP ports: in range, different from each other,
// not one of the reserved ports and not used by another USRP channel
// of rpt.conf (other lists the ports those use).
func CheckPorts(rx, tx int, reserved, other []int) error {
	if !ValidPort(rx) || !ValidPort(tx) {
		return errors.New("ports must be between 1024 and 65535")
	}
	if rx == tx {
		return errors.New("the two USRP ports must differ")
	}
	for _, p := range []int{rx, tx} {
		for _, r := range reserved {
			if p == r {
				return &PortConflict{p, "the digital modes engine"}
			}
		}
		for _, o := range other {
			if p == o {
				return &PortConflict{p, "another USRP node in rpt.conf"}
			}
		}
	}
	return nil
}

var reAnyNode = regexp.MustCompile(`^[1-9][0-9]{0,9}$`)

func isNodeName(s string) bool { return reAnyNode.MatchString(s) }

// UsedNodes returns every node number rpt.conf names: section headers (not
// templates) and the keys of [nodes].
func UsedNodes(t *astconf.Tree) map[string]bool {
	used := map[string]bool{}
	for _, s := range t.Sections {
		if s.Template {
			continue
		}
		if isNodeName(s.Name) {
			used[s.Name] = true
		}
		if s.Name == "nodes" {
			for _, e := range s.Entries {
				if isNodeName(strings.TrimSpace(e.Key)) {
					used[strings.TrimSpace(e.Key)] = true
				}
			}
		}
	}
	return used
}

// USRPPorts returns the ports of the USRP channels of every node except the
// ones named in except.
func USRPPorts(t *astconf.Tree, except ...string) (ports []int) {
	seen := map[string]bool{}
	for _, e := range except {
		seen[e] = true
	}
	for _, s := range t.Sections {
		if s.Template || !isNodeName(s.Name) || seen[s.Name] {
			continue
		}
		seen[s.Name] = true
		es, err := astconf.Effective(t, s.Name)
		if err != nil {
			continue
		}
		rx := ""
		for _, e := range es {
			if strings.EqualFold(e.Key, "rxchannel") {
				rx = strings.TrimSpace(e.Value)
			}
		}
		drv, rest, _ := strings.Cut(rx, "/")
		if !strings.EqualFold(drv, "usrp") {
			continue
		}
		parts := strings.Split(rest, ":")
		for _, p := range parts[1:] { // USRP/host:rx:tx
			if n, err := strconv.Atoi(strings.TrimSpace(p)); err == nil {
				ports = append(ports, n)
			}
		}
	}
	sort.Ints(ports)
	return ports
}

// IsUSRPNode reports whether the effective rxchannel of node is USRP.
func IsUSRPNode(t *astconf.Tree, node string) bool {
	es, err := astconf.Effective(t, node)
	if err != nil {
		return false
	}
	rx := ""
	for _, e := range es {
		if strings.EqualFold(e.Key, "rxchannel") {
			rx = strings.TrimSpace(e.Value)
		}
	}
	drv, _, _ := strings.Cut(rx, "/")
	return strings.EqualFold(drv, "usrp")
}

// USRPChannel is the chan_usrp channel of a node: rxchannel
// USRP/<Host>:<RxPort>:<TxPort>. Standard is true for the form ASLCP writes,
// USRP/127.0.0.1:<port>:<port> (two ports, local host).
type USRPChannel struct {
	Node           string
	Host           string
	RxPort, TxPort int
	Standard       bool
}

// USRPChannels lists the nodes of rpt.conf whose effective rxchannel is a
// USRP channel, in file order.
func USRPChannels(t *astconf.Tree) []USRPChannel {
	var out []USRPChannel
	seen := map[string]bool{}
	for _, s := range t.Sections {
		if s.Template || !isNodeName(s.Name) || seen[s.Name] {
			continue
		}
		seen[s.Name] = true
		es, err := astconf.Effective(t, s.Name)
		if err != nil {
			continue
		}
		rx := ""
		for _, e := range es {
			if strings.EqualFold(e.Key, "rxchannel") {
				rx = strings.TrimSpace(e.Value)
			}
		}
		drv, rest, _ := strings.Cut(rx, "/")
		if !strings.EqualFold(drv, "usrp") {
			continue
		}
		c := USRPChannel{Node: s.Name}
		parts := strings.Split(rest, ":")
		c.Host = strings.TrimSpace(parts[0])
		if len(parts) == 3 {
			a, e1 := strconv.Atoi(strings.TrimSpace(parts[1]))
			b, e2 := strconv.Atoi(strings.TrimSpace(parts[2]))
			c.RxPort, c.TxPort = a, b
			c.Standard = e1 == nil && e2 == nil && c.Host == "127.0.0.1" && a >= 1024 && a <= 65535 && b >= 1024 && b <= 65535
		}
		out = append(out, c)
	}
	return out
}
