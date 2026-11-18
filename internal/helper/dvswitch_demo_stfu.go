package helper

import (
	"context"
	"time"
)

// Demo side of the DMR connection method: STFU's install is a short
// simulated job, the switch only changes memory, the status cycles through
// connecting and logged in with placeholder values.

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
	via       string
	server    string
	alias     string
	startTG   string
	at        time.Time // last switch to STFU (the simulated login)
}

func (d *DemoDVSwitch) stfu() *demoSTFU {
	if d.st == nil {
		d.st = &demoSTFU{via: DVViaMMDVM, server: DefaultDVSTFUServer, startTG: dvSTFUStartTG}
	}
	if !d.installed {
		d.st.installed, d.st.via, d.st.started = false, DVViaMMDVM, time.Time{}
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

// dmrConnView is the setup view part (caller holds d.mu).
func (d *DemoDVSwitch) dmrConnView() DVDMRConnView {
	s := d.stfu()
	v := DVDMRConnView{Via: s.via}
	if s.via == DVViaSTFU {
		v.Network = DVNetBrandMeister
	}
	if s.installed {
		alias := s.alias
		if alias == "" {
			alias = d.cfg().identity.Callsign
		}
		v.STFU = DVSTFUView{Present: true, Server: s.server, Port: dvSTFUDefaultPort, TalkerAlias: alias, StartTG: s.startTG,
			TxPort: dvSTFUTx, RxPort: dvSTFURx, PasswordSet: s.via == DVViaSTFU && d.cfg().set}
	}
	return v
}

// DVSwitchDMRVia implements the API's DVSwitch interface: it validates like
// the helper and switches the method in memory.
func (d *DemoDVSwitch) DVSwitchDMRVia(_ context.Context, r DVSwitchRequest) (DVApplyResult, error) {
	v := r.DMRVia
	if v == nil {
		return DVApplyResult{}, dvInvalid("dmrVia")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.ready(); err != nil {
		return DVApplyResult{}, err
	}
	s := d.stfu()
	c := d.cfg()
	files := []string{DVFileDVSwitch, DVFileMMDVMBridge, DVFileAnalogBridge}
	switch v.Via {
	case DVViaMMDVM:
		if v.Network != "" || v.Server != "" || v.TalkerAlias != nil || v.StartTG != nil {
			return DVApplyResult{}, dvInvalid("dmrVia")
		}
		if s.via == DVViaMMDVM {
			return dvNoChange(), nil
		}
		s.via = DVViaMMDVM
		files = files[1:]
	case DVViaSTFU:
		if _, ok := dvDMRMethodFor(v.Network, v.Via); !ok {
			return DVApplyResult{}, dvInvalid("dmrVia/network")
		}
		if v.Server != "" && !vHostname(v.Server) {
			return DVApplyResult{}, dvInvalid("dmrVia/server")
		}
		if v.TalkerAlias != nil && !vTalkerAlias(*v.TalkerAlias) {
			return DVApplyResult{}, dvInvalid("dmrVia/talkerAlias")
		}
		if v.StartTG != nil && !reDVTG.MatchString(*v.StartTG) {
			return DVApplyResult{}, dvInvalid("dmrVia/startTg")
		}
		if !s.installed {
			return DVApplyResult{}, ErrDVSTFUNotInstalled
		}
		if !c.set {
			return DVApplyResult{}, ErrDVNoPassword
		}
		if v.Server != "" {
			s.server = v.Server
		}
		if v.TalkerAlias != nil {
			s.alias = *v.TalkerAlias
		}
		if v.StartTG != nil {
			s.startTG = *v.StartTG
		}
		s.via, s.at = DVViaSTFU, d.now()
	default:
		return DVApplyResult{}, dvInvalid("dmrVia/via")
	}
	c.times++
	for _, id := range files {
		if c.hist[id] < dvHistoryKeep {
			c.hist[id]++
		}
	}
	return DVApplyResult{Files: files, Skipped: []string{}, RestartHint: []string{}, Hashes: map[string]string{}}, nil
}

// demoSTFUStatus is the DMR status with STFU in use (caller holds d.mu).
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
