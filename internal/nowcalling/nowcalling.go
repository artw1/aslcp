// Package nowcalling works out who is talking right now (AllStar, DMR or
// YSF) for the dashboard's "Now calling" strip, and keeps the last few
// talkers. State is in memory on the server, so a page reload shows the same
// thing; a restart of ASLCP starts with what the DVSwitch logs still hold.
package nowcalling

import (
	"sort"
	"sync"
	"time"
)

// Kinds of a call.
const (
	KindAllStar = "allstar"
	KindDMR     = "dmr"
	KindYSF     = "ysf"
)

const (
	// RecentMax is how many finished calls are kept.
	RecentMax = 10
	// MinDuration: shorter AllStar keyups are glitches, not recorded (the
	// same limit as the activity recorder).
	MinDuration = 500 * time.Millisecond
	// noBridgeGrace is how long a DVSwitch call without an end line may wait
	// for the bridge link to key (the log line can come first) before it is
	// recorded as a finished call of unknown length.
	noBridgeGrace = 6 * time.Second
	// maxOpen is how long a DVSwitch call without an end line may stay
	// "talking" on the bridge link's keyed state alone.
	maxOpen = 15 * time.Minute
	// dvHangover is how long an open DVSwitch call without an end line is
	// held open after the bridge link unkeys. A choppy YSF or DMR stream
	// makes Analog_Bridge stop sending audio for a moment, ASL unkeys the
	// link, and when audio resumes there is no new "Begin TX" line: without
	// the hangover the call would end and the strip stay empty.
	dvHangover = 5 * time.Second
	// bridgeLag is how long an STFU call may wait for the bridge link to key.
	bridgeLag = 5 * time.Second
	// maxSinceKey bounds the "keyed for" age of a link taken as the start.
	maxSinceKey = 2 * time.Hour
)

// Call is one talker. Fields are filled by the source and then by Resolve
// (name and location from the radioid.net list or the node list).
type Call struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// Call is the callsign ("" when only an ID is known).
	Call string `json:"call,omitempty"`
	// First is the first name, Name the full name.
	First   string `json:"first,omitempty"`
	Name    string `json:"name,omitempty"`
	City    string `json:"city,omitempty"`
	State   string `json:"state,omitempty"`
	Country string `json:"country,omitempty"`
	// Location is free text (the node list's location) for AllStar calls
	// that the user list does not know.
	Location string `json:"location,omitempty"`
	// DMRID is the talker's DMR ID (DMR, and YSF when the line has one).
	DMRID string `json:"dmrId,omitempty"`
	// AllStar: the adjacent link that is keyed, its description and our
	// node it is linked to.
	Node  string `json:"node,omitempty"`
	Desc  string `json:"desc,omitempty"`
	Local string `json:"local,omitempty"`
	// DMR: Network is "BrandMeister" (through STFU) or "DMR".
	Network string `json:"network,omitempty"`
	TG      string `json:"tg,omitempty"`
	TGName  string `json:"tgName,omitempty"`
	Slot    int    `json:"slot,omitempty"`
	Private bool   `json:"private,omitempty"`
	// YSF: the room (the bridge's tune).
	Room string `json:"room,omitempty"`
	// Start is RFC 3339 UTC. End is empty while talking, and also for a
	// finished call whose length is unknown.
	Start  string `json:"start"`
	End    string `json:"end,omitempty"`
	Active bool   `json:"active"`

	start time.Time
	end   time.Time
}

// Snapshot is what the page shows.
type Snapshot struct {
	// Current is the one talker shown (nil: nobody is talking).
	Current *Call `json:"current"`
	// Recent holds finished calls, newest first.
	Recent []Call `json:"recent"`
}

// Select picks the call to show from the calls that are talking: the most
// recently keyed. Two at once would be a crosslink fault, and the newer one
// is the news. Ties go to the larger ID so the choice is stable.
func Select(active []Call) *Call {
	var best *Call
	for i := range active {
		c := &active[i]
		if best == nil || c.start.After(best.start) || c.start.Equal(best.start) && c.ID > best.ID {
			best = c
		}
	}
	return best
}

// Keyed is one keyed adjacent AllStar link of a local node.
type Keyed struct {
	Node string
	// Since is how long ago the link keyed (0: unknown).
	Since time.Duration
	Desc  string
}

// Talk is one DVSwitch call as the helper read it from the logs.
type Talk struct {
	Kind    string // KindDMR or KindYSF (anything else is shown as DMR)
	Via     string // "stfu" or "analog_bridge"
	Call    string
	DMRID   string
	Dst     string
	Slot    int
	Private bool
	Start   time.Time
	// End is zero when the log has no end line.
	End time.Time
}

// Tracker folds the sources. Zero value is not usable; use New.
type Tracker struct {
	// Now is the clock (tests).
	Now func() time.Time
	// Resolve fills the name and location of a new call (may be nil).
	Resolve func(*Call)
	// OnChange is called without locks held when the snapshot changed.
	OnChange func(Snapshot)

	mu      sync.Mutex
	as      map[string]*Call // active AllStar calls by "local/node"
	dv      *Call            // active DVSwitch call
	dvDown  time.Time        // when the bridge link first unkeyed under dv (zero: keyed)
	dvDone  map[string]bool  // DVSwitch calls already finished
	talks   []Talk           // as the helper read them
	eff     []Talk           // talks that count: started while the bridge was linked
	bridge  map[string]bool  // local node -> bridge link keyed
	bridgeK bool             // the bridge node is linked to at least one local node (not stale)
	// linkedSince is when bridgeK last became true (zero while unlinked).
	// Talks that started before it are never shown or recorded.
	linkedSince time.Time
	recent      []Call
	key         string
}

// New returns a Tracker.
func New() *Tracker {
	return &Tracker{Now: time.Now, as: map[string]*Call{}, dvDone: map[string]bool{}, bridge: map[string]bool{}}
}

func (t *Tracker) now() time.Time { return t.Now() }

// UpdateAllStar reports the keyed adjacent links of one local node, as the
// monitor saw them (the DVSwitch bridge link and local monitor links are left
// out by the caller). bridgeSeen says the node has a link to the DVSwitch
// bridge node and bridgeKeyed whether that link is keyed. stale: the state is
// unknown, open calls are dropped without a record.
func (t *Tracker) UpdateAllStar(local string, keyed []Keyed, bridgeSeen, bridgeKeyed, stale bool) {
	t.mu.Lock()
	now := t.now()
	if stale {
		for k := range t.as {
			if len(k) > len(local) && k[:len(local)+1] == local+"/" {
				delete(t.as, k)
			}
		}
		delete(t.bridge, local)
	} else {
		seen := map[string]bool{}
		for _, k := range keyed {
			id := local + "/" + k.Node
			seen[id] = true
			if _, ok := t.as[id]; ok {
				continue
			}
			start := now
			if k.Since > 0 && k.Since < maxSinceKey {
				start = now.Add(-k.Since)
			}
			c := &Call{ID: "as:" + id + "@" + start.UTC().Format("150405.000"), Kind: KindAllStar, Node: k.Node, Desc: k.Desc, Local: local, start: start, Active: true}
			t.fill(c)
			t.as[id] = c
		}
		for id, c := range t.as {
			if seen[id] || len(id) <= len(local) || id[:len(local)+1] != local+"/" {
				continue
			}
			delete(t.as, id)
			t.finish(c, now, now.Sub(c.start) >= MinDuration)
		}
		if bridgeSeen {
			t.bridge[local] = bridgeKeyed
		} else {
			delete(t.bridge, local)
		}
	}
	t.bridgeK = len(t.bridge) > 0
	t.syncDV(now)
	snap, changed := t.snapshotLocked(now)
	t.mu.Unlock()
	t.emit(snap, changed)
}

// UpdateDV reports the DVSwitch calls of the logs, oldest first (the helper
// reads them on every poll of the bridge).
func (t *Tracker) UpdateDV(talks []Talk) {
	t.mu.Lock()
	now := t.now()
	t.talks = talks
	t.syncDV(now)
	snap, changed := t.snapshotLocked(now)
	t.mu.Unlock()
	t.emit(snap, changed)
}

// Tick re-evaluates time based rules (a DVSwitch call that has no end line
// and no bridge link information ends after a few seconds). Call it about
// once a second.
func (t *Tracker) Tick() {
	t.mu.Lock()
	now := t.now()
	t.syncDV(now)
	snap, changed := t.snapshotLocked(now)
	t.mu.Unlock()
	t.emit(snap, changed)
}

// Snapshot returns the current state.
func (t *Tracker) Snapshot() Snapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	s, _ := t.snapshotLocked(t.now())
	return s
}

func (t *Tracker) emit(s Snapshot, changed bool) {
	if changed && t.OnChange != nil {
		t.OnChange(s)
	}
}

func (t *Tracker) fill(c *Call) {
	c.Start = c.start.UTC().Format(time.RFC3339)
	if t.Resolve != nil {
		t.Resolve(c)
	}
}

// finish moves a call to the recent list (record false: dropped).
func (t *Tracker) finish(c *Call, end time.Time, record bool) {
	if !record {
		return
	}
	for _, r := range t.recent {
		if r.ID == c.ID {
			return
		}
	}
	c.Active = false
	if !end.IsZero() {
		c.end = end
		c.End = end.UTC().Format(time.RFC3339)
	}
	t.recent = append([]Call{*c}, t.recent...)
	sort.SliceStable(t.recent, func(i, j int) bool { return t.recent[i].start.After(t.recent[j].start) })
	if len(t.recent) > RecentMax {
		t.recent = t.recent[:RecentMax]
	}
}

func talkID(k Talk) string {
	return "dv:" + k.Start.UTC().Format("20060102150405.000") + ":" + k.DMRID + k.Call
}

// syncDV decides whether the newest DVSwitch call is talking (caller holds
// mu). DVSwitch calls only count while the bridge node is linked to a local
// node: while unlinked nothing is current or recorded, and a call open when
// the link drops ends at that moment like a finished call. Talks that started
// before the link came up are ignored (the logs are re-read on every poll).
// With an end line the call ends there. Without one (YSF, DMR through
// MMDVM_Bridge) it lasts as long as the bridge link is keyed.
func (t *Tracker) syncDV(now time.Time) {
	if !t.bridgeK {
		t.linkedSince = time.Time{}
		if c := t.dv; c != nil {
			end := now
			for _, k := range t.eff {
				if talkID(k) == c.ID && !k.End.IsZero() && k.End.Before(end) {
					end = k.End
				}
			}
			t.dv = nil
			t.dvDown = time.Time{}
			t.dvDone[c.ID] = true
			t.finish(c, end, true)
		}
		t.eff = nil
		return
	}
	if t.linkedSince.IsZero() {
		t.linkedSince = now
	}
	t.eff = nil
	for _, k := range t.talks {
		if !k.Start.Before(t.linkedSince) {
			t.eff = append(t.eff, k)
		}
	}
	talks := t.eff
	bridgeKeyed := false
	for _, k := range t.bridge {
		bridgeKeyed = bridgeKeyed || k
	}
	// calls not seen before that already ended: history (a restart, or a call
	// shorter than a poll), recorded oldest first.
	for i, k := range talks {
		id := talkID(k)
		if t.dvDone[id] || (t.dv != nil && t.dv.ID == id) {
			continue
		}
		newest := i == len(talks)-1
		over := !k.End.IsZero()
		if !over && newest {
			continue // may be talking: handled below
		}
		if !over && !newest {
			// superseded without an end line: length unknown
			t.dvDone[id] = true
			c := t.newDV(k)
			c.Active = false
			t.finish(c, time.Time{}, true)
			continue
		}
		t.dvDone[id] = true
		c := t.newDV(k)
		t.finish(c, k.End, true)
	}
	if t.dv != nil {
		// a newer call replaces the open one
		var latest *Talk
		if n := len(talks); n > 0 {
			latest = &talks[n-1]
		}
		if latest != nil && talkID(*latest) != t.dv.ID {
			old := t.dv
			t.dv = nil
			t.dvDown = time.Time{}
			t.dvDone[old.ID] = true
			t.finish(old, time.Time{}, true)
		}
	}
	n := len(talks)
	if n == 0 {
		t.endDV(now, bridgeKeyed)
		return
	}
	k := talks[n-1]
	id := talkID(k)
	if t.dvDone[id] {
		return
	}
	if t.dv == nil {
		if !t.talking(k, now, bridgeKeyed) {
			if !k.End.IsZero() || now.Sub(k.Start) > noBridgeGrace {
				t.dvDone[id] = true
				c := t.newDV(k)
				end := k.End
				t.finish(c, end, true)
			}
			return
		}
		t.dv = t.newDV(k)
		t.dv.Active = true
		return
	}
	t.endDV(now, bridgeKeyed)
}

// talking: is call k (the newest) in progress? An end line ends it. Without
// one the bridge link's keyed state decides. STFU calls are given a few
// seconds for the link to key (a lost end line must not keep the strip red).
func (t *Tracker) talking(k Talk, now time.Time, bridgeKeyed bool) bool {
	age := now.Sub(k.Start)
	if !k.End.IsZero() || age > maxOpen {
		return false
	}
	return bridgeKeyed || k.Via == "stfu" && age < bridgeLag
}

// endDV closes the open DVSwitch call when it is over.
func (t *Tracker) endDV(now time.Time, bridgeKeyed bool) {
	if t.dv == nil {
		return
	}
	var k *Talk
	for i := range t.eff {
		if talkID(t.eff[i]) == t.dv.ID {
			k = &t.eff[i]
		}
	}
	if k != nil && t.talking(*k, now, bridgeKeyed) {
		t.dvDown = time.Time{} // keyed again: the same call goes on
		return
	}
	// A call without an end line that was keyed on the bridge link and now
	// is not: hold it open for dvHangover (it is still reported as talking,
	// so the strip stays red) and end it at the moment the link unkeyed.
	down := now
	if k != nil && k.End.IsZero() && k.Via != "stfu" && !bridgeKeyed && now.Sub(k.Start) <= maxOpen {
		if t.dvDown.IsZero() {
			t.dvDown = now
		}
		if now.Sub(t.dvDown) < dvHangover {
			return
		}
		down = t.dvDown
	}
	t.dvDown = time.Time{}
	c := t.dv
	t.dv = nil
	t.dvDone[c.ID] = true
	end := down
	if k != nil && !k.End.IsZero() {
		end = k.End
	}
	t.finish(c, end, true)
	if len(t.dvDone) > 64 {
		t.dvDone = map[string]bool{c.ID: true}
	}
}

func (t *Tracker) newDV(k Talk) *Call {
	c := &Call{ID: talkID(k), Kind: k.Kind, Call: k.Call, DMRID: k.DMRID, TG: k.Dst, Slot: k.Slot, Private: k.Private, start: k.Start}
	if c.Kind != KindYSF {
		c.Kind = KindDMR
		c.Network = "DMR"
		if k.Via == "stfu" {
			c.Network = "BrandMeister"
		}
	} else {
		c.TG, c.Slot, c.DMRID = "", 0, k.DMRID
	}
	t.fill(c)
	return c
}

func (t *Tracker) snapshotLocked(now time.Time) (Snapshot, bool) {
	var active []Call
	for _, c := range t.as {
		active = append(active, *c)
	}
	if t.dv != nil {
		active = append(active, *t.dv)
	}
	s := Snapshot{Recent: append([]Call{}, t.recent...)}
	if best := Select(active); best != nil {
		c := *best
		s.Current = &c
	}
	key := ""
	if s.Current != nil {
		key = s.Current.ID
	}
	key += "|"
	for _, r := range s.Recent {
		key += r.ID + r.End + ";"
	}
	changed := key != t.key
	t.key = key
	return s, changed
}
