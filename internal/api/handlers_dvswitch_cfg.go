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
const dvRestartWarning = "A restart resets the running mode and talkgroup to the startup values in the ini files."

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
	s.dvWrite(w, r, u, "dvswitch.credentials", keys, func(s *Server, r *http.Request) (helper.DVApplyResult, error) {
		return s.d.DVSwitch.DVSwitchCredentials(r.Context(), helper.DVSwitchRequest{Base: b.Base, Author: u.Username,
			Credentials: &helper.DVCredentials{Address: b.Address, Port: b.Port, Password: b.Password, ClearPassword: b.Clear}})
	})
}

func (s *Server) handleDVSwitchModes(w http.ResponseWriter, r *http.Request, u store.User) {
	if s.d.DVSwitch == nil {
		s.dvswitchError(w, errDVUnavailable)
		return
	}
	var b struct {
		DMR  *bool             `json:"dmr"`
		YSF  *bool             `json:"ysf"`
		P25  *bool             `json:"p25"`
		NXDN *bool             `json:"nxdn"`
		Base map[string]string `json:"base"`
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
		return s.d.DVSwitch.DVSwitchModes(r.Context(), helper.DVSwitchRequest{Base: b.Base, Author: u.Username,
			Modes: &helper.DVModes{DMR: b.DMR, YSF: b.YSF, P25: b.P25, NXDN: b.NXDN}})
	})
}

// handleDVSwitchDMRConn: PUT /api/dvswitch/dmr-connection {via, network,
// server, talkerAlias, startTg}. Switches how DMR reaches its network:
// "mmdvm_bridge" (MMDVM_Bridge's own DMR network) or "stfu" with network
// "brandmeister". The helper writes the inis, restarts what is needed and
// rolls back on failure; the BrandMeister password is taken from the saved
// credentials and never passes through here.
func (s *Server) handleDVSwitchDMRConn(w http.ResponseWriter, r *http.Request, u store.User) {
	if s.d.DVSwitch == nil {
		s.dvswitchError(w, errDVUnavailable)
		return
	}
	var b struct {
		Via         string            `json:"via"`
		Network     string            `json:"network"`
		Server      string            `json:"server"`
		TalkerAlias *string           `json:"talkerAlias"`
		StartTG     *string           `json:"startTg"`
		Base        map[string]string `json:"base"`
	}
	if !decode(w, r, &b, false) {
		return
	}
	keys := []string{"via"}
	for _, k := range []struct {
		n  string
		on bool
	}{{"network", b.Network != ""}, {"server", b.Server != ""}, {"talkerAlias", b.TalkerAlias != nil}, {"startTg", b.StartTG != nil}} {
		if k.on {
			keys = append(keys, k.n)
		}
	}
	res, err := s.d.DVSwitch.DVSwitchDMRVia(r.Context(), helper.DVSwitchRequest{Base: b.Base, Author: u.Username,
		DMRVia: &helper.DVDMRVia{Via: b.Via, Network: b.Network, Server: b.Server, TalkerAlias: b.TalkerAlias, StartTG: b.StartTG}})
	if err != nil {
		s.audit(u, "dvswitch.dmr_connection.refused", map[string]any{"keys": keys, "via": safeVia(b.Via)})
		s.dvswitchError(w, err)
		return
	}
	s.audit(u, "dvswitch.dmr_connection", map[string]any{"keys": keys, "via": safeVia(b.Via), "files": res.Files})
	s.d.Logger.Printf("dvswitch.dmr_connection %s by %s (%d files)", safeVia(b.Via), u.Username, len(res.Files))
	s.dvs.invalidate()
	s.dvs.refreshSoon()
	s.dvSetupView(w, r, &res)
}

// safeVia keeps a client value out of the audit unless it is a known method.
func safeVia(v string) string {
	if v == helper.DVViaMMDVM || v == helper.DVViaSTFU {
		return v
	}
	return "other"
}

func (s *Server) handleDVSwitchStartup(w http.ResponseWriter, r *http.Request, u store.User) {
	if s.d.DVSwitch == nil {
		s.dvswitchError(w, errDVUnavailable)
		return
	}
	var b struct {
		AmbeMode  *string           `json:"ambeMode"`
		TxTg      *string           `json:"txTg"`
		TxTs      *int              `json:"txTs"`
		ColorCode *int              `json:"colorCode"`
		YSF       *string           `json:"ysf"`
		P25       *string           `json:"p25"`
		NXDN      *string           `json:"nxdn"`
		Base      map[string]string `json:"base"`
	}
	if !decode(w, r, &b, false) {
		return
	}
	keys := []string{}
	for _, k := range []struct {
		n  string
		ok bool
	}{{"ambeMode", b.AmbeMode != nil}, {"txTg", b.TxTg != nil}, {"txTs", b.TxTs != nil}, {"colorCode", b.ColorCode != nil},
		{"ysf", b.YSF != nil}, {"p25", b.P25 != nil}, {"nxdn", b.NXDN != nil}} {
		if k.ok {
			keys = append(keys, k.n)
		}
	}
	s.dvWrite(w, r, u, "dvswitch.startup", keys, func(s *Server, r *http.Request) (helper.DVApplyResult, error) {
		return s.d.DVSwitch.DVSwitchStartup(r.Context(), helper.DVSwitchRequest{Base: b.Base, Author: u.Username,
			Startup: &helper.DVStartup{AmbeMode: b.AmbeMode, TxTg: b.TxTg, TxTs: b.TxTs, ColorCode: b.ColorCode,
				YSF: b.YSF, P25: b.P25, NXDN: b.NXDN}})
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
