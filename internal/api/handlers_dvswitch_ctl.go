package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/artw1/aslcp/internal/helper"
	"github.com/artw1/aslcp/internal/store"
)

// Mode switch, unlink, snapshot and favourites of the DVSwitch dashboard card
// (docs/DVSWITCH.md, "API").

func (s *Server) dvswitchCtlRoutes() {
	m := s.mux
	m.HandleFunc("GET /api/dvswitch/snapshot", s.authed(store.RoleViewer, s.handleDVSwitchSnapshot))
	m.HandleFunc("POST /api/dvswitch/mode", s.authed(store.RoleOperator, s.handleDVSwitchMode))
	m.HandleFunc("POST /api/dvswitch/unlink", s.authed(store.RoleOperator, s.handleDVSwitchUnlink))
	m.HandleFunc("GET /api/dvswitch/favorites", s.authed(store.RoleViewer, s.handleDVFavList))
	m.HandleFunc("POST /api/dvswitch/favorites", s.authed(store.RoleOperator, s.handleDVFavCreate))
	m.HandleFunc("PUT /api/dvswitch/favorites/{id}", s.authed(store.RoleOperator, s.handleDVFavUpdate))
	m.HandleFunc("DELETE /api/dvswitch/favorites/{id}", s.authed(store.RoleOperator, s.handleDVFavDelete))
}

// dvCmdReply is the reply of the three commands: what the helper reported and
// the snapshot read after it.
type dvCmdReply struct {
	Mode      string `json:"mode"`
	Switched  bool   `json:"switched"`
	Confirmed bool   `json:"confirmed"`
	// Left: the mode a switch left and unlinked first (helper.DVTuneResult).
	Left     string     `json:"left,omitempty"`
	Snapshot DVSnapshot `json:"snapshot"`
}

// dvDone builds the reply of a command, publishes the new snapshot and
// schedules the follow-up reads.
func (s *Server) dvDone(ctx context.Context, res helper.DVTuneResult) dvCmdReply {
	sn := s.dvs.snapshot(ctx, true)
	s.dvs.publish(sn)
	s.dvs.afterCommand()
	return dvCmdReply{Mode: res.Mode, Switched: res.Switched, Confirmed: res.Confirmed, Left: res.Left, Snapshot: sn}
}

// handleDVSwitchSnapshot: GET /api/dvswitch/snapshot, every role. The admin
// only GET /api/dvswitch (install status and logs) stays as it is.
func (s *Server) handleDVSwitchSnapshot(w http.ResponseWriter, r *http.Request, _ store.User) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, s.dvs.snapshot(r.Context(), false))
}

// dvModeEnabled: BM when the BrandMeister connection is configured, the
// others when MMDVM_Bridge enables them.
func dvModeEnabled(setup helper.DVSwitchSetup, mode string) bool {
	v := setup.Modes
	switch mode {
	case helper.DVModeBM:
		return setup.BrandMeister.Configured
	case "DMR":
		return v.DMR
	case "YSF":
		return v.YSF
	case "P25":
		return v.P25
	case "NXDN":
		return v.NXDN
	}
	return false
}

func dvKnownMode(m string) bool {
	for _, k := range helper.DVTuneModes {
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
	if !helper.DVValidTune(mode, t) || helper.DVUnlinkValue(mode, t) || helper.DVPrivateTune(mode, t) {
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

// handleDVSwitchMode: POST /api/dvswitch/mode {mode}. Switches the mode and
// tunes it: to the tune the node remembers for it (dvRememberedTune), else to
// its unlink value, so the new mode never inherits the txTg the previous one
// left. The helper unlinks the mode being left first (DVSwitchTune). 409
// mode_disabled when the mode is not set up.
func (s *Server) handleDVSwitchMode(w http.ResponseWriter, r *http.Request, u store.User) {
	if s.d.DVSwitch == nil {
		s.dvswitchError(w, errDVUnavailable)
		return
	}
	var b struct {
		Mode string `json:"mode"`
	}
	if !decode(w, r, &b, false) {
		return
	}
	mode := strings.ToUpper(strings.TrimSpace(b.Mode))
	if !dvKnownMode(mode) {
		writeErr(w, http.StatusBadRequest, "bad_request", "mode must be BM, DMR, YSF, P25 or NXDN")
		return
	}
	setup, err := s.d.DVSwitch.DVSwitchSetup(r.Context())
	if err != nil {
		s.dvswitchError(w, err)
		return
	}
	if !dvModeEnabled(setup, mode) {
		s.audit(u, "dvswitch.mode.refused", map[string]any{"mode": mode, "reason": "disabled"})
		writeErr(w, http.StatusConflict, "mode_disabled", mode+" is not set up")
		return
	}
	cur, ierr := s.d.DVSwitch.DVSwitchInfo(r.Context())
	curMode := ""
	if ierr == nil && cur.Found {
		curMode = helper.DVPanelMode(cur)
	}
	// A mode that is disconnected now stays so: the poll may not have seen
	// the unlink yet, so drop its remembered tune here.
	if curMode != "" && cur.Tune != "" && helper.DVUnlinkValue(curMode, cur.Tune) {
		s.dvForget(curMode)
	}
	// Already in the mode: nothing to send.
	tn := &helper.DVTune{Mode: mode, ModeOnly: true}
	if curMode != mode {
		// Tune the new mode in the same sequence: to its remembered target,
		// else to its unlink value. A switch that sent no tune left the
		// previous mode's txTg in place (a YSF reflector's number showed as
		// DMR talkgroup 76).
		tn = &helper.DVTune{Mode: mode, Tune: helper.DVUnlinkTune(mode)}
		if last := s.dvRememberedTune(mode); last != "" {
			tn.Tune = last
		}
	}
	res, err := s.d.DVSwitch.DVSwitchTune(r.Context(), helper.DVSwitchRequest{Author: u.Username, Tune: tn})
	if err != nil && !tn.ModeOnly && tn.Tune != helper.DVUnlinkTune(mode) {
		// the remembered value was refused (a host list changed, say):
		// switch to the mode unlinked
		tn = &helper.DVTune{Mode: mode, Tune: helper.DVUnlinkTune(mode)}
		res, err = s.d.DVSwitch.DVSwitchTune(r.Context(), helper.DVSwitchRequest{Author: u.Username, Tune: tn})
	}
	if err != nil {
		s.audit(u, "dvswitch.mode.refused", map[string]any{"mode": mode})
		s.dvswitchError(w, err)
		return
	}
	audit := map[string]any{"mode": mode}
	if res.Left != "" {
		audit["left"] = res.Left
	}
	if res.LeftFailed {
		s.d.Logger.Printf("dvswitch.mode: the unlink of %s could not be sent before the switch", res.Left)
	}
	s.audit(u, "dvswitch.mode", audit)
	s.d.Logger.Printf("dvswitch.mode %s by %s", mode, u.Username)
	// ABInfo shows the new mode within about 2 s: wait for it.
	for i := 0; !res.Confirmed && i < 6; i++ {
		select {
		case <-r.Context().Done():
		case <-time.After(300 * time.Millisecond):
		}
		if info, err := s.d.DVSwitch.DVSwitchInfo(r.Context()); err == nil && info.Found && helper.DVPanelMode(info) == mode {
			res.Confirmed = true
		}
	}
	writeJSON(w, 200, s.dvDone(r.Context(), res))
}

// handleDVSwitchUnlink: POST /api/dvswitch/unlink {}. Sends the unlink value
// of the mode ABInfo shows now (DVUnlinkTune).
func (s *Server) handleDVSwitchUnlink(w http.ResponseWriter, r *http.Request, u store.User) {
	if s.d.DVSwitch == nil {
		s.dvswitchError(w, errDVUnavailable)
		return
	}
	var b struct{}
	if !decode(w, r, &b, true) {
		return
	}
	info, err := s.d.DVSwitch.DVSwitchInfo(r.Context())
	if err != nil {
		s.dvswitchError(w, err)
		return
	}
	if !info.Found {
		s.dvswitchError(w, helper.ErrDVNoInfo)
		return
	}
	mode := helper.DVPanelMode(info)
	if mode == "" {
		writeErr(w, http.StatusConflict, "mode_unknown", "the bridge is in a mode that cannot be unlinked here")
		return
	}
	value := helper.DVUnlinkTune(mode)
	res, err := s.d.DVSwitch.DVSwitchTune(r.Context(), helper.DVSwitchRequest{Author: u.Username, Tune: &helper.DVTune{Mode: mode, Tune: value}})
	if err != nil {
		s.audit(u, "dvswitch.unlink.refused", map[string]any{"mode": mode})
		s.dvswitchError(w, err)
		return
	}
	s.dvForget(mode)
	s.audit(u, "dvswitch.unlink", map[string]any{"mode": mode, "tune": value})
	s.d.Logger.Printf("dvswitch.unlink %s by %s", mode, u.Username)
	writeJSON(w, 200, s.dvDone(r.Context(), res))
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
	if !dvKnownMode(mode) || !helper.DVValidTune(mode, value) {
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
	s.audit(u, "dvswitch.favorite.add", map[string]any{"id": f.ID, "mode": mode, "value": value})
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
	s.audit(u, "dvswitch.favorite.update", map[string]any{"id": id})
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
	s.audit(u, "dvswitch.favorite.delete", map[string]any{"id": id})
	s.dvs.refreshSoon()
	noContent(w)
}
