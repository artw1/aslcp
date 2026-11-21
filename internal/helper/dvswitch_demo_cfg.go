package helper

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
)

// demoCallers is a fixed set of placeholder talkers; the newest changes every
// 30 s so the panel has something to show.
func demoCallers(now time.Time) *DVCallers {
	all := []DVCaller{
		{Call: "N0CALL", DMRID: "1234567", Dst: "91", Slot: 2},
		{Call: "N0DEMO", DMRID: "1234568", Dst: "91", Slot: 2},
		{Call: "N0TEST", DMRID: "1234569", Dst: "91", Slot: 2},
	}
	n := int(now.Unix()/30) % len(all)
	rec := []DVCaller{}
	for i := 0; i < len(all); i++ {
		c := all[(n-i+len(all))%len(all)]
		c.At = now.UTC().Add(-time.Duration(i) * 30 * time.Second).Format("2006-01-02 15:04:05")
		rec = append(rec, c)
	}
	cur := rec[0]
	return &DVCallers{Current: &cur, Recent: rec, Talks: demoTalks(now)}
}

// demoTalks: one placeholder call every 30 s, 12 s long, in turn BrandMeister
// (an end line, as STFU writes), YSF (no end line) and BrandMeister again. A
// call has no end until it is over, like the real log.
func demoTalks(now time.Time) []DVTalk {
	w := now.Unix() / 30
	var out []DVTalk
	for i := w - 2; i <= w; i++ {
		start := time.Unix(i*30+3, 0).UTC()
		t := DVTalk{Kind: DVTalkDMR, Via: DVViaSTFU, DMRID: "1234567", Dst: "91", Start: start.Format(time.RFC3339Nano)}
		switch i % 3 {
		case 1:
			t.DMRID, t.Call = "1234568", "N0DEMO"
		case 2:
			t = DVTalk{Kind: DVTalkYSF, Via: "analog_bridge", Call: "N0TEST", DMRID: "1234569", Dst: "9", Slot: 2, Start: start.Format(time.RFC3339Nano)}
		}
		if end := start.Add(12 * time.Second); t.Kind == DVTalkDMR && !end.After(now) {
			t.End, t.Frames = end.Format(time.RFC3339Nano), 200
		}
		if !start.After(now) {
			out = append(out, t)
		}
	}
	return out
}

// Demo side of the M4 ops: values live in memory, nothing is written. The
// demo password is never stored, only whether one was set.

type demoDVCfg struct {
	set      bool
	identity DVIdentityView
	dmr      DVDMRView
	modes    DVModesView
	startup  DVStartupView
	gw       DVGateways
	hist     map[string]int
	times    int

	// the private bridge node (M2): the ini's [USRP] values and the record
	usrp DVUSRPView
	priv *DVPrivateRecord

	// The demo's ABInfo: what the running bridge shows. It lives in memory
	// only (the demo never reads /tmp or runs a binary). A restart of the
	// bridge resets it to the startup values and sets a new start time.
	live    demoDVLive
	started time.Time
}

type demoDVLive struct {
	mode, tg, slot, tune string
	panel                string // BM or DMR in ambe_mode DMR
	private              bool
}

func newDemoDVCfg() *demoDVCfg {
	return &demoDVCfg{
		set: true,
		identity: DVIdentityView{Callsign: "N0CALL", DMRID: "1234567", ESSID: "01", NXDNID: "12346", Consistent: true,
			Info: DVInfoView{Location: dvStockInfoLoc, Description: "MMDVM_Bridge", URL: "https://example.org/dvswitch", FrequencyHz: 0, RXHz: dvStockInfoRX, TXHz: 224940000, Latitude: "41.7333", Longitude: "-50.3999", Stock: true}},
		dmr:     DVDMRView{Address: "192.0.2.10", Port: 62031},
		modes:   DVModesView{DMR: true, YSF: true},
		startup: DVStartupView{AmbeMode: "DMR", TxTg: "9", TxTs: 2, ColorCode: 1},
		gw:      DVGateways{YSF: DVGatewayView{Present: true, Startup: "US-Example"}, P25: DVGatewayView{Present: true}, NXDN: DVGatewayView{Present: true}},
		hist:    map[string]int{},
		usrp:    DVUSRPView{Address: "127.0.0.1", RxPort: 31001, TxPort: 31001},
	}
}

func (d *DemoDVSwitch) cfg() *demoDVCfg {
	if d.c == nil {
		d.c = newDemoDVCfg()
	}
	return d.c
}

func (d *DemoDVSwitch) ready() error {
	if job, _ := d.progress(); !d.installed || job == JobRunning {
		return ErrDVNotInstalled
	}
	return nil
}

// DVSwitchSetup implements the API's DVSwitch interface.
func (d *DemoDVSwitch) DVSwitchSetup(context.Context) (DVSwitchSetup, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.setup(), nil
}

func (d *DemoDVSwitch) setup() DVSwitchSetup {
	d.progress()
	c := d.cfg()
	v := DVSwitchSetup{Identity: c.identity, DMR: c.dmr, Modes: c.modes, Startup: c.startup, Gateways: c.gw,
		USRP: c.usrp, Private: c.priv, ReservedPorts: dvReservedPortsDemo(), Files: []DVFileView{}}
	v.DMR.PasswordSet = c.set
	v.DMR.Enabled = c.modes.DMR
	v.DMR.PointsAtBM = DVBMHostName(c.dmr.Address)
	v.BrandMeister = d.bmView()
	v.Warnings = dvWarnings(v.Modes, v.Identity)
	for i, id := range DVFileIDs() {
		v.Files = append(v.Files, DVFileView{ID: id, Present: d.installed, Writable: d.installed,
			SHA256: "demo" + strconv.Itoa(i) + "-" + strconv.Itoa(c.times), History: c.hist[id]})
	}
	return v
}

// DVSwitchInfo implements the API's DVSwitch interface.
func (d *DemoDVSwitch) DVSwitchInfo(context.Context) (DVSwitchInfo, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ready() != nil {
		return DVSwitchInfo{}, nil
	}
	c := d.cfg()
	if c.started.IsZero() {
		d.resetLive(c)
	}
	now := d.now().UTC()
	return DVSwitchInfo{Found: true, Version: "1.6.4", Mode: c.live.mode, TG: c.live.tg, Slot: c.live.slot, Tune: c.live.tune,
		ColorCode: strconv.Itoa(c.startup.ColorCode), Mute: "OFF", GatewayID: c.identity.DMRID, RepeaterID: c.identity.DMRID + c.identity.ESSID,
		Private: c.live.private, Panel: c.live.panel, Updated: now.Format(time.RFC3339), Started: c.started.UTC().Format(time.RFC3339),
		Callers: demoCallers(now)}, nil
}

// resetLive is what Analog_Bridge does on a start: the startup values, an
// empty last tune and a new start time.
func (d *DemoDVSwitch) resetLive(c *demoDVCfg) {
	c.live = demoDVLive{mode: c.startup.AmbeMode, panel: DVNormMode(c.startup.AmbeMode), tg: c.startup.TxTg, slot: strconv.Itoa(c.startup.TxTs)}
	c.started = d.now()
}

// Installed reports whether the demo node has DVSwitch (the tracker polls
// only then).
func (d *DemoDVSwitch) Installed() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.ready() == nil
}

// DVSwitchTune implements the API's DVSwitch interface: it validates like the
// helper and changes the demo ABInfo, nothing else.
func (d *DemoDVSwitch) DVSwitchTune(_ context.Context, r DVSwitchRequest) (DVTuneResult, error) {
	t := r.Tune
	if t == nil {
		return DVTuneResult{}, errors.New("invalid request: tune only")
	}
	okMode := false
	for _, m := range DVTuneModes {
		okMode = okMode || m == t.Mode
	}
	if !okMode {
		return DVTuneResult{}, dvInvalid("tune/mode")
	}
	if t.ModeOnly {
		if t.Tune != "" {
			return DVTuneResult{}, dvInvalid("tune/value")
		}
	} else if !DVValidTune(t.Mode, t.Tune) {
		return DVTuneResult{}, dvInvalid("tune/value")
	}
	value := t.Tune
	if t.Mode == "YSF" && !t.ModeOnly {
		ok := false
		if d.Lists != nil {
			value, ok = d.Lists.dvYSFTarget(t.Tune)
		} else {
			value, ok = t.Tune, t.Tune == "disconnect" || reDVHostPort.MatchString(t.Tune)
		}
		if !ok {
			return DVTuneResult{}, dvInvalid("tune/value")
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.ready(); err != nil {
		return DVTuneResult{}, err
	}
	c := d.cfg()
	if c.started.IsZero() {
		d.resetLive(c)
	}
	res := DVTuneResult{Mode: t.Mode, Confirmed: true}
	if t.Mode == DVModeBM && !d.stfu().stay && !d.stfuRunning() {
		d.stfu().at = d.now() // started on demand; the login is simulated as seen
	}
	if c.live.panel != t.Mode {
		res.Switched = true
		// like the helper: the mode left is unlinked first when it was linked
		if lt := dvLinkedTune(c.live.panel, DVSwitchInfo{Tune: c.live.tune, TG: c.live.tg}); c.live.panel != "" && lt != "" && !DVUnlinkValue(c.live.panel, lt) {
			res.Left = c.live.panel
		}
		c.live.mode, c.live.panel = t.Mode, t.Mode
		switch t.Mode {
		case "YSF":
			c.live.mode = "YSFN"
		case DVModeBM:
			c.live.mode = DVModeDMR
		}
	}
	if t.ModeOnly {
		return res, nil
	}
	c.live.private = DVPrivateTune(t.Mode, value)
	if c.live.private {
		// a private call is a one-off, not a target: last_tune is not a tune
		c.live.tune = ""
		c.live.tg = strings.TrimSuffix(value, DVPrivateMark)
		return res, nil
	}
	c.live.tune = value
	if DVDMRFamily(t.Mode) {
		c.live.tg = value
	}
	return res, nil
}

func (d *DemoDVSwitch) demoWrite(files []string, hint []string, f func(c *demoDVCfg) error) (DVApplyResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.ready(); err != nil {
		return DVApplyResult{}, err
	}
	c := d.cfg()
	if err := f(c); err != nil {
		return DVApplyResult{}, err
	}
	c.times++
	for _, id := range files {
		if c.hist[id] < dvHistoryKeep {
			c.hist[id]++
		}
	}
	return DVApplyResult{Files: files, Skipped: []string{}, RestartHint: hint, Hashes: map[string]string{}}, nil
}

// DVSwitchIdentity implements the API's DVSwitch interface.
func (d *DemoDVSwitch) DVSwitchIdentity(_ context.Context, r DVSwitchRequest) (DVApplyResult, error) {
	id := r.Identity
	if id == nil || !reDVCall.MatchString(id.Callsign) {
		return DVApplyResult{}, dvInvalid("identity/callsign")
	}
	if !reDVDigits7.MatchString(id.DMRID) {
		return DVApplyResult{}, dvInvalid("identity/dmrId")
	}
	if !reDVESSID.MatchString(id.ESSID) {
		return DVApplyResult{}, dvInvalid("identity/essid")
	}
	if id.NXDNID != "" && !vNXDNID(id.NXDNID) {
		return DVApplyResult{}, dvInvalid("identity/nxdnId")
	}
	if id.Info != nil {
		if _, err := dvInfoChanges(id.Info); err != nil {
			return DVApplyResult{}, err
		}
	}
	return d.demoWrite(DVFileIDs(), []string{"analog_bridge", "mmdvm_bridge", "ysfgateway", "p25gateway", "nxdngateway"}, func(c *demoDVCfg) error {
		nx := c.identity.NXDNID
		if id.NXDNID != "" {
			nx = id.NXDNID
		}
		info := c.identity.Info
		if in := id.Info; in != nil {
			info = DVInfoView{Location: in.Location, Description: in.Description, URL: in.URL, FrequencyHz: in.FrequencyHz,
				RXHz: in.FrequencyHz, TXHz: in.FrequencyHz, Latitude: in.Latitude, Longitude: in.Longitude}
			if in.Latitude == "" && in.Longitude == "" {
				info.Latitude, info.Longitude = c.identity.Info.Latitude, c.identity.Info.Longitude
			}
		}
		c.identity = DVIdentityView{Callsign: id.Callsign, DMRID: id.DMRID, ESSID: id.ESSID, NXDNID: nx, Consistent: true, Info: info}
		return nil
	})
}

// DVSwitchCredentials implements the API's DVSwitch interface.
func (d *DemoDVSwitch) DVSwitchCredentials(_ context.Context, r DVSwitchRequest) (DVApplyResult, error) {
	cr := r.Credentials
	if cr == nil || cr.Address == nil && cr.Port == nil && cr.Password == nil && !cr.ClearPassword {
		return dvNoChange(), nil
	}
	if cr.Address != nil && !reDVHost.MatchString(*cr.Address) {
		return DVApplyResult{}, dvInvalid("DMR Network/Address")
	}
	if cr.Address != nil && DVBMHostName(*cr.Address) {
		return DVApplyResult{}, ErrDVBMMaster
	}
	if cr.Port != nil && !vInt(1, 65535)(strconv.Itoa(*cr.Port)) {
		return DVApplyResult{}, dvInvalid("DMR Network/Port")
	}
	if cr.Password != nil && cr.ClearPassword {
		return DVApplyResult{}, dvInvalid("DMR Network/Password")
	}
	if cr.Password != nil && !vPassword(*cr.Password) {
		return DVApplyResult{}, dvInvalid("DMR Network/Password")
	}
	return d.demoWrite([]string{DVFileMMDVMBridge}, []string{"mmdvm_bridge"}, func(c *demoDVCfg) error {
		if cr.Address != nil {
			c.dmr.Address = *cr.Address
		}
		if cr.Port != nil {
			c.dmr.Port = *cr.Port
		}
		if cr.Password != nil {
			c.set = true
		}
		if cr.ClearPassword {
			c.set = false
		}
		return nil
	})
}

// DVSwitchModes implements the API's DVSwitch interface.
func (d *DemoDVSwitch) DVSwitchModes(_ context.Context, r DVSwitchRequest) (DVApplyResult, error) {
	m := r.Modes
	if m == nil || m.DMR == nil && m.YSF == nil && m.P25 == nil && m.NXDN == nil {
		return dvNoChange(), nil
	}
	return d.demoWrite([]string{DVFileMMDVMBridge}, []string{"analog_bridge", "mmdvm_bridge"}, func(c *demoDVCfg) error {
		for _, e := range []struct {
			in  *bool
			out *bool
		}{{m.DMR, &c.modes.DMR}, {m.YSF, &c.modes.YSF}, {m.P25, &c.modes.P25}, {m.NXDN, &c.modes.NXDN}} {
			if e.in != nil {
				*e.out = *e.in
			}
		}
		return nil
	})
}

// DVSwitchStartup implements the API's DVSwitch interface.
func (d *DemoDVSwitch) DVSwitchStartup(_ context.Context, r DVSwitchRequest) (DVApplyResult, error) {
	s := r.Startup
	if s == nil || s.AmbeMode == nil && s.TxTg == nil && s.TxTs == nil && s.ColorCode == nil && s.YSF == nil && s.P25 == nil && s.NXDN == nil {
		return dvNoChange(), nil
	}
	if s.AmbeMode != nil && !dvAmbeModes[*s.AmbeMode] {
		return DVApplyResult{}, dvInvalid("AMBE_AUDIO/ambeMode")
	}
	if s.TxTg != nil && !reDVTG.MatchString(*s.TxTg) {
		return DVApplyResult{}, dvInvalid("AMBE_AUDIO/txTg")
	}
	if s.TxTs != nil && (*s.TxTs < 1 || *s.TxTs > 2) {
		return DVApplyResult{}, dvInvalid("AMBE_AUDIO/txTs")
	}
	if s.ColorCode != nil && (*s.ColorCode < 0 || *s.ColorCode > 15) {
		return DVApplyResult{}, dvInvalid("AMBE_AUDIO/colorCode")
	}
	for _, g := range []struct {
		v  *string
		re func(string) bool
	}{{s.YSF, func(x string) bool { return x == "" || vYSFStartup(x) }}, {s.P25, vOrEmpty(reDVRefl)}, {s.NXDN, vOrEmpty(reDVRefl)}} {
		if g.v != nil && !g.re(*g.v) {
			return DVApplyResult{}, dvInvalid("Network/Startup")
		}
	}
	return d.demoWrite([]string{DVFileAnalogBridge}, []string{"analog_bridge"}, func(c *demoDVCfg) error {
		if s.AmbeMode != nil {
			c.startup.AmbeMode = *s.AmbeMode
		}
		if s.TxTg != nil {
			c.startup.TxTg = *s.TxTg
		}
		if s.TxTs != nil {
			c.startup.TxTs = *s.TxTs
		}
		if s.ColorCode != nil {
			c.startup.ColorCode = *s.ColorCode
		}
		if s.YSF != nil {
			c.gw.YSF.Startup = *s.YSF
		}
		if s.P25 != nil {
			c.gw.P25.Startup = *s.P25
		}
		if s.NXDN != nil {
			c.gw.NXDN.Startup = *s.NXDN
		}
		return nil
	})
}

// DVSwitchRollback implements the API's DVSwitch interface.
func (d *DemoDVSwitch) DVSwitchRollback(_ context.Context, r DVSwitchRequest) (DVApplyResult, error) {
	def, ok := dvFileByID(r.File)
	if !ok {
		return DVApplyResult{}, dvInvalid("file")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.ready(); err != nil {
		return DVApplyResult{}, err
	}
	c := d.cfg()
	if c.hist[def.id] == 0 {
		return DVApplyResult{}, ErrDVNoHistory
	}
	c.hist[def.id]--
	c.times++
	return DVApplyResult{Files: []string{def.id}, Skipped: []string{}, RestartHint: []string{def.unit}, Hashes: map[string]string{}}, nil
}

// DVSwitchRestart implements the API's DVSwitch interface.
func (d *DemoDVSwitch) DVSwitchRestart(_ context.Context, r DVSwitchRequest) (DVRestartResult, error) {
	action := r.Action
	if action == "" {
		action = "restart"
	}
	if action != "restart" && action != "start" {
		return DVRestartResult{}, dvInvalid("action")
	}
	units, ok := dvScopes[r.Scope]
	if !ok {
		return DVRestartResult{}, dvInvalid("scope")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.ready(); err != nil {
		return DVRestartResult{}, err
	}
	if (r.Scope == "dmr" || r.Scope == "all") && d.stfuRunning() {
		units = append(append([]string{}, units...), dvSTFUUnit)
	}
	for _, u := range units {
		if u == dvSTFUUnit {
			d.stfu().at = d.now()
		}
		if u == "mmdvm_bridge" {
			d.dmrAt, d.dmrRuns = d.now(), d.dmrRuns+1
		}
		if u == "analog_bridge" {
			d.resetLive(d.cfg())
		}
	}
	return DVRestartResult{Action: action, Units: append([]string{}, units...), Skipped: []string{}, States: d.status().Units}, nil
}

// demoDMROutcomes is what the simulated master answers after each restart of
// mmdvm_bridge, in turn. The first (before any restart) is a login.
var demoDMROutcomes = []struct{ state, line string }{
	{DMRLoggedIn, "DMR, Logged into the master successfully: 192.0.2.10:62031"},
	{DMRLoginFailed, "DMR, Login to the master has failed, retrying login ..."},
	{DMRTimedOut, "DMR, Connection to the master has timed out, retrying connection"},
	{DMRSocketFailed, "DMR, Socket has failed, retrying connection to the master"},
	{DMRLoggedIn, "DMR, Logged into the master successfully: 192.0.2.10:62031"},
}

// demoDMRConnect is how long the simulated login takes after a restart.
const demoDMRConnect = 4 * time.Second

// DVSwitchDMRStatus implements the API's DVSwitch interface. States cycle
// with the restarts of mmdvm_bridge; the host and the master are placeholders.
func (d *DemoDVSwitch) DVSwitchDMRStatus(context.Context) (DVDMRStatus, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.installed {
		return DVDMRStatus{State: DMRNotInstalled}, nil
	}
	res := d.demoMMDVMStatus()
	if s := d.stfu(); s.installed {
		bm := DVDMRStatus{State: DMRDisabled, Via: DVViaSTFU}
		switch {
		case !s.pwSet:
		case !d.stfuRunning():
			bm.State = DMRIdle // on demand: stopped while the panel is not in BM mode
		default:
			bm = d.demoSTFUStatus()
		}
		res.BM = &bm
	}
	return res, nil
}

// demoMMDVMStatus is DMR - Other's login (caller holds d.mu).
func (d *DemoDVSwitch) demoMMDVMStatus() DVDMRStatus {
	c := d.cfg()
	if !c.modes.DMR {
		return DVDMRStatus{State: DMRDisabled, Via: DVViaMMDVM}
	}
	now := d.now()
	since := d.dmrAt
	if since.IsZero() {
		since = now.Add(-time.Hour)
	}
	res := DVDMRStatus{State: DMRConnecting, Via: DVViaMMDVM, Since: since.UTC().Format(time.RFC3339)}
	if now.Sub(since) < demoDMRConnect {
		return res
	}
	o := demoDMROutcomes[d.dmrRuns%len(demoDMROutcomes)]
	res.State, res.LastLine = o.state, o.line
	res.At = since.Add(demoDMRConnect).UTC().Format(time.RFC3339)
	if o.state == DMRLoggedIn {
		res.Master = "192.0.2.10:62031"
	}
	return res
}

// dvReservedPortsDemo: the TLV and AMBE ports of the stock files.
func dvReservedPortsDemo() []int {
	return append(append([]int(nil), dvTLVPorts...), dvSTFUTx, dvSTFURx)
}

// DVSwitchUSRP implements the API's DVSwitch interface: the demo keeps the
// [USRP] values in memory (the real ini is never touched).
func (d *DemoDVSwitch) DVSwitchUSRP(_ context.Context, r DVSwitchRequest) (DVApplyResult, error) {
	u := r.USRP
	if u == nil || (u.RxPort != 0 && u.RxPort == u.TxPort && !u.Restore) {
		return DVApplyResult{}, dvInvalid("usrp/ports")
	}
	if u.Address != "" && u.Address != "127.0.0.1" {
		return DVApplyResult{}, dvInvalid("usrp/address")
	}
	return d.demoWrite([]string{DVFileAnalogBridge}, []string{"analog_bridge"}, func(c *demoDVCfg) error {
		if u.Address != "" {
			c.usrp.Address = u.Address
		}
		if u.RxPort != 0 {
			c.usrp.RxPort = u.RxPort
		}
		if u.TxPort != 0 {
			c.usrp.TxPort = u.TxPort
		}
		return nil
	})
}

// DVSwitchPrivate implements the API's DVSwitch interface (in memory).
func (d *DemoDVSwitch) DVSwitchPrivate(_ context.Context, p DVPrivate) (DVPrivateReply, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	c := d.cfg()
	switch p.Action {
	case "get":
	case "set":
		if !p.Record.valid() {
			return DVPrivateReply{}, dvInvalid("private/record")
		}
		r := *p.Record
		c.priv = &r
	case "clear":
		c.priv = nil
	default:
		return DVPrivateReply{}, dvInvalid("private/action")
	}
	return DVPrivateReply{Record: c.priv}, nil
}
