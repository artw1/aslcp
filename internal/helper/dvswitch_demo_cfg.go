package helper

import (
	"context"
	"strconv"
	"time"
)

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
}

func newDemoDVCfg() *demoDVCfg {
	return &demoDVCfg{
		set:      true,
		identity: DVIdentityView{Callsign: "N0CALL", DMRID: "1234567", ESSID: "01", NXDNID: "12346", Consistent: true},
		dmr:      DVDMRView{Address: "192.0.2.10", Port: 62031},
		modes:    DVModesView{DMR: true, YSF: true},
		startup:  DVStartupView{AmbeMode: "DMR", TxTg: "9", TxTs: 2, ColorCode: 1},
		gw:       DVGateways{YSF: DVGatewayView{Present: true, Startup: "US-Example"}, P25: DVGatewayView{Present: true}, NXDN: DVGatewayView{Present: true}},
		hist:     map[string]int{},
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
		USRP: DVUSRPView{Address: "127.0.0.1", RxPort: 34001, TxPort: 32001}, Files: []DVFileView{}}
	v.DMR.PasswordSet = c.set
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
	return DVSwitchInfo{Found: true, Version: "1.6.4", Mode: c.startup.AmbeMode, TG: c.startup.TxTg, Slot: strconv.Itoa(c.startup.TxTs),
		ColorCode: strconv.Itoa(c.startup.ColorCode), Mute: "OFF", GatewayID: c.identity.DMRID, RepeaterID: c.identity.DMRID + c.identity.ESSID}, nil
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
	return d.demoWrite(DVFileIDs(), []string{"analog_bridge", "mmdvm_bridge", "ysfgateway", "p25gateway", "nxdngateway"}, func(c *demoDVCfg) error {
		nx := c.identity.NXDNID
		if id.NXDNID != "" {
			nx = id.NXDNID
		}
		c.identity = DVIdentityView{Callsign: id.Callsign, DMRID: id.DMRID, ESSID: id.ESSID, NXDNID: nx, Consistent: true}
		return nil
	})
}

// DVSwitchCredentials implements the API's DVSwitch interface.
func (d *DemoDVSwitch) DVSwitchCredentials(_ context.Context, r DVSwitchRequest) (DVApplyResult, error) {
	cr := r.Credentials
	if cr == nil || cr.Address == nil && cr.Port == nil && cr.Password == nil {
		return dvNoChange(), nil
	}
	if cr.Address != nil && !reDVHost.MatchString(*cr.Address) {
		return DVApplyResult{}, dvInvalid("DMR Network/Address")
	}
	if cr.Port != nil && !vInt(1, 65535)(strconv.Itoa(*cr.Port)) {
		return DVApplyResult{}, dvInvalid("DMR Network/Port")
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
	if s == nil || !s.UseCurrent && s.AmbeMode == nil && s.TxTg == nil && s.TxTs == nil && s.ColorCode == nil && s.YSF == nil && s.P25 == nil && s.NXDN == nil {
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
		if s.UseCurrent {
			return nil // the demo ABInfo is the startup state already
		}
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
	for _, u := range units {
		if u == "mmdvm_bridge" {
			d.dmrAt, d.dmrRuns = d.now(), d.dmrRuns+1
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
	c := d.cfg()
	if !c.modes.DMR {
		return DVDMRStatus{State: DMRDisabled}, nil
	}
	now := d.now()
	since := d.dmrAt
	if since.IsZero() {
		since = now.Add(-time.Hour)
	}
	res := DVDMRStatus{State: DMRConnecting, Since: since.UTC().Format(time.RFC3339)}
	if now.Sub(since) < demoDMRConnect {
		return res, nil
	}
	o := demoDMROutcomes[d.dmrRuns%len(demoDMROutcomes)]
	res.State, res.LastLine = o.state, o.line
	res.At = since.Add(demoDMRConnect).UTC().Format(time.RFC3339)
	if o.state == DMRLoggedIn {
		res.Master = "192.0.2.10:62031"
	}
	return res, nil
}
