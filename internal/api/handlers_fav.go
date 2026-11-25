package api

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/artw1/aslcp/internal/allscan"
	"github.com/artw1/aslcp/internal/store"
)

const (
	maxLabel = 120
	maxGroup = 60
)

func (s *Server) defaultLabel(node string) string {
	if s.d.Nodelist != nil {
		if in, ok := s.d.Nodelist.Lookup(node); ok {
			l := strings.TrimSpace(in.Callsign + " " + in.Description)
			if l != "" {
				return truncate(l, maxLabel)
			}
		}
	}
	return node
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

func cleanText(s string, max int) (string, bool) {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > max {
		return "", false
	}
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return "", false
		}
	}
	return s, true
}

func (s *Server) handleFavList(w http.ResponseWriter, r *http.Request, _ store.User) {
	favs, err := s.favoritesJSON()
	if err != nil {
		s.internal(w, err)
		return
	}
	writeJSON(w, 200, favs)
}

func (s *Server) handleFavCreate(w http.ResponseWriter, r *http.Request, _ store.User) {
	var in struct{ Node, Label, Group string }
	if !decode(w, r, &in, false) {
		return
	}
	node := strings.TrimSpace(in.Node)
	if !reConnectTarget.MatchString(node) {
		writeErr(w, 400, "bad_target", "node must be a node number")
		return
	}
	label, ok1 := cleanText(in.Label, maxLabel)
	group, ok2 := cleanText(in.Group, maxGroup)
	if !ok1 || !ok2 {
		writeErr(w, 400, "bad_request", "label or group is too long or has control characters")
		return
	}
	if label == "" {
		label = s.defaultLabel(node)
	}
	f, err := s.d.Store.AddFavorite(node, label, group)
	if err != nil {
		s.internal(w, err)
		return
	}
	s.broadcastFavorites()
	writeJSON(w, 201, s.favJSON(f))
}

func (s *Server) handleFavUpdate(w http.ResponseWriter, r *http.Request, _ store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Node, Label, Group *string
		Pinned             *bool
	}
	if !decode(w, r, &in, false) {
		return
	}
	var node, label, group *string
	if in.Node != nil {
		n := strings.TrimSpace(*in.Node)
		if !reConnectTarget.MatchString(n) {
			writeErr(w, 400, "bad_target", "node must be a node number")
			return
		}
		node = &n
	}
	if in.Label != nil {
		l, ok := cleanText(*in.Label, maxLabel)
		if !ok {
			writeErr(w, 400, "bad_request", "label is too long or has control characters")
			return
		}
		if l == "" {
			n := ""
			if node != nil {
				n = *node
			} else if cur, err := s.d.Store.GetFavorite(id); err == nil {
				n = cur.Node
			}
			l = s.defaultLabel(n)
		}
		label = &l
	}
	if in.Group != nil {
		g, ok := cleanText(*in.Group, maxGroup)
		if !ok {
			writeErr(w, 400, "bad_request", "group is too long or has control characters")
			return
		}
		group = &g
	}
	f, err := s.d.Store.UpdateFavorite(id, node, label, group, in.Pinned)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, 404, "not_found", "no such favorite")
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	s.broadcastFavorites()
	writeJSON(w, 200, s.favJSON(f))
}

func (s *Server) handleFavDelete(w http.ResponseWriter, r *http.Request, _ store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	err := s.d.Store.DeleteFavorite(id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, 404, "not_found", "no such favorite")
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	s.broadcastFavorites()
	noContent(w)
}

func (s *Server) handleFavReorder(w http.ResponseWriter, r *http.Request, _ store.User) {
	var in struct {
		IDs []int64 `json:"ids"`
	}
	if !decode(w, r, &in, false) {
		return
	}
	err := s.d.Store.ReorderFavorites(in.IDs)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, 404, "not_found", "unknown favorite id")
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	s.broadcastFavorites()
	noContent(w)
}

// -------------------------------------------------------- AllScan import

func (s *Server) handleImportPreview(w http.ResponseWriter, r *http.Request, _ store.User) {
	res, err := allscan.Find(s.d.AllScanDirs)
	if err != nil {
		s.d.Logger.Printf("allscan import: %v", err)
	}
	writeJSON(w, 200, res)
}

func (s *Server) handleImport(w http.ResponseWriter, r *http.Request, _ store.User) {
	res, err := allscan.Find(s.d.AllScanDirs)
	if err != nil {
		s.d.Logger.Printf("allscan import: %v", err)
	}
	existing, err := s.d.Store.ListFavorites()
	if err != nil {
		s.internal(w, err)
		return
	}
	have := map[string]bool{}
	for _, f := range existing {
		have[f.Node] = true
	}
	imported, skipped := 0, 0
	for _, it := range res.Items {
		if have[it.Node] {
			skipped++
			continue
		}
		label, ok := cleanText(it.Label, maxLabel)
		if !ok {
			label = truncate(strings.Map(func(r rune) rune {
				if r < 0x20 || r == 0x7f {
					return -1
				}
				return r
			}, it.Label), maxLabel)
			label = strings.TrimSpace(label)
		}
		if label == "" {
			label = s.defaultLabel(it.Node)
		}
		if _, err := s.d.Store.AddFavorite(it.Node, label, ""); err != nil {
			s.internal(w, err)
			return
		}
		have[it.Node] = true
		imported++
	}
	if imported > 0 {
		s.broadcastFavorites()
	}
	writeJSON(w, 200, map[string]int{"imported": imported, "skipped": skipped})
}
