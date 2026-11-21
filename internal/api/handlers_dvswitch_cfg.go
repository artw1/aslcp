package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/artw1/aslcp/internal/helper"
	"github.com/artw1/aslcp/internal/store"
)

// DVSwitch credentials and config (docs/DVSWITCH.md, M4). The password is
// write-only: it is never in a reply, a log line or an audit value (the audit
// records the key name "password" only).

// dvRestartWarning accompanies every restart and start.
const dvRestartWarning = "After a restart DVSwitch starts in the last mode; with \"Resume after restart\" on, ASLCP also re-tunes the last talkgroup or reflector."

// dvSetupReply is the Setup view, the live ABInfo state and, after a write,
// what changed.
type dvSetupReply struct {
	helper.DVSwitchSetup
	Current     helper.DVSwitchInfo `json:"current"`
	Changed     []string            `json:"changed,omitempty"`
	Skipped     []string            `json:"skipped,omitempty"`
	RestartHint []string            `json:"restartHint,omitempty"`
	// BridgeNode is what the "Bridge node" section needs (M2).
	BridgeNode DVBridgeSetup `json:"bridgeNode"`
	// BMLogin (BrandMeister save): "logged_in" or "pending", STFU's login
	// after the restart. STFUStopFailed: STFU should be stopped again and
	// was not.
	BMLogin        string `json:"bmLogin,omitempty"`
	STFUStopFailed bool   `json:"stfuStopFailed,omitempty"`
}

func (s *Server) dvSetupView(w http.ResponseWriter, r *http.Request, res *helper.DVApplyResult) {
	v, err := s.d.DVSwitch.DVSwitchSetup(r.Context())
	if err != nil {
		s.dvswitchError(w, err)
		return
	}
	reply := dvSetupReply{DVSwitchSetup: v}
	reply.BridgeNode = s.dvBridgeSetup(r.Context(), v)
	// ABInfo is optional: a bridge that is stopped or a file in an
	// unexpected format leaves "found": false.
	if info, err := s.d.DVSwitch.DVSwitchInfo(r.Context()); err == nil {
		reply.Current = info
	}
	if res != nil {
		reply.Changed, reply.Skipped, reply.RestartHint = res.Files, res.Skipped, res.RestartHint
		reply.BMLogin, reply.STFUStopFailed = res.BMLogin, res.STFUStopFailed
	}
	writeJSON(w, 200, reply)
}

func (s *Server) handleDVSwitchSetup(w http.ResponseWriter, r *http.Request, _ store.User) {
	if s.d.DVSwitch == nil {
		s.dvswitchError(w, errDVUnavailable)
		return
	}
	s.dvSetupView(w, r, nil)
}

// dvWrite runs one write op: audit (key names and file ids only), log,
// reply with the Setup view.
func (s *Server) dvWrite(w http.ResponseWriter, r *http.Request, u store.User, action string, keys []string,
	call func(*Server, *http.Request) (helper.DVApplyResult, error)) {
	if len(keys) == 0 {
		// A request with no values is a no-op, not an error: nothing is
		// written, so nothing changes and nothing needs a restart.
		s.dvSetupView(w, r, &helper.DVApplyResult{})
		return
	}
	res, err := call(s, r)
	if err != nil {
		s.audit(u, action+".refused", map[string]any{"keys": keys})
		s.dvswitchError(w, err)
		return
	}
	s.audit(u, action, map[string]any{"keys": keys, "files": res.Files})
	s.d.Logger.Printf("%s by %s (%d files)", action, u.Username, len(res.Files))
	s.dvs.invalidate()
	s.dvs.refreshSoon()
	s.dvSetupView(w, r, &res)
}

func (s *Server) handleDVSwitchIdentity(w http.ResponseWriter, r *http.Request, u store.User) {
	if s.d.DVSwitch == nil {
		s.dvswitchError(w, errDVUnavailable)
		return
	}
	var b struct {
		Callsign string            `json:"callsign"`
		DMRID    string            `json:"dmrId"`
		ESSID    string            `json:"essid"`
		NXDNID   string            `json:"nxdnId"`
		Info     *helper.DVInfo    `json:"info"`
		Base     map[string]string `json:"base"`
	}
	if !decode(w, r, &b, false) {
		return
	}
	keys := []string{"callsign", "dmrId", "essid", "nxdnId"}
	if b.Info != nil {
		keys = append(keys, "info") // the key name, never the location
	}
	s.dvWrite(w, r, u, "dvswitch.identity", keys, func(s *Server, r *http.Request) (helper.DVApplyResult, error) {
		return s.d.DVSwitch.DVSwitchIdentity(r.Context(), helper.DVSwitchRequest{Base: b.Base, Author: u.Username,
			Identity: &helper.DVIdentity{Callsign: b.Callsign, DMRID: b.DMRID, ESSID: b.ESSID, NXDNID: b.NXDNID, Info: b.Info}})
	})
}

// handleDVSwitchCredentials: PUT /api/dvswitch/credentials {dmrAddress,
// dmrPort, dmrPassword, clearPassword, base}: DMR - Other's save (the older
// name of PUT /api/dvswitch/networks/DMR).
func (s *Server) handleDVSwitchCredentials(w http.ResponseWriter, r *http.Request, u store.User) {
	if s.d.DVSwitch == nil {
		s.dvswitchError(w, errDVUnavailable)
		return
	}
	var b struct {
		Address  *string           `json:"dmrAddress"`
		Port     *int              `json:"dmrPort"`
		Password *string           `json:"dmrPassword"`
		Clear    bool              `json:"clearPassword"`
		Base     map[string]string `json:"base"`
	}
	if !decode(w, r, &b, false) {
		return
	}
	s.dvNetworkSave(w, r, u, helper.DVModeDMR, b.Address, b.Port, b.Password, b.Clear, b.Base)
}

// handleDVSwitchNetwork: PUT /api/dvswitch/networks/{id}. BM takes the
// BrandMeister body (handleDVSwitchBMSave); an MMDVM_Bridge network
// {address?, port?, password?, clearPassword?, base} with the fields of its
// registry entry (TGIF: the password only, its master comes from
// DMR_Hosts.txt). The settings are stored by the helper (backed up);
// MMDVM_Bridge.ini is written only when [DMR Network] holds this network.
func (s *Server) handleDVSwitchNetwork(w http.ResponseWriter, r *http.Request, u store.User) {
	id := r.PathValue("id")
	if id == helper.DVModeBM {
		s.handleDVSwitchBMSave(w, r, u)
		return
	}
	n, ok := helper.DVNetworkByID(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "not_found", "no such DMR network")
		return
	}
	if s.d.DVSwitch == nil {
		s.dvswitchError(w, errDVUnavailable)
		return
	}
	var b struct {
		Address  *string           `json:"address"`
		Port     *int              `json:"port"`
		Password *string           `json:"password"`
		Clear    bool              `json:"clearPassword"`
		Base     map[string]string `json:"base"`
	}
	if !decode(w, r, &b, false) {
		return
	}
	s.dvNetworkSave(w, r, u, n.ID, b.Address, b.Port, b.Password, b.Clear, b.Base)
}

// dvNetworkSave saves an MMDVM_Bridge network. The audit names the keys,
// never a value.
func (s *Server) dvNetworkSave(w http.ResponseWriter, r *http.Request, u store.User, id string, addr *string, port *int, pw *string, clear bool, base map[string]string) {
	if pw != nil && clear {
		s.dvswitchError(w, fmt.Errorf("%w (network/password)", helper.ErrDVInvalid)) // both set
		return
	}
	var keys []string
	for _, k := range []struct {
		n  string
		on bool
	}{{"address", addr != nil}, {"port", port != nil}, {"password", pw != nil}, {"clearPassword", clear}} {
		if k.on {
			keys = append(keys, k.n) // the key name, never the value
		}
	}
	s.dvWrite(w, r, u, "dvswitch.network."+strings.ToLower(id), keys, func(s *Server, r *http.Request) (helper.DVApplyResult, error) {
		return s.d.DVSwitch.DVSwitchNetwork(r.Context(), helper.DVSwitchRequest{Base: base, Author: u.Username,
			Network: &helper.DVNetworkReq{ID: id, Action: helper.DVNetSave, Address: addr, Port: port, Password: pw, ClearPassword: clear}})
	})
}

// handleDVSwitchNetworkEnabled: PUT /api/dvswitch/networks/{id}/enabled
// {enabled, base}. Changes that network's flag only (BM: the BrandMeister
// Enable and Disable); MMDVM_Bridge's DMR Enable is on while any of its
// networks is enabled. Disable keeps the settings.
func (s *Server) handleDVSwitchNetworkEnabled(w http.ResponseWriter, r *http.Request, u store.User) {
	id := r.PathValue("id")
	if id == helper.DVModeBM {
		s.handleDVSwitchBMEnabled(w, r, u)
		return
	}
	n, ok := helper.DVNetworkByID(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "not_found", "no such DMR network")
		return
	}
	if s.d.DVSwitch == nil {
		s.dvswitchError(w, errDVUnavailable)
		return
	}
	var b struct {
		Enabled *bool             `json:"enabled"`
		Base    map[string]string `json:"base"`
	}
	if !decode(w, r, &b, false) {
		return
	}
	if b.Enabled == nil {
		s.dvswitchError(w, fmt.Errorf("%w (enabled)", helper.ErrDVInvalid))
		return
	}
	act, name := helper.DVNetDisable, ".disable"
	if *b.Enabled {
		act, name = helper.DVNetEnable, ".enable"
	}
	s.dvWrite(w, r, u, "dvswitch.network."+strings.ToLower(n.ID)+name, []string{"enabled"}, func(s *Server, r *http.Request) (helper.DVApplyResult, error) {
		return s.d.DVSwitch.DVSwitchNetwork(r.Context(), helper.DVSwitchRequest{Base: b.Base, Author: u.Username,
			Network: &helper.DVNetworkReq{ID: n.ID, Action: act}})
	})
}

// handleDVSwitchModes: PUT /api/dvswitch/modes {ysf?, p25?, nxdn?, base}.
// The DMR networks are enabled on their own endpoints.
func (s *Server) handleDVSwitchModes(w http.ResponseWriter, r *http.Request, u store.User) {
	if s.d.DVSwitch == nil {
		s.dvswitchError(w, errDVUnavailable)
		return
	}
	var b struct {
		YSF  *bool             `json:"ysf"`
		P25  *bool             `json:"p25"`
		NXDN *bool             `json:"nxdn"`
		DMR  *bool             `json:"dmr"` // refused: a DMR network's own Enable
		Base map[string]string `json:"base"`
	}
	if !decode(w, r, &b, false) {
		return
	}
	if b.DMR != nil {
		s.dvswitchError(w, fmt.Errorf("%w (modes/dmr: use /api/dvswitch/networks/{id}/enabled)", helper.ErrDVInvalid))
		return
	}
	var keys []string
	for _, k := range []struct {
		n string
		v *bool
	}{{"ysf", b.YSF}, {"p25", b.P25}, {"nxdn", b.NXDN}} {
		if k.v != nil {
			keys = append(keys, k.n)
		}
	}
	s.dvWrite(w, r, u, "dvswitch.modes", keys, func(s *Server, r *http.Request) (helper.DVApplyResult, error) {
		return s.d.DVSwitch.DVSwitchModes(r.Context(), helper.DVSwitchRequest{Base: b.Base, Author: u.Username,
			Modes: &helper.DVModes{YSF: b.YSF, P25: b.P25, NXDN: b.NXDN}})
	})
}

// handleDVSwitchBMSave: PUT /api/dvswitch/brandmeister {server, password,
// talkerAlias, startTg, stayLoggedIn, base}. Writes [STFU], restarts stfu to
// check the login and leaves it running only while the panel is in BM mode
// or stayLoggedIn is set (then it is also enabled). The password (write-only,
// omitted keeps the saved one) never reaches the audit or the log: they name
// the keys only.
func (s *Server) handleDVSwitchBMSave(w http.ResponseWriter, r *http.Request, u store.User) {
	if s.d.DVSwitch == nil {
		s.dvswitchError(w, errDVUnavailable)
		return
	}
	var b struct {
		Server      string            `json:"server"`
		Password    *string           `json:"password"`
		TalkerAlias *string           `json:"talkerAlias"`
		StartTG     *string           `json:"startTg"`
		Stay        *bool             `json:"stayLoggedIn"`
		Base        map[string]string `json:"base"`
	}
	if !decode(w, r, &b, false) {
		return
	}
	keys := []string{"brandmeister"}
	for _, k := range []struct {
		n  string
		on bool
	}{{"server", b.Server != ""}, {"password", b.Password != nil}, {"talkerAlias", b.TalkerAlias != nil}, {"startTg", b.StartTG != nil}, {"stayLoggedIn", b.Stay != nil}} {
		if k.on {
			keys = append(keys, k.n)
		}
	}
	s.dvWrite(w, r, u, "dvswitch.brandmeister", keys, func(s *Server, r *http.Request) (helper.DVApplyResult, error) {
		res, err := s.d.DVSwitch.DVSwitchBM(r.Context(), helper.DVSwitchRequest{Base: b.Base, Author: u.Username,
			BM: &helper.DVBM{Action: helper.DVBMSave, Server: b.Server, Password: b.Password, TalkerAlias: b.TalkerAlias, StartTG: b.StartTG, StayLoggedIn: b.Stay}})
		if err == nil {
			if b.Stay != nil {
				s.audit(u, "dvswitch.brandmeister.stay", map[string]any{"stayLoggedIn": *b.Stay})
			}
			if res.STFUStopFailed {
				s.d.Logger.Printf("dvswitch.brandmeister: stfu could not be stopped after the login check")
			}
		}
		return res, err
	})
}

// handleDVSwitchBMEnabled: PUT /api/dvswitch/brandmeister/enabled
// {enabled}. Disable stops and disables stfu and takes BM off the panel; the
// master, password and settings stay, so Enable restores it without
// retyping (Enable needs a saved password).
func (s *Server) handleDVSwitchBMEnabled(w http.ResponseWriter, r *http.Request, u store.User) {
	if s.d.DVSwitch == nil {
		s.dvswitchError(w, errDVUnavailable)
		return
	}
	var b struct {
		Enabled *bool `json:"enabled"`
	}
	if !decode(w, r, &b, false) {
		return
	}
	if b.Enabled == nil {
		s.dvswitchError(w, fmt.Errorf("%w (enabled)", helper.ErrDVInvalid))
		return
	}
	act, name := helper.DVBMDisable, "dvswitch.brandmeister.disable"
	if *b.Enabled {
		act, name = helper.DVBMEnable, "dvswitch.brandmeister.enable"
	}
	s.dvWrite(w, r, u, name, []string{"enabled"}, func(s *Server, r *http.Request) (helper.DVApplyResult, error) {
		return s.d.DVSwitch.DVSwitchBM(r.Context(), helper.DVSwitchRequest{Author: u.Username, BM: &helper.DVBM{Action: act}})
	})
}

func (s *Server) handleDVSwitchRollback(w http.ResponseWriter, r *http.Request, u store.User) {
	if s.d.DVSwitch == nil {
		s.dvswitchError(w, errDVUnavailable)
		return
	}
	var b struct {
		File string            `json:"file"`
		Base map[string]string `json:"base"`
	}
	if !decode(w, r, &b, false) {
		return
	}
	res, err := s.d.DVSwitch.DVSwitchRollback(r.Context(), helper.DVSwitchRequest{File: b.File, Base: b.Base, Author: u.Username})
	if err != nil {
		s.audit(u, "dvswitch.rollback.refused", map[string]any{"file": safeAuditName(b.File)})
		s.dvswitchError(w, err)
		return
	}
	s.audit(u, "dvswitch.rollback", map[string]any{"file": safeAuditName(b.File)})
	s.d.Logger.Printf("dvswitch.rollback by %s", u.Username)
	s.dvSetupView(w, r, &res)
}

// safeAuditName keeps a client-chosen file id out of the audit log unless it
// is one of the allowlisted ids.
func safeAuditName(id string) string {
	for _, k := range helper.DVFileIDs() {
		if k == id {
			return id
		}
	}
	return "?"
}

func (s *Server) handleDVSwitchRestart(w http.ResponseWriter, r *http.Request, u store.User) {
	if s.d.DVSwitch == nil {
		s.dvswitchError(w, errDVUnavailable)
		return
	}
	var b struct {
		Scope  string `json:"scope"`
		Action string `json:"action"`
	}
	if !decode(w, r, &b, false) {
		return
	}
	res, err := s.d.DVSwitch.DVSwitchRestart(r.Context(), helper.DVSwitchRequest{Scope: b.Scope, Action: b.Action, Author: u.Username})
	if err != nil {
		s.audit(u, "dvswitch.restart.refused", map[string]any{"scope": safeScope(b.Scope)})
		s.dvswitchError(w, err)
		return
	}
	s.audit(u, "dvswitch."+res.Action, map[string]any{"scope": safeScope(b.Scope), "units": res.Units})
	s.d.Logger.Printf("dvswitch.%s %s by %s", res.Action, b.Scope, u.Username)
	s.dvs.invalidate()
	s.dvs.afterCommand()
	writeJSON(w, 200, map[string]any{"action": res.Action, "units": res.Units, "skipped": res.Skipped, "states": res.States, "warning": dvRestartWarning})
}

func safeScope(s string) string {
	switch s {
	case "dmr", "bridge", "gateways", "all":
		return s
	}
	return "?"
}

// handleDVSwitchLists serves the host and TG list of one mode for the
// pickers: GET /api/dvswitch/lists?mode=dmr|ysf|p25|nxdn. The mode is an enum;
// the file behind it is fixed in the helper. A missing file is 404 no_list
// (the UI falls back to free text). The ETag is a hash of the file content.
func (s *Server) handleDVSwitchLists(w http.ResponseWriter, r *http.Request, _ store.User) {
	mode := r.URL.Query().Get("mode")
	if !helper.ValidDVListMode(mode) {
		writeErr(w, http.StatusBadRequest, "bad_request", "mode must be dmr, ysf, p25 or nxdn")
		return
	}
	if s.d.DVSwitch == nil {
		s.dvswitchError(w, errDVUnavailable)
		return
	}
	l, err := s.d.DVSwitch.DVSwitchLists(r.Context(), mode)
	if err != nil {
		s.dvswitchError(w, err)
		return
	}
	tag := `"` + mode + "-" + l.ETag + `"`
	w.Header().Set("ETag", tag)
	w.Header().Set("Cache-Control", "private, max-age=300")
	if inm := r.Header.Get("If-None-Match"); inm != "" && inm == tag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	writeJSON(w, 200, l)
}
