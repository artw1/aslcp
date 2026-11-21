package helper

import (
	"context"
	"time"
)

// Demo side of the BrandMeister connection: STFU comes with the DVSwitch
// install (its own install is a short simulated job), saving only changes
// memory, the status cycles through connecting and logged in with
// placeholder values.

const demoSTFUDuration = 3 * time.Second

var demoSTFULog = []string{
	"Get:1 https://dvswitch.org/apt bookworm/main stfu",
	"recorded 1 newly installed packages: stfu",
	"Setting up stfu ...",
	"stfu install finished",
}

type demoSTFU struct {
	installed bool
	started   time.Time // install job start
	stay      bool      // stay logged in outside BM mode (the unit is enabled)
	pwSet     bool      // BrandMeister saved
	enabled   bool      // BrandMeister enabled in ASLCP (off by default)
	server    string
	alias     string
	startTG   string
	at        time.Time // last (re)start of stfu (the simulated login)
}

// stfuRunning: the simulated stfu unit runs (caller holds d.mu): BrandMeister
// is saved and enabled, and either it stays logged in or the panel is in BM
// mode.
func (d *DemoDVSwitch) stfuRunning() bool {
	s := d.stfu()
	return s.pwSet && s.enabled && (s.stay || d.cfg().live.panel == DVModeBM)
}

func (d *DemoDVSwitch) stfu() *demoSTFU {
	if d.st == nil {
		d.st = &demoSTFU{server: DefaultDVSTFUServer, startTG: dvSTFUStartTG}
	}
	if !d.installed {
		*d.st = demoSTFU{server: DefaultDVSTFUServer, startTG: dvSTFUStartTG}
	} else if !d.st.installed && d.st.started.IsZero() {
		d.st.installed = true // installed together with DVSwitch
	}
	return d.st
}

// stfuJob returns the simulated install job.
func (d *DemoDVSwitch) stfuJob() DVSwitchJob {
	s := d.stfu()
	if s.started.IsZero() {
		return DVSwitchJob{Job: JobIdle}
	}
	el := d.now().Sub(s.started)
	if el >= demoSTFUDuration {
		s.installed = true
		return DVSwitchJob{Job: JobSucceeded, Log: joinLines(demoSTFULog)}
	}
	n := int(el * time.Duration(len(demoSTFULog)) / demoSTFUDuration)
	return DVSwitchJob{Job: JobRunning, Log: joinLines(demoSTFULog[:n])}
}

func joinLines(l []string) string {
	out := ""
	for i, s := range l {
		if i > 0 {
			out += "\n"
		}
		out += s
	}
	return out
}

// stfuStatus fills the STFU part of the status (caller holds d.mu).
func (d *DemoDVSwitch) stfuStatus(st *DVSwitchStatus) {
	j := d.stfuJob()
	s := d.stfu()
	st.STFU = DVSTFUStatus{Installed: s.installed, Job: j}
	if s.installed {
		st.STFU.Version = "1.0.0"
	}
	st.STFU.CanInstall = d.installed && !s.installed && j.Job != JobRunning && st.Install.Job != JobRunning && st.Uninstall.Job != JobRunning
}

// DVSwitchSTFUInstall implements the API's DVSwitch interface.
func (d *DemoDVSwitch) DVSwitchSTFUInstall(context.Context) (DVSwitchStatus, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.ready(); err != nil {
		return DVSwitchStatus{}, err
	}
	s := d.stfu()
	if j := d.stfuJob(); j.Job == JobRunning {
		return DVSwitchStatus{}, ErrBusy
	}
	if s.installed {
		return DVSwitchStatus{}, ErrDVSTFUInstalled
	}
	s.started = d.now()
	return d.status(), nil
}

// bmView is the setup view part (caller holds d.mu).
func (d *DemoDVSwitch) bmView() DVBMView {
	s := d.stfu()
	v := DVBMView{Installed: s.installed, UnitEnabled: s.stay && s.pwSet && s.enabled, Enabled: s.enabled, StayLoggedIn: s.stay}
	if !s.installed {
		return v
	}
	alias := s.alias
	if alias == "" {
		alias = d.cfg().identity.Callsign
	}
	v.Present, v.Server, v.Port, v.TalkerAlias, v.StartTG = true, s.server, dvSTFUDefaultPort, alias, s.startTG
	v.TxPort, v.RxPort, v.PasswordSet = dvSTFUTx, dvSTFURx, s.pwSet
	v.Configured = s.pwSet && reDVDigits7.MatchString(d.cfg().identity.DMRID)
	return v
}

// DVSwitchBM implements the API's DVSwitch interface: it validates like the
// helper and changes memory.
func (d *DemoDVSwitch) DVSwitchBM(_ context.Context, r DVSwitchRequest) (DVApplyResult, error) {
	b := r.BM
	if b == nil {
		return DVApplyResult{}, dvInvalid("brandmeister")
	}
	switch b.Action {
	case DVBMSave:
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
	case DVBMEnable, DVBMDisable:
		if b.Server != "" || b.Password != nil || b.TalkerAlias != nil || b.StartTG != nil || b.StayLoggedIn != nil {
			return DVApplyResult{}, dvInvalid("brandmeister")
		}
	default:
		return DVApplyResult{}, dvInvalid("brandmeister/action")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.ready(); err != nil {
		return DVApplyResult{}, err
	}
	s := d.stfu()
	c := d.cfg()
	var res DVApplyResult
	switch b.Action {
	case DVBMDisable:
		if !s.enabled {
			return dvNoChange(), nil
		}
		s.enabled = false // the settings and the password stay
		if c.live.panel == DVModeBM {
			c.live.panel = DVModeDMR
		}
		return DVApplyResult{Files: []string{DVStateBMEnabled}, Skipped: []string{}, RestartHint: []string{}, Hashes: map[string]string{}}, nil
	case DVBMEnable:
		if !s.installed {
			return DVApplyResult{}, ErrDVSTFUNotInstalled
		}
		if !s.pwSet || !reDVDigits7.MatchString(c.identity.DMRID) {
			return DVApplyResult{}, ErrDVBMNotConfigured
		}
		if s.enabled {
			return dvNoChange(), nil
		}
		s.enabled, s.at = true, d.now()
		return DVApplyResult{Files: []string{DVStateBMEnabled}, Skipped: []string{}, RestartHint: []string{}, Hashes: map[string]string{}, BMLogin: "logged_in"}, nil
	default:
		if !s.installed {
			return DVApplyResult{}, ErrDVSTFUNotInstalled
		}
		if !reDVCall.MatchString(c.identity.Callsign) || !reDVDigits7.MatchString(c.identity.DMRID) {
			return DVApplyResult{}, ErrDVNoIdentity
		}
		if b.Password == nil && !s.pwSet {
			return DVApplyResult{}, ErrDVNoPassword
		}
		if b.Server != "" {
			s.server = b.Server
		}
		if b.TalkerAlias != nil {
			s.alias = *b.TalkerAlias
		}
		if b.StartTG != nil {
			s.startTG = *b.StartTG
		}
		if b.StayLoggedIn != nil {
			s.stay = *b.StayLoggedIn
		}
		s.pwSet = true
		if s.enabled {
			s.at = d.now()
			res.BMLogin = "logged_in"
		}
	}
	c.times++
	if c.hist[DVFileDVSwitch] < dvHistoryKeep {
		c.hist[DVFileDVSwitch]++
	}
	res.Files, res.Skipped, res.RestartHint, res.Hashes = []string{DVFileDVSwitch}, []string{}, []string{}, map[string]string{}
	return res, nil
}

// demoSTFUStatus is the BrandMeister login (caller holds d.mu).
func (d *DemoDVSwitch) demoSTFUStatus() DVDMRStatus {
	s := d.stfu()
	now := d.now()
	res := DVDMRStatus{State: DMRConnecting, Via: DVViaSTFU, Since: s.at.UTC().Format(time.RFC3339), Master: s.server + ":54006",
		LastLine: "Connecting to STFU server at address: " + s.server + ":54006"}
	if now.Sub(s.at) < demoDMRConnect {
		return res
	}
	res.State, res.LastLine = DMRLoggedIn, "Successful connection to BM server at ip address:"
	// a transmission from the network every half minute, 6 s long
	el := now.Sub(s.at) - demoDMRConnect
	start := s.at.Add(demoDMRConnect + el/(30*time.Second)*(30*time.Second))
	if el >= 10*time.Second {
		res.RX = &DVDMRRx{Active: now.Sub(start) < 6*time.Second, Src: "1234567", Dst: "91", Group: true, Time: start.UTC().Format(dvLogTimeLayout)}
		if !res.RX.Active {
			res.RX.Frames = 100
		}
	}
	return res
}
