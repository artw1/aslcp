package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/artw1/aslcp/internal/dv/ctl"
	"github.com/artw1/aslcp/internal/dvpanel"
	"github.com/artw1/aslcp/internal/helper"
	"github.com/artw1/aslcp/internal/store"
)

// Per-network settings and the identity of the digital modes (docs/API.md,
// "Digital modes networks"). Admin only. The password is write-only: no reply,
// log line or audit entry carries it, the views say only that one is set.

func (s *Server) dvNetRoutes() {
	m := s.mux
	m.HandleFunc("GET /api/dv/networks", s.authed(store.RoleAdmin, s.handleDVNetworks))
	m.HandleFunc("GET /api/dv/networks/{mode}", s.authed(store.RoleAdmin, s.handleDVNetworkGet))
	m.HandleFunc("PUT /api/dv/networks/{mode}", s.authed(store.RoleAdmin, s.handleDVNetworkPut))
	m.HandleFunc("GET /api/dv/identity", s.authed(store.RoleAdmin, s.handleDVIdentityGet))
	m.HandleFunc("PUT /api/dv/identity", s.authed(store.RoleAdmin, s.handleDVIdentityPut))
}

// DVNetFields says which of address, port and password a mode takes.
type DVNetFields struct {
	Address  bool `json:"address"`
	Port     bool `json:"port"`
	Password bool `json:"password"`
}

// DVNetView is one mode of GET /api/dv/networks. Available comes from the
// engine (the static dvpanel.NativeModes while it is down); Login and Detail
// are the engine's login state.
type DVNetView struct {
	Mode          string            `json:"mode"`
	Label         string            `json:"label"`
	Available     bool              `json:"available"`
	Enabled       bool              `json:"enabled"`
	Address       string            `json:"address"`
	Port          int               `json:"port"`
	DefaultPort   int               `json:"defaultPort,omitempty"`
	DefaultAddr   string            `json:"defaultAddress,omitempty"`
	AddrOptional  bool              `json:"addressOptional,omitempty"`
	PasswordSet   bool              `json:"passwordSet"`
	Fields        DVNetFields       `json:"fields"`
	NeedsIdentity bool              `json:"needsIdentity"`
	Help          string            `json:"help,omitempty"`
	Options       []dvpanel.OptSpec `json:"options"`
	Values        map[string]string `json:"values"`
	Login         string            `json:"login,omitempty"`
	Detail        string            `json:"detail,omitempty"`
}

// DVIdentity is the node's DMR identity.
type DVIdentity struct {
	Callsign   string `json:"callsign"`
	DMRID      uint32 `json:"dmrId"`
	RepeaterID uint32 `json:"repeaterId,omitempty"`
}

// DVNetworks is the reply of GET /api/dv/networks.
type DVNetworks struct {
	Identity DVIdentity  `json:"identity"`
	Modes    []DVNetView `json:"modes"`
}

// dvNetStatus is the engine's view of a mode. A mode the engine does not
// list, or no engine at all, falls back to the static list of the build.
func dvNetStatus(connected bool, nets []ctl.NetStatus, mode string) ctl.NetStatus {
	if connected {
		for _, n := range nets {
			if n.Mode == mode {
				return n
			}
		}
	}
	return ctl.NetStatus{Mode: mode, Available: dvpanel.NativeMode(mode), Enabled: mode == dvpanel.ModeLoop}
}

func (s *Server) dvNetEngine(mode string) ctl.NetStatus {
	if s.dvs == nil {
		return dvNetStatus(false, nil, mode)
	}
	s.dvs.mu.Lock()
	defer s.dvs.mu.Unlock()
	return dvNetStatus(s.dvs.connected, s.dvs.st.Networks, mode)
}

func dvNetViewOf(sp dvpanel.NetSpec, v helper.DVNetworkView, eng ctl.NetStatus) DVNetView {
	vals := v.Options
	if vals == nil {
		vals = map[string]string{}
	}
	out := DVNetView{
		Mode: sp.Mode, Label: sp.Label, Available: eng.Available, Enabled: v.Enabled, Address: v.Address, Port: v.Port,
		DefaultPort: sp.DefaultPort, DefaultAddr: sp.DefaultAddress, AddrOptional: sp.AddressOptional, PasswordSet: v.PasswordSet, NeedsIdentity: sp.NeedsIdentity, Help: sp.Help,
		Fields:  DVNetFields{Address: sp.Address, Port: sp.Port, Password: sp.Password},
		Options: dvpanel.OptSpecs(sp.Mode), Values: vals,
	}
	if eng.Available && v.Enabled {
		out.Login, out.Detail = string(eng.Login), eng.Detail
	}
	return out
}

func (s *Server) dvNetReply(rep helper.DVNetworkReply) DVNetworks {
	out := DVNetworks{Identity: DVIdentity(rep.Identity), Modes: []DVNetView{}}
	byMode := map[string]helper.DVNetworkView{}
	for _, v := range rep.Networks {
		byMode[v.Mode] = v
	}
	for _, sp := range dvpanel.NetSpecs {
		v, ok := byMode[sp.Mode]
		if !ok {
			v = helper.DVNetworkView{Mode: sp.Mode}
		}
		out.Modes = append(out.Modes, dvNetViewOf(sp, v, s.dvNetEngine(sp.Mode)))
	}
	return out
}

func (s *Server) handleDVNetworks(w http.ResponseWriter, r *http.Request, _ store.User) {
	if s.d.DVEngine == nil {
		s.dvError(w, errDVUnavailable)
		return
	}
	rep, err := s.d.DVEngine.DVNetworkGet(r.Context())
	if err != nil {
		s.dvError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, s.dvNetReply(rep))
}

func (s *Server) dvNetPick(w http.ResponseWriter, r *http.Request) (dvpanel.NetSpec, bool) {
	sp, ok := dvpanel.NetSpecFor(r.PathValue("mode"))
	if !ok {
		writeErr(w, http.StatusNotFound, "not_found", "no settings for this mode")
	}
	return sp, ok
}

func (s *Server) handleDVNetworkGet(w http.ResponseWriter, r *http.Request, _ store.User) {
	sp, ok := s.dvNetPick(w, r)
	if !ok {
		return
	}
	if s.d.DVEngine == nil {
		s.dvError(w, errDVUnavailable)
		return
	}
	rep, err := s.d.DVEngine.DVNetworkGet(r.Context())
	if err != nil {
		s.dvError(w, err)
		return
	}
	for _, m := range s.dvNetReply(rep).Modes {
		if m.Mode == sp.Mode {
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, 200, m)
			return
		}
	}
}

// dvNetApplyResult is what a settings write answers: the new view and whether
// the engine took it. Applied false with ApplyError: the settings are saved,
// the engine did not reload (it is not running, or the file is refused).
type dvNetApplyResult struct {
	DVNetView
	Applied    bool   `json:"applied"`
	ApplyError string `json:"applyError,omitempty"`
}

// dvApplyToEngine reloads the engine after a settings write (the helper has
// rendered the file). It reports whether the engine took it.
func (s *Server) dvApplyToEngine(ctx context.Context, rendered bool) (bool, string) {
	if !rendered || !s.dvEnabled() {
		return false, "digital modes are not enabled: the settings apply when you enable them"
	}
	err := s.dvReloadEngine(ctx)
	if s.dvs != nil {
		s.dvs.invalidate()
		s.dvs.publish(s.dvs.snapshot(ctx))
	}
	if err != nil {
		var ce *ctl.Error
		if errors.As(err, &ce) {
			return false, ce.Msg
		}
		s.d.Logger.Printf("dv networks: reload: %v", err)
		return false, "the digital modes engine did not take the new settings"
	}
	return true, ""
}

func (s *Server) handleDVNetworkPut(w http.ResponseWriter, r *http.Request, u store.User) {
	sp, ok := s.dvNetPick(w, r)
	if !ok {
		return
	}
	if s.d.DVEngine == nil {
		s.dvError(w, errDVUnavailable)
		return
	}
	var b struct {
		Enabled       *bool             `json:"enabled"`
		Address       *string           `json:"address"`
		Port          *int              `json:"port"`
		Password      *string           `json:"password"`
		ClearPassword bool              `json:"clearPassword"`
		Options       map[string]string `json:"options"`
	}
	if !decode(w, r, &b, false) {
		return
	}
	upd, err := dvpanel.CheckUpdate(sp.Mode, dvpanel.NetUpdate{Enabled: b.Enabled, Address: b.Address, Port: b.Port,
		Password: b.Password, ClearPassword: b.ClearPassword, Options: b.Options})
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, "invalid_field", err.Error())
		return
	}
	// what changed, for the audit line: names only, never a value
	var fields []string
	for name, set := range map[string]bool{"enabled": b.Enabled != nil, "address": b.Address != nil, "port": b.Port != nil,
		"password": b.Password != nil && *b.Password != "" || b.ClearPassword, "options": b.Options != nil} {
		if set {
			fields = append(fields, name)
		}
	}
	rep, err := s.d.DVEngine.DVNetworkSet(r.Context(), helper.DVNetworkRequest{Mode: sp.Mode, Enabled: upd.Enabled, Address: upd.Address,
		Port: upd.Port, Password: b.Password, ClearPassword: b.ClearPassword, Options: upd.Options})
	if err != nil {
		s.audit(u, "dv.network.refused", map[string]any{"mode": sp.Mode})
		s.dvError(w, err)
		return
	}
	s.audit(u, "dv.network", map[string]any{"mode": sp.Mode, "fields": fields})
	s.d.Logger.Printf("dv.network %s changed by %s", sp.Mode, u.Username)
	applied, why := s.dvApplyToEngine(r.Context(), rep.Rendered)
	for _, m := range s.dvNetReply(rep).Modes {
		if m.Mode == sp.Mode {
			writeJSON(w, 200, dvNetApplyResult{DVNetView: m, Applied: applied, ApplyError: why})
			return
		}
	}
}

func (s *Server) handleDVIdentityGet(w http.ResponseWriter, r *http.Request, _ store.User) {
	if s.d.DVEngine == nil {
		s.dvError(w, errDVUnavailable)
		return
	}
	rep, err := s.d.DVEngine.DVNetworkGet(r.Context())
	if err != nil {
		s.dvError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, DVIdentity(rep.Identity))
}

func (s *Server) handleDVIdentityPut(w http.ResponseWriter, r *http.Request, u store.User) {
	if s.d.DVEngine == nil {
		s.dvError(w, errDVUnavailable)
		return
	}
	var b DVIdentity
	if !decode(w, r, &b, false) {
		return
	}
	id, err := dvpanel.ValidateIdentity(dvpanel.IdentityState{Callsign: b.Callsign, DMRID: b.DMRID, RepeaterID: b.RepeaterID})
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, "invalid_field", err.Error())
		return
	}
	rep, err := s.d.DVEngine.DVNetworkSet(r.Context(), helper.DVNetworkRequest{Identity: &helper.DVIdentityView{Callsign: id.Callsign, DMRID: id.DMRID, RepeaterID: id.RepeaterID}})
	if err != nil {
		s.audit(u, "dv.identity.refused", nil)
		s.dvError(w, err)
		return
	}
	s.audit(u, "dv.identity", nil)
	s.d.Logger.Printf("dv.identity changed by %s", u.Username)
	applied, why := s.dvApplyToEngine(r.Context(), rep.Rendered)
	writeJSON(w, 200, struct {
		DVIdentity
		Applied    bool   `json:"applied"`
		ApplyError string `json:"applyError,omitempty"`
	}{DVIdentity(rep.Identity), applied, why})
}
