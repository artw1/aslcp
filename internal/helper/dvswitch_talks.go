package helper

import (
	"bufio"
	"bytes"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Talks: every call heard through the DVSwitch bridge, with a start time and,
// where a log has one, an end. The "Now calling" strip folds them with the
// AllStar keyed state of the bridge link (internal/nowcalling). Sources:
//
//   - STFU.log (BrandMeister): "ODMR Begin Tx: src = <id>, dst = <tg> (GROUP)"
//     and "ODMR End Tx:DMR frame count was <n> frames". The end is the start
//     plus 60 ms per frame. The talker alias lines are not read.
//   - Analog_Bridge.log (DMR through MMDVM_Bridge, and YSF): "Begin TX:
//     src=<id> rpt=<id> dst=<n> slot=<n> cc=<n> call=<CALL>". There is no end
//     line; the bridge link's keyed state ends the call. A YSF caller is the
//     call= of the line, while the log's last "ambeMode = YSFN" says the
//     bridge is in YSF mode.
//
// Time zone: both logs write UTC, like every other log the helper reads (the
// DMR login status parses them as UTC); the talks carry RFC 3339 UTC times.

// Kinds of a talk.
const (
	DVTalkDMR = "dmr" // DMR: BrandMeister through STFU, or an MMDVM_Bridge network (TGIF, DMR - Other)
	DVTalkYSF = "ysf"
	DVTalkDV  = "dv" // another digital mode (P25, NXDN)
)

const (
	dvTalksMax     = 8
	dvTalkMsFrame  = 60 * time.Millisecond
	dvTalkDedupe   = 5 * time.Second
	dvTalkLayoutMs = "2006-01-02 15:04:05.000"
)

// DVTalk is one call. Via is "stfu" or "analog_bridge". End is empty when the
// log has no end line. Personal data: logged-in roles only.
type DVTalk struct {
	Kind    string `json:"kind"`
	Via     string `json:"via"`
	Call    string `json:"call,omitempty"`
	DMRID   string `json:"dmrId,omitempty"`
	Dst     string `json:"dst,omitempty"`
	Slot    int    `json:"slot,omitempty"`
	Private bool   `json:"private,omitempty"`
	Start   string `json:"start"`
	End     string `json:"end,omitempty"`
	Frames  int    `json:"frames,omitempty"`
}

var (
	reDVSTFUStamp = regexp.MustCompile(`^[A-Z][:0-9] ([0-9]{4}-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2}\.[0-9]{3}) `)
	reDVAmbeMode  = regexp.MustCompile(`ambeMode\s*=\s*([A-Za-z0-9]{2,8})`)
)

// parseSTFUTalks scans STFU.log text in order. Lines that name a password are
// dropped, as everywhere else. An End Tx closes the last open Begin; a Begin
// that follows a Begin closes the earlier one without an end.
func parseSTFUTalks(data []byte) []DVTalk {
	var out []DVTalk
	open := -1
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64<<10), dvLogMaxLine)
	for sc.Scan() {
		raw := sc.Text()
		line := dvSTFUClean(raw)
		if !dvSTFUSafe(raw) || !dvSTFUSafe(line) {
			continue
		}
		isBegin, isEnd := strings.Contains(line, "ODMR Begin Tx:"), strings.Contains(line, "ODMR End Tx")
		if !isBegin && !isEnd {
			continue
		}
		sm := reDVSTFUStamp.FindStringSubmatch(line)
		if sm == nil {
			continue
		}
		at, err := time.ParseInLocation(dvTalkLayoutMs, sm[1], time.UTC)
		if err != nil {
			continue
		}
		if isBegin {
			m := reSTFURxBegin.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			out = append(out, DVTalk{Kind: DVTalkDMR, Via: DVViaSTFU, DMRID: strings.TrimLeft(m[1], "0"), Dst: m[2],
				Private: m[3] != "GROUP", Start: at.Format(time.RFC3339Nano)})
			open = len(out) - 1
			continue
		}
		if open < 0 {
			continue
		}
		t := &out[open]
		start, _ := time.Parse(time.RFC3339Nano, t.Start)
		end := at
		if fm := reSTFUFrames.FindStringSubmatch(line); fm != nil {
			if n, _ := strconv.Atoi(fm[1]); n > 0 {
				t.Frames = n
				end = start.Add(time.Duration(n) * dvTalkMsFrame)
			}
		}
		if end.Before(start) {
			end = start
		}
		t.End = end.Format(time.RFC3339Nano)
		open = -1
	}
	if len(out) > dvTalksMax {
		out = out[len(out)-dvTalksMax:]
	}
	return out
}

// parseABTalks scans Analog_Bridge.log text in order. panel is the bridge's
// mode now ("YSF", ...), the fallback while the window holds no ambeMode line.
func parseABTalks(data []byte, panel string) []DVTalk {
	var out []DVTalk
	mode := ""
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64<<10), dvLogMaxLine)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if m := reDVAmbeMode.FindStringSubmatch(line); m != nil && !strings.Contains(line, "Begin TX") {
			mode = strings.ToUpper(m[1])
			continue
		}
		ev, ok := parseAnalogBridgeLine(line)
		if !ok || ev.End {
			continue
		}
		c := ev.Caller
		at, err := time.ParseInLocation("2006-01-02 15:04:05", c.At, time.UTC)
		if err != nil {
			continue
		}
		out = append(out, DVTalk{Kind: dvTalkKind(mode, panel), Via: "analog_bridge", Call: c.Call, DMRID: strings.TrimLeft(c.DMRID, "0"),
			Dst: c.Dst, Slot: c.Slot, Start: at.Format(time.RFC3339Nano)})
	}
	if len(out) > dvTalksMax {
		out = out[len(out)-dvTalksMax:]
	}
	return out
}

// dvTalkKind maps the bridge's mode (log: YSFN, DMR, P25D, NXDN; panel: YSF,
// a DMR network, P25, NXDN) to a kind. The log's ambeMode wins over the panel.
func dvTalkKind(mode, panel string) string {
	m := mode
	if m == "" {
		m = strings.ToUpper(panel)
	}
	switch {
	case strings.HasPrefix(m, "YSF"):
		return DVTalkYSF
	case m == "" || DVDMRFamily(m):
		return DVTalkDMR
	}
	return DVTalkDV
}

// mergeTalks combines the two sources, oldest first. An Analog_Bridge begin
// for a DMR caller that STFU also logged (same ID, within 5 s) is the same
// call: the STFU row has the end, so it wins.
func mergeTalks(stfu, ab []DVTalk) []DVTalk {
	all := append([]DVTalk(nil), stfu...)
	for _, a := range ab {
		dup := false
		if a.Kind == DVTalkDMR {
			at, _ := time.Parse(time.RFC3339Nano, a.Start)
			for _, s := range stfu {
				st, _ := time.Parse(time.RFC3339Nano, s.Start)
				if s.DMRID == a.DMRID && at.Sub(st) < dvTalkDedupe && st.Sub(at) < dvTalkDedupe {
					dup = true
					break
				}
			}
		}
		if !dup {
			all = append(all, a)
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		a, _ := time.Parse(time.RFC3339Nano, all[i].Start)
		b, _ := time.Parse(time.RFC3339Nano, all[j].Start)
		return a.Before(b)
	})
	if len(all) > dvTalksMax {
		all = all[len(all)-dvTalksMax:]
	}
	return all
}

// dvCollectTalks reads both logs and resolves callsigns that the log did not
// carry from the DMR ID list.
func (o *Ops) dvCollectTalks(panel, gatewayID string) []DVTalk {
	var stfu, ab []DVTalk
	if data, ok := dvReadTail(o.dvSTFULogPath()); ok {
		stfu = parseSTFUTalks(data)
	}
	if data, ok := dvReadTail(analogBridgeLogPath(o)); ok {
		ab = parseABTalks(data, panel)
	}
	talks := mergeTalks(stfu, ab)
	var need []string
	seen := map[string]bool{}
	for i := range talks {
		t := &talks[i]
		if t.DMRID == DVParrotID || (gatewayID != "" && t.Dst == gatewayID) {
			t.Private = true
		}
		if t.Call == "" && t.DMRID != "" && !seen[t.DMRID] && !t.Private {
			seen[t.DMRID] = true
			need = append(need, t.DMRID)
		}
	}
	if len(need) > 0 {
		calls := o.dvLookupCalls(need)
		for i := range talks {
			if talks[i].Call == "" {
				talks[i].Call = calls[talks[i].DMRID]
			}
		}
	}
	return talks
}

func analogBridgeLogPath(o *Ops) string {
	return analogBridgeCallers.Files(o)[0]
}
