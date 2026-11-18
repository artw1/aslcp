package rpt

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/artw1/aslcp/internal/ami"
)

// API is the subset of *ami.Conn / *ami.Client that rpt needs.
type API interface {
	Action(ctx context.Context, name string, headers ...ami.Header) (*ami.Message, error)
	Command(ctx context.Context, cmd string) ([]string, error)
}

// Client issues app_rpt queries and control commands over an AMI API.
type Client struct {
	api API

	// MinGap is the minimum time between "rpt cmd" commands to the same
	// local node. app_rpt has a single command slot per node: a command
	// that arrives before the previous one has been picked up by the
	// node's main loop is refused silently, so back-to-back commands
	// (disconnect then connect) would lose the second one.
	MinGap time.Duration

	pmu  sync.Mutex
	pace map[string]*nodePace
}

type nodePace struct {
	mu   sync.Mutex // held while waiting for and sending a command
	last time.Time
}

// DefaultMinGap is the default spacing between commands to one node.
const DefaultMinGap = 500 * time.Millisecond

// New wraps an AMI connection or client.
func New(api API) *Client { return &Client{api: api, MinGap: DefaultMinGap} }

// paced runs fn for node with at least MinGap since the previous command to
// that node; commands to one node are serialized.
func (c *Client) paced(ctx context.Context, node string, fn func() error) error {
	c.pmu.Lock()
	if c.pace == nil {
		c.pace = map[string]*nodePace{}
	}
	p := c.pace[node]
	if p == nil {
		p = &nodePace{}
		c.pace[node] = p
	}
	c.pmu.Unlock()

	p.mu.Lock()
	defer p.mu.Unlock()
	if wait := time.Until(p.last.Add(c.MinGap)); wait > 0 {
		t := time.NewTimer(wait)
		select {
		case <-t.C:
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		}
	}
	err := fn()
	p.last = time.Now()
	return err
}

var reNode = regexp.MustCompile(`^[0-9]{1,10}$`)

func checkNode(n string) error {
	if !reNode.MatchString(n) {
		return fmt.Errorf("rpt: invalid node %q", n)
	}
	return nil
}

// LocalNodes lists the locally configured nodes via RptLocalNodes (XML),
// falling back to RptStatus/RptStat if that action is unavailable or its
// reply cannot be parsed.
func (c *Client) LocalNodes(ctx context.Context) ([]string, error) {
	m, err := c.api.Action(ctx, "RptLocalNodes")
	if err == nil {
		if nodes, perr := ParseLocalNodesXML(m.Body); perr == nil {
			return nodes, nil
		}
	} else if ctx.Err() != nil || errors.Is(err, ami.ErrClosed) || errors.Is(err, ami.ErrNotConnected) {
		return nil, err
	}
	m, err = c.api.Action(ctx, "RptStatus", ami.H("Command", "RptStat"))
	if err != nil {
		return nil, err
	}
	return ParseRptStat(m)
}

// XStat runs RptStatus/XStat for a local node.
func (c *Client) XStat(ctx context.Context, node string) (*XStat, error) {
	if err := checkNode(node); err != nil {
		return nil, err
	}
	m, err := c.api.Action(ctx, "RptStatus", ami.H("Command", "XStat"), ami.H("Node", node))
	if err != nil {
		return nil, err
	}
	x, err := ParseXStat(m)
	if err != nil {
		return nil, err
	}
	if x.Node == "" {
		x.Node = node
	}
	return x, nil
}

// SawStat runs RptStatus/SawStat for a local node.
func (c *Client) SawStat(ctx context.Context, node string) ([]SawConn, error) {
	if err := checkNode(node); err != nil {
		return nil, err
	}
	m, err := c.api.Action(ctx, "RptStatus", ami.H("Command", "SawStat"), ami.H("Node", node))
	if err != nil {
		return nil, err
	}
	return ParseSawStat(m)
}

// NodeStatus returns XStat merged with SawStat. Any failure of either query
// is returned as an error.
func (c *Client) NodeStatus(ctx context.Context, node string) (NodeStatus, error) {
	x, err := c.XStat(ctx, node)
	if err != nil {
		return NodeStatus{}, err
	}
	saw, err := c.SawStat(ctx, node)
	if err != nil {
		return NodeStatus{}, err
	}
	return Merge(node, x, saw, time.Now()), nil
}

// Uptime returns Asterisk uptime via "core show uptime seconds".
func (c *Client) Uptime(ctx context.Context) (Uptime, error) {
	lines, err := c.api.Command(ctx, "core show uptime seconds")
	if err != nil {
		return Uptime{}, err
	}
	return ParseUptime(lines)
}

// Link control -------------------------------------------------------------

// ConnectMode selects how Connect links to a target node.
type ConnectMode int

const (
	ConnTransceive ConnectMode = iota
	ConnMonitor
	ConnLocalMonitor
)

func (m ConnectMode) String() string {
	switch m {
	case ConnTransceive:
		return "transceive"
	case ConnMonitor:
		return "monitor"
	case ConnLocalMonitor:
		return "local-monitor"
	}
	return "invalid"
}

// ilink function codes from rpt_functions.c function_ilink(); codes above 10
// are the permanent variants (perma = code > 10).
const (
	ilinkDisconnect     = 1
	ilinkMonitor        = 2
	ilinkTransceive     = 3
	ilinkDisconnectAll  = 6 // all links off including permanent ones
	ilinkLocalMonitor   = 8
	ilinkPermDisconnect = 11 // 12, 13 and 18 are the permanent connect variants
	permanentOffset     = 10
)

// connectCode returns the ilink code for a mode.
func connectCode(m ConnectMode, permanent bool) (int, error) {
	var code int
	switch m {
	case ConnTransceive:
		code = ilinkTransceive
	case ConnMonitor:
		code = ilinkMonitor
	case ConnLocalMonitor:
		code = ilinkLocalMonitor
	default:
		return 0, fmt.Errorf("rpt: invalid mode %d", int(m))
	}
	if permanent {
		code += permanentOffset
	}
	return code, nil
}

var (
	// Targets to connect to: AllStar/EchoLink numbers. A leading 0 is
	// refused because app_rpt treats it as "the last linked node".
	reConnectTarget = regexp.MustCompile(`^[1-9][0-9]{0,9}$`)
	// Targets to disconnect: also direct clients and phone portals,
	// e.g. "N0CALL", "W1AW-P". Must start with an alphanumeric, and not
	// with 0 (same "last node" rule).
	reDisconnectTarget = regexp.MustCompile(`^[A-Za-z1-9][A-Za-z0-9-]{0,19}$`)
)

// rptCmdFailures are substrings (lowercase) of CLI output that mean the
// "rpt cmd" was not accepted (rpt_cli.c).
var rptCmdFailures = []string{
	"unknown node", "is not ready", "unknown action", "usage:", "no such command",
}

func (c *Client) ilink(ctx context.Context, node string, args ...any) error {
	cmd := "rpt cmd " + node + " ilink"
	for _, a := range args {
		cmd += fmt.Sprintf(" %v", a)
	}
	var lines []string
	err := c.paced(ctx, node, func() (err error) {
		lines, err = c.api.Command(ctx, cmd)
		return err
	})
	if err != nil {
		return err
	}
	for _, l := range lines {
		ll := strings.ToLower(l)
		for _, f := range rptCmdFailures {
			if strings.Contains(ll, f) {
				return fmt.Errorf("rpt: %s", strings.TrimSpace(l))
			}
		}
	}
	return nil
}

// Connect links local node `node` to `target` in the given mode. permanent
// links are retried by app_rpt until disconnected with a permanent
// disconnect. The command is queued asynchronously inside app_rpt, so a nil
// error means it was accepted, not that the link came up; watch the status.
func (c *Client) Connect(ctx context.Context, node, target string, mode ConnectMode, permanent bool) error {
	if err := checkNode(node); err != nil {
		return err
	}
	if !reConnectTarget.MatchString(target) {
		return fmt.Errorf("rpt: invalid connect target %q", target)
	}
	if node == target {
		return errors.New("rpt: cannot connect a node to itself")
	}
	code, err := connectCode(mode, permanent)
	if err != nil {
		return err
	}
	return c.ilink(ctx, node, code, target)
}

// Disconnect drops the link from `node` to `target`. app_rpt silently
// ignores a non-permanent disconnect of a permanent link, so pass
// permanent=true for those.
func (c *Client) Disconnect(ctx context.Context, node, target string, permanent bool) error {
	if err := checkNode(node); err != nil {
		return err
	}
	if !reDisconnectTarget.MatchString(target) {
		return fmt.Errorf("rpt: invalid disconnect target %q", target)
	}
	code := ilinkDisconnect
	if permanent {
		code = ilinkPermDisconnect
	}
	return c.ilink(ctx, node, code, target)
}

// DisconnectAll drops every link of the node, permanent ones included
// (ilink 6, which takes no target).
func (c *Client) DisconnectAll(ctx context.Context, node string) error {
	if err := checkNode(node); err != nil {
		return err
	}
	return c.ilink(ctx, node, ilinkDisconnectAll)
}

// StatField is one "Key: Value" line of a NodeStat reply.
type StatField struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// NodeStat runs RptStatus/NodeStat for a local node and returns the reply's
// key/value lines in wire order (Response and ActionID are dropped).
func (c *Client) NodeStat(ctx context.Context, node string) ([]StatField, error) {
	if err := checkNode(node); err != nil {
		return nil, err
	}
	m, err := c.api.Action(ctx, "RptStatus", ami.H("Command", "NodeStat"), ami.H("Node", node))
	if err != nil {
		return nil, err
	}
	return ParseNodeStat(m), nil
}

// ParseNodeStat converts a NodeStat reply into an ordered field list. The
// Response, ActionID and Node/Message bookkeeping headers are skipped.
func ParseNodeStat(m *ami.Message) []StatField {
	out := []StatField{}
	for _, f := range m.Fields {
		switch strings.ToLower(f.Key) {
		case "response", "actionid", "node", "message":
			continue
		}
		out = append(out, StatField{Key: f.Key, Value: f.Value})
	}
	return out
}

// RestartAsterisk asks Asterisk to restart once all calls end ("core restart
// gracefully"). The AMI connection drops when it does; links are lost.
//
// An idle Asterisk restarts immediately and closes the AMI connection before
// replying, so a connection lost after the command was written means the
// restart started and is reported as success. Errors before the command was
// written (not connected, write failure) are still returned.
func (c *Client) RestartAsterisk(ctx context.Context) error {
	_, err := c.api.Command(ctx, "core restart gracefully")
	if err != nil && errors.Is(err, ami.ErrLostAfterSend) {
		return nil
	}
	return err
}
