package helper

import (
	"context"
	"strings"
	"sync"
	"time"
)

// DemoDVSwitch is an in-memory DVSwitch for the demo mode: install and
// uninstall are simulated jobs that take a few seconds and touch nothing.
type DemoDVSwitch struct {
	mu        sync.Mutex
	now       func() time.Time
	installed bool
	deps      bool
	kind      string // "install" or "uninstall" while a job runs or after one
	started   time.Time
	finished  bool
	c         *demoDVCfg // M4 config (dvswitch_demo_cfg.go)
	// Lists serves the host lists from a temporary directory (NewDemoOps);
	// nil means there are none.
	Lists *Ops

	// DMR login simulation (dvswitch_demo_cfg.go): the demo never reads the
	// real /var/log/mmdvm.
	dmrAt   time.Time
	dmrRuns int

	// BrandMeister (STFU) (dvswitch_demo_stfu.go)
	st *demoSTFU
}

// NewDemoDVSwitch returns a demo node without DVSwitch.
func NewDemoDVSwitch() *DemoDVSwitch { return &DemoDVSwitch{now: time.Now} }

const demoDVDuration = 6 * time.Second

var demoDVInstallLog = []string{
	"Hit:1 http://deb.debian.org/debian bookworm InRelease",
	"Get:2 https://dvswitch.org/apt bookworm InRelease",
	"recorded 9 newly installed packages: analog-bridge dvswitch-base md380-emu mmdvm-bridge nxdngateway p25gateway qemu-user-static wget ysfgateway",
	"Setting up dvswitch-base ...",
	"Setting up analog-bridge ...",
	"Setting up mmdvm-bridge ...",
	"host files ok",
	"dvswitch install finished",
}

var demoDVUninstallLog = []string{
	"purge plan: analog-bridge dvswitch-base md380-emu mmdvm-bridge nxdngateway p25gateway qemu-user-static ysfgateway",
	"Stopping analog_bridge.service ...",
	"Removing analog-bridge ...",
	"Purging configuration files for dvswitch-base ...",
	"dvswitch uninstall finished",
}

// progress returns the job state and the part of the log shown so far.
func (d *DemoDVSwitch) progress() (job string, log string) {
	if d.kind == "" {
		return JobIdle, ""
	}
	lines := demoDVInstallLog
	if d.kind == "uninstall" {
		lines = demoDVUninstallLog
	}
	el := d.now().Sub(d.started)
	if el >= demoDVDuration {
		if !d.finished {
			d.finished = true
			d.installed = d.kind == "install"
			d.deps = d.installed
		}
		return JobSucceeded, strings.Join(lines, "\n")
	}
	n := int(el * time.Duration(len(lines)) / demoDVDuration)
	return JobRunning, strings.Join(lines[:n], "\n")
}

func (d *DemoDVSwitch) status() DVSwitchStatus {
	job, log := d.progress()
	st := DVSwitchStatus{
		Arch: "arm64", OSCodename: "bookworm", Supported: true,
		Packages: []DVSwitchPkg{}, HostFiles: []DVSwitchHostFile{}, Units: []DVSwitchUnit{},
		UninstallPaths: (&Ops{}).DVSwitchUninstallPaths(), UninstallUnits: append([]string{}, dvUnits...),
		UninstallPackages: []string{},
	}
	for _, n := range DVSwitchPackages() {
		st.Packages = append(st.Packages, DVSwitchPkg{Name: n, Installed: d.installed, Version: map[bool]string{true: "1.7.0"}[d.installed]})
	}
	for _, u := range dvUnits {
		a := DVSwitchUnit{Name: u, Loaded: d.installed, Active: "inactive"}
		if d.installed {
			a.Active, a.Enabled = "active", "enabled"
		}
		st.Units = append(st.Units, a)
	}
	for _, n := range dvHostFiles {
		st.HostFiles = append(st.HostFiles, DVSwitchHostFile{Name: n, Present: d.installed, Size: map[bool]int64{true: 4096}[d.installed]})
	}
	st.HostFilesOK = d.installed
	running := job == JobRunning
	if d.installed {
		st.Installed, st.InstalledBy, st.Version = true, "aslcp", "1.7.0"
		st.RepoPresent, st.KeyPresent, st.KeyOK, st.KeyFingerprint = true, true, true, DVKeyFingerprint
		st.Marker = &DVSwitchMarker{Version: 1, InstalledAt: "2026-11-17T12:00:00Z", Packages: []string{"qemu-user-static", "wget"}}
		st.UninstallPackages = append(DVSwitchPackages(), "qemu-user-static")
	}
	st.CanInstall = !d.installed && !running
	st.CanUninstall = d.installed && !running
	switch d.kind {
	case "install":
		st.Install = DVSwitchJob{Job: job, Log: log}
	case "uninstall":
		st.Uninstall = DVSwitchJob{Job: job, Log: log}
	}
	if st.Install.Job == "" {
		st.Install.Job = JobIdle
	}
	if st.Uninstall.Job == "" {
		st.Uninstall.Job = JobIdle
	}
	d.stfuStatus(&st)
	return st
}

// DVSwitchStatus implements the API's DVSwitch interface.
func (d *DemoDVSwitch) DVSwitchStatus(context.Context) (DVSwitchStatus, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.status(), nil
}

// DVSwitchJob implements the API's DVSwitch interface.
func (d *DemoDVSwitch) DVSwitchJob(context.Context) (DVSwitchJobs, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	st := d.status()
	return DVSwitchJobs{Install: st.Install, Uninstall: st.Uninstall}, nil
}

func (d *DemoDVSwitch) start(kind string) (DVSwitchStatus, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if job, _ := d.progress(); job == JobRunning {
		return DVSwitchStatus{}, ErrBusy
	}
	if kind == "install" && d.installed {
		return DVSwitchStatus{}, ErrDVAlreadyInstalled
	}
	if kind == "uninstall" && !d.installed {
		return DVSwitchStatus{}, ErrDVOutsideASLCP
	}
	d.kind, d.started, d.finished = kind, d.now(), false
	return d.status(), nil
}

// DVSwitchInstall implements the API's DVSwitch interface.
func (d *DemoDVSwitch) DVSwitchInstall(context.Context) (DVSwitchStatus, error) {
	return d.start("install")
}

// DVSwitchUninstall implements the API's DVSwitch interface.
func (d *DemoDVSwitch) DVSwitchUninstall(context.Context) (DVSwitchStatus, error) {
	return d.start("uninstall")
}
