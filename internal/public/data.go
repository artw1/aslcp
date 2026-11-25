package public

import (
	"regexp"
	"strings"
	"time"

	"github.com/artw1/aslcp/internal/nodelist"
	"github.com/artw1/aslcp/internal/rpt"
	"github.com/artw1/aslcp/internal/store"
)

// The structs below are what the public page receives. They are deliberately
// separate from the admin API structs: a field added to an admin struct can
// never leak here, and none of these has a place for an IP address or system
// information.

// Info is the node-list entry of a node.
type Info struct {
	Callsign    string `json:"callsign"`
	Description string `json:"description"`
	Location    string `json:"location"`
}

// Link is one connected link of a local node. Info is omitted when callsigns
// are hidden.
type Link struct {
	Node       string `json:"node"`
	Label      string `json:"label,omitempty"` // the digital modes bridge node's display name
	Kind       string `json:"kind"`
	Info       *Info  `json:"info,omitempty"`
	Direction  string `json:"direction"`
	Mode       string `json:"mode"` // T transceive, R monitor, L local monitor, C connecting
	Connected  int64  `json:"connected"`
	Keyed      bool   `json:"keyed"`
	SinceKey   *int64 `json:"sinceKey"`
	SinceUnkey *int64 `json:"sinceUnkey"`
}

// NetNode is one entry of the linked-network list. Callsign is omitted when
// callsigns are hidden.
type NetNode struct {
	Node     string `json:"node"`
	Label    string `json:"label,omitempty"` // the digital modes bridge node's display name
	Callsign string `json:"callsign,omitempty"`
}

// Node is the status of one local node. Network is omitted from "node" SSE
// events (it is sent in snapshots and "network" events).
type Node struct {
	Node     string    `json:"node"`
	Info     *Info     `json:"info"`
	Stale    bool      `json:"stale"`
	TxKeyed  bool      `json:"txKeyed"`
	RxKeyed  bool      `json:"rxKeyed"`
	NumLinks int       `json:"numLinks"`
	Links    []Link    `json:"links"`
	Network  []NetNode `json:"network,omitempty"`
}

// Activity is one recent transmission.
type Activity struct {
	Node     string `json:"node"`            // local node
	Link     string `json:"link"`            // link node number, or "local" for the local receiver
	Label    string `json:"label,omitempty"` // the digital modes bridge node's display name
	Callsign string `json:"callsign,omitempty"`
	At       int64  `json:"at"`       // unix seconds
	Duration int64  `json:"duration"` // seconds, at least 1
	Kerchunk bool   `json:"kerchunk"`
}

// Snapshot is the whole page state (status.json and the SSE "snapshot").
type Snapshot struct {
	Title        string     `json:"title"`
	Now          int64      `json:"now"` // server time, unix seconds
	Callsigns    bool       `json:"callsigns"`
	ShowNetwork  bool       `json:"showNetwork"`
	ShowActivity bool       `json:"showActivity"`
	ShowDV       bool       `json:"showDV"` // the digital modes setting; DV is sent only when it is on
	Nodes        []Node     `json:"nodes"`
	Activity     []Activity `json:"activity,omitempty"`
	DV           *DV        `json:"dv,omitempty"`
}

// ActivityEvent is the SSE "activity" payload.
type ActivityEvent struct {
	Now      int64      `json:"now"`
	Activity []Activity `json:"activity"`
}

// NetworkEvent is the SSE "network" payload.
type NetworkEvent struct {
	Node    string    `json:"node"`
	Network []NetNode `json:"network"`
}

// ActivityLimit is the number of recent transmissions shown.
const ActivityLimit = 25

// Lookup resolves node numbers.
type Lookup interface {
	Lookup(node string) (nodelist.Info, bool)
}

var (
	reIPv4 = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}\b`)
	reIPv6 = regexp.MustCompile(`(?i)(?:\b[0-9a-f]{1,4}:){4,7}[0-9a-f]{0,4}\b|[0-9a-f:]*::[0-9a-f:]*[0-9a-f]`)
)

// hasIP reports whether s contains something that looks like an IP address.
func hasIP(s string) bool { return reIPv4.MatchString(s) || reIPv6.MatchString(s) }

// scrub removes IP-address-like text from free text (defence in depth: node
// list fields are user supplied).
func scrub(s string) string {
	if !hasIP(s) {
		return s
	}
	s = reIPv4.ReplaceAllString(s, "")
	s = reIPv6.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

// scrubNode returns a node identifier that is safe to publish: a node number,
// a callsign or an EchoLink number, never an address.
func scrubNode(s string) string {
	if hasIP(s) || strings.ContainsAny(s, " \t\r\n<>\"'&") {
		return "unknown"
	}
	return s
}

var kinds = map[string]bool{"allstar": true, "echolink": true, "phone": true, "client": true, "unknown": true}

func infoOf(l Lookup, node string) *Info {
	if l == nil {
		return nil
	}
	in, ok := l.Lookup(node)
	if !ok {
		return nil
	}
	return &Info{Callsign: scrub(in.Callsign), Description: scrub(in.Description), Location: scrub(in.Location)}
}

// View is how a build is configured.
type View struct {
	Callsigns bool
	Network   bool
	Now       time.Time
	// Bridge is the private digital modes bridge node and BridgeName its display
	// name (as on the dashboard). Both empty when there is none. The number
	// is shown as is (scrubNode only hides addresses); the label tells
	// visitors what it is.
	Bridge     string
	BridgeName string
}

// label is the display name of node when it is the digital modes bridge node.
func (v View) label(node string) string {
	if v.Bridge != "" && node == v.Bridge {
		return v.BridgeName
	}
	return ""
}

// BuildNode converts a monitor status to its public form. Age fields
// (connected, since key/unkey) are brought forward to v.Now, because a status
// kept up to date by events can be older than the data in it.
func BuildNode(st rpt.NodeStatus, l Lookup, v View, withNetwork bool) Node {
	n := Node{
		Node: scrubNode(st.Node), Info: infoOf(l, st.Node), Stale: st.Stale,
		TxKeyed: st.TxKeyed, RxKeyed: st.RxKeyed, NumLinks: st.NumLinks,
		Links: make([]Link, 0, len(st.Links)),
	}
	var age time.Duration
	if !st.Updated.IsZero() && v.Now.After(st.Updated) {
		age = v.Now.Sub(st.Updated)
	}
	secs := func(d time.Duration) int64 { return int64((d + age) / time.Second) }
	opt := func(d *time.Duration) *int64 {
		if d == nil {
			return nil
		}
		x := secs(*d)
		return &x
	}
	for _, lk := range st.Links {
		mode := lk.Mode.String()
		if lk.State == rpt.StateConnecting {
			mode = "C"
		}
		kind := lk.Kind.String()
		if !kinds[kind] {
			kind = "unknown"
		}
		out := Link{
			Node: scrubNode(lk.Node), Label: v.label(lk.Node), Kind: kind, Direction: string(lk.Direction), Mode: mode,
			Connected: secs(lk.Connected), Keyed: lk.Keyed, SinceKey: opt(lk.SinceKey), SinceUnkey: opt(lk.SinceUnkey),
		}
		if v.Callsigns {
			out.Info = infoOf(l, lk.Node)
		}
		n.Links = append(n.Links, out)
	}
	if withNetwork && v.Network {
		n.Network = BuildNetwork(st, l, v)
	}
	return n
}

// BuildNetwork converts the linked-network list.
func BuildNetwork(st rpt.NodeStatus, l Lookup, v View) []NetNode {
	out := make([]NetNode, 0, len(st.Network))
	seen := make(map[string]bool, len(st.Network))
	for _, nn := range st.Network {
		e := NetNode{Node: scrubNode(nn.Node), Label: v.label(nn.Node)}
		// Several entries that were scrubbed to "unknown" (addresses) must not
		// be listed more than once.
		if seen[e.Node] {
			continue
		}
		seen[e.Node] = true
		if v.Callsigns {
			if in := infoOf(l, nn.Node); in != nil {
				e.Callsign = in.Callsign
			}
		}
		out = append(out, e)
	}
	return out
}

// BuildActivity converts stored transmissions.
func BuildActivity(rows []store.Activity, l Lookup, v View) []Activity {
	out := make([]Activity, 0, len(rows))
	for _, r := range rows {
		d := int64((r.Duration + 500*time.Millisecond) / time.Second)
		if d < 1 {
			d = 1
		}
		a := Activity{Node: scrubNode(r.Node), Link: scrubNode(r.Link), Label: v.label(r.Link), At: r.Start.Unix(), Duration: d, Kerchunk: r.Kerchunk}
		if v.Callsigns && r.Link != "local" {
			if in := infoOf(l, r.Link); in != nil {
				a.Callsign = in.Callsign
			}
		}
		out = append(out, a)
	}
	return out
}
