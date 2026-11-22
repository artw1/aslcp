package api

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/artw1/aslcp/internal/dv/ctl"
	"github.com/artw1/aslcp/internal/dvpanel"
	"github.com/artw1/aslcp/internal/nowcalling"
	"github.com/artw1/aslcp/internal/radioid"
	"github.com/artw1/aslcp/internal/rpt"
	"github.com/artw1/aslcp/internal/store"
)

// "Now calling" (docs/API.md, "Now calling"): who is talking right now, from
// the keyed AllStar links, the digital modes engine's talk events and the
// keyed state of the bridge link. internal/nowcalling folds them; this file feeds it
// and sends the snapshot as the SSE event "nowcalling" and GET /api/nowcalling.
//
// Callsigns and names are personal data heard on air. Like the digital modes
// callers they go to every logged-in role over the event stream and the
// REST call, and never to the public status view or the embed.

type ncState struct {
	tr *nowcalling.Tracker
	// bridge is the digital modes bridge link's node number ("" when none).
	bridge atomic.Value // string
	// br is the digital modes bridge state of the last snapshot (YSF room).
	br atomic.Value // DVBridge

	mu    sync.Mutex
	keyed map[string]bool // local node -> bridge link keyed
}

func (s *Server) initNowCalling() {
	n := &ncState{tr: nowcalling.New(), keyed: map[string]bool{}}
	n.tr.Resolve = s.ncResolve
	n.tr.OnChange = func(sn nowcalling.Snapshot) {
		f := frame("nowcalling", sn)
		s.hub.broadcast(sseMsg{priv: f, pub: f})
	}
	s.nc = n
}

// ncTick runs the time based rules once a second until stopped.
func (s *Server) ncTick(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.nc.tr.Tick()
		}
	}
}

// ncRPT folds one monitor update: the keyed adjacent links of the node (the
// digital modes bridge link is not an AllStar talker, local monitor links are not
// audio from the network).
func (s *Server) ncRPT(u rpt.Update) {
	st := u.Status
	bridge, _ := s.nc.bridge.Load().(string)
	var keyed []nowcalling.Keyed
	seen, bk := false, false
	for _, l := range st.Links {
		if l.Mode == rpt.ModeLocalMonitor {
			continue
		}
		if bridge != "" && l.Node == bridge {
			seen, bk = true, l.Keyed
			continue
		}
		if !l.Keyed {
			continue
		}
		k := nowcalling.Keyed{Node: l.Node}
		if l.SinceKey != nil {
			k.Since = *l.SinceKey
		}
		keyed = append(keyed, k)
	}
	s.nc.tr.UpdateAllStar(st.Node, keyed, seen, bk, st.Stale)
	s.nc.mu.Lock()
	s.nc.keyed[st.Node] = bk
	s.nc.mu.Unlock()
}

// ncDV folds the calls of the engine and its bridge state.
func (s *Server) ncDV(talks []ctl.Talk, sn DVSnapshot) {
	if sn.BridgeNode.Present {
		s.nc.bridge.Store(sn.BridgeNode.Node)
	} else {
		s.nc.bridge.Store("")
	}
	s.nc.br.Store(sn.Bridge)
	s.nc.tr.UpdateDV(dvNowTalks(talks))
}

// ncResolve fills the details of a new call. An AllStar call gets its callsign,
// description and location from the node list and nothing else: the radioid.net
// list is never consulted for it (a node's callsign is not a person). DMR and
// YSF callers get name and place from the radioid.net list by DMR ID or
// callsign. Without the list a DMR caller keeps the callsign of the DMR ID list
// and a YSF caller the bare callsign.
func (s *Server) ncResolve(c *nowcalling.Call) {
	switch c.Kind {
	case nowcalling.KindAllStar:
		if s.d.Nodelist != nil {
			if in, ok := s.d.Nodelist.Lookup(c.Node); ok {
				c.Call = firstWord(in.Callsign)
				c.Desc, c.Location = in.Description, in.Location
			}
		}
		return
	case nowcalling.KindYSF:
		if b, ok := s.nc.br.Load().(DVBridge); ok && b.Mode == "YSF" {
			c.Room = b.TargetName
			if c.Room == "" {
				c.Room = b.Tune
			}
		}
	case nowcalling.KindDMR:
		c.TGName = s.ncTGName(c.TG)
	}
	if s.d.RadioID == nil {
		return
	}
	var u radioid.User
	var ok bool
	if c.Kind == nowcalling.KindDMR && c.DMRID != "" {
		u, ok = s.d.RadioID.ByID(parseID(c.DMRID))
	}
	if !ok && c.Call != "" {
		u, ok = s.d.RadioID.ByCallsign(c.Call)
	}
	if !ok {
		return
	}
	if c.Call == "" {
		c.Call = u.Callsign
	}
	c.First, c.Name, c.City, c.State, c.Country = u.First, u.Name(), u.City, u.State, u.Country
}

// ncTGName is the name of a talkgroup from the digital modes favourites ("" when unknown).
func (s *Server) ncTGName(tg string) string {
	if tg == "" || s.d.Store == nil {
		return ""
	}
	favs, err := s.d.Store.ListDVFavorites()
	if err != nil {
		return ""
	}
	for _, f := range favs {
		if dvpanel.DMRFamily(f.Mode) && f.Value == tg {
			return f.Name
		}
	}
	return ""
}

func firstWord(s string) string {
	if f := strings.Fields(s); len(f) > 0 {
		return f[0]
	}
	return ""
}

func parseID(s string) uint32 {
	var n uint64
	for _, r := range s {
		if r < '0' || r > '9' || n > 1<<32 {
			return 0
		}
		n = n*10 + uint64(r-'0')
	}
	return uint32(n)
}

func (s *Server) nowCallingRoutes() {
	m := s.mux
	m.HandleFunc("GET /api/nowcalling", s.authed(store.RoleViewer, s.handleNowCalling))
	m.HandleFunc("GET /api/dv/userlist", s.authed(store.RoleAdmin, s.handleUserListGet))
	m.HandleFunc("PUT /api/dv/userlist", s.authed(store.RoleAdmin, s.handleUserListSet))
	m.HandleFunc("POST /api/dv/userlist/download", s.authed(store.RoleAdmin, s.handleUserListDownload))
}

func (s *Server) handleNowCalling(w http.ResponseWriter, _ *http.Request, _ store.User) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, s.nc.tr.Snapshot())
}

func (s *Server) userListUnavailable(w http.ResponseWriter) {
	writeErr(w, http.StatusServiceUnavailable, "userlist_unavailable", "the radioid.net user list is not available on this node")
}

func (s *Server) handleUserListGet(w http.ResponseWriter, _ *http.Request, _ store.User) {
	if s.d.RadioID == nil {
		s.userListUnavailable(w)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, s.d.RadioID.Status())
}

func (s *Server) handleUserListSet(w http.ResponseWriter, r *http.Request, u store.User) {
	if s.d.RadioID == nil {
		s.userListUnavailable(w)
		return
	}
	var b struct {
		Enabled *bool `json:"enabled"`
	}
	if !decode(w, r, &b, false) {
		return
	}
	if b.Enabled == nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "enabled must be true or false")
		return
	}
	if err := s.d.RadioID.SetEnabled(*b.Enabled); err != nil {
		s.d.Logger.Printf("radioid: %v", err)
		writeErr(w, http.StatusInternalServerError, "internal", "could not save the setting")
		return
	}
	s.audit(u, "dv.userlist", map[string]any{"enabled": *b.Enabled})
	if *b.Enabled && s.d.RadioID.Status().Entries == 0 {
		_ = s.d.RadioID.DownloadAsync(context.Background(), false)
	}
	writeJSON(w, 200, s.d.RadioID.Status())
}

func (s *Server) handleUserListDownload(w http.ResponseWriter, _ *http.Request, u store.User) {
	if s.d.RadioID == nil {
		s.userListUnavailable(w)
		return
	}
	switch err := s.d.RadioID.DownloadAsync(context.Background(), true); err {
	case nil:
	case radioid.ErrOff:
		writeErr(w, http.StatusConflict, "userlist_off", err.Error())
		return
	default:
		writeErr(w, http.StatusConflict, "userlist_busy", err.Error())
		return
	}
	s.audit(u, "dv.userlist.download", nil)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusAccepted, s.d.RadioID.Status())
}
