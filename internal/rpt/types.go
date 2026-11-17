package rpt

import "time"

// LinkMode is the one-letter link mode app_rpt reports.
type LinkMode byte

const (
	ModeTransceive   LinkMode = 'T' // two-way
	ModeMonitor      LinkMode = 'R' // receive only (we monitor them)
	ModeLocalMonitor LinkMode = 'L' // local monitor (not reported to the network)
	ModeConnecting   LinkMode = 'C' // not yet established
)

func (m LinkMode) String() string {
	if m == 0 {
		return ""
	}
	return string(rune(m))
}

// Name returns a human readable mode name.
func (m LinkMode) Name() string {
	switch m {
	case ModeTransceive:
		return "transceive"
	case ModeMonitor:
		return "monitor"
	case ModeLocalMonitor:
		return "local-monitor"
	case ModeConnecting:
		return "connecting"
	}
	return "unknown"
}

// Direction of a link relative to the local node.
type Direction string

const (
	DirIn  Direction = "IN"
	DirOut Direction = "OUT"
)

// Link states from the XStat Conn table.
const (
	StateEstablished = "ESTABLISHED"
	StateConnecting  = "CONNECTING"
)

// Conn is one row of the XStat connection table.
type Conn struct {
	Node       string
	Kind       NodeKind
	IP         string // may be empty (outbound/non-IAX/unknown)
	Reconnects int
	Direction  Direction
	Connected  time.Duration // time since the link was connected
	State      string        // ESTABLISHED or CONNECTING
}

// LinkedNode is a node in the network as seen from this node (the
// LinkedNodes line and RPT_LINKS), including indirectly linked nodes.
type LinkedNode struct {
	Node string
	Kind NodeKind
	Mode LinkMode
}

// ALink is an adjacent link from RPT_ALINKS.
type ALink struct {
	Node  string
	Kind  NodeKind
	Mode  LinkMode
	Keyed bool
}

// SawConn is one SawStat row.
type SawConn struct {
	Node       string
	Keyed      bool
	SinceKey   *time.Duration // nil if never keyed
	SinceUnkey *time.Duration // nil if never unkeyed
}

// TotState is the transmit time-out timer state (tot_state).
type TotState int

const (
	TotTimedOut TotState = 0
	TotArmed    TotState = 1
	TotReset    TotState = 2
)

func (s TotState) String() string {
	switch s {
	case TotTimedOut:
		return "timed-out"
	case TotArmed:
		return "armed"
	case TotReset:
		return "reset"
	}
	return "unknown"
}

// IderState is the identification state (ider_state).
type IderState int

const (
	IderQueuedInTail IderState = 0
	IderQueuedClean  IderState = 1 // queued for cleanup
	IderClean        IderState = 2
)

func (s IderState) String() string {
	switch s {
	case IderQueuedInTail:
		return "queued-in-tail"
	case IderQueuedClean:
		return "queued-for-cleanup"
	case IderClean:
		return "clean"
	}
	return "unknown"
}

// PatchState is the autopatch call state (patch_state).
type PatchState int

const (
	PatchDialing    PatchState = 0
	PatchConnecting PatchState = 1
	PatchUp         PatchState = 2
	PatchFailed     PatchState = 3
	PatchDown       PatchState = 4
)

func (s PatchState) String() string {
	switch s {
	case PatchDialing:
		return "dialing"
	case PatchConnecting:
		return "connecting"
	case PatchUp:
		return "up"
	case PatchFailed:
		return "failed"
	case PatchDown:
		return "down"
	}
	return "unknown"
}

// Flags are the trailing state lines of XStat.
//
// Naming follows rpt_manager.c, not the wire names: sys_ena, tot_ena,
// link_ena, patch_ena, sch_ena and user_funs are actually "disabled" flags
// (1 means the feature is DISABLED), while parrot_ena really is "enabled".
type Flags struct {
	Valid             bool // true when the trailing lines were present
	ParrotEnabled     bool // parrot_ena: parrot mode != off
	TxDisabled        bool // sys_ena: sysstate txdisable
	TotDisabled       bool // tot_ena: sysstate totdisable
	LinkFunsDisabled  bool // link_ena: sysstate linkfundisable
	AutopatchDisabled bool // patch_ena: sysstate autopatchdisable
	SchedulerDisabled bool // sch_ena: sysstate schedulerdisable
	UserFunsDisabled  bool // user_funs: sysstate userfundisable
	AlternateTail     bool // tail_type: sysstate alternatetail
	NoIncomingConns   bool // iconns: sysstate noincomingconns
	TotState          TotState
	IderState         IderState
	PatchState        PatchState
	// TelMode: 0 telemetry off, 1 on, 2 partial, 3 not dynamic (fixed
	// by configuration).
	TelMode int
}

// XStat is the parsed result of RptStatus/XStat.
type XStat struct {
	Node  string
	Conns []Conn
	// Network is the LinkedNodes line: adjacent and indirectly linked nodes.
	// Local-monitor links are not reported here by app_rpt.
	Network []LinkedNode
	// Vars are the "Var: KEY=VALUE" lines of the node's RX channel.
	Vars map[string]string
	// Typed views of well-known variables.
	RxKeyed    bool // RPT_RXKEYED
	TxKeyed    bool // RPT_TXKEYED
	ExtTxKeyed bool // RPT_ETXKEYED (Allmon3 looks for TXEKEYED, which never exists)
	NumLinks   int  // RPT_NUMLINKS
	Links      []LinkedNode
	NumALinks  int // RPT_NUMALINKS
	ALinks     []ALink
	// State holds the trailing state lines by wire name (parrot_ena, ...).
	State map[string]string
	Flags Flags
}

// Link is one connected link with merged XStat and SawStat information.
type Link struct {
	Node       string
	Kind       NodeKind
	IP         string
	Direction  Direction
	Reconnects int
	Connected  time.Duration
	State      string
	Mode       LinkMode
	Keyed      bool
	SinceKey   *time.Duration // nil if never keyed (or unknown)
	SinceUnkey *time.Duration // nil if never unkeyed (or unknown)
}

// NodeStatus is the merged live status of one local node.
type NodeStatus struct {
	Node       string
	RxKeyed    bool
	TxKeyed    bool
	ExtTxKeyed bool
	Links      []Link
	Network    []LinkedNode
	NumLinks   int
	NumALinks  int
	Flags      Flags
	// Updated is when the data was last refreshed from the node by a poll.
	Updated time.Time
	// Stale is set by Monitor when the data cannot be trusted (AMI
	// disconnected, poll failing, or not yet polled).
	Stale bool
	// Err is the last poll error when Stale (empty otherwise).
	Err string
}

// Clone returns a deep copy.
func (s NodeStatus) Clone() NodeStatus {
	c := s
	c.Links = make([]Link, len(s.Links))
	for i, l := range s.Links {
		if l.SinceKey != nil {
			v := *l.SinceKey
			l.SinceKey = &v
		}
		if l.SinceUnkey != nil {
			v := *l.SinceUnkey
			l.SinceUnkey = &v
		}
		c.Links[i] = l
	}
	c.Network = append([]LinkedNode(nil), s.Network...)
	return c
}
