package api

import (
	"net/http"

	"github.com/artw1/aslcp/internal/accesslist"
	"github.com/artw1/aslcp/internal/nodecfg"
	"github.com/artw1/aslcp/internal/store"
)

func (s *Server) accessRoutes() {
	s.mux.HandleFunc("GET /api/config/access", s.authed(store.RoleAdmin, s.handleAccessGet))
	s.mux.HandleFunc("POST /api/config/access", s.authed(store.RoleAdmin, s.handleAccessPost))
	s.mux.HandleFunc("GET /api/config/dtmf", s.authed(store.RoleAdmin, s.handleDTMF))
}

type accessView struct {
	accesslist.Lists
	// Enforced: the dialplan in extensions.conf checks the lists. When false
	// entries have no effect and the page says so.
	Enforced bool `json:"enforced"`
	// Limit and NoteMax are the bounds the server applies.
	Limit   int `json:"limit"`
	NoteMax int `json:"noteMax"`
}

// accessNode checks that node is a local node of rpt.conf.
func (s *Server) accessNode(w http.ResponseWriter, world *nodecfg.World, node string) bool {
	for _, n := range world.LocalNodes() {
		if n == node {
			return true
		}
	}
	writeErr(w, http.StatusBadRequest, "bad_request", "node has no stanza in rpt.conf")
	return false
}

func (s *Server) accessView(r *http.Request, world *nodecfg.World, node string) (accessView, error) {
	ctx, cancel := s.cmdContext(r)
	defer cancel()
	l, err := accesslist.Read(ctx, s.d.AMI, node)
	if err != nil {
		return accessView{}, err
	}
	ext, _ := world.Content(nodecfg.FileExt)
	return accessView{Lists: l, Enforced: accesslist.Enforced(ext), Limit: accesslist.MaxEntries, NoteMax: accesslist.MaxNote}, nil
}

func (s *Server) handleAccessGet(w http.ResponseWriter, r *http.Request, _ store.User) {
	svc := s.configSvc(w)
	if svc == nil {
		return
	}
	world := s.cfgWorld(r.Context(), svc)
	node := r.URL.Query().Get("node")
	if !s.accessNode(w, world, node) {
		return
	}
	v, err := s.accessView(r, world, node)
	if err != nil {
		s.commandError(w, err)
		return
	}
	writeJSON(w, 200, v)
}

type accessReq struct {
	Node   string `json:"node"`
	Op     string `json:"op"` // add or remove
	Family string `json:"family"`
	Remote string `json:"remote"`
	Note   string `json:"note"`
}

func (s *Server) handleAccessPost(w http.ResponseWriter, r *http.Request, u store.User) {
	svc := s.configSvc(w)
	if svc == nil {
		return
	}
	var in accessReq
	if !decodeMax(w, r, &in, false, 4096) {
		return
	}
	world := s.cfgWorld(r.Context(), svc)
	if !s.accessNode(w, world, in.Node) {
		return
	}
	if in.Op != "add" && in.Op != "remove" {
		writeErr(w, http.StatusBadRequest, "bad_request", "op must be add or remove")
		return
	}
	ctx, cancel := s.cmdContext(r)
	defer cancel()
	cur, err := accesslist.Read(ctx, s.d.AMI, in.Node)
	if err != nil {
		s.commandError(w, err)
		return
	}
	if in.Op == "add" {
		err = accesslist.Put(ctx, s.d.AMI, cur, in.Family, in.Remote, in.Note)
	} else {
		err = accesslist.Del(ctx, s.d.AMI, cur, in.Family, in.Remote)
	}
	if err != nil {
		if accesslist.IsInvalid(err) {
			writeErr(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		s.commandError(w, err)
		return
	}
	s.audit(u, "config.access", map[string]any{"node": in.Node, "op": in.Op, "family": in.Family, "remote": in.Remote})
	v, err := s.accessView(r, world, in.Node)
	if err != nil {
		s.commandError(w, err)
		return
	}
	writeJSON(w, 200, v)
}

func (s *Server) handleDTMF(w http.ResponseWriter, r *http.Request, _ store.User) {
	svc := s.configSvc(w)
	if svc == nil {
		return
	}
	d, err := s.cfgWorld(r.Context(), svc).DTMFFor(r.URL.Query().Get("node"))
	if err != nil {
		s.cfgFormError(w, err)
		return
	}
	writeJSON(w, 200, d)
}
