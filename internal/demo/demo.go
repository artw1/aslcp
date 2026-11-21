// Package demo runs a simulated app_rpt node as an in-process fake AMI
// server, so the real AMI client, monitor and API are exercised with no
// Asterisk installed. Callsigns and node data are fictional.
package demo

import (
	"bufio"
	"context"
	"fmt"
	"math/rand/v2"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/artw1/aslcp/internal/nodelist"
)

// AMI credentials of the simulated server.
const (
	Username = "demo"
	Secret   = "demo-ami"
)

// LocalNodes are the simulated local nodes.
var LocalNodes = []string{"2006", "2007"}

// Version is reported by "core show version".
const Version = "22.10.1+asl3-3.10.8-1.deb13"

// hubs are demo nodes that bring a small network with them when linked.
var hubs = map[string][]string{
	"50241": {"1021", "1330", "1430", "1528791", "1528792", "1528793", "1528794", "1530", "1799", "1899",
		"1980", "1992", "1994", "1995", "1996", "1999", "27131", "27277", "27542", "28507", "29307",
		"3069556", "N0AAA", "N0AAB", "N0AAC", "N0AAD"},
	"2000": {"2001", "2002", "2003", "2004", "2005"},
	"1999": {"1998", "1997", "1996"},
}

// NodeList returns the built-in fake node database.
func NodeList() []nodelist.Info {
	named := []nodelist.Info{
		{Node: "2006", Callsign: "N0AAD", Description: "Demo node (this node)", Location: "Demoville, ST"},
		{Node: "2007", Callsign: "N0AAE", Description: "Demo hub node", Location: "Demoville, ST"},
		{Node: "50241", Callsign: "N0AAF", Description: "Demo Network Hub", Location: "Springfield, ST"},
		{Node: "2000", Callsign: "N0AAG", Description: "Demo Reflector 2000", Location: "Nowhere, ST"},
		{Node: "1999", Callsign: "N0AAH", Description: "Demo Test Hub", Location: "Metropolis, ST"},
		{Node: "27339", Callsign: "N0AAI", Description: "Demo East Reflector", Location: "Shelbyville, ST"},
		{Node: "43763", Callsign: "N0AAJ", Description: "Demo Club Hub", Location: "Capital City, ST"},
		{Node: "555491", Callsign: "N0AAK", Description: "Demo Metro VHF", Location: "Gotham, ST"},
		{Node: "3001234", Callsign: "N0AAL-R", Description: "Demo EchoLink Gateway", Location: "Ogdenville, ST"},
	}
	have := map[string]bool{}
	for _, n := range named {
		have[n.Node] = true
	}
	out := append([]nodelist.Info(nil), named...)
	letters := "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	i := 0
	var all []string
	for _, h := range hubs {
		all = append(all, h...)
	}
	sort.Strings(all)
	for _, n := range all {
		if have[n] || !isNum(n) {
			continue
		}
		have[n] = true
		cs := fmt.Sprintf("N0%c%c%c", letters[(i/26/26)%26], letters[(i/26)%26], letters[i%26])
		out = append(out, nodelist.Info{Node: n, Callsign: cs, Description: "Demo repeater " + n, Location: "Demoville, ST"})
		i++
	}
	return out
}

func isNum(s string) bool {
	_, err := strconv.ParseUint(s, 10, 64)
	return err == nil
}

type link struct {
	node      string
	mode      byte // 'T', 'R', 'L'
	out       bool
	ip        string
	since     time.Time
	behind    []string
	keyed     bool
	lastKey   time.Time // zero = never
	lastUnkey time.Time
	nextFlip  time.Time
}

type nodeState struct {
	id      string
	rx, tx  bool
	links   []*link
	rxUntil time.Time
	nextRx  time.Time
	// slotBusy mimics app_rpt's single command slot per node: a command
	// is picked up by the node's main loop a moment after it arrives, and
	// another command arriving meanwhile is refused silently.
	slotBusy bool
}

// demoCmdDelay is how long the simulated main loop takes to pick up and run
// a queued command.
const demoCmdDelay = 250 * time.Millisecond

// Sim is the simulated AMI server.
type Sim struct {
	Addr string

	ln    net.Listener
	start time.Time

	mu       sync.Mutex
	nodes    map[string]*nodeState
	sessions map[*session]struct{}
	rng      *rand.Rand
	wg       sync.WaitGroup
	radio    *demoRadio
	// OnTuneSave is called with the simpleusb.conf content a simulated
	// "susb tune save" writes (set before the first command).
	OnTuneSave func(conf string)
}

type session struct {
	conn   net.Conn
	wmu    sync.Mutex
	authed bool
	events bool
}

func (s *session) write(text string) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_ = s.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, _ = s.conn.Write([]byte(text))
}

// Start listens on 127.0.0.1:0 and runs the simulation until ctx is done.
func Start(ctx context.Context) (*Sim, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	now := time.Now()
	sim := &Sim{
		Addr: ln.Addr().String(), ln: ln, start: now.Add(-33*time.Hour - 30*time.Minute),
		nodes: map[string]*nodeState{}, sessions: map[*session]struct{}{},
		rng: rand.New(rand.NewPCG(uint64(now.UnixNano()), 7)),
	}
	for _, n := range LocalNodes {
		sim.nodes[n] = &nodeState{id: n, nextRx: now.Add(8 * time.Second)}
	}
	sim.addLink("2006", "50241", 'T', true, "192.0.2.58", now.Add(-29*time.Second))
	sim.wg.Add(2)
	go func() { defer sim.wg.Done(); sim.accept() }()
	go func() { defer sim.wg.Done(); sim.run(ctx) }()
	go func() {
		<-ctx.Done()
		ln.Close()
		sim.mu.Lock()
		for s := range sim.sessions {
			s.conn.Close()
		}
		sim.mu.Unlock()
	}()
	return sim, nil
}

// Wait blocks until the simulation goroutines stop (after ctx is cancelled).
func (s *Sim) Wait() { s.wg.Wait() }

func (s *Sim) addLink(node, target string, mode byte, out bool, ip string, since time.Time) {
	n := s.nodes[node]
	l := &link{node: target, mode: mode, out: out, ip: ip, since: since,
		behind:   append([]string(nil), hubs[target]...),
		nextFlip: time.Now().Add(time.Duration(2+s.rng.IntN(6)) * time.Second)}
	n.links = append(n.links, l)
}

func (s *Sim) accept() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		ss := &session{conn: c}
		s.mu.Lock()
		s.sessions[ss] = struct{}{}
		s.mu.Unlock()
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.serve(ss)
			s.mu.Lock()
			delete(s.sessions, ss)
			s.mu.Unlock()
			c.Close()
		}()
	}
}

type request struct {
	fields map[string]string
}

func (r request) get(k string) string { return r.fields[strings.ToLower(k)] }

func readRequest(br *bufio.Reader) (request, error) {
	r := request{fields: map[string]string{}}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return r, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if len(r.fields) == 0 {
				continue
			}
			return r, nil
		}
		if k, v, ok := strings.Cut(line, ":"); ok {
			r.fields[strings.ToLower(k)] = strings.TrimPrefix(v, " ")
		}
	}
}

func reply(r request, resp string, kv ...string) string {
	var sb strings.Builder
	sb.WriteString("Response: " + resp + "\r\n")
	if id := r.get("ActionID"); id != "" {
		sb.WriteString("ActionID: " + id + "\r\n")
	}
	for i := 0; i+1 < len(kv); i += 2 {
		sb.WriteString(kv[i] + ": " + kv[i+1] + "\r\n")
	}
	sb.WriteString("\r\n")
	return sb.String()
}

func cmdOutput(r request, lines ...string) string {
	kv := []string{"Message", "Command output follows"}
	for _, l := range lines {
		kv = append(kv, "Output", l)
	}
	return reply(r, "Success", kv...)
}

func (s *Sim) serve(ss *session) {
	ss.write("Asterisk Call Manager/11.0.0\r\n")
	br := bufio.NewReader(ss.conn)
	for {
		req, err := readRequest(br)
		if err != nil {
			return
		}
		switch strings.ToLower(req.get("Action")) {
		case "login":
			if req.get("Username") == Username && req.get("Secret") == Secret {
				s.mu.Lock()
				ss.authed = true
				ss.events = strings.Contains(strings.ToLower(req.get("Events")), "call") || strings.EqualFold(req.get("Events"), "on")
				s.mu.Unlock()
				ss.write(reply(req, "Success", "Message", "Authentication accepted"))
				continue
			}
			ss.write(reply(req, "Error", "Message", "Authentication failed"))
			return
		case "logoff":
			ss.write(reply(req, "Goodbye", "Message", "Thanks for all the fish."))
			return
		case "ping":
			ss.write(reply(req, "Success", "Ping", "Pong"))
			continue
		}
		s.mu.Lock()
		authed := ss.authed
		s.mu.Unlock()
		if !authed {
			ss.write(reply(req, "Error", "Message", "Permission denied"))
			continue
		}
		switch strings.ToLower(req.get("Action")) {
		case "rptlocalnodes":
			xml := []string{`<?xml version="1.0"?>`, `<nodes>`}
			for _, n := range LocalNodes {
				xml = append(xml, "  <node>"+n+"</node>")
			}
			xml = append(xml, `</nodes>`)
			ss.write(strings.Join(xml, "\r\n") + "\r\n\r\n")
		case "rptstatus":
			ss.write(s.rptStatus(req))
		case "command":
			ss.write(s.command(req))
		default:
			ss.write(reply(req, "Error", "Message", "Invalid/unknown command: "+req.get("Action")))
		}
	}
}

func hms(d time.Duration) string {
	sec := int(d / time.Second)
	return fmt.Sprintf("%02d:%02d:%02d", sec/3600, sec/60%60, sec%60)
}

func (l *link) established(now time.Time) bool { return now.Sub(l.since) >= 2*time.Second }

func (s *Sim) rptStatus(req request) string {
	node := req.get("Node")
	cmd := req.get("Command")
	s.mu.Lock()
	defer s.mu.Unlock()
	if cmd == "RptStat" {
		return reply(req, "Success", "Nodes", strings.Join(LocalNodes, ","), "RptUptime", hms(time.Since(s.start)))
	}
	n := s.nodes[node]
	if n == nil {
		return reply(req, "Error", "Message", "RptStatus unknown or missing node")
	}
	now := time.Now()
	switch cmd {
	case "XStat":
		kv := []string{"Node", node}
		var net, alinks []string
		seen := map[string]bool{}
		for _, l := range n.links {
			state := "ESTABLISHED"
			if !l.established(now) {
				state = "CONNECTING"
			}
			ip := l.ip
			if ip == "" {
				ip = "(none)"
			}
			dir := "IN"
			if l.out {
				dir = "OUT"
			}
			kv = append(kv, "Conn", fmt.Sprintf("%-10s%-20s%-12d%-11s%-20s%-20s", l.node, ip, 0, dir, hms(now.Sub(l.since)), state))
			if state == "ESTABLISHED" && l.mode != 'L' {
				net = append(net, string(l.mode)+l.node)
				seen[l.node] = true
				k := "U"
				if l.keyed {
					k = "K"
				}
				alinks = append(alinks, l.node+string(l.mode)+k)
				for _, b := range l.behind {
					if !seen[b] {
						seen[b] = true
						net = append(net, "T"+b)
					}
				}
			}
		}
		ln := "<NONE>"
		if len(net) > 0 {
			ln = strings.Join(net, ", ")
		}
		kv = append(kv, "LinkedNodes", ln,
			"Var", "XX_RPT_RXKEYED=0",
			"Var", "RPT_TXKEYED="+b01(n.tx),
			"Var", fmt.Sprintf("RPT_NUMLINKS=%d", len(net)))
		if len(net) > 0 {
			kv = append(kv, "Var", fmt.Sprintf("RPT_LINKS=%d,%s", len(net), strings.Join(net, ",")))
		} else {
			kv = append(kv, "Var", "RPT_LINKS=0")
		}
		kv = append(kv, "Var", fmt.Sprintf("RPT_NUMALINKS=%d", len(alinks)))
		if len(alinks) > 0 {
			kv = append(kv, "Var", fmt.Sprintf("RPT_ALINKS=%d,%s", len(alinks), strings.Join(alinks, ",")))
		} else {
			kv = append(kv, "Var", "RPT_ALINKS=0")
		}
		kv = append(kv, "Var", "RPT_AUTOPATCHUP=0", "Var", "RPT_ETXKEYED=0", "Var", "RPT_RXKEYED="+b01(n.rx),
			"parrot_ena", "0", "sys_ena", "0", "tot_ena", "0", "link_ena", "0", "patch_ena", "0", "patch_state", "4",
			"sch_ena", "0", "user_funs", "0", "tail_type", "0", "iconns", "0", "tot_state", "2", "ider_state", "2", "tel_mode", "0")
		return reply(req, "Success", kv...)
	case "SawStat":
		kv := []string{"Node", node}
		for _, l := range n.links {
			kv = append(kv, "Conn", fmt.Sprintf("%s %s %s %s", l.node, b01(l.keyed), sawAge(now, l.lastKey), sawAge(now, l.lastUnkey)))
		}
		return reply(req, "Success", kv...)
	case "NodeStat":
		keys := len(n.links)
		linked := "<NONE>"
		if len(n.links) > 0 {
			var ns []string
			for _, l := range n.links {
				ns = append(ns, l.node)
			}
			linked = strings.Join(ns, ", ")
		}
		return reply(req, "Success",
			"IsRemoteBase", "NO", "NodeState", "0", "SignalOnInput", map[bool]string{true: "YES", false: "NO"}[n.rx],
			"TransmitterKeyed", map[bool]string{true: "YES", false: "NO"}[n.tx], "Transmitter", "ENABLED", "Parrot", "DISABLED",
			"Scheduler", "ENABLED", "TailLength", "STANDARD", "TimeOutTimer", "ENABLED", "TimeOutTimerState", "RESET",
			"TimeOutsSinceSystemInitialization", "0", "IdentifierState", "CLEAN",
			"KerchunksToday", "0", "KerchunksSinceSystemInitialization", "0",
			"KeyupsToday", strconv.Itoa(12+keys), "KeyupsSinceSystemInitialization", strconv.Itoa(140+keys),
			"DtmfCommandsToday", "3", "DtmfCommandsSinceSystemInitialization", "21", "LastDtmfCommandExecuted", "N/A",
			"TxTimeToday", "00:04:12:340", "TxTimeSinceSystemInitialization", "01:22:45:008",
			"NodesCurrentlyConnectedToUs", linked, "Autopatch", "ENABLED", "AutopatchState", "DOWN",
			"AutopatchCalledNumber", "N/A", "ReversePatchIaxrptConnected", "DOWN",
			"UserLinkingCommands", "ENABLED", "UserFunctions", "ENABLED")
	}
	return reply(req, "Error", "Message", "RptStatus unknown command")
}

func sawAge(now, t time.Time) string {
	if t.IsZero() {
		return "-1"
	}
	return strconv.Itoa(int(now.Sub(t) / time.Second))
}

func b01(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

var reIlink = regexp.MustCompile(`^rpt cmd ([0-9]+) ilink ([0-9]+)(?: ([A-Za-z0-9-]+))?$`)

func (s *Sim) command(req request) string {
	cmd := strings.TrimSpace(req.get("Command"))
	switch cmd {
	case "core show uptime seconds":
		up := int(time.Since(s.start) / time.Second)
		return cmdOutput(req, fmt.Sprintf("System uptime: %d", up), fmt.Sprintf("Last reload: %d", up))
	case "core restart gracefully":
		return cmdOutput(req) // the simulator keeps running
	case "module reload app_rpt.so", "module reload chan_simpleusb.so", "module reload chan_usbradio.so",
		"module reload chan_echolink.so", "module reload res_rpt_http_registrations.so",
		"iax2 reload", "dialplan reload", "manager reload", "logger reload":
		return cmdOutput(req) // config reloads (configsvc) succeed silently
	case "core show version":
		return cmdOutput(req, "Asterisk "+Version+" built by demo @ aslcp on a simulated host running Linux")
	}
	if out, ok := s.susbCommand(cmd); ok {
		return cmdOutput(req, out...)
	}
	m := reIlink.FindStringSubmatch(cmd)
	if m == nil {
		return cmdOutput(req, "No such command '"+cmd+"' (type 'core show help "+cmd+"' for other possible commands)")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.nodes[m[1]]
	if n == nil {
		return cmdOutput(req, "Unknown node number "+m[1])
	}
	code, _ := strconv.Atoi(m[2])
	target := m[3]
	perm := code > 10
	if perm {
		code -= 10
	}
	switch code {
	case 1, 2, 3, 6, 8:
	default:
		return cmdOutput(req, "Unknown action")
	}
	if n.slotBusy {
		return cmdOutput(req) // refused silently, like app_rpt
	}
	n.slotBusy = true
	time.AfterFunc(demoCmdDelay, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		n.slotBusy = false
		switch code {
		case 1:
			n.removeLink(target)
		case 6:
			n.links = nil
		case 2, 3, 8:
			mode := map[int]byte{2: 'R', 3: 'T', 8: 'L'}[code]
			n.removeLink(target) // reconnecting replaces
			s.addLink(n.id, target, mode, true, "", time.Now())
		}
	})
	return cmdOutput(req)
}

func (n *nodeState) removeLink(target string) {
	out := n.links[:0]
	for _, l := range n.links {
		if l.node != target {
			out = append(out, l)
		}
	}
	n.links = out
}

// run advances the simulation once per second: links key and unkey, and the
// local receiver is occasionally keyed. Changes are also pushed as events.
func (s *Sim) run(ctx context.Context) {
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			s.step(now)
		}
	}
}

func (s *Sim) step(now time.Time) {
	var events []string
	s.mu.Lock()
	// Only one node transmits at a time (two keyed at once would be a
	// crosslink fault), so a link may key only while no link is keyed on
	// any local node. This also keeps the Now Calling talker unique.
	busy := false
	for _, id := range LocalNodes {
		for _, l := range s.nodes[id].links {
			if l.keyed {
				busy = true
			}
		}
	}
	for _, id := range LocalNodes {
		n := s.nodes[id]
		linkChanged := false
		anyKeyed := false
		for _, l := range n.links {
			if !l.established(now) || l.mode == 'L' {
				continue
			}
			if l.mode == 'T' && now.After(l.nextFlip) {
				if l.keyed {
					l.keyed, l.lastUnkey = false, now
					busy, linkChanged = false, true
					l.nextFlip = now.Add(time.Duration(3+s.rng.IntN(6)) * time.Second)
				} else if !busy {
					busy, linkChanged = true, true
					l.keyed, l.lastKey = true, now
					l.nextFlip = now.Add(time.Duration(2+s.rng.IntN(5)) * time.Second)
				}
			}
			if l.keyed {
				anyKeyed = true
			}
		}
		// local receiver (COS) now and then
		rx := n.rx
		if n.rx && now.After(n.rxUntil) {
			rx = false
			n.nextRx = now.Add(time.Duration(10+s.rng.IntN(20)) * time.Second)
		} else if !n.rx && len(n.links) > 0 && now.After(n.nextRx) {
			rx = true
			n.rxUntil = now.Add(time.Duration(2+s.rng.IntN(4)) * time.Second)
		}
		tx := anyKeyed || rx
		if rx != n.rx {
			n.rx = rx
			events = append(events, event("RPT_RXKEYED", id, b01(rx)))
		}
		if tx != n.tx {
			n.tx = tx
			events = append(events, event("RPT_TXKEYED", id, b01(tx)))
		}
		if linkChanged {
			var al []string
			for _, l := range n.links {
				if l.established(now) && l.mode != 'L' {
					k := "U"
					if l.keyed {
						k = "K"
					}
					al = append(al, l.node+string(l.mode)+k)
				}
			}
			events = append(events, event("RPT_ALINKS", id, fmt.Sprintf("%d,%s", len(al), strings.Join(al, ","))))
		}
	}
	var targets []*session
	for ss := range s.sessions {
		if ss.authed && ss.events {
			targets = append(targets, ss)
		}
	}
	s.mu.Unlock()
	for _, ss := range targets {
		for _, e := range events {
			ss.write(e)
		}
	}
}

func event(name, node, value string) string {
	return fmt.Sprintf("Event: %s\r\nPrivilege: call,all\r\nNode: %s\r\nChannel: SimpleUSB/%s\r\nEventValue: %s\r\n\r\n", name, node, node, value)
}
