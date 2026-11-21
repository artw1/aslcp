package api

import (
	"fmt"
	"net/http"

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
	s.dvCredentials(w, r, u, "dvswitch.credentials", helper.DVProfileOther, b.Address, b.Port, b.Password, b.Clear, b.Base)
}

// dvCredentials writes the DMR - Other or the TGIF credentials (profile).
// The profile that is not on is stored by the helper and written to
// MMDVM_Bridge.ini when it is enabled.
func (s *Server) dvCredentials(w http.ResponseWriter, r *http.Request, u store.User, action, profile string, addr *string, port *int, pw *string, clear bool, base map[string]string) {
	b := struct {
		Address  *string
		Port     *int
		Password *string
		Clear    bool
		Base     map[string]string
	}{addr, port, pw, clear, base}
	if b.Password != nil && b.Clear {
		s.dvswitchError(w, fmt.Errorf("%w (DMR Network/Password)", helper.ErrDVInvalid)) // both set
		return
	}
	var keys []string
	if b.Address != nil {
		keys = append(keys, "dmrAddress")
	}
	if b.Port != nil {
		keys = append(keys, "dmrPort")
	}
	if b.Password != nil {
		keys = append(keys, "dmrPassword") // the key name, never the value
	}
	if b.Clear {
		keys = append(keys, "clearPassword")
	}
	s.dvWrite(w, r, u, action, keys, func(s *Server, r *http.Request) (helper.DVApplyResult, error) {
		return s.d.DVSwitch.DVSwitchCredentials(r.Context(), helper.DVSwitchRequest{Base: b.Base, Author: u.Username,
			Credentials: &helper.DVCredentials{Address: b.Address, Port: b.Port, Password: b.Password, ClearPassword: b.Clear, Profile: profile}})
	})
}

// handleDVSwitchTGIF: PUT /api/dvswitch/tgif {password?, clearPassword?,
// base}. TGIF's master comes from DMR_Hosts.txt and is not settable.
func (s *Server) handleDVSwitchTGIF(w http.ResponseWriter, r *http.Request, u store.User) {
	if s.d.DVSwitch == nil {
		s.dvswitchError(w, errDVUnavailable)
		return
	}
	var b struct {
		Password *string           `json:"password"`
		Clear    bool              `json:"clearPassword"`
		Base     map[string]string `json:"base"`
	}
	if !decode(w, r, &b, false) {
		return
	}
	s.dvCredentials(w, r, u, "dvswitch.tgif", helper.DVProfileTGIF, nil, nil, b.Password, b.Clear, b.Base)
}

// handleDVSwitchTGIFEnabled: PUT /api/dvswitch/tgif/enabled {enabled, base}.
// Enabling TGIF writes its master and password to [DMR Network] and turns
// DMR - Other off (they share MMDVM_Bridge's DMR network); DMR - Other's
// values are kept. Disabling turns DMR off and keeps the password.
func (s *Server) handleDVSwitchTGIFEnabled(w http.ResponseWriter, r *http.Request, u store.User) {
	s.dvProfileEnabled(w, r, u, "dvswitch.tgif.enabled", helper.DVProfileTGIF)
}

// dvProfileEnabled turns the DMR network on with profile, or off when it is
// the live one.
func (s *Server) dvProfileEnabled(w http.ResponseWriter, r *http.Request, u store.User, action, profile string) {
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
	s.dvWrite(w, r, u, action, []string{"enabled"}, func(s *Server, r *http.Request) (helper.DVApplyResult, error) {
		return s.dvModesWrite(r, u, helper.DVSwitchRequest{Base: b.Base, Author: u.Username,
			Modes: &helper.DVModes{DMR: b.Enabled, DMRProfile: profile}})
	})
}

// dvModesWrite runs DVSwitchModes and forgets the remembered DMR tune when
// the DMR profile changed: a talkgroup of one network does not carry over to
// the other.
func (s *Server) dvModesWrite(r *http.Request, u store.User, req helper.DVSwitchRequest) (helper.DVApplyResult, error) {
	res, err := s.d.DVSwitch.DVSwitchModes(r.Context(), req)
	if err == nil && res.DMRProfileChanged {
		s.dvForget("DMR")
	}
	return res, err
}

func (s *Server) handleDVSwitchModes(w http.ResponseWriter, r *http.Request, u store.User) {
	if s.d.DVSwitch == nil {
		s.dvswitchError(w, errDVUnavailable)
		return
	}
	var b struct {
		DMR  *bool `json:"dmr"`
		YSF  *bool `json:"ysf"`
		P25  *bool `json:"p25"`
		NXDN *bool `json:"nxdn"`
		// DMRProfile ("other", the default for this endpoint, or "tgif"): the
		// profile dmr turns on or off.
		DMRProfile string            `json:"dmrProfile"`
		Base       map[string]string `json:"base"`
	}
	if !decode(w, r, &b, false) {
		return
	}
	var keys []string
	for _, k := range []struct {
		n string
		v *bool
	}{{"dmr", b.DMR}, {"ysf", b.YSF}, {"p25", b.P25}, {"nxdn", b.NXDN}} {
		if k.v != nil {
			keys = append(keys, k.n)
		}
	}
	s.dvWrite(w, r, u, "dvswitch.modes", keys, func(s *Server, r *http.Request) (helper.DVApplyResult, error) {
		prof := b.DMRProfile
		if prof == "" && b.DMR != nil {
			prof = helper.DVProfileOther
		}
		return s.dvModesWrite(r, u, helper.DVSwitchRequest{Base: b.Base, Author: u.Username,
			Modes: &helper.DVModes{DMR: b.DMR, YSF: b.YSF, P25: b.P25, NXDN: b.NXDN, DMRProfile: prof}})
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
