package rpt

import (
	"encoding/xml"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/artw1/aslcp/internal/ami"
)

// ParseLocalNodesXML parses the raw body of RptLocalNodes:
//
//	<?xml version="1.0"?>
//	<nodes>
//	  <node>1999</node>
//	</nodes>
func ParseLocalNodesXML(lines []string) ([]string, error) {
	var doc struct {
		Nodes []string `xml:"node"`
	}
	if err := xml.Unmarshal([]byte(strings.Join(lines, "\n")), &doc); err != nil {
		return nil, fmt.Errorf("rpt: parsing RptLocalNodes XML: %w", err)
	}
	out := make([]string, 0, len(doc.Nodes))
	for _, n := range doc.Nodes {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	return out, nil
}

// ParseRptStat parses RptStatus/RptStat: a "Nodes: 1999,2000" field, or a raw
// "<NONE>" line when no nodes are configured.
func ParseRptStat(m *ami.Message) ([]string, error) {
	if m.Has("Nodes") {
		var out []string
		for _, n := range strings.Split(m.Get("Nodes"), ",") {
			if n = strings.TrimSpace(n); n != "" {
				out = append(out, n)
			}
		}
		return out, nil
	}
	for _, l := range m.Body {
		if strings.TrimSpace(l) == "<NONE>" {
			return nil, nil
		}
	}
	return nil, errors.New("rpt: RptStat reply has neither Nodes nor <NONE>")
}

// ParseHMS parses "HH:MM:SS" where hours may have any number of digits (the
// source prints "%02d", so it exceeds 24 on long-lived links).
func ParseHMS(s string) (time.Duration, error) {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) != 3 {
		return 0, fmt.Errorf("rpt: bad duration %q", s)
	}
	var v [3]int64
	for i, p := range parts {
		n, err := strconv.ParseInt(p, 10, 64)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("rpt: bad duration %q", s)
		}
		v[i] = n
	}
	return time.Duration(v[0])*time.Hour + time.Duration(v[1])*time.Minute + time.Duration(v[2])*time.Second, nil
}

// ParseConn parses the value of a "Conn:" line from XStat, written by
//
//	"Conn: %-10s%-20s%-12d%-11s%-20s%-20s"
//
// as name, peer IP, reconnects, IN/OUT, HH:MM:SS, ESTABLISHED/CONNECTING.
// The IP may be empty (and is "(none)" when there is no channel); it is
// recognised by position: the last four whitespace separated tokens are
// reconnects, direction, time and state, the first is the name, and anything
// between is the IP. A name of 10 or more characters runs into the IP column
// in the source format; that case cannot be told apart and yields an empty IP.
func ParseConn(v string) (Conn, error) {
	f := strings.Fields(v)
	if len(f) < 5 {
		return Conn{}, fmt.Errorf("rpt: short Conn line %q", v)
	}
	n := len(f)
	c := Conn{Node: f[0]}
	if n > 5 {
		c.IP = strings.Join(f[1:n-4], " ")
	} else if name, ip, ok := splitNameIP(f[0]); ok {
		// The name column is "%-10s", so a name of 10+ characters runs
		// straight into the IP with no separating space.
		c.Node, c.IP = name, ip
	}
	c.Kind = Classify(c.Node)
	if c.IP == "(none)" {
		c.IP = ""
	}
	var err error
	if c.Reconnects, err = strconv.Atoi(f[n-4]); err != nil {
		return Conn{}, fmt.Errorf("rpt: bad reconnects in Conn line %q", v)
	}
	switch Direction(f[n-3]) {
	case DirIn, DirOut:
		c.Direction = Direction(f[n-3])
	default:
		return Conn{}, fmt.Errorf("rpt: bad direction in Conn line %q", v)
	}
	if c.Connected, err = ParseHMS(f[n-2]); err != nil {
		return Conn{}, err
	}
	c.State = f[n-1]
	if c.State != StateEstablished && c.State != StateConnecting {
		return Conn{}, fmt.Errorf("rpt: bad state in Conn line %q", v)
	}
	return c, nil
}

// splitNameIP splits a token where a 10+ character name was printed with no
// space before the peer IP. It prefers the split at the column width (10),
// then the longest valid IP suffix. ok is false if no IP suffix is found.
func splitNameIP(tok string) (name, ip string, ok bool) {
	if len(tok) <= 10 {
		return "", "", false
	}
	if net.ParseIP(tok[10:]) != nil {
		return tok[:10], tok[10:], true
	}
	for i := 11; i < len(tok); i++ {
		if net.ParseIP(tok[i:]) != nil {
			return tok[:i], tok[i:], true
		}
	}
	return "", "", false
}

func isMode(b byte) bool {
	return b == 'T' || b == 'R' || b == 'L' || b == 'C'
}

// ParseLinkedNodes parses "T1234, R2000, L2001, C2002" or "<NONE>". Each
// entry is a mode letter followed by the name. The "R000000" truncation
// marker (used only when message length limiting is on) is skipped.
//
// app_rpt can list the same node more than once (it is reachable over several
// paths); duplicates are removed, keeping the first entry. The count reported
// by app_rpt (RPT_NUMLINKS) includes duplicates.
func ParseLinkedNodes(v string) []LinkedNode {
	v = strings.TrimSpace(v)
	if v == "" || v == "<NONE>" {
		return nil
	}
	var out []LinkedNode
	seen := map[string]bool{}
	for _, tok := range strings.Split(v, ",") {
		tok = strings.TrimSpace(tok)
		if len(tok) < 2 || !isMode(tok[0]) {
			continue
		}
		name := tok[1:]
		if name == "000000" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, LinkedNode{Node: name, Kind: Classify(name), Mode: LinkMode(tok[0])})
	}
	return out
}

// ParseLinks parses an RPT_LINKS value: "<count>,<mode><name>,..." (an empty
// value means no links).
func ParseLinks(v string) (int, []LinkedNode) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, nil
	}
	toks := strings.Split(v, ",")
	n, err := strconv.Atoi(strings.TrimSpace(toks[0]))
	if err != nil {
		return 0, ParseLinkedNodes(v)
	}
	return n, ParseLinkedNodes(strings.Join(toks[1:], ","))
}

// ParseALinks parses an RPT_ALINKS value: "<count>,<name><mode><K|U>,..."
// for example "2,2000TK,2001RU". Only adjacent links appear, and local
// monitor links are never reported. An empty value means no links.
func ParseALinks(v string) (int, []ALink, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, nil, nil
	}
	toks := strings.Split(v, ",")
	n, err := strconv.Atoi(strings.TrimSpace(toks[0]))
	if err != nil {
		return 0, nil, fmt.Errorf("rpt: bad RPT_ALINKS count in %q", v)
	}
	var out []ALink
	for _, tok := range toks[1:] {
		tok = strings.TrimSpace(tok)
		if len(tok) < 3 {
			return 0, nil, fmt.Errorf("rpt: bad RPT_ALINKS entry %q", tok)
		}
		k, m := tok[len(tok)-1], tok[len(tok)-2]
		if (k != 'K' && k != 'U') || !isMode(m) {
			return 0, nil, fmt.Errorf("rpt: bad RPT_ALINKS entry %q", tok)
		}
		name := tok[:len(tok)-2]
		if name == "000000" { // truncation marker
			continue
		}
		out = append(out, ALink{Node: name, Kind: Classify(name), Mode: LinkMode(m), Keyed: k == 'K'})
	}
	return n, out, nil
}

var flagKeys = map[string]bool{
	"parrot_ena": true, "sys_ena": true, "tot_ena": true, "link_ena": true,
	"patch_ena": true, "patch_state": true, "sch_ena": true, "user_funs": true,
	"tail_type": true, "iconns": true, "tot_state": true, "ider_state": true,
	"tel_mode": true,
}

func buildFlags(st map[string]string) Flags {
	if len(st) == 0 {
		return Flags{}
	}
	b := func(k string) bool { return st[k] == "1" }
	i := func(k string) int { n, _ := strconv.Atoi(st[k]); return n }
	return Flags{
		Valid:             true,
		ParrotEnabled:     b("parrot_ena"),
		TxDisabled:        b("sys_ena"),
		TotDisabled:       b("tot_ena"),
		LinkFunsDisabled:  b("link_ena"),
		AutopatchDisabled: b("patch_ena"),
		SchedulerDisabled: b("sch_ena"),
		UserFunsDisabled:  b("user_funs"),
		AlternateTail:     b("tail_type"),
		NoIncomingConns:   b("iconns"),
		TotState:          TotState(i("tot_state")),
		IderState:         IderState(i("ider_state")),
		PatchState:        PatchState(i("patch_state")),
		TelMode:           i("tel_mode"),
	}
}

// ParseXStat parses an RptStatus/XStat reply.
func ParseXStat(m *ami.Message) (*XStat, error) {
	x := &XStat{Node: m.Get("Node"), Vars: map[string]string{}, State: map[string]string{}}
	for _, f := range m.Fields {
		key := strings.ToLower(f.Key)
		switch {
		case key == "conn":
			c, err := ParseConn(f.Value)
			if err != nil {
				return nil, err
			}
			x.Conns = append(x.Conns, c)
		case key == "linkednodes":
			x.Network = ParseLinkedNodes(f.Value)
		case key == "var":
			k, v, ok := strings.Cut(f.Value, "=")
			if ok {
				x.Vars[k] = v
			}
		case flagKeys[key]:
			x.State[key] = strings.TrimSpace(f.Value)
		}
	}
	x.RxKeyed = x.Vars["RPT_RXKEYED"] == "1"
	x.TxKeyed = x.Vars["RPT_TXKEYED"] == "1"
	x.ExtTxKeyed = x.Vars["RPT_ETXKEYED"] == "1"
	x.NumLinks, _ = strconv.Atoi(strings.TrimSpace(x.Vars["RPT_NUMLINKS"]))
	x.NumALinks, _ = strconv.Atoi(strings.TrimSpace(x.Vars["RPT_NUMALINKS"]))
	if v, ok := x.Vars["RPT_LINKS"]; ok {
		_, x.Links = ParseLinks(v)
	}
	if v, ok := x.Vars["RPT_ALINKS"]; ok {
		n, al, err := ParseALinks(v)
		if err != nil {
			return nil, err
		}
		x.ALinks = al
		if _, has := x.Vars["RPT_NUMALINKS"]; !has {
			x.NumALinks = n
		}
	}
	x.Flags = buildFlags(x.State)
	return x, nil
}

func secs(v string) (*time.Duration, error) {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return nil, fmt.Errorf("rpt: bad seconds value %q", v)
	}
	if n < 0 {
		return nil, nil
	}
	d := time.Duration(n) * time.Second
	return &d, nil
}

// ParseSawStat parses a RptStatus/SawStat reply. Each "Conn:" line is
// "<name> <keyed 0/1> <secs since key or -1> <secs since unkey or -1>".
func ParseSawStat(m *ami.Message) ([]SawConn, error) {
	var out []SawConn
	for _, v := range m.All("Conn") {
		f := strings.Fields(v)
		if len(f) != 4 {
			return nil, fmt.Errorf("rpt: bad SawStat line %q", v)
		}
		k, err := strconv.Atoi(f[1])
		if err != nil {
			return nil, fmt.Errorf("rpt: bad SawStat line %q", v)
		}
		sk, err := secs(f[2])
		if err != nil {
			return nil, err
		}
		su, err := secs(f[3])
		if err != nil {
			return nil, err
		}
		out = append(out, SawConn{Node: f[0], Keyed: k != 0, SinceKey: sk, SinceUnkey: su})
	}
	return out, nil
}

// Merge combines XStat and SawStat results into a NodeStatus.
//
// Link mode comes from the LinkedNodes line (a link absent from it is a
// local-monitor link, which app_rpt never reports there). The keyed flag
// comes from SawStat, falling back to RPT_ALINKS when SawStat has no row.
func Merge(node string, x *XStat, saw []SawConn, now time.Time) NodeStatus {
	st := NodeStatus{
		Node:       node,
		RxKeyed:    x.RxKeyed,
		TxKeyed:    x.TxKeyed,
		ExtTxKeyed: x.ExtTxKeyed,
		Network:    append([]LinkedNode(nil), x.Network...),
		NumLinks:   x.NumLinks,
		NumALinks:  x.NumALinks,
		Flags:      x.Flags,
		Updated:    now,
		Links:      make([]Link, 0, len(x.Conns)),
	}
	sawBy := make(map[string]SawConn, len(saw))
	for _, s := range saw {
		sawBy[s.Node] = s
	}
	alBy := make(map[string]ALink, len(x.ALinks))
	for _, a := range x.ALinks {
		alBy[a.Node] = a
	}
	adj := make(map[string]LinkMode, len(x.Conns))
	for _, n := range x.Network {
		if _, ok := adj[n.Node]; !ok {
			adj[n.Node] = n.Mode
		}
	}
	for _, c := range x.Conns {
		l := Link{Node: c.Node, Kind: c.Kind, IP: c.IP, Direction: c.Direction,
			Reconnects: c.Reconnects, Connected: c.Connected, State: c.State}
		if m, ok := adj[c.Node]; ok {
			l.Mode = m
		} else {
			l.Mode = ModeLocalMonitor
		}
		if s, ok := sawBy[c.Node]; ok {
			l.Keyed, l.SinceKey, l.SinceUnkey = s.Keyed, s.SinceKey, s.SinceUnkey
		} else if a, ok := alBy[c.Node]; ok {
			l.Keyed = a.Keyed
		}
		st.Links = append(st.Links, l)
	}
	return st
}

var (
	reUptime = regexp.MustCompile(`(?i)^\s*System uptime:\s*(\d+)\s*$`)
	reReload = regexp.MustCompile(`(?i)^\s*Last reload:\s*(\d+)\s*$`)
)

// Uptime is the result of "core show uptime seconds".
type Uptime struct {
	System     time.Duration
	LastReload time.Duration
}

// ParseUptime parses the output of "core show uptime seconds":
//
//	System uptime: 12345
//	Last reload: 678
func ParseUptime(lines []string) (Uptime, error) {
	var u Uptime
	var gotSys bool
	for _, l := range lines {
		if m := reUptime.FindStringSubmatch(l); m != nil {
			n, _ := strconv.ParseInt(m[1], 10, 64)
			u.System, gotSys = time.Duration(n)*time.Second, true
		} else if m := reReload.FindStringSubmatch(l); m != nil {
			n, _ := strconv.ParseInt(m[1], 10, 64)
			u.LastReload = time.Duration(n) * time.Second
		}
	}
	if !gotSys {
		return u, errors.New("rpt: no 'System uptime' in output")
	}
	return u, nil
}
