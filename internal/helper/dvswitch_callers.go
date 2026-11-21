package helper

import (
	"bufio"
	"bytes"
	"context"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Caller callsign (docs/DVSWITCH.md, "Caller callsign"). Who is talking on
// the bridge comes from log lines the helper reads as root, bounded like the
// DMR login status (the tail of one file, O_NOFOLLOW, regular files only).
// The source is pluggable: a DVCallerSource names the files and parses one
// line into an event, so another log can be added to dvCallerSources without
// touching the rest.

const (
	// DefaultDVBridgeLogDir is AnalogBridgeLogDir of the analog_bridge unit.
	DefaultDVBridgeLogDir = "/var/log/dvswitch"
	dvCallersRecent       = 5
	// dvCallersMaxEvents bounds the events kept from one tail read.
	dvCallersMaxEvents = 512
)

// DVCaller is one talker: the callsign (from the log's call=, else from the
// DMR ID list; empty when neither knows it, the panel then shows the ID), the
// DMR ID when the line has one, Private for a private call to this node (the
// BM Parrot's reply, say: src=9990, dst our own ID), the destination (talkgroup), the slot (0: unknown)
// and the time as written in the log ("2006-01-02 15:04:05", no zone: the
// log does not say which one it uses).
type DVCaller struct {
	Call    string `json:"call,omitempty"`
	Private bool   `json:"private,omitempty"`
	DMRID   string `json:"dmrId,omitempty"`
	Dst     string `json:"dst,omitempty"`
	Slot    int    `json:"slot,omitempty"`
	At      string `json:"at,omitempty"`
}

// DVCallers is what the info op adds: Current is the caller of the call that
// has begun and not ended in the log (nil: none), Recent the last few distinct
// callers, newest first. Personal data: only for logged-in roles.
type DVCallers struct {
	Current *DVCaller  `json:"current,omitempty"`
	Recent  []DVCaller `json:"recent"`
	// Talks are the calls of both logs with start and end times, oldest
	// first (dvswitch_talks.go).
	Talks []DVTalk `json:"talks,omitempty"`
}

// DVCallerEvent is one parsed line: a call begins (Caller set) or ends.
type DVCallerEvent struct {
	End    bool
	Caller DVCaller
}

// DVCallerSource is one place callers come from. Files returns the log files
// to read, oldest first (at most two are read); Parse turns one line into an
// event (ok false: not a caller line).
type DVCallerSource struct {
	Files func(o *Ops) []string
	Parse func(line string) (DVCallerEvent, bool)
}

// dvCallerSources are the sources the info op reads. The Analog_Bridge log is
// the only one for now; another source is added to this list.
var dvCallerSources = []DVCallerSource{analogBridgeCallers}

var analogBridgeCallers = DVCallerSource{
	Files: func(o *Ops) []string {
		return []string{filepath.Join(orDefault(o.DVBridgeLogDir, DefaultDVBridgeLogDir), "Analog_Bridge.log")}
	},
	Parse: parseAnalogBridgeLine,
}

var (
	reDVABStamp = regexp.MustCompile(`^(?:[A-Z]: )?([0-9]{4}-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2})`)
	// I: 2026-11-18 18:51:48.337 Begin TX: src=1234567 rpt=123456701 dst=3100 slot=1 cc=1 call=
	// call= is empty when the call came through STFU (MMDVM_Bridge used to fill it).
	reDVABBegin = regexp.MustCompile(`Begin TX: src=([0-9]{1,9}) rpt=[0-9]{1,9} dst=([0-9]{1,10}) slot=([0-9]) cc=[0-9]{1,3} call=([A-Za-z0-9/-]{0,12})\s*$`)
)

func parseAnalogBridgeLine(line string) (DVCallerEvent, bool) {
	at := ""
	if m := reDVABStamp.FindStringSubmatch(line); m != nil {
		at = m[1]
	}
	if m := reDVABBegin.FindStringSubmatch(line); m != nil {
		slot, _ := strconv.Atoi(m[3])
		return DVCallerEvent{Caller: DVCaller{Call: m[4], DMRID: m[1], Dst: m[2], Slot: slot, At: at}}, true
	}
	if strings.Contains(line, "Begin TX:") {
		return DVCallerEvent{}, false // a Begin line in a shape we do not know
	}
	// Analog_Bridge logs no end line for received traffic: the panel clears
	// the caller with the RX unkey.
	return DVCallerEvent{}, false
}

// dvCollectCallers reads the sources and folds the events: the last Begin
// without a later end is current; recent holds the last distinct callers.
func (o *Ops) dvCollectCallers(gatewayID string) DVCallers {
	var evs []DVCallerEvent
	for _, src := range dvCallerSources {
		files := src.Files(o)
		if len(files) > 2 {
			files = files[len(files)-2:]
		}
		for _, p := range files {
			data, ok := dvReadTail(p)
			if !ok {
				continue
			}
			evs = append(evs, dvParseCallerLines(data, src.Parse)...)
		}
	}
	dvResolveCallers(o, evs, gatewayID)
	return foldCallers(evs)
}

// dvResolveCallers fills each Begin event in place: a call to this node (dst
// is the gateway ID, or the talker is the Parrot) is Private and has no
// callsign; an empty call= is looked up by DMR ID in the DMR ID list.
func dvResolveCallers(o *Ops, evs []DVCallerEvent, gatewayID string) {
	var need []string
	seen := map[string]bool{}
	for i := range evs {
		c := &evs[i].Caller
		if evs[i].End {
			continue
		}
		if c.DMRID == DVParrotID || (gatewayID != "" && c.Dst == gatewayID) {
			c.Private = true
			continue
		}
		if c.Call == "" && c.DMRID != "" && !seen[c.DMRID] {
			seen[c.DMRID] = true
			need = append(need, c.DMRID)
		}
	}
	if len(need) == 0 {
		return
	}
	calls := o.dvLookupCalls(need)
	for i := range evs {
		if c := &evs[i].Caller; !evs[i].End && c.Call == "" {
			c.Call = calls[c.DMRID]
		}
	}
}

// dvParseCallerLines scans log text in order (a line over dvLogMaxLine bytes
// ends the scan) and keeps the last dvCallersMaxEvents events.
func dvParseCallerLines(data []byte, parse func(string) (DVCallerEvent, bool)) []DVCallerEvent {
	var evs []DVCallerEvent
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64<<10), dvLogMaxLine)
	for sc.Scan() {
		if ev, ok := parse(strings.TrimRight(sc.Text(), "\r")); ok {
			evs = append(evs, ev)
			if len(evs) > dvCallersMaxEvents {
				evs = evs[1:]
			}
		}
	}
	return evs
}

func foldCallers(evs []DVCallerEvent) DVCallers {
	res := DVCallers{Recent: []DVCaller{}}
	var cur *DVCaller
	for _, ev := range evs {
		if ev.End {
			cur = nil
			continue
		}
		c := ev.Caller
		cur = &c
		// one call can log its begin more than once: keep one row
		if n := len(res.Recent); n > 0 && res.Recent[n-1].Call == c.Call && res.Recent[n-1].DMRID == c.DMRID && res.Recent[n-1].Dst == c.Dst && res.Recent[n-1].Slot == c.Slot {
			res.Recent[n-1] = c
			continue
		}
		res.Recent = append(res.Recent, c)
	}
	res.Current = cur
	if len(res.Recent) > dvCallersRecent {
		res.Recent = res.Recent[len(res.Recent)-dvCallersRecent:]
	}
	for i, j := 0, len(res.Recent)-1; i < j; i, j = i+1, j-1 {
		res.Recent[i], res.Recent[j] = res.Recent[j], res.Recent[i]
	}
	return res
}

// dvInfoOp is the dvswitch-info op: the live state of Analog_Bridge plus the
// callers. DVSwitchInfo alone stays cheap (the tune sequence polls it).
func (o *Ops) dvInfoOp(ctx context.Context) (DVSwitchInfo, error) {
	info, err := o.DVSwitchInfo(ctx)
	if err == nil && (info.Found || info.Started != "") {
		c := o.dvCollectCallers(info.GatewayID)
		c.Talks = o.dvCollectTalks(info.Panel, info.GatewayID)
		info.Callers = &c
	}
	return info, err
}
