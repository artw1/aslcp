package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/artw1/aslcp/internal/dv/ctl"
	"github.com/artw1/aslcp/internal/dvpanel"
	"github.com/artw1/aslcp/internal/store"
)

// Mode switch, unlink, snapshot and favourites of the dashboard card
// (docs/DV.md, "API").

func (s *Server) dvCtlRoutes() {
	m := s.mux
	m.HandleFunc("GET /api/dv/snapshot", s.authed(store.RoleViewer, s.handleDVSnapshot))
	m.HandleFunc("POST /api/dv/mode", s.authed(store.RoleOperator, s.handleDVMode))
	m.HandleFunc("POST /api/dv/unlink", s.authed(store.RoleOperator, s.handleDVUnlink))
	m.HandleFunc("POST /api/dv/tune", s.authed(store.RoleOperator, s.handleDVTune))
	m.HandleFunc("GET /api/dv/favorites", s.authed(store.RoleViewer, s.handleDVFavList))
	m.HandleFunc("POST /api/dv/favorites", s.authed(store.RoleOperator, s.handleDVFavCreate))
	m.HandleFunc("PUT /api/dv/favorites/{id}", s.authed(store.RoleOperator, s.handleDVFavUpdate))
	m.HandleFunc("DELETE /api/dv/favorites/{id}", s.authed(store.RoleOperator, s.handleDVFavDelete))
}

// dvCmdReply is the reply of the three commands: what the engine did and the
// snapshot read after it.
type dvCmdReply struct {
	Mode      string `json:"mode"`
	Switched  bool   `json:"switched"`
	Confirmed bool   `json:"confirmed"`
	// Left is the mode a switch left (its target is unlinked by the engine).
	Left     string     `json:"left,omitempty"`
	Snapshot DVSnapshot `json:"snapshot"`
}

// dvDone builds the reply of a command from the status the engine returned,
// and publishes the new snapshot.
func (s *Server) dvDone(ctx context.Context, st ctl.Status, mode, left string, switched bool) dvCmdReply {
	s.dvs.setStatus(st)
	sn := s.dvs.snapshot(ctx)
	s.dvs.publish(sn)
	return dvCmdReply{Mode: mode, Switched: switched, Confirmed: st.Mode == mode, Left: left, Snapshot: sn}
}

// handleDVSnapshot: GET snapshot, every role.
func (s *Server) handleDVSnapshot(w http.ResponseWriter, r *http.Request, _ store.User) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, s.dvs.snapshot(r.Context()))
}

func dvKnownMode(m string) bool {
	for _, k := range dvpanel.PanelModes {
		if k == m {
			return true
		}
	}
	return false
}

// dvRememberedTune is the tracker's last tune of the mode when it is valid for
// it and not an unlink value; "" when there is none.
func (s *Server) dvRememberedTune(mode string) string {
	if s.d.DVResume == nil {
		return ""
	}
	t := s.d.DVResume.Get().PerMode[mode].Tune
	if !dvpanel.ValidTune(mode, t) || dvpanel.UnlinkValue(mode, t) || dvpanel.PrivateTune(mode, t) {
		return ""
	}
	return t
}

// dvForget erases the remembered tune of a mode (the operator disconnected it).
func (s *Server) dvForget(mode string) {
	if f, ok := s.d.DVResume.(interface{ Forget(string) }); ok {
		f.Forget(mode)
	}
}

// dvGate answers 503 without a control socket, 409 not_available for a mode
// the engine does not implement (from the status the stream last left), and
// 409 mode_disabled for a mode switched off in its config.
func (s *Server) dvGate(w http.ResponseWriter, mode string) bool {
	if s.d.DVCtl == nil {
		s.dvError(w, errDVUnavailable)
		return false
	}
	if !s.dvEnabled() {
		writeErr(w, http.StatusConflict, "not_enabled", "digital modes are not enabled")
		return false
	}
	s.dvs.mu.Lock()
	connected, nets := s.dvs.connected, s.dvs.st.Networks
	s.dvs.mu.Unlock()
	if !connected {
		writeErr(w, http.StatusServiceUnavailable, "engine_down", "the digital modes engine is not running")
		return false
	}
	for _, n := range nets {
		if n.Mode != mode {
			continue
		}
		if !n.Available {
			writeErr(w, http.StatusConflict, "not_available", mode+" is not yet available in this build")
			return false
		}
		if !n.Enabled {
			writeErr(w, http.StatusConflict, "mode_disabled", mode+" is not set up")
			return false
		}
		return true
	}
	if !dvpanel.NativeMode(mode) {
		writeErr(w, http.StatusConflict, "not_available", mode+" is not yet available in this build")
		return false
	}
	if mode != dvpanel.ModeLoop {
		writeErr(w, http.StatusConflict, "mode_disabled", mode+" is not set up")
		return false
	}
	return true
}

// dvCtlFail answers an error of a control call.
func (s *Server) dvCtlFail(w http.ResponseWriter, err error) {
	var ce *ctl.Error
	if errors.As(err, &ce) {
		s.dvCtlError(w, ce)
		return
	}
	s.d.Logger.Printf("dv engine: %v", err)
	writeErr(w, http.StatusServiceUnavailable, "engine_down", "the digital modes engine did not answer")
}

// dvSendTune builds and sends a tune (or the unlink of an unlink value).
func (s *Server) dvSendTune(ctx context.Context, mode, tune string) (ctl.Status, bool, error) {
	var resolve func(string, string) (ctl.Target, bool)
	if s.d.HostLists != nil {
		resolve = s.d.HostLists.Resolve
	}
	args, unlink, err := dvpanel.BuildTune(mode, tune, resolve)
	if err != nil {
		return ctl.Status{}, false, err
	}
	if unlink {
		st, err := s.d.DVCtl.Unlink(ctx, ctl.UnlinkArgs{Mode: mode})
		return st, true, err
	}
	st, err := s.d.DVCtl.Tune(ctx, args)
	return st, false, err
}

// dvTuneError answers a tune value the engine never saw.
func (s *Server) dvTuneError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, dvpanel.ErrUnknownTarget):
		writeErr(w, http.StatusUnprocessableEntity, "unknown_target", "this target is not in the host list")
	case errors.Is(err, dvpanel.ErrBadTune):
		writeErr(w, http.StatusBadRequest, "invalid_field", "the tune value is not valid for this mode")
	default:
		return false
	}
	return true
}

// handleDVTune: POST tune {mode, tune}; the reply is {mode, switched,
// confirmed, snapshot}. The audit log gets the mode and the value only when
// they passed the validator (a TG or a reflector is public data).
func (s *Server) handleDVTune(w http.ResponseWriter, r *http.Request, u store.User) {
	var b struct {
		Mode string `json:"mode"`
		Tune string `json:"tune"`
	}
	if !decode(w, r, &b, false) {
		return
	}
	mode := dvpanel.NormMode(b.Mode)
	if mode == "" || !dvKnownMode(mode) {
		writeErr(w, http.StatusBadRequest, "bad_request", "mode must be one of "+strings.Join(dvpanel.PanelModes, ", "))
		return
	}
	fields := map[string]any{"mode": "?"}
	if dvpanel.ValidTune(mode, b.Tune) {
		fields = map[string]any{"mode": mode, "tune": b.Tune}
	}
	if !s.dvGate(w, mode) {
		s.audit(u, "dv.tune.refused", fields)
		return
	}
	left := s.dvLeft(mode)
	st, unlink, err := s.dvSendTune(r.Context(), mode, b.Tune)
	if err != nil {
		s.audit(u, "dv.tune.refused", fields)
		if !s.dvTuneError(w, err) {
			s.dvCtlFail(w, err)
		}
		return
	}
	if unlink {
		s.dvForget(mode)
	}
	s.audit(u, "dv.tune", fields)
	s.d.Logger.Printf("dv.tune %s by %s", mode, u.Username)
	writeJSON(w, 200, s.dvDone(r.Context(), st, mode, left, left != "" || s.dvs.info().Mode != mode))
}

// dvLeft is the mode a switch to mode leaves ("" when it stays or none was
// set).
func (s *Server) dvLeft(mode string) string {
	cur := s.dvs.info()
	if cur.Mode != "" && cur.Mode != mode {
		return cur.Mode
	}
	return ""
}

// handleDVMode: POST mode {mode}. Switches the mode and tunes it to the
// target the node remembers for it, else leaves it unlinked, so the new mode
// never inherits the target the previous one left.
func (s *Server) handleDVMode(w http.ResponseWriter, r *http.Request, u store.User) {
	var b struct {
		Mode string `json:"mode"`
	}
	if !decode(w, r, &b, false) {
		return
	}
	mode := strings.ToUpper(strings.TrimSpace(b.Mode))
	if !dvKnownMode(mode) {
		writeErr(w, http.StatusBadRequest, "bad_request", "mode must be one of "+strings.Join(dvpanel.PanelModes, ", "))
		return
	}
	if !s.dvGate(w, mode) {
		s.audit(u, "dv.mode.refused", map[string]any{"mode": mode})
		return
	}
	ctx := r.Context()
	cur := s.dvs.info()
	// A mode that is disconnected now stays so: drop its remembered tune.
	if cur.Mode != "" && cur.Tune != "" && dvpanel.UnlinkValue(cur.Mode, cur.Tune) {
		s.dvForget(cur.Mode)
	}
	switched := cur.Mode != mode
	left := ""
	if switched && cur.Mode != "" {
		left = cur.Mode
	}
	var (
		st  ctl.Status
		err error
	)
	if last := s.dvRememberedTune(mode); switched && last != "" {
		st, _, err = s.dvSendTune(ctx, mode, last)
		if err != nil {
			var ce *ctl.Error
			if errors.As(err, &ce) && ce.Code == ctl.CodeBusy {
				s.audit(u, "dv.mode.refused", map[string]any{"mode": mode})
				s.dvCtlError(w, ce)
				return
			}
			// the remembered value was refused (a host list changed, say):
			// switch to the mode unlinked
			st, err = s.d.DVCtl.Mode(ctx, ctl.ModeArgs{Mode: mode})
		}
	} else {
		st, err = s.d.DVCtl.Mode(ctx, ctl.ModeArgs{Mode: mode})
	}
	if err != nil {
		s.audit(u, "dv.mode.refused", map[string]any{"mode": mode})
		s.dvCtlFail(w, err)
		return
	}
	audit := map[string]any{"mode": mode}
	if left != "" {
		audit["left"] = left
	}
	s.audit(u, "dv.mode", audit)
	s.d.Logger.Printf("dv.mode %s by %s", mode, u.Username)
	writeJSON(w, 200, s.dvDone(ctx, st, mode, left, switched))
}

// handleDVUnlink: POST unlink {}. Sends the unlink of the mode the engine is
// in now.
func (s *Server) handleDVUnlink(w http.ResponseWriter, r *http.Request, u store.User) {
	var b struct{}
	if !decode(w, r, &b, true) {
		return
	}
	mode := s.dvs.info().Mode
	if s.d.DVCtl == nil {
		s.dvError(w, errDVUnavailable)
		return
	}
	if mode == "" {
		writeErr(w, http.StatusConflict, "mode_unknown", "the bridge is in a mode that cannot be unlinked here")
		return
	}
	if !s.dvGate(w, mode) {
		return
	}
	st, err := s.d.DVCtl.Unlink(r.Context(), ctl.UnlinkArgs{Mode: mode})
	if err != nil {
		s.audit(u, "dv.unlink.refused", map[string]any{"mode": mode})
		s.dvCtlFail(w, err)
		return
	}
	s.dvForget(mode)
	s.audit(u, "dv.unlink", map[string]any{"mode": mode, "tune": dvpanel.UnlinkTune(mode)})
	s.d.Logger.Printf("dv.unlink %s by %s", mode, u.Username)
	writeJSON(w, 200, s.dvDone(r.Context(), st, mode, "", false))
}

// Favourites -----------------------------------------------------------

const dvFavNameMax = 40

// dvFavName checks a favourite's name: 1 to 40 printable characters.
func dvFavName(s string) (string, bool) {
	s = strings.TrimSpace(s)
	n := utf8.RuneCountInString(s)
	if n < 1 || n > dvFavNameMax {
		return "", false
	}
	for _, r := range s {
		if !unicode.IsPrint(r) {
			return "", false
		}
	}
	return s, true
}

func (s *Server) handleDVFavList(w http.ResponseWriter, _ *http.Request, _ store.User) {
	l, err := s.d.Store.ListDVFavorites()
	if err != nil {
		s.internal(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, l)
}

func (s *Server) handleDVFavCreate(w http.ResponseWriter, r *http.Request, u store.User) {
	var in struct {
		Mode  string `json:"mode"`
		Value string `json:"value"`
		Name  string `json:"name"`
	}
	if !decode(w, r, &in, false) {
		return
	}
	mode := strings.ToUpper(strings.TrimSpace(in.Mode))
	value := strings.TrimSpace(in.Value)
	if !dvKnownMode(mode) || !dvpanel.ValidTune(mode, value) {
		writeErr(w, http.StatusBadRequest, "bad_request", "the mode or the value is not valid")
		return
	}
	name, ok := dvFavName(in.Name)
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad_request", "the name must be 1 to 40 printable characters")
		return
	}
	f, err := s.d.Store.AddDVFavorite(mode, value, name)
	switch {
	case errors.Is(err, store.ErrDuplicate):
		writeErr(w, http.StatusConflict, "duplicate", "this target is a favourite already")
		return
	case errors.Is(err, store.ErrLimit):
		writeErr(w, http.StatusConflict, "limit", "too many favourites for this mode")
		return
	case err != nil:
		s.internal(w, err)
		return
	}
	s.audit(u, "dv.favorite.add", map[string]any{"id": f.ID, "mode": mode, "value": value})
	s.dvs.refreshSoon()
	writeJSON(w, 201, f)
}

func (s *Server) handleDVFavUpdate(w http.ResponseWriter, r *http.Request, u store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Name *string `json:"name"`
		Sort *int    `json:"sort"`
	}
	if !decode(w, r, &in, false) {
		return
	}
	var name *string
	if in.Name != nil {
		n, ok := dvFavName(*in.Name)
		if !ok {
			writeErr(w, http.StatusBadRequest, "bad_request", "the name must be 1 to 40 printable characters")
			return
		}
		name = &n
	}
	if in.Sort != nil && (*in.Sort < 0 || *in.Sort > 100000) {
		writeErr(w, http.StatusBadRequest, "bad_request", "sort is out of range")
		return
	}
	f, err := s.d.Store.UpdateDVFavorite(id, name, in.Sort)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not_found", "no such favourite")
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	s.audit(u, "dv.favorite.update", map[string]any{"id": id})
	s.dvs.refreshSoon()
	writeJSON(w, 200, f)
}

func (s *Server) handleDVFavDelete(w http.ResponseWriter, r *http.Request, u store.User) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	err := s.d.Store.DeleteDVFavorite(id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not_found", "no such favourite")
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	s.audit(u, "dv.favorite.delete", map[string]any{"id": id})
	s.dvs.refreshSoon()
	noContent(w)
}
