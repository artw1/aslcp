package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/artw1/aslcp/internal/auth"
	"github.com/artw1/aslcp/internal/rpt"
	"github.com/artw1/aslcp/internal/store"
)

// sseMsg is a pre-framed SSE message in two variants: for privileged
// clients (admin/operator) and for viewers (link IPs removed).
type sseMsg struct {
	priv, pub []byte
	// admin, when set, is sent to admin clients instead of priv/pub; a
	// message with only admin set reaches admins and nobody else.
	admin []byte
}

func frame(event string, v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		b = []byte("null")
	}
	return []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", event, b))
}

type client struct {
	ch    chan sseMsg
	priv  atomic.Bool
	admin atomic.Bool
	done  chan struct{}
	once  sync.Once
}

func (c *client) close() { c.once.Do(func() { close(c.done) }) }

// hub fans events out to SSE clients. A client whose buffer is full is
// dropped (its handler returns, the browser reconnects and gets full state).
type hub struct {
	mu      sync.Mutex
	clients map[*client]struct{}
	onCount func(n int)
	dropped atomic.Uint64
}

func newHub(onCount func(int)) *hub {
	return &hub{clients: map[*client]struct{}{}, onCount: onCount}
}

const clientBuffer = 64

func (h *hub) add(priv bool) *client {
	c := &client{ch: make(chan sseMsg, clientBuffer), done: make(chan struct{})}
	c.priv.Store(priv)
	h.mu.Lock()
	h.clients[c] = struct{}{}
	n := len(h.clients)
	h.mu.Unlock()
	h.onCount(n)
	return c
}

func (h *hub) remove(c *client) {
	h.mu.Lock()
	_, ok := h.clients[c]
	delete(h.clients, c)
	n := len(h.clients)
	h.mu.Unlock()
	c.close()
	if ok {
		h.onCount(n)
	}
}

func (h *hub) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

func (h *hub) broadcast(m sseMsg) {
	h.mu.Lock()
	var dropped []*client
	for c := range h.clients {
		select {
		case c.ch <- m:
		default:
			dropped = append(dropped, c)
			delete(h.clients, c)
		}
	}
	n := len(h.clients)
	h.mu.Unlock()
	for _, c := range dropped {
		c.close()
		h.dropped.Add(1)
	}
	if len(dropped) > 0 {
		h.onCount(n)
	}
}

func (h *hub) closeAll() {
	h.mu.Lock()
	cs := h.clients
	h.clients = map[*client]struct{}{}
	h.mu.Unlock()
	for c := range cs {
		c.close()
	}
}

func (s *Server) broadcastNode(st rpt.NodeStatus, event string) {
	s.hub.broadcast(sseMsg{priv: frame(event, s.nodeJSON(st, true)), pub: frame(event, s.nodeJSON(st, false))})
}

func (s *Server) allNodes(priv bool) []NodeStatusJSON {
	out := []NodeStatusJSON{}
	m := s.monitor()
	if m == nil {
		return out
	}
	snap := m.Snapshot()
	for _, n := range m.Nodes() {
		if st, ok := snap[n]; ok {
			out = append(out, s.nodeJSON(st, priv))
		}
	}
	return out
}

func (s *Server) broadcastNodes() {
	s.hub.broadcast(sseMsg{priv: frame("nodes", s.allNodes(true)), pub: frame("nodes", s.allNodes(false))})
}

func (s *Server) broadcastFavorites() {
	if s.d.FavStats != nil {
		s.d.FavStats.Refresh() // the next node to scan may have changed
	}
	favs, err := s.favoritesJSON()
	if err != nil {
		s.d.Logger.Printf("favorites broadcast: %v", err)
		return
	}
	f := frame("favorites", favs)
	s.hub.broadcast(sseMsg{priv: f, pub: f})
}

// broadcastSchedules sends the schedules snapshot (the same frame for all
// roles).
func (s *Server) broadcastSchedules() {
	if s.d.Schedules == nil {
		return
	}
	f := frame("schedules", s.d.Schedules.Snapshot())
	s.hub.broadcast(sseMsg{priv: f, pub: f})
}

// updateInterval re-evaluates the poll interval; the argument is ignored
// (admin and public SSE clients are both counted).
func (s *Server) updateInterval(int) {
	if s.d.FavStats != nil {
		s.d.FavStats.SetViewers(s.hub.count())
	}
	if m := s.monitor(); m != nil {
		s.applyInterval(m, s.hub.count()+s.pub.Clients())
	}
}

func (s *Server) applyInterval(m *rpt.Monitor, clients int) {
	if clients > 0 {
		m.SetInterval(s.FastInterval)
	} else {
		m.SetInterval(s.SlowInterval)
	}
}

// AttachMonitor wires a running (or about to run) Monitor: it forwards
// updates to SSE clients and sets the poll interval from client count.
// It blocks until ctx is done or the monitor closes.
func (s *Server) AttachMonitor(ctx context.Context, m *rpt.Monitor) {
	s.monMu.Lock()
	s.mon = m
	s.monMu.Unlock()
	s.pub.SetMonitor(m)
	s.applyInterval(m, s.hub.count()+s.pub.Clients())
	ch, cancel := m.Subscribe(256)
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return
		case u, ok := <-ch:
			if !ok {
				return
			}
			s.rec.Process(u)
			s.ncRPT(u)
			s.pub.HandleUpdate(u)
			if u.Changes.Recovered || u.Changes.Resync || u.Changes.Added || u.Changes.Removed {
				// Full state after an AMI reconnect, a dropped-update resync or a
				// change of the node set (the client drops nodes missing from it).
				s.broadcastNodes()
				continue
			}
			s.broadcastNode(u.Status, "node")
		}
	}
}

func (s *Server) monitor() *rpt.Monitor {
	s.monMu.RLock()
	defer s.monMu.RUnlock()
	return s.mon
}

// systemLoop pushes System to clients every SystemInterval while any are
// connected.
func (s *Server) systemLoop(ctx context.Context) {
	t := time.NewTicker(s.SystemInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if s.hub.count() == 0 {
				continue
			}
			f := frame("system", s.systemJSON(ctx))
			s.hub.broadcast(sseMsg{priv: f, pub: f})
			if s.d.Updates != nil {
				s.broadcastUpdates()
			}
		}
	}
}

func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request, u store.User) {
	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	h.Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	write := func(b []byte) bool {
		_ = rc.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if _, err := w.Write(b); err != nil {
			return false
		}
		return rc.Flush() == nil
	}

	c := s.hub.add(auth.RoleRank(u.Role) >= auth.RoleRank(store.RoleOperator))
	c.admin.Store(u.Role == store.RoleAdmin)
	defer s.hub.remove(c)

	priv := c.priv.Load()
	if !write(frame("hello", map[string]any{"user": userJSON(u), "version": s.d.Version, "instance": s.instance, "settings": s.settings()})) ||
		!write(frame("nodes", s.allNodes(priv))) {
		return
	}
	if !write(frame("nowcalling", s.nc.tr.Snapshot())) {
		return
	}
	if c.admin.Load() && s.d.Updates != nil && !write(frame("updates", s.updatesBadge())) {
		return
	}
	// The digital modes snapshot is sent on every connect too: the browser
	// reconnects by itself after a restart of the panel, and a change that was
	// published while it was away (or before it came back) is never repeated.
	if s.dvs != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		sn := s.dvs.snapshot(ctx)
		cancel()
		if !write(frame("dv", sn)) {
			return
		}
	}
	// Initial favorites/system are available via REST; the contract only
	// requires hello + nodes on connect.

	ping := time.NewTicker(s.PingInterval)
	defer ping.Stop()
	token := auth.TokenFrom(r)
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.stop:
			return
		case <-c.done:
			return
		case m := <-c.ch:
			b := m.pub
			if c.priv.Load() {
				b = m.priv
			}
			if m.admin != nil {
				b = nil
				if c.admin.Load() {
					b = m.admin
				}
			}
			if len(b) == 0 {
				continue
			}
			if !write(b) {
				return
			}
		case <-ping.C:
			// Re-validate: logout, deletion or a role change take effect.
			cu, err := s.d.Auth.Validate(token)
			if err != nil {
				return
			}
			c.priv.Store(auth.RoleRank(cu.Role) >= auth.RoleRank(store.RoleOperator))
			c.admin.Store(cu.Role == store.RoleAdmin)
			if !write([]byte(": ping\n\n")) {
				return
			}
		}
	}
}
