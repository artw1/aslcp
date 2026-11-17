package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/artw1/aslcp/internal/nodelist"
	"github.com/artw1/aslcp/internal/public"
	"github.com/artw1/aslcp/internal/store"
)

// PublicSettingsJSON is the API object of GET/PUT /api/public (admin).
type PublicSettingsJSON struct {
	Enabled        bool        `json:"enabled"`
	Title          string      `json:"title"`
	Nodes          []string    `json:"nodes"` // empty: all local nodes
	ShowCallsigns  bool        `json:"showCallsigns"`
	ShowNetwork    bool        `json:"showNetwork"`
	ShowActivity   bool        `json:"showActivity"`
	AllowEmbedding bool        `json:"allowEmbedding"`
	TrustProxy     bool        `json:"trustProxy"`
	DefaultTitle   string      `json:"defaultTitle"`
	LocalNodes     []LocalNode `json:"localNodes"`
	Clients        int         `json:"clients"`
}

// LocalNode is a local node with its node-list entry.
type LocalNode struct {
	Node string         `json:"node"`
	Info *nodelist.Info `json:"info"`
}

func (s *Server) localNodes() []LocalNode {
	out := []LocalNode{}
	if m := s.monitor(); m != nil {
		for _, n := range m.Nodes() {
			out = append(out, LocalNode{Node: n, Info: s.info(n)})
		}
	}
	return out
}

func (s *Server) publicJSON() PublicSettingsJSON {
	c := s.pub.Settings()
	out := PublicSettingsJSON{
		Enabled: c.Enabled, Title: c.Title, Nodes: append([]string{}, c.Nodes...),
		ShowCallsigns: c.ShowCallsigns, ShowNetwork: c.ShowNetwork, ShowActivity: c.ShowActivity,
		AllowEmbedding: c.AllowEmbedding, TrustProxy: c.TrustProxy,
		DefaultTitle: "AllStarLink node status", LocalNodes: s.localNodes(), Clients: s.pub.Clients(),
	}
	if len(out.LocalNodes) > 0 {
		if in := out.LocalNodes[0].Info; in != nil && in.Callsign != "" {
			out.DefaultTitle = in.Callsign + " AllStarLink node " + out.LocalNodes[0].Node
		}
	}
	return out
}

func (s *Server) handlePublicGet(w http.ResponseWriter, r *http.Request, _ store.User) {
	writeJSON(w, 200, s.publicJSON())
}

func (s *Server) handlePublicPut(w http.ResponseWriter, r *http.Request, u store.User) {
	var in struct {
		Enabled        *bool     `json:"enabled"`
		Title          *string   `json:"title"`
		Nodes          *[]string `json:"nodes"`
		ShowCallsigns  *bool     `json:"showCallsigns"`
		ShowNetwork    *bool     `json:"showNetwork"`
		ShowActivity   *bool     `json:"showActivity"`
		AllowEmbedding *bool     `json:"allowEmbedding"`
		TrustProxy     *bool     `json:"trustProxy"`
	}
	if !decode(w, r, &in, false) {
		return
	}
	c := s.pub.Settings()
	set := func(dst *bool, v *bool) {
		if v != nil {
			*dst = *v
		}
	}
	set(&c.Enabled, in.Enabled)
	set(&c.ShowCallsigns, in.ShowCallsigns)
	set(&c.ShowNetwork, in.ShowNetwork)
	set(&c.ShowActivity, in.ShowActivity)
	set(&c.AllowEmbedding, in.AllowEmbedding)
	set(&c.TrustProxy, in.TrustProxy)
	if in.Title != nil {
		c.Title = *in.Title
	}
	if in.Nodes != nil {
		known := map[string]bool{}
		for _, n := range s.localNodes() {
			known[n.Node] = true
		}
		seen := map[string]bool{}
		nodes := []string{}
		for _, n := range *in.Nodes {
			if !public.ValidNode(n) {
				writeErr(w, 400, "bad_request", "invalid node number "+strconv.Quote(n))
				return
			}
			if len(known) > 0 && !known[n] {
				writeErr(w, 400, "bad_request", "node "+n+" is not a local node")
				return
			}
			if !seen[n] {
				seen[n] = true
				nodes = append(nodes, n)
			}
		}
		// Selecting every local node is the same as "all".
		if len(known) > 0 && len(nodes) == len(known) {
			nodes = nil
		}
		c.Nodes = nodes
	}
	if err := s.pub.Update(c); err != nil {
		writeErr(w, 400, "bad_request", err.Error())
		return
	}
	s.d.Logger.Printf("public page settings changed by %s (enabled=%v)", u.Username, c.Enabled)
	writeJSON(w, 200, s.publicJSON())
}

// ActivityJSON is one entry of GET /api/activity.
type ActivityJSON struct {
	ID       int64          `json:"id"`
	Node     string         `json:"node"`
	Link     string         `json:"link"` // link node number or "local"
	Info     *nodelist.Info `json:"info"`
	Start    string         `json:"start"`
	Duration float64        `json:"duration"` // seconds
	Kerchunk bool           `json:"kerchunk"`
}

func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request, _ store.User) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 500 {
			writeErr(w, 400, "bad_request", "limit must be 1-500")
			return
		}
		limit = n
	}
	var nodes []string
	if n := r.URL.Query().Get("node"); n != "" {
		if !public.ValidNode(n) {
			writeErr(w, 400, "bad_request", "invalid node")
			return
		}
		nodes = []string{n}
	}
	rows, err := s.d.Store.ListActivity(nodes, limit)
	if err != nil {
		s.internal(w, err)
		return
	}
	out := make([]ActivityJSON, 0, len(rows))
	for _, a := range rows {
		j := ActivityJSON{ID: a.ID, Node: a.Node, Link: a.Link, Start: a.Start.UTC().Format(time.RFC3339),
			Duration: a.Duration.Seconds(), Kerchunk: a.Kerchunk}
		if a.Link != "local" {
			j.Info = s.info(a.Link)
		}
		out = append(out, j)
	}
	writeJSON(w, 200, out)
}
