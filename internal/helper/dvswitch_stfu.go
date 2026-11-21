package helper

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/artw1/aslcp/internal/dvswitch/ini"
)

// BrandMeister through STFU and "DMR - Other" through MMDVM_Bridge
// (docs/DVSWITCH.md, "BrandMeister and DMR - Other"). The two are separate
// DMR connections that may run at the same time:
//
//   - BrandMeister: STFU, a client of the Open DMR Terminal protocol (it
//     shows in BrandMeister SelfCare under "DMR Terminals" as "Open DMR
//     Terminal"), on its own TLV ports from DVSwitch.ini [STFU];
//   - DMR - Other: MMDVM_Bridge's own DMR network ([DMR Network] of
//     MMDVM_Bridge.ini) on the [DMR] TLV ports, for any master that is not
//     BrandMeister.
//
// Switching between them is the ordinary runtime mode switch of
// Analog_Bridge (ambeMode=DMR with the section's ports, dvswitch_tune.go):
// no file is rewritten and nothing restarts. Analog_Bridge reports
// ambe_mode DMR for both; which one is live follows from its TLV ports
// (dvPanel).

// Operation names.
const (
	OpDVSwitchBM          = "dvswitch-bm"           // save, enable or disable the BrandMeister connection
	OpDVSwitchSTFUInstall = "dvswitch-stfu-install" // apt-get install stfu in a transient unit
)

// Actions of dvswitch-bm.
const (
	DVBMSave    = "save"    // write [STFU]; enabled: (re)start stfu and check the login
	DVBMEnable  = "enable"  // turn BrandMeister on in ASLCP with the saved settings
	DVBMDisable = "disable" // turn it off: stop and disable stfu, keep the settings
)

// DMR connections (DVDMRStatus.Via).
const (
	DVViaMMDVM = "mmdvm_bridge"
	DVViaSTFU  = "stfu"
)

// Panel modes of BrandMeister and DMR - Other (DVNetworks).
const (
	DVModeBM  = "BM"
	DVModeDMR = "DMR"
)

const (
	// DefaultDVSTFUServer is the BrandMeister server STFU logs in to when
	// none was set.
	DefaultDVSTFUServer = "3102.repeater.net"
	// DefaultDVSTFULog is STFU's log; at startup it logs every setting,
	// the BM password included, so it is read with those lines dropped and
	// is made root-only (0600) when STFU is enabled.
	DefaultDVSTFULog = "/var/log/dvswitch/STFU.log"
	// DefaultDVSTFUDropin makes STFU (which runs as root) create its log
	// files 0600: logrotate has `nocreate`, so STFU recreates the log after
	// each rotation with its own umask.
	DefaultDVSTFUDropin = "/etc/systemd/system/stfu.service.d/aslcp.conf"

	dvSTFUSection     = "STFU"
	dvSTFULocal       = "127.0.0.1"
	dvSTFUDefaultPort = 54006
	dvSTFUTx          = 36100 // [STFU] txPort: STFU sends here, Analog_Bridge listens (rxport)
	dvSTFURx          = 36103 // [STFU] rxPort: STFU listens here, Analog_Bridge sends (txport)
	dvSTFUStartTG     = "4000"
	dvSTFUPkg         = "stfu"
	dvSTFUUnit        = "stfu"
	dvSTFUJobUnit     = "aslcp-dvswitch-stfu"
	dvSTFUSimFile     = "stfu-sim.txt"
	dvTalkerAliasMax  = 27

	// StepSTFUPlan is the dependency guard of the STFU install unit.
	StepSTFUPlan = "stfu-plan"
)

// Errors (mapped back by the client, client.go).
var (
	ErrDVSTFUNotInstalled = errors.New("stfu_not_installed: STFU is not installed, or DVSwitch.ini has no [STFU] section")
	ErrDVSTFUInstalled    = errors.New("stfu_installed: STFU is already installed")
	ErrDVNoIdentity       = errors.New("no_identity: save the callsign and DMR ID first")
	ErrDVNoPassword       = errors.New("no_password: enter the BrandMeister hotspot security password")
	ErrDVBMMaster         = errors.New("bm_master: BrandMeister masters are set up under BrandMeister, not on another DMR network's card")
	ErrDVSwitchFailed     = errors.New("switch_failed: the BrandMeister connection could not be changed; the previous settings were restored")
	ErrDVModeDisabled     = errors.New("mode_disabled: this mode is disabled; enable it on the DVSwitch page first")
	ErrDVBMNotConfigured  = errors.New("bm_not_configured: save the BrandMeister password first")
)

// DVBM is the argument of dvswitch-bm. Save: Server is the BrandMeister
// server STFU logs in to ("" keeps the saved one, or the default); its port
// is always 54006. Password is the hotspot security password of SelfCare
// (nil keeps the saved one; write-only). Nil TalkerAlias and StartTG keep
// the saved value (or the default: the callsign and 4000). Enable and
// Disable take no other field.
type DVBM struct {
	Action      string  `json:"action"`
	Server      string  `json:"server,omitempty"`
	Password    *string `json:"password,omitempty"`
	TalkerAlias *string `json:"talkerAlias,omitempty"`
	StartTG     *string `json:"startTg,omitempty"`
	// StayLoggedIn (nil keeps the saved value, default off) keeps STFU
	// enabled and running in every panel mode; off, STFU runs only while the
	// panel is in BM mode (dvswitch_stfu_demand.go).
	StayLoggedIn *bool `json:"stayLoggedIn,omitempty"`
}

func (o *Ops) dvSTFULogPath() string    { return orDefault(o.DVSTFULog, DefaultDVSTFULog) }
func (o *Ops) dvSTFUDropinPath() string { return orDefault(o.DVSTFUDropin, DefaultDVSTFUDropin) }
func (o *Ops) dvSTFUSimPath() string    { return filepath.Join(o.dvStateDir(), dvSTFUSimFile) }

// ---- validators

var reDVHostLabel = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

// vHostname accepts a DNS name or dotted IPv4 address: labels of letters,
// digits and inner hyphens.
func vHostname(s string) bool {
	if !reDVHost.MatchString(s) {
		return false
	}
	for _, l := range strings.Split(s, ".") {
		if !reDVHostLabel.MatchString(l) {
			return false
		}
	}
	return true
}

// vTalkerAlias: 1 to 27 printable ASCII characters without quotes, ';', '#'
// or '=' and without blanks at either end.
func vTalkerAlias(s string) bool {
	if s == "" || len(s) > dvTalkerAliasMax || s != strings.TrimSpace(s) {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c > 0x7e || strings.IndexByte(`"';#=`, c) >= 0 {
			return false
		}
	}
	return true
}

// dvBMDomains are the host name suffixes of BrandMeister's servers.
var dvBMDomains = []string{"brandmeister.network", "repeater.net"}

// DVBMHostName reports whether host is one of BrandMeister's by name
// (*.brandmeister.network, *.repeater.net).
func DVBMHostName(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	for _, d := range dvBMDomains {
		if h == d || strings.HasSuffix(h, "."+d) {
			return true
		}
	}
	return false
}

// ---- hooks of the other config ops

// dvBMIdentity: with BrandMeister configured ([STFU] holds a password), a
// new DMR ID goes to [STFU] UserID too.
func (o *Ops) dvBMIdentity(dmrID string) []dvChange {
	def, _ := dvFileByID(DVFileDVSwitch)
	st, err := o.dvRead(def)
	if err != nil || !st.regular {
		return nil
	}
	f := ini.Parse(st.data)
	if pw, _ := f.GetFold(dvSTFUSection, "BMPassword"); f.HasSection(dvSTFUSection) && dvPasswordSet(pw) {
		return []dvChange{{DVFileDVSwitch, dvSTFUSection, "UserID", dmrID}}
	}
	return nil
}

// dvPasswordSet: a password that is not empty, not the stock one and valid.
func dvPasswordSet(pw string) bool { return pw != "" && pw != dvStockPassword && vPassword(pw) }

// ---- setup view

// DVBMView is the BrandMeister part of the setup view. Installed: the stfu
// unit is there; Present: DVSwitch.ini has [STFU]; UnitEnabled: the unit is
// enabled at boot (only with StayLoggedIn: on demand it stays disabled).
// Configured: installed, present, a saved password and a 7-digit UserID.
// Enabled: BrandMeister is turned on in ASLCP (the card's Enable/Disable,
// off by default); the panel offers BrandMeister when it is configured and
// enabled. StayLoggedIn: STFU stays logged in outside BM mode (the saved
// setting; it applies only while enabled). The password only as
// PasswordSet.
type DVBMView struct {
	Installed    bool   `json:"installed"`
	Present      bool   `json:"present"`
	UnitEnabled  bool   `json:"unitEnabled"`
	Enabled      bool   `json:"enabled"`
	StayLoggedIn bool   `json:"stayLoggedIn"`
	Configured   bool   `json:"configured"`
	Server       string `json:"server"`
	Port         int    `json:"port"`
	TalkerAlias  string `json:"talkerAlias"`
	StartTG      string `json:"startTg"`
	TxPort       int    `json:"txPort"`
	RxPort       int    `json:"rxPort"`
	PasswordSet  bool   `json:"passwordSet"`
}

// dvBMUnit reports whether the stfu unit is loaded and enabled.
func (o *Ops) dvBMUnit(ctx context.Context) (loaded, enabled bool) {
	loaded, enabled, _ = o.dvBMUnitState(ctx)
	return loaded, enabled
}

// dvBMUnitState is dvBMUnit plus whether the unit is active.
func (o *Ops) dvBMUnitState(ctx context.Context) (loaded, enabled, active bool) {
	for _, u := range o.dvUnitStates(ctx) {
		if u.Name == dvSTFUUnit {
			return u.Loaded, u.Loaded && u.Enabled == "enabled", u.Loaded && dvUnitRunning(u.Active)
		}
	}
	return false, false, false
}

// dvBMConfigured reports whether the stfu unit is loaded and BrandMeister is
// configured (see DVBMView), whatever the unit's state.
func (o *Ops) dvBMConfigured(ctx context.Context) (loaded, configured bool) {
	loaded, enabled := o.dvBMUnit(ctx)
	if !loaded {
		return false, false
	}
	def, _ := dvFileByID(DVFileDVSwitch)
	st, err := o.dvRead(def)
	if err != nil || !st.regular {
		return true, false
	}
	return true, dvBMView(ini.Parse(st.data), loaded, enabled, o.dvSTFUStay()).Configured
}

func dvBMView(dv *ini.File, loaded, enabled, stay bool) DVBMView {
	v := DVBMView{Installed: loaded, UnitEnabled: enabled, StayLoggedIn: stay}
	if dv == nil || !dv.HasSection(dvSTFUSection) {
		return v
	}
	get := func(k string) string { s, _ := dv.GetFold(dvSTFUSection, k); return s }
	atoi := func(s string) int { n, _ := strconv.Atoi(s); return n }
	v.Present = true
	v.Server, v.Port, v.TalkerAlias, v.StartTG = get("BMAddress"), atoi(get("BMPort")), get("TalkerAlias"), get("StartTG")
	v.TxPort, v.RxPort = atoi(get("txPort")), atoi(get("rxPort"))
	v.PasswordSet = dvPasswordSet(get("BMPassword"))
	v.Configured = loaded && v.PasswordSet && reDVDigits7.MatchString(get("UserID"))
	return v
}

// ---- ports

// dvIniPort reads a TLV port (1024-65535) case-insensitively; 0 when unset
// or invalid.
func dvIniPort(f *ini.File, sec, key string) int {
	if f == nil {
		return 0
	}
	v, _ := f.GetFold(sec, key)
	if !vInt(1024, 65535)(v) {
		return 0
	}
	n, _ := strconv.Atoi(v)
	return n
}

// dvSTFUPortsFree reports whether STFU's stock TLV ports 36100/36103 clash
// with nothing else: the other DVSwitch.ini sections, the stock mode ports,
// the park port, Analog_Bridge's USRP and AMBE ports.
func dvSTFUPortsFree(dv, ab *ini.File) bool {
	used := map[int]bool{dvTLVSwitchPort: true}
	for _, p := range dvTLVPorts {
		used[p] = true
	}
	for _, sec := range dv.Sections() {
		if !strings.EqualFold(sec, dvSTFUSection) {
			used[dvIniPort(dv, sec, "txPort")], used[dvIniPort(dv, sec, "rxPort")] = true, true
		}
	}
	for _, k := range [][2]string{{"USRP", "txPort"}, {"USRP", "rxPort"}, {"AMBE_AUDIO", "ambePort"}} {
		used[dvIniPort(ab, k[0], k[1])] = true
	}
	return !used[dvSTFUTx] && !used[dvSTFURx]
}

// ---- dvswitch-bm

// dvBMFiles are the parsed files the op reads.
type dvBMFiles struct {
	mm, dv, ab *ini.File
}

func (o *Ops) dvBMRead(needSTFU bool) (dvBMFiles, error) {
	var out dvBMFiles
	for _, x := range []struct {
		id string
		f  **ini.File
	}{{DVFileMMDVMBridge, &out.mm}, {DVFileAnalogBridge, &out.ab}, {DVFileDVSwitch, &out.dv}} {
		def, _ := dvFileByID(x.id)
		st, err := o.dvRead(def)
		if err != nil {
			return out, fmt.Errorf("cannot read %s: %w", def.id, err)
		}
		if !st.regular {
			if x.id == DVFileDVSwitch {
				if needSTFU {
					return out, ErrDVSTFUNotInstalled
				}
				continue
			}
			return out, ErrDVNotInstalled
		}
		*x.f = ini.Parse(st.data)
	}
	if needSTFU && !out.dv.HasSection(dvSTFUSection) {
		return out, ErrDVSTFUNotInstalled
	}
	return out, nil
}

func dvSectionPorts(f *ini.File, sec string) [2]int {
	return [2]int{dvIniPort(f, sec, "txPort"), dvIniPort(f, sec, "rxPort")}
}

// DVSwitchBM saves, clears or moves to the BrandMeister connection.
func (o *Ops) DVSwitchBM(ctx context.Context, req *DVSwitchRequest) (DVApplyResult, error) {
	req, err := o.dvArgs(req)
	if err != nil {
		return DVApplyResult{}, err
	}
	b := req.BM
	if b == nil || req.Identity != nil || req.Credentials != nil || req.Modes != nil || req.Tune != nil || req.USRP != nil || req.Private != nil {
		return DVApplyResult{}, errors.New("invalid request: brandmeister only")
	}
	o.bmMu.Lock()
	defer o.bmMu.Unlock()
	switch b.Action {
	case DVBMSave:
		return o.dvBMSave(ctx, b, req.Base)
	case DVBMEnable, DVBMDisable:
		if b.Server != "" || b.Password != nil || b.TalkerAlias != nil || b.StartTG != nil || b.StayLoggedIn != nil {
			return DVApplyResult{}, dvInvalid("brandmeister")
		}
		if b.Action == DVBMEnable {
			return o.dvBMEnable(ctx)
		}
		return o.dvBMDisable(ctx)
	}
	return DVApplyResult{}, dvInvalid("brandmeister/action")
}

func (o *Ops) dvBMSave(ctx context.Context, b *DVBM, base map[string]string) (DVApplyResult, error) {
	if b.Server != "" && !vHostname(b.Server) {
		return DVApplyResult{}, dvInvalid("brandmeister/server")
	}
	if b.Password != nil && !dvPasswordSet(*b.Password) {
		return DVApplyResult{}, dvInvalid("brandmeister/password")
	}
	if b.TalkerAlias != nil && !vTalkerAlias(*b.TalkerAlias) {
		return DVApplyResult{}, dvInvalid("brandmeister/talkerAlias")
	}
	if b.StartTG != nil && !reDVTG.MatchString(*b.StartTG) {
		return DVApplyResult{}, dvInvalid("brandmeister/startTg")
	}
	f, err := o.dvBMReady(ctx)
	if err != nil {
		return DVApplyResult{}, err
	}
	if b.Password == nil {
		if pw, _ := f.dv.GetFold(dvSTFUSection, "BMPassword"); !dvPasswordSet(pw) {
			return DVApplyResult{}, ErrDVNoPassword
		}
	}
	if !dvSTFUPortsFree(f.dv, f.ab) {
		return DVApplyResult{}, dvInvalid("brandmeister/ports")
	}
	call, _ := f.mm.Get("General", "Callsign")
	id, _ := f.mm.Get("General", "Id")
	cur := func(k string, ok func(string) bool, def string) string {
		if v, _ := f.dv.GetFold(dvSTFUSection, k); ok(v) {
			return v
		}
		return def
	}
	server := b.Server
	if server == "" {
		server = cur("BMAddress", vHostname, DefaultDVSTFUServer)
	}
	alias := call
	if len(alias) > dvTalkerAliasMax {
		alias = alias[:dvTalkerAliasMax]
	}
	alias = cur("TalkerAlias", vTalkerAlias, alias)
	if b.TalkerAlias != nil {
		alias = *b.TalkerAlias
	}
	startTG := cur("StartTG", reDVTG.MatchString, dvSTFUStartTG)
	if b.StartTG != nil {
		startTG = *b.StartTG
	}
	prevStay := o.dvSTFUStay()
	stay := prevStay
	if b.StayLoggedIn != nil {
		stay = *b.StayLoggedIn
	}
	oldPorts := dvSectionPorts(f.dv, dvSTFUSection)
	set := func(k, v string) dvChange { return dvChange{DVFileDVSwitch, dvSTFUSection, k, v} }
	ch := []dvChange{
		set("BMAddress", server),
		set("BMPort", strconv.Itoa(dvSTFUDefaultPort)),
		set("Address", dvSTFULocal),
		set("txPort", strconv.Itoa(dvSTFUTx)),
		set("rxPort", strconv.Itoa(dvSTFURx)),
		set("UserID", id[:7]),
		set("TalkerAlias", alias),
		set("StartTG", startTG),
	}
	if b.Password != nil {
		ch = append(ch, set("BMPassword", *b.Password))
	}

	if !o.dvBMEnabled() {
		// Disabled: the settings are saved for a later Enable; stfu is not
		// touched (it is stopped and stays so).
		res, err := o.dvApply(ctx, ch, base)
		if err != nil {
			return res, err
		}
		if err := o.dvSTFUStaySet(stay); err != nil {
			o.dvBMUndo(ctx, res.Files)
			return DVApplyResult{}, fmt.Errorf("%w (state)", ErrDVSwitchFailed)
		}
		res.RestartHint = []string{}
		return res, nil
	}
	_, wasEnabled, wasActive := o.dvBMUnitState(ctx)
	res, err := o.dvApply(ctx, ch, base)
	if err != nil {
		return res, err
	}
	return o.dvBMActivate(ctx, res, stay, prevStay, oldPorts, wasEnabled, wasActive)
}

// dvBMActivate is the tail of a save or an Enable: STFU's log made
// root-only, the umask drop-in, the stay setting, the unit enabled at boot
// (stay) or not (on demand), a restart and the login check. A failure puts
// everything back, the files the op wrote (res.Files) included.
func (o *Ops) dvBMActivate(ctx context.Context, res DVApplyResult, stay, prevStay bool, oldPorts [2]int, wasEnabled, wasActive bool) (DVApplyResult, error) {
	_, dropErr := os.Lstat(o.dvSTFUDropinPath())
	hadDropin := dropErr == nil
	// On demand (the default) the unit stays disabled and ASLCP starts it
	// when the panel enters BM; with "stay logged in" it is enabled.
	boot := "disable"
	if stay {
		boot = "enable"
	}
	step := ""
	switch {
	case o.dvSTFULogPrep() != nil:
		step = "log"
	case !o.dvSTFUDropinWrite(ctx):
		step = "umask"
	case o.dvSTFUStaySet(stay) != nil:
		step = "state"
	case !o.dvSystemctl(ctx, boot, dvSTFUUnit+".service") || !o.dvSystemctl(ctx, "restart", dvSTFUUnit+".service"):
		step = dvSTFUUnit
	}
	if step != "" {
		o.dvBMUndo(ctx, res.Files)
		if !hadDropin {
			o.dvSTFUDropinRemove(ctx)
		}
		_ = o.dvSTFUStaySet(prevStay)
		switch {
		case wasEnabled:
			o.dvSystemctl(ctx, "enable", dvSTFUUnit+".service")
			o.dvSystemctl(ctx, "restart", dvSTFUUnit+".service")
		case wasActive:
			o.dvSystemctl(ctx, "disable", dvSTFUUnit+".service")
			o.dvSystemctl(ctx, "restart", dvSTFUUnit+".service")
		default:
			o.dvSystemctl(ctx, "disable", "--now", dvSTFUUnit+".service")
		}
		return DVApplyResult{}, fmt.Errorf("%w (%s)", ErrDVSwitchFailed, step)
	}
	if oldPorts != [2]int{dvSTFUTx, dvSTFURx} {
		o.dvRepointDMR(ctx, oldPorts, dvSTFUTx, dvSTFURx)
	}
	res.RestartHint = []string{}
	res.BMLogin, res.STFUStopFailed = o.dvBMVerify(ctx, stay)
	return res, nil
}

// dvBMReady checks what a save needs: stfu installed, [STFU] in
// DVSwitch.ini, the identity saved.
func (o *Ops) dvBMReady(ctx context.Context) (dvBMFiles, error) {
	have, err := o.dpkgInstalled(ctx, []string{dvSTFUPkg})
	if err != nil {
		return dvBMFiles{}, err
	}
	if _, ok := have[dvSTFUPkg]; !ok {
		return dvBMFiles{}, ErrDVSTFUNotInstalled
	}
	f, err := o.dvBMRead(true)
	if err != nil {
		return f, err
	}
	call, _ := f.mm.Get("General", "Callsign")
	id, _ := f.mm.Get("General", "Id")
	if !reDVCall.MatchString(call) || !reDVDigits9.MatchString(id) && !reDVDigits7.MatchString(id) {
		return f, ErrDVNoIdentity
	}
	return f, nil
}

// dvBMEnable turns BrandMeister on with the saved settings: nothing is
// typed again. It needs a configured [STFU] (password and UserID saved),
// then starts stfu like a save does and checks the login (on demand stfu is
// stopped again after the check).
func (o *Ops) dvBMEnable(ctx context.Context) (DVApplyResult, error) {
	f, err := o.dvBMReady(ctx)
	if err != nil {
		return DVApplyResult{}, err
	}
	if !dvBMView(f.dv, true, false, false).Configured {
		return DVApplyResult{}, ErrDVBMNotConfigured
	}
	if o.dvBMEnabled() {
		return dvNoChange(), nil
	}
	if err := o.dvBMEnabledSet(true); err != nil {
		return DVApplyResult{}, fmt.Errorf("%w (state)", ErrDVSwitchFailed)
	}
	stay := o.dvSTFUStay()
	_, wasEnabled, wasActive := o.dvBMUnitState(ctx)
	res, err := o.dvBMActivate(ctx, dvNoChange(), stay, stay, [2]int{dvSTFUTx, dvSTFURx}, wasEnabled, wasActive)
	if err != nil {
		_ = o.dvBMEnabledSet(false)
		o.dvSystemctl(ctx, "disable", "--now", dvSTFUUnit+".service")
		return res, err
	}
	res.Files = []string{DVStateBMEnabled}
	return res, nil
}

// DVStateBMEnabled is the id the BrandMeister Enable and Disable report in
// DVApplyResult.Files: the helper's flag changed, no ini file.
const DVStateBMEnabled = "bm_enabled"

// dvBMDisable turns BrandMeister off and keeps its settings: stfu is stopped
// and disabled, the [STFU] values, the password and the stay setting stay
// for the next Enable. A live session on STFU's ports moves to [DMR]'s.
func (o *Ops) dvBMDisable(ctx context.Context) (DVApplyResult, error) {
	f, err := o.dvBMRead(false)
	if err != nil {
		return DVApplyResult{}, err
	}
	if !o.dvBMEnabled() {
		// already off; make sure stfu is not left running
		if loaded, _ := o.dvBMUnit(ctx); loaded {
			o.dvSystemctl(ctx, "disable", "--now", dvSTFUUnit+".service")
		}
		return dvNoChange(), nil
	}
	if loaded, _ := o.dvBMUnit(ctx); loaded && !o.dvSystemctl(ctx, "disable", "--now", dvSTFUUnit+".service") {
		return DVApplyResult{}, fmt.Errorf("%w (%s)", ErrDVSwitchFailed, dvSTFUUnit)
	}
	if err := o.dvBMEnabledSet(false); err != nil {
		return DVApplyResult{}, fmt.Errorf("%w (state)", ErrDVSwitchFailed)
	}
	if f.dv != nil && f.dv.HasSection(dvSTFUSection) {
		stfu, dmr := dvSectionPorts(f.dv, dvSTFUSection), dvSectionPorts(f.dv, "DMR")
		if stfu[0] != 0 && dmr[0] != 0 && dmr[1] != 0 {
			o.dvRepointDMR(ctx, stfu, dmr[0], dmr[1])
		}
	}
	res := dvNoChange()
	res.Files = []string{DVStateBMEnabled}
	return res, nil
}

// dvBMUndo puts back the newest history copy of each file the op wrote (the
// copy dvApply just made).
func (o *Ops) dvBMUndo(ctx context.Context, files []string) {
	for _, id := range files {
		_, _ = o.DVSwitchRollback(ctx, &DVSwitchRequest{File: id})
	}
}

// ---- panel mode

// DVPanelMode is the panel's mode of a bridge state: a DMR network of the
// registry (BM, TGIF, DMR; all ambe_mode DMR, told apart by the TLV ports
// and [DMR Network], see dvPanel), YSF, P25, NXDN; "" for a mode the panel
// does not handle. A state without Panel (a test fake, an older helper)
// falls back to the AMBE mode.
func DVPanelMode(info DVSwitchInfo) string {
	if m := DVNormMode(info.Panel); m != "" {
		return m
	}
	return DVNormMode(info.Mode)
}

// DVDMRFamily: the DMR networks of the registry are all DMR on the air
// (talkgroups, private calls, slot).
func DVDMRFamily(mode string) bool {
	_, ok := DVNetworkByID(mode)
	return ok
}

// dvPanel sets info.Panel: in ambe_mode DMR, BM when Analog_Bridge's live TLV
// ports are [STFU]'s (it sends to STFU's rxPort and listens on its txPort),
// else the MMDVM_Bridge network [DMR Network] holds (dvMMDVMLive). [STFU]
// ports equal to [DMR]'s cannot be told apart and read as MMDVM_Bridge.
func (o *Ops) dvPanel(info *DVSwitchInfo) {
	info.Panel = DVNormMode(info.Mode)
	if info.Panel != DVModeDMR {
		return
	}
	if o.dvPanelOnSTFU(info) {
		info.Panel = DVModeBM
		return
	}
	info.Panel = o.dvMMDVMLive()
}

func (o *Ops) dvPanelOnSTFU(info *DVSwitchInfo) bool {
	if info.tlvTx == 0 {
		return false
	}
	tx, rx, err := o.dvModePorts(dvSTFUSection)
	if err != nil || info.tlvTx != rx || info.tlvRx != tx {
		return false
	}
	if dtx, drx, err := o.dvModePorts("DMR"); err == nil && dtx == tx && drx == rx {
		return false
	}
	return true
}

// ---- unit and file helpers

// dvSystemctl runs one fixed systemctl command and reports success. The
// output is not passed on.
func (o *Ops) dvSystemctl(ctx context.Context, args ...string) bool {
	_, code, err := o.Run.Run(ctx, append([]string{"systemctl"}, args...)...)
	return err == nil && code == 0
}

// dvSTFULogPrep makes STFU's log a root-only file before STFU starts: it
// logs the BM password at startup. A missing file is created (0600); a
// symlink or other non-regular file stops the switch. Rotated copies
// (STFU.log.1, STFU.log.2.gz, ...) are made 0600 too.
func (o *Ops) dvSTFULogPrep() error {
	p := o.dvSTFULogPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	fd, err := os.OpenFile(p, os.O_RDONLY|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return err
	}
	defer fd.Close()
	fi, err := fd.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		return errors.New("STFU.log is not a regular file")
	}
	if err := fd.Chmod(0o600); err != nil {
		return err
	}
	rot, _ := filepath.Glob(p + ".*")
	for _, r := range rot {
		o.dvChmodRegular(r)
	}
	return nil
}

// dvChmodRegular sets 0600 on a regular file; a symlink or any other
// file type is left alone (O_NOFOLLOW, checked on the open descriptor).
func (o *Ops) dvChmodRegular(path string) {
	fd, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return
	}
	defer fd.Close()
	if fi, err := fd.Stat(); err == nil && fi.Mode().IsRegular() {
		_ = fd.Chmod(0o600)
	}
}

const dvSTFUDropinText = "[Service]\nUMask=0077\n"

// dvSTFUDropinWrite installs the drop-in that makes STFU create its log
// files 0600 (STFU runs as root; logrotate does not recreate the log), then
// reloads systemd. Takes effect at STFU's next start.
func (o *Ops) dvSTFUDropinWrite(ctx context.Context) bool {
	p := o.dvSTFUDropinPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return false
	}
	if b, err := os.ReadFile(p); err == nil && string(b) == dvSTFUDropinText {
		return true
	}
	if err := writeStateFile(p, []byte(dvSTFUDropinText), 0o644); err != nil {
		return false
	}
	return o.dvSystemctl(ctx, "daemon-reload")
}

// dvSTFUDropinRemove removes the drop-in (and its directory when empty) and
// reloads systemd. Nothing to remove is success.
func (o *Ops) dvSTFUDropinRemove(ctx context.Context) bool {
	p := o.dvSTFUDropinPath()
	if err := os.Remove(p); err != nil {
		return errors.Is(err, fs.ErrNotExist)
	}
	_ = os.Remove(filepath.Dir(p))
	return o.dvSystemctl(ctx, "daemon-reload")
}

// dvRepointDMR points a running Analog_Bridge that is in DMR on the partner
// with the from ports (DVSwitch.ini txPort, rxPort) at the partner with
// these ports (its txport is the partner's rxPort), like a mode switch.
// Best effort: Analog_Bridge in another mode or on other ports is left
// alone.
func (o *Ops) dvRepointDMR(ctx context.Context, from [2]int, tx, rx int) bool {
	unlock, err := o.lockTune(ctx)
	if err != nil {
		return false
	}
	defer unlock()
	info, err := o.DVSwitchInfo(ctx)
	if err != nil || !info.Found || info.tlvRx == 0 || DVNormMode(info.Mode) != "DMR" {
		return false
	}
	if info.tlvTx == rx && info.tlvRx == tx {
		return true
	}
	if info.tlvTx != from[1] || info.tlvRx != from[0] {
		return false
	}
	_, err = o.dvTLVSend(ctx, info.tlvRx, o.dvSwitchCmds("DMR", tx, rx))
	return err == nil
}

// ---- STFU install

// DVSTFUStatus is the STFU part of dvswitch-status.
type DVSTFUStatus struct {
	Installed  bool        `json:"installed"`
	Version    string      `json:"version,omitempty"`
	CanInstall bool        `json:"canInstall"`
	Job        DVSwitchJob `json:"job"`
}

func (o *Ops) dvSTFUJob(ctx context.Context) DVSwitchJob {
	var j DVSwitchJob
	j.Job, j.Log = o.jobUnit(ctx, dvSTFUJobUnit)
	j.At = o.dvJobTime(ctx, dvSTFUJobUnit, j.Job)
	return j
}

// dvSTFUInstallScript is the fixed script of the STFU install unit. The
// package enables its unit on install: it is disabled again at once, until
// the BrandMeister section is saved.
func (o *Ops) dvSTFUInstallScript() string {
	self, sim := shQuote(o.selfPath()), shQuote(o.dvSTFUSimPath())
	apt := "-o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold -o Dpkg::Use-Pty=0"
	return strings.Join([]string{
		"set -e",
		"apt-get update -q -o DPkg::Lock::Timeout=600 --allow-releaseinfo-change",
		"apt-get -s -q install --no-install-recommends " + dvSTFUPkg + " > " + sim + " || { cat " + sim + "; exit 1; }",
		self + " dvswitch-step " + StepSTFUPlan,
		"apt-get install -y -q -o DPkg::Lock::Timeout=600 --no-install-recommends " + apt + " " + dvSTFUPkg,
		"systemctl disable --now " + dvSTFUUnit + ".service || true",
		"echo 'stfu install finished'",
	}, "\n")
}

// DVSwitchSTFUInstall starts apt-get install stfu in a transient unit. It
// needs DVSwitch installed (the apt source is its) and no other apt job.
func (o *Ops) DVSwitchSTFUInstall(ctx context.Context) (DVSwitchStatus, error) {
	o.applyMu.Lock()
	defer o.applyMu.Unlock()
	if o.aptJobRunning(ctx) {
		return DVSwitchStatus{}, ErrBusy
	}
	if !o.osCheckMu.TryLock() {
		return DVSwitchStatus{}, ErrBusy
	}
	o.osCheckMu.Unlock()
	st, err := o.DVSwitchStatus(ctx)
	if err != nil {
		return st, err
	}
	switch {
	case !st.Installed:
		return DVSwitchStatus{}, ErrDVNotInstalled
	case st.STFU.Installed:
		return DVSwitchStatus{}, ErrDVSTFUInstalled
	case !st.Supported:
		return DVSwitchStatus{}, fmt.Errorf("%w: %s", ErrDVUnsupported, st.UnsupportedReason)
	case !st.STFU.CanInstall:
		return DVSwitchStatus{}, ErrBusy
	}
	if err := os.MkdirAll(o.dvStateDir(), 0o700); err != nil {
		return DVSwitchStatus{}, fmt.Errorf("cannot create the state directory: %w", err)
	}
	out, code, err := o.Run.Run(ctx, dvArgv(dvSTFUJobUnit, o.dvSTFUInstallScript())...)
	o.resetJobUnit(dvSTFUJobUnit)
	if err != nil {
		return DVSwitchStatus{}, fmt.Errorf("systemd-run: %w", err)
	}
	if code != 0 {
		return DVSwitchStatus{}, fmt.Errorf("systemd-run failed: %s", LastLines(out, 5))
	}
	st.STFU.Job = DVSwitchJob{Job: JobRunning}
	st.STFU.CanInstall, st.CanInstall, st.CanUninstall = false, false, false
	return st, nil
}

// dvSTFUPlan is the step of the STFU install unit: the same dependency guard
// as the DVSwitch install; on an ASLCP install the new packages join the
// marker, so the full uninstall removes STFU too.
func (o *Ops) dvSTFUPlan(w io.Writer) error {
	data, err := os.ReadFile(o.dvSTFUSimPath())
	if err != nil {
		return fmt.Errorf("cannot read the simulation: %w", err)
	}
	inst := ParseInstLines(string(data))
	if bad := UnexpectedDeps(inst); len(bad) > 0 {
		fmt.Fprintf(w, "unexpected_deps: the install would also install %s; nothing was installed\n", strings.Join(bad, ", "))
		return errors.New("unexpected_deps")
	}
	var newPkgs []string
	for _, i := range inst {
		if !i.New {
			continue
		}
		if !dvNameRe.MatchString(i.Name) || dvProtectedRe.MatchString(i.Name) {
			return fmt.Errorf("unexpected package name %q in the simulation", i.Name)
		}
		newPkgs = append(newPkgs, i.Name)
	}
	prev, err := ReadDVMarker(o.dvMarkerPath())
	if err != nil {
		return err
	}
	if prev != nil {
		m := MergeDVMarker(prev, newPkgs, prev.WgetPreinstalled, o.now())
		if err := WriteDVMarker(o.dvMarkerPath(), m); err != nil {
			return fmt.Errorf("cannot write the install marker: %w", err)
		}
		fmt.Fprintf(w, "recorded %d newly installed packages: %s\n", len(newPkgs), strings.Join(newPkgs, " "))
	} else {
		fmt.Fprintln(w, "DVSwitch was installed outside ASLCP: STFU is not recorded for the uninstall")
	}
	_ = os.Remove(o.dvSTFUSimPath())
	return nil
}

// ---- DMR status from STFU.log

// DVDMRRx is the last DMR transmission received from the network (ODMR
// Begin/End Tx in STFU.log). Src is the talker's DMR ID; Time is the log's
// own clock ("YYYY-MM-DD hh:mm:ss", time zone as STFU writes it).
type DVDMRRx struct {
	Active bool   `json:"active"`
	Src    string `json:"src,omitempty"`
	Dst    string `json:"dst,omitempty"`
	Group  bool   `json:"group"`
	Time   string `json:"time,omitempty"`
	Frames int    `json:"frames,omitempty"`
}

var (
	reSTFULine    = regexp.MustCompile(`^[A-Z][:0-9] ([0-9]{4}-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2})\.[0-9]{3} ([\x20-\x7e]+)$`)
	reSTFUConnect = regexp.MustCompile(`Connecting to STFU server at address: ?([A-Za-z0-9.-]{1,253}:[0-9]{1,5})`)
	reSTFURxBegin = regexp.MustCompile(`ODMR Begin Tx: src = ([0-9]{1,8}), dst = ([0-9]{1,8}) \(([A-Z]{1,10})\)`)
	reSTFUFrames  = regexp.MustCompile(`frame count was ([0-9]{1,7}) frames`)
)

// dvSTFUSafe drops every line that names a password (STFU logs its settings,
// the BM password among them, at startup): nothing after this sees one.
func dvSTFUSafe(line string) bool {
	return !strings.Contains(strings.ToLower(line), "password")
}

// dvSTFUMsgMax bounds the message of a log line, after dvSTFUClean.
const dvSTFUMsgMax = 300

// dvSTFUClean keeps the printable ASCII bytes (0x20-0x7e) of a log line and
// drops the rest, invalid UTF-8 included. STFU prints the server's IP address
// after "Successful connection to BM server at ip address:" as raw bytes, so
// the line carries binary junk (even a newline) that no terminal shows; the
// line must still count.
func dvSTFUClean(line string) string {
	b := make([]byte, 0, len(line))
	for i := 0; i < len(line); i++ {
		if c := line[i]; c >= 0x20 && c <= 0x7e {
			b = append(b, c)
		}
	}
	return string(b)
}

// parseSTFULog scans STFU.log text in order. Logged in: the latest
// "Successful connection to BM server" comes after the latest "Connecting".
// The order decides, not the time stamps (their time zone is STFU's).
//
// STFU logs traffic indefinitely, so the login line scrolls out of the tail
// window that is read. Traffic from the network (ODMR Begin/End Tx) after
// the latest "Connecting" line, or with no "Connecting" line in the window
// at all, is proof of a login too. It reports whether the window held a
// "Connecting" line (false: the start of this run is out of view).
func parseSTFULog(data []byte, cur *DVDMRStatus) (sawConnect bool) {
	traffic := false
	defer func() {
		if cur.State == DMRConnecting && traffic {
			cur.State = DMRLoggedIn
		}
	}()
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64<<10), dvLogMaxLine)
	for sc.Scan() {
		raw := sc.Text()
		line := dvSTFUClean(raw)
		// checked on both: control bytes inside a word must not hide it
		if !dvSTFUSafe(raw) || !dvSTFUSafe(line) {
			continue
		}
		m := reSTFULine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		msg := m[2]
		if len(msg) > dvSTFUMsgMax {
			msg = msg[:dvSTFUMsgMax]
		}
		switch {
		case strings.Contains(msg, "Setting [STFU]"):
			// STFU logs its settings first thing after a start: what came
			// before belongs to an earlier run (it is restarted on demand).
			sawConnect, traffic = true, false
			cur.State, cur.LastLine, cur.Master, cur.RX = DMRConnecting, "Starting", "", nil
		case strings.Contains(msg, "Connecting to STFU server"):
			sawConnect, traffic = true, false
			cur.State, cur.LastLine, cur.Master = DMRConnecting, dvRedact(msg), ""
			if mm := reSTFUConnect.FindStringSubmatch(msg); mm != nil {
				cur.Master = dvRedact(mm[1])
			}
			if cur.RX != nil {
				cur.RX.Active = false
			}
		case strings.Contains(msg, "Successful connection to BM server"):
			// nothing after "ip address:" is text
			if i := strings.Index(msg, "ip address:"); i >= 0 {
				msg = msg[:i+len("ip address:")]
			}
			cur.State, cur.LastLine = DMRLoggedIn, dvRedact(msg)
		case strings.Contains(msg, "ODMR Begin Tx:"):
			traffic = true
			if mm := reSTFURxBegin.FindStringSubmatch(msg); mm != nil {
				cur.RX = &DVDMRRx{Active: true, Src: mm[1], Dst: mm[2], Group: mm[3] == "GROUP", Time: m[1]}
			}
		case strings.Contains(msg, "ODMR End Tx"):
			traffic = true
			if cur.RX != nil {
				cur.RX.Active = false
				if mm := reSTFUFrames.FindStringSubmatch(msg); mm != nil {
					cur.RX.Frames, _ = strconv.Atoi(mm[1])
				}
			}
		}
	}
	return sawConnect
}

// dvSTFUStatus is the DMR status with STFU in use.
func (o *Ops) dvSTFUStatus(ctx context.Context) DVDMRStatus {
	loaded, active, since := o.dvUnitStart(ctx, dvSTFUUnit+".service")
	if !loaded {
		return DVDMRStatus{State: DMRNotInstalled, Via: DVViaSTFU}
	}
	res := DVDMRStatus{State: DMRNotRunning, Via: DVViaSTFU}
	if !since.IsZero() {
		res.Since = since.Format(time.RFC3339)
	}
	if !dvUnitRunning(active) {
		if !o.dvSTFUWanted(ctx) {
			res.State = DMRIdle // on demand: stopped because the panel is not in BM mode
		}
		return res
	}
	res.State = DMRConnecting
	p := o.dvSTFULogPath()
	data, ok := dvReadTail(p)
	if !ok {
		return res
	}
	if fi, err := os.Lstat(p); err == nil && !since.IsZero() && fi.ModTime().Before(since) {
		return res // nothing written since the start: the lines are an older run's
	}
	if !parseSTFULog(data, &res) {
		// The start of this run is not in the window: after a rotation it
		// is in STFU.log.1 (STFU may even still write to that file).
		if old, ok := dvReadTail(p + ".1"); ok {
			if fi, err := os.Lstat(p + ".1"); err == nil && (since.IsZero() || !fi.ModTime().Before(since)) {
				var r DVDMRStatus
				r.State, r.Via = DMRConnecting, DVViaSTFU
				if parseSTFULog(old, &r) {
					res = DVDMRStatus{State: DMRConnecting, Via: DVViaSTFU, Since: res.Since}
					parseSTFULog(old, &res)
					parseSTFULog(data, &res)
				}
			}
		}
	}
	if res.State == DMRConnecting && res.LastLine == "" {
		// Running, but the log has no login attempt and no traffic: say so
		// rather than "Connecting…" forever.
		res.State = DMRUnknown
	}
	return res
}
