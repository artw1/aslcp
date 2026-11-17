package rpt

import (
	"strconv"
	"strings"
	"time"

	"github.com/artw1/aslcp/internal/ami"
)

// EventKind identifies an app_rpt manager event.
type EventKind int

const (
	EventOther EventKind = iota // other RPT_* (or MDC-1200 etc.) event
	EventRxKeyed
	EventTxKeyed
	EventExtTxKeyed
	EventLinks
	EventNumLinks
	EventALinks
	EventNumALinks
	EventAutopatchUp
)

// Event names (the Event header equals the channel variable name).
const (
	EvRxKeyed     = "RPT_RXKEYED"
	EvTxKeyed     = "RPT_TXKEYED"
	EvExtTxKeyed  = "RPT_ETXKEYED"
	EvLinks       = "RPT_LINKS"
	EvNumLinks    = "RPT_NUMLINKS"
	EvALinks      = "RPT_ALINKS"
	EvNumALinks   = "RPT_NUMALINKS"
	EvAutopatchUp = "RPT_AUTOPATCHUP"
)

var eventKinds = map[string]EventKind{
	EvRxKeyed: EventRxKeyed, EvTxKeyed: EventTxKeyed, EvExtTxKeyed: EventExtTxKeyed,
	EvLinks: EventLinks, EvNumLinks: EventNumLinks, EvALinks: EventALinks,
	EvNumALinks: EventNumALinks, EvAutopatchUp: EventAutopatchUp,
}

// Event is a typed app_rpt event, as sent by rpt_manager_trigger():
//
//	Event: RPT_RXKEYED
//	Node: 1999
//	Channel: SimpleUSB/usb
//	EventValue: 1
//	LastKeyedTime: Tue Nov 11 10:00:00 2025
//	LastTxKeyedTime: Thu Jan  1 00:00:00 1970
type Event struct {
	Name    string
	Kind    EventKind
	Node    string // local node the event is about
	Channel string
	Value   string // raw EventValue
	// LastKeyed and LastTxKeyed are zero when never keyed or unparsable.
	LastKeyed   time.Time
	LastTxKeyed time.Time

	// Typed payloads, valid for the matching Kind.
	Keyed  bool         // RxKeyed, TxKeyed, ExtTxKeyed, AutopatchUp
	Count  int          // NumLinks, NumALinks
	Links  []LinkedNode // EventLinks
	ALinks []ALink      // EventALinks
	// Err is set when the typed payload for a known Kind could not be parsed.
	Err error

	Msg *ami.Message
}

func parseCtime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	t, err := time.ParseInLocation(time.ANSIC, s, time.Local)
	if err != nil || t.Unix() <= 0 {
		return time.Time{}
	}
	return t
}

// ParseEvent translates a manager event into an Event. ok is false when the
// message is not an app_rpt trigger event (it needs Event, Node and
// EventValue headers). Unknown event names are returned with Kind
// EventOther.
func ParseEvent(m *ami.Message) (Event, bool) {
	name := m.Event()
	if name == "" || !m.Has("Node") || !m.Has("EventValue") {
		return Event{}, false
	}
	e := Event{
		Name:        name,
		Kind:        eventKinds[name],
		Node:        strings.TrimSpace(m.Get("Node")),
		Channel:     m.Get("Channel"),
		Value:       strings.TrimSpace(m.Get("EventValue")),
		LastKeyed:   parseCtime(m.Get("LastKeyedTime")),
		LastTxKeyed: parseCtime(m.Get("LastTxKeyedTime")),
		Msg:         m,
	}
	switch e.Kind {
	case EventRxKeyed, EventTxKeyed, EventExtTxKeyed, EventAutopatchUp:
		e.Keyed = e.Value == "1"
	case EventNumLinks, EventNumALinks:
		n, err := strconv.Atoi(e.Value)
		e.Count, e.Err = n, err
	case EventLinks:
		e.Count, e.Links = ParseLinks(e.Value)
	case EventALinks:
		e.Count, e.ALinks, e.Err = ParseALinks(e.Value)
	}
	return e, true
}
