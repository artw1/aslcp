package helper

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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

// The DMR connection method (docs/DVSWITCH.md, "DMR connection method").
// DMR reaches its network either through MMDVM_Bridge's own DMR network (the
// stock setup) or through a separate client: for BrandMeister, STFU, which
// speaks the Open DMR Terminal protocol and shows in BrandMeister SelfCare as
// an "Open DMR Terminal". The choice is a (network, via) pair kept in a root
// record; other networks (TGIF, FreeDMR, HBlink) are further rows of
// dvDMRMethods. Only one client may log in to BrandMeister with the ID, so
// with STFU MMDVM_Bridge's DMR is switched off ([DMR] Enable=0 and
// [DMR Network] Enable=0) while it keeps running for YSF, P25 and NXDN; the
// previous flags are restored on the way back.

// Operation names.
const (
	OpDVSwitchDMRVia      = "dvswitch-dmr-via"      // switch the DMR connection method
	OpDVSwitchSTFUInstall = "dvswitch-stfu-install" // apt-get install stfu in a transient unit
)

// DMR connection methods and networks.
const (
	DVViaMMDVM        = "mmdvm_bridge"
	DVViaSTFU         = "stfu"
	DVNetBrandMeister = "brandmeister"
	// DefaultDVSTFUServer is the BrandMeister ODMRT server STFU logs in to
	// when none was set.
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
	dvDMRRecordFile   = "dmr.json"
	dvTalkerAliasMax  = 27

	// StepSTFUPlan is the dependency guard of the STFU install unit.
	StepSTFUPlan = "stfu-plan"

	// DVDMRRecordWarning is the setup-view warning for an unreadable record.
	DVDMRRecordWarning = "The DMR connection record cannot be read: switch the DMR connection again to rewrite it"
)

// dvDMRMethod is one way to reach a DMR network other than MMDVM_Bridge's
// own DMR network: Section is its section in DVSwitch.ini (its TLV ports),
// Unit its systemd unit, Package its Debian package.
type dvDMRMethod struct {
	Network, Via, Section, Unit, Package string
}

var dvDMRMethods = []dvDMRMethod{
	{Network: DVNetBrandMeister, Via: DVViaSTFU, Section: dvSTFUSection, Unit: dvSTFUUnit, Package: dvSTFUPkg},
}

func dvDMRMethodFor(network, via string) (dvDMRMethod, bool) {
	for _, m := range dvDMRMethods {
		if m.Network == network && m.Via == via {
			return m, true
		}
	}
	return dvDMRMethod{}, false
}

// Errors (mapped back by the client, client.go).
var (
	ErrDVSTFUNotInstalled = errors.New("stfu_not_installed: STFU is not installed, or DVSwitch.ini has no [STFU] section")
	ErrDVSTFUInstalled    = errors.New("stfu_installed: STFU is already installed")
	ErrDVNoIdentity       = errors.New("no_identity: save the callsign and DMR ID first")
	ErrDVNoPassword       = errors.New("no_password: save the BrandMeister hotspot password first")
	ErrDVDMRRecord        = errors.New("dmr_record: the DMR connection record cannot be read")
	ErrDVSwitchFailed     = errors.New("switch_failed: the DMR connection could not be switched; the previous settings were restored")
)

// DVDMRVia is the argument of dvswitch-dmr-via. Network is required for a
// method other than MMDVM_Bridge (only "brandmeister" for STFU). Server is
// the BrandMeister ODMRT server ("" keeps the saved one, or the default).
// Nil TalkerAlias and StartTG keep the saved value (or the default: the
// callsign and 4000).
type DVDMRVia struct {
	Via         string  `json:"via"`
	Network     string  `json:"network,omitempty"`
	Server      string  `json:"server,omitempty"`
	TalkerAlias *string `json:"talkerAlias,omitempty"`
	StartTG     *string `json:"startTg,omitempty"`
}

// DVDMRRecord is the root-only record of the method in use. No file means
// MMDVM_Bridge. PrevMMDVMDMR: MMDVM_Bridge's DMR flags were on before the
// switch. ABRetargeted: Analog_Bridge's [AMBE_AUDIO] ports were moved from
// MMDVM_Bridge's [DMR] ports to the method's.
type DVDMRRecord struct {
	Version      int    `json:"version"`
	Network      string `json:"network"`
	Via          string `json:"via"`
	PrevMMDVMDMR bool   `json:"prevMMDVMDMR"`
	ABRetargeted bool   `json:"abRetargeted"`
}

func (o *Ops) dvDMRRecordPath() string  { return filepath.Join(o.dvStateDir(), dvDMRRecordFile) }
func (o *Ops) dvSTFULogPath() string    { return orDefault(o.DVSTFULog, DefaultDVSTFULog) }
func (o *Ops) dvSTFUDropinPath() string { return orDefault(o.DVSTFUDropin, DefaultDVSTFUDropin) }
func (o *Ops) dvSTFUSimPath() string    { return filepath.Join(o.dvStateDir(), dvSTFUSimFile) }

// ReadDVDMRRecord reads the record; (nil, nil) when there is none. Anything
// unexpected is an error (never half-trusted).
func ReadDVDMRRecord(path string) (*DVDMRRecord, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, ErrDVDMRRecord
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 4<<10))
	if err != nil {
		return nil, ErrDVDMRRecord
	}
	var r DVDMRRecord
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil || r.Version != 1 {
		return nil, ErrDVDMRRecord
	}
	if _, ok := dvDMRMethodFor(r.Network, r.Via); !ok {
		return nil, ErrDVDMRRecord
	}
	return &r, nil
}

func (o *Ops) writeDVDMRRecord(r DVDMRRecord) error {
	if err := os.MkdirAll(o.dvStateDir(), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(r, "", "  ")
	return writeStateFile(o.dvDMRRecordPath(), append(b, '\n'), 0o600)
}

// dvVia returns the method in use (DVViaMMDVM when there is no record).
func (o *Ops) dvVia() (string, *DVDMRRecord, error) {
	r, err := ReadDVDMRRecord(o.dvDMRRecordPath())
	if err != nil {
		return "", nil, err
	}
	if r == nil {
		return DVViaMMDVM, nil, nil
	}
	return r.Via, r, nil
}

// dvDMRSection is the DVSwitch.ini section whose ports the DMR mode switch
// points Analog_Bridge at: [DMR] (MMDVM_Bridge) or the method's section.
func (o *Ops) dvDMRSection() string {
	if _, r, err := o.dvVia(); err == nil && r != nil {
		if m, ok := dvDMRMethodFor(r.Network, r.Via); ok {
			return m.Section
		}
	}
	return "DMR"
}

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

// ---- hooks of the other config ops

// dvSTFUIdentity: with STFU in use, a new DMR ID goes to [STFU] UserID too.
func (o *Ops) dvSTFUIdentity(dmrID string) []dvChange {
	if via, _, err := o.dvVia(); err == nil && via == DVViaSTFU {
		return []dvChange{{DVFileDVSwitch, dvSTFUSection, "UserID", dmrID}}
	}
	return nil
}

// dvSTFUCredentials: with STFU in use, a new BM password goes to
// [STFU] BMPassword too; clearing it is refused (BrandMeister needs one).
func (o *Ops) dvSTFUCredentials(c *DVCredentials) ([]dvChange, error) {
	via, _, err := o.dvVia()
	if err != nil {
		return nil, err
	}
	if via != DVViaSTFU {
		return nil, nil
	}
	if c.ClearPassword {
		return nil, dvInvalid("DMR Network/Password")
	}
	if c.Password != nil {
		return []dvChange{{DVFileDVSwitch, dvSTFUSection, "BMPassword", *c.Password}}, nil
	}
	return nil, nil
}

// dvSTFUModes: with STFU in use MMDVM_Bridge's DMR stays off. "DMR on" is
// already true (skipped); "DMR off" is refused: switch the method back first.
func (o *Ops) dvSTFUModes(on bool) (skip bool, err error) {
	via, _, err := o.dvVia()
	if err != nil {
		return false, err
	}
	if via != DVViaSTFU {
		return false, nil
	}
	if on {
		return true, nil
	}
	return false, dvInvalid("modes/dmr")
}

// ---- setup view

// DVDMRConnView is the DMR connection part of the setup view. Error: the
// record cannot be read (Via then reads mmdvm_bridge).
type DVDMRConnView struct {
	Via     string     `json:"via"`
	Network string     `json:"network"`
	Error   bool       `json:"error,omitempty"`
	STFU    DVSTFUView `json:"stfu"`
}

// DVSTFUView: the [STFU] values of DVSwitch.ini. Present: the section exists.
// The password only as PasswordSet.
type DVSTFUView struct {
	Present     bool   `json:"present"`
	Server      string `json:"server"`
	Port        int    `json:"port"`
	TalkerAlias string `json:"talkerAlias"`
	StartTG     string `json:"startTg"`
	TxPort      int    `json:"txPort"`
	RxPort      int    `json:"rxPort"`
	PasswordSet bool   `json:"passwordSet"`
}

func (o *Ops) dvDMRConnView(dv, _ *ini.File) DVDMRConnView {
	v := DVDMRConnView{Via: DVViaMMDVM}
	if _, r, err := o.dvVia(); err != nil {
		v.Error = true
	} else if r != nil {
		v.Via, v.Network = r.Via, r.Network
	}
	if dv != nil && dv.HasSection(dvSTFUSection) {
		get := func(k string) string { s, _ := dv.GetFold(dvSTFUSection, k); return s }
		atoi := func(s string) int { n, _ := strconv.Atoi(s); return n }
		pw := get("BMPassword")
		v.STFU = DVSTFUView{Present: true, Server: get("BMAddress"), Port: atoi(get("BMPort")), TalkerAlias: get("TalkerAlias"),
			StartTG: get("StartTG"), TxPort: atoi(get("txPort")), RxPort: atoi(get("rxPort")), PasswordSet: pw != "" && pw != dvStockPassword}
	}
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

// dvSTFUPorts picks STFU's TLV ports: the [STFU] ones when they are valid and
// clash with nothing else (the other DVSwitch.ini sections, the stock mode
// ports, the park port, Analog_Bridge's USRP and AMBE ports), else the stock
// 36100/36103 when those are free.
func dvSTFUPorts(dv, ab *ini.File) (tx, rx int, err error) {
	used := map[int]bool{dvTLVSwitchPort: true}
	for _, p := range dvTLVPorts {
		used[p] = true
	}
	for _, sec := range dv.Sections() {
		if sec != dvSTFUSection {
			used[dvIniPort(dv, sec, "txPort")], used[dvIniPort(dv, sec, "rxPort")] = true, true
		}
	}
	for _, k := range [][2]string{{"USRP", "txPort"}, {"USRP", "rxPort"}, {"AMBE_AUDIO", "ambePort"}} {
		used[dvIniPort(ab, k[0], k[1])] = true
	}
	free := func(t, r int) bool { return t != 0 && r != 0 && t != r && !used[t] && !used[r] }
	if t, r := dvIniPort(dv, dvSTFUSection, "txPort"), dvIniPort(dv, dvSTFUSection, "rxPort"); free(t, r) {
		return t, r, nil
	}
	if free(dvSTFUTx, dvSTFURx) {
		return dvSTFUTx, dvSTFURx, nil
	}
	return 0, 0, dvInvalid("dmrVia/ports")
}

// dvABRetarget returns the changes that move Analog_Bridge's [AMBE_AUDIO]
// partner ports to (toTx, toRx) of a DVSwitch.ini section when they point at
// one of the from pairs (each the section's txPort, rxPort); nil otherwise.
// Analog_Bridge's txPort is the partner's rxPort and the other way round.
func dvABRetarget(ab *ini.File, toTx, toRx int, from ...[2]int) []dvChange {
	abTx, abRx := dvIniPort(ab, "AMBE_AUDIO", "txPort"), dvIniPort(ab, "AMBE_AUDIO", "rxPort")
	if abTx == toRx && abRx == toTx {
		return nil
	}
	for _, p := range from {
		if p[0] != 0 && abTx == p[1] && abRx == p[0] {
			return []dvChange{
				{DVFileAnalogBridge, "AMBE_AUDIO", "txPort", strconv.Itoa(toRx)},
				{DVFileAnalogBridge, "AMBE_AUDIO", "rxPort", strconv.Itoa(toTx)},
			}
		}
	}
	return nil
}

// ---- the switch

// dvViaFiles are the parsed files the switch reads.
type dvViaFiles struct {
	mm, dv, ab *ini.File
}

func (o *Ops) dvViaRead(needSTFU bool) (dvViaFiles, error) {
	var out dvViaFiles
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
				return out, ErrDVSTFUNotInstalled
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

// DVSwitchDMRVia switches the DMR connection method. To STFU: [STFU] is
// written (server, port, password from MMDVM_Bridge's [DMR Network], UserID
// from the identity, talker alias, start TG, ports), MMDVM_Bridge's DMR is
// switched off, Analog_Bridge's startup ports follow; then mmdvm_bridge
// restarts (if it runs), stfu is enabled and restarted, and a running
// Analog_Bridge in DMR is pointed at STFU's ports through its TLV port (no
// restart). Back to MMDVM_Bridge it is the reverse. A failed step restores
// the files from their history copies and the units, and reports
// switch_failed.
func (o *Ops) DVSwitchDMRVia(ctx context.Context, req *DVSwitchRequest) (DVApplyResult, error) {
	req, err := o.dvArgs(req)
	if err != nil {
		return DVApplyResult{}, err
	}
	d := req.DMRVia
	if d == nil || req.Identity != nil || req.Credentials != nil || req.Modes != nil || req.Startup != nil || req.Tune != nil || req.USRP != nil || req.Private != nil {
		return DVApplyResult{}, errors.New("invalid request: dmrVia only")
	}
	o.dmrViaMu.Lock()
	defer o.dmrViaMu.Unlock()
	_, rec, err := o.dvVia()
	if err != nil && d.Via != DVViaMMDVM {
		return DVApplyResult{}, err
	}
	switch d.Via {
	case DVViaMMDVM:
		if d.Network != "" || d.Server != "" || d.TalkerAlias != nil || d.StartTG != nil {
			return DVApplyResult{}, dvInvalid("dmrVia")
		}
		return o.dvToMMDVM(ctx, rec, req.Base)
	case DVViaSTFU:
		return o.dvToSTFU(ctx, d, rec, req.Base)
	}
	return DVApplyResult{}, dvInvalid("dmrVia/via")
}

func (o *Ops) dvToSTFU(ctx context.Context, d *DVDMRVia, rec *DVDMRRecord, base map[string]string) (DVApplyResult, error) {
	m, ok := dvDMRMethodFor(d.Network, d.Via)
	if !ok {
		return DVApplyResult{}, dvInvalid("dmrVia/network")
	}
	if d.Server != "" && !vHostname(d.Server) {
		return DVApplyResult{}, dvInvalid("dmrVia/server")
	}
	if d.TalkerAlias != nil && !vTalkerAlias(*d.TalkerAlias) {
		return DVApplyResult{}, dvInvalid("dmrVia/talkerAlias")
	}
	if d.StartTG != nil && !reDVTG.MatchString(*d.StartTG) {
		return DVApplyResult{}, dvInvalid("dmrVia/startTg")
	}
	have, err := o.dpkgInstalled(ctx, []string{m.Package})
	if err != nil {
		return DVApplyResult{}, err
	}
	if _, ok := have[m.Package]; !ok {
		return DVApplyResult{}, ErrDVSTFUNotInstalled
	}
	f, err := o.dvViaRead(true)
	if err != nil {
		return DVApplyResult{}, err
	}
	call, _ := f.mm.Get("General", "Callsign")
	id, _ := f.mm.Get("General", "Id")
	if !reDVCall.MatchString(call) || !reDVDigits9.MatchString(id) && !reDVDigits7.MatchString(id) {
		return DVApplyResult{}, ErrDVNoIdentity
	}
	pw, _ := f.mm.Get("DMR Network", "Password")
	if pw == "" || pw == dvStockPassword || !vPassword(pw) {
		return DVApplyResult{}, ErrDVNoPassword
	}
	tx, rx, err := dvSTFUPorts(f.dv, f.ab)
	if err != nil {
		return DVApplyResult{}, err
	}
	cur := func(k string, ok func(string) bool, def string) string {
		if v, _ := f.dv.GetFold(m.Section, k); ok(v) {
			return v
		}
		return def
	}
	server := d.Server
	if server == "" {
		server = cur("BMAddress", vHostname, DefaultDVSTFUServer)
	}
	alias := call
	if len(alias) > dvTalkerAliasMax {
		alias = alias[:dvTalkerAliasMax]
	}
	alias = cur("TalkerAlias", vTalkerAlias, alias)
	if d.TalkerAlias != nil {
		alias = *d.TalkerAlias
	}
	startTG := cur("StartTG", reDVTG.MatchString, dvSTFUStartTG)
	if d.StartTG != nil {
		startTG = *d.StartTG
	}
	set := func(k, v string) dvChange { return dvChange{DVFileDVSwitch, m.Section, k, v} }
	ch := []dvChange{
		set("BMAddress", server),
		set("BMPort", cur("BMPort", vInt(1, 65535), strconv.Itoa(dvSTFUDefaultPort))),
		set("BMPassword", pw),
		set("Address", dvSTFULocal),
		set("txPort", strconv.Itoa(tx)),
		set("rxPort", strconv.Itoa(rx)),
		set("UserID", id[:7]),
		set("TalkerAlias", alias),
		set("StartTG", startTG),
		{DVFileMMDVMBridge, "DMR", "Enable", "0"},
		{DVFileMMDVMBridge, "DMR Network", "Enable", "0"},
	}
	ch = append(ch, dvABRetarget(f.ab, tx, rx,
		[2]int{dvIniPort(f.dv, "DMR", "txPort"), dvIniPort(f.dv, "DMR", "rxPort")},
		[2]int{dvIniPort(f.dv, m.Section, "txPort"), dvIniPort(f.dv, m.Section, "rxPort")})...)

	wasSTFU := rec != nil && rec.Via == m.Via
	next := DVDMRRecord{Version: 1, Network: m.Network, Via: m.Via}
	if wasSTFU {
		next.PrevMMDVMDMR, next.ABRetargeted = rec.PrevMMDVMDMR, rec.ABRetargeted
	} else {
		a, _ := f.mm.Get("DMR", "Enable")
		b, _ := f.mm.Get("DMR Network", "Enable")
		next.PrevMMDVMDMR = a == "1" && b == "1"
	}
	for _, c := range ch {
		if c.file == DVFileAnalogBridge {
			next.ABRetargeted = true
		}
	}

	res, err := o.dvApply(ctx, ch, base)
	if err != nil {
		return res, err
	}
	changed := func(id string) bool {
		for _, x := range res.Files {
			if x == id {
				return true
			}
		}
		return false
	}
	_, dropErr := os.Lstat(o.dvSTFUDropinPath())
	hadDropin := dropErr == nil
	step := ""
	switch {
	case o.dvSTFULogPrep() != nil:
		step = "log"
	case !o.dvSTFUDropinWrite(ctx):
		step = "umask"
	case changed(DVFileMMDVMBridge) && !o.dvSystemctl(ctx, "try-restart", "mmdvm_bridge.service"):
		step = "mmdvm_bridge"
	case !o.dvSystemctl(ctx, "enable", m.Unit+".service") || !o.dvSystemctl(ctx, "restart", m.Unit+".service"):
		step = m.Unit
	case o.writeDVDMRRecord(next) != nil:
		step = "record"
	}
	if step != "" {
		o.dvViaUndo(ctx, res.Files)
		if !hadDropin {
			o.dvSTFUDropinRemove(ctx)
		}
		if wasSTFU {
			o.dvSystemctl(ctx, "restart", m.Unit+".service")
		} else {
			o.dvSystemctl(ctx, "disable", "--now", m.Unit+".service")
		}
		o.dvSystemctl(ctx, "try-restart", "mmdvm_bridge.service")
		return DVApplyResult{}, fmt.Errorf("%w (%s)", ErrDVSwitchFailed, step)
	}
	o.dvRepointDMR(ctx, tx, rx)
	res.RestartHint = []string{}
	return res, nil
}

func (o *Ops) dvToMMDVM(ctx context.Context, rec *DVDMRRecord, base map[string]string) (DVApplyResult, error) {
	if rec == nil {
		// unreadable record: make sure the other client is off, leave the
		// files alone
		if _, err := ReadDVDMRRecord(o.dvDMRRecordPath()); err != nil {
			for _, m := range dvDMRMethods {
				o.dvSystemctl(ctx, "disable", "--now", m.Unit+".service")
			}
			o.dvSTFUDropinRemove(ctx)
			_ = os.Remove(o.dvDMRRecordPath())
		}
		return dvNoChange(), nil
	}
	m, _ := dvDMRMethodFor(rec.Network, rec.Via)
	f, err := o.dvViaRead(false)
	if err != nil {
		return DVApplyResult{}, err
	}
	val := "0"
	if rec.PrevMMDVMDMR {
		val = "1"
	}
	ch := []dvChange{
		{DVFileMMDVMBridge, "DMR", "Enable", val},
		{DVFileMMDVMBridge, "DMR Network", "Enable", val},
	}
	dmrTx, dmrRx := dvIniPort(f.dv, "DMR", "txPort"), dvIniPort(f.dv, "DMR", "rxPort")
	if rec.ABRetargeted && dmrTx != 0 && dmrRx != 0 {
		ch = append(ch, dvABRetarget(f.ab, dmrTx, dmrRx, [2]int{dvIniPort(f.dv, m.Section, "txPort"), dvIniPort(f.dv, m.Section, "rxPort")})...)
	}
	res, err := o.dvApply(ctx, ch, base)
	if err != nil {
		return res, err
	}
	step := ""
	switch {
	case !o.dvSystemctl(ctx, "disable", "--now", m.Unit+".service"):
		step = m.Unit
	case !o.dvSTFUDropinRemove(ctx):
		step = "umask"
	case !o.dvSystemctl(ctx, "try-restart", "mmdvm_bridge.service"):
		step = "mmdvm_bridge"
	}
	if step == "" {
		if err := os.Remove(o.dvDMRRecordPath()); err != nil && !errors.Is(err, fs.ErrNotExist) {
			step = "record"
		}
	}
	if step != "" {
		o.dvViaUndo(ctx, res.Files)
		o.dvSTFUDropinWrite(ctx)
		o.dvSystemctl(ctx, "enable", m.Unit+".service")
		o.dvSystemctl(ctx, "restart", m.Unit+".service")
		o.dvSystemctl(ctx, "try-restart", "mmdvm_bridge.service")
		return DVApplyResult{}, fmt.Errorf("%w (%s)", ErrDVSwitchFailed, step)
	}
	if dmrTx != 0 && dmrRx != 0 {
		o.dvRepointDMR(ctx, dmrTx, dmrRx)
	}
	res.RestartHint = []string{}
	return res, nil
}

// dvViaUndo puts back the newest history copy of each file the switch
// wrote (the copy dvApply just made).
func (o *Ops) dvViaUndo(ctx context.Context, files []string) {
	for _, id := range files {
		_, _ = o.DVSwitchRollback(ctx, &DVSwitchRequest{File: id})
	}
}

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

// dvRepointDMR points a running Analog_Bridge that is in DMR at the partner
// with these DVSwitch.ini ports (its txport is the partner's rxPort), like a
// mode switch. Best effort: Analog_Bridge in another mode, or not running,
// picks the ports up at its next switch to DMR.
func (o *Ops) dvRepointDMR(ctx context.Context, tx, rx int) bool {
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
// package enables its unit on install: it is disabled again at once, so STFU
// does not log in to BrandMeister next to MMDVM_Bridge before the method is
// switched.
func (o *Ops) dvSTFUInstallScript() string {
	self, sim := shQuote(o.selfPath()), shQuote(o.dvSTFUSimPath())
	apt := "-o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold -o Dpkg::Use-Pty=0"
	return strings.Join([]string{
		"set -e",
		"apt-get update -q --allow-releaseinfo-change",
		"apt-get -s -q install --no-install-recommends " + dvSTFUPkg + " > " + sim + " || { cat " + sim + "; exit 1; }",
		self + " dvswitch-step " + StepSTFUPlan,
		"apt-get install -y -q --no-install-recommends " + apt + " " + dvSTFUPkg,
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
	reSTFULine    = regexp.MustCompile(`^[A-Z][:0-9] ([0-9]{4}-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2})\.[0-9]{3} ([\x20-\x7e]{1,300})$`)
	reSTFUConnect = regexp.MustCompile(`Connecting to STFU server at address: ?([A-Za-z0-9.-]{1,253}:[0-9]{1,5})`)
	reSTFURxBegin = regexp.MustCompile(`ODMR Begin Tx: src = ([0-9]{1,8}), dst = ([0-9]{1,8}) \(([A-Z]{1,10})\)`)
	reSTFUFrames  = regexp.MustCompile(`frame count was ([0-9]{1,7}) frames`)
)

// dvSTFUSafe drops every line that names a password (STFU logs its settings,
// the BM password among them, at startup): nothing after this sees one.
func dvSTFUSafe(line string) bool {
	return !strings.Contains(strings.ToLower(line), "password")
}

// parseSTFULog scans STFU.log text in order. Logged in: the latest
// "Successful connection to BM server" comes after the latest "Connecting".
// The order decides, not the time stamps (their time zone is STFU's).
func parseSTFULog(data []byte, cur *DVDMRStatus) {
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64<<10), dvLogMaxLine)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if !dvSTFUSafe(line) {
			continue
		}
		m := reSTFULine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		msg := m[2]
		switch {
		case strings.Contains(msg, "Connecting to STFU server"):
			cur.State, cur.LastLine, cur.Master = DMRConnecting, dvRedact(msg), ""
			if mm := reSTFUConnect.FindStringSubmatch(msg); mm != nil {
				cur.Master = dvRedact(mm[1])
			}
			if cur.RX != nil {
				cur.RX.Active = false
			}
		case strings.Contains(msg, "Successful connection to BM server"):
			cur.State, cur.LastLine = DMRLoggedIn, dvRedact(msg)
		case strings.Contains(msg, "ODMR Begin Tx:"):
			if mm := reSTFURxBegin.FindStringSubmatch(msg); mm != nil {
				cur.RX = &DVDMRRx{Active: true, Src: mm[1], Dst: mm[2], Group: mm[3] == "GROUP", Time: m[1]}
			}
		case strings.Contains(msg, "ODMR End Tx"):
			if cur.RX != nil {
				cur.RX.Active = false
				if mm := reSTFUFrames.FindStringSubmatch(msg); mm != nil {
					cur.RX.Frames, _ = strconv.Atoi(mm[1])
				}
			}
		}
	}
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
	if active != "active" && active != "activating" && active != "reloading" {
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
	parseSTFULog(data, &res)
	return res
}
