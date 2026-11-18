package helper

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Operating system updates. Everything here runs fixed command lines: no
// package name, option or path ever comes from a client.

const (
	osUpgradeUnit = "aslcp-osupgrade"

	// DefaultRebootFile and DefaultRebootPkgsFile are the markers Debian
	// packages leave when a reboot is needed.
	DefaultRebootFile     = "/var/run/reboot-required"
	DefaultRebootPkgsFile = "/var/run/reboot-required.pkgs"

	// osCheckTimeout bounds apt-get update; osCallTimeout bounds the whole
	// os-check request (update plus the simulated upgrade).
	osCheckTimeout = 5 * time.Minute
	osCallTimeout  = osCheckTimeout + time.Minute

	maxOSPackages = 2000
)

// ErrAptBusy is returned by os-check when another apt or dpkg process holds
// the package locks.
var ErrAptBusy = errors.New("apt is busy, try again later")

// osUpgradeScript is the fixed script of the OS upgrade unit: a plain
// "upgrade" (never full-upgrade or dist-upgrade: nothing is removed and no
// new package is pulled in; packages that would need that stay "kept back"),
// keeping the local version of changed conffiles.
const osUpgradeScript = "apt-get update -q && " +
	"apt-get upgrade -y -q -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold -o Dpkg::Use-Pty=0"

// osUpgradeArgv is the systemd-run command line of os-apply.
func osUpgradeArgv() []string {
	return []string{"systemd-run", "--no-block", "--unit=" + osUpgradeUnit, "--collect", "--property=Type=oneshot",
		"--setenv=DEBIAN_FRONTEND=noninteractive", "--setenv=APT_LISTCHANGES_FRONTEND=none",
		"/bin/sh", "-c", osUpgradeScript}
}

// aptJobRunning reports whether an upgrade, OS upgrade or DVSwitch install or
// uninstall unit is running.
func (o *Ops) aptJobRunning(ctx context.Context) bool {
	for _, u := range []string{upgradeUnit, osUpgradeUnit, dvInstallUnit, dvUninstallUnit, dvSTFUJobUnit} {
		if state, _ := o.jobUnit(ctx, u); state == JobRunning {
			return true
		}
	}
	return false
}

func (o *Ops) rebootFiles() (string, string) {
	a, b := o.RebootFile, o.RebootPkgsFile
	if a == "" {
		a = DefaultRebootFile
	}
	if b == "" {
		b = DefaultRebootPkgsFile
	}
	return a, b
}

// ReadReboot reports whether a reboot is required and which packages asked
// for it.
func ReadReboot(file, pkgsFile string) (bool, []string) {
	if _, err := os.Stat(file); err != nil {
		return false, []string{}
	}
	pkgs := []string{}
	if f, err := os.Open(pkgsFile); err == nil {
		defer f.Close()
		seen := map[string]bool{}
		sc := bufio.NewScanner(f)
		for sc.Scan() && len(pkgs) < 200 {
			p := strings.TrimSpace(sc.Text())
			if p != "" && len(p) <= 200 && !seen[p] {
				seen[p] = true
				pkgs = append(pkgs, p)
			}
		}
		sort.Strings(pkgs)
	}
	return true, pkgs
}

// OSStatus reports the OS upgrade job and the reboot-required marker. It
// runs no apt.
func (o *Ops) OSStatus(ctx context.Context) (OSStatus, error) {
	st := OSStatus{Packages: []OSPackage{}, KeptBack: []string{}}
	st.Job, st.JobLog = o.jobUnit(ctx, osUpgradeUnit)
	st.RebootRequired, st.RebootPkgs = ReadReboot(o.rebootFiles())
	return st, nil
}

// OSCheck refreshes the package index (apt-get update) and lists what a plain
// upgrade would install. It refuses while an upgrade runs (ErrBusy) and
// reports ErrAptBusy when another apt process holds the locks.
func (o *Ops) OSCheck(ctx context.Context) (OSStatus, error) {
	if !o.osCheckMu.TryLock() {
		return OSStatus{}, ErrAptBusy
	}
	defer o.osCheckMu.Unlock()
	if o.aptJobRunning(ctx) {
		return OSStatus{}, ErrBusy
	}
	timeout := o.OSCheckTimeout
	if timeout == 0 {
		timeout = osCheckTimeout
	}
	uctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	out, code, err := o.Run.Run(uctx, "env", "LC_ALL=C", "DEBIAN_FRONTEND=noninteractive", "apt-get", "-q", "update")
	if err != nil {
		return OSStatus{}, fmt.Errorf("apt-get update: %w", err)
	}
	if code != 0 {
		return OSStatus{}, aptFailure("apt-get update", out)
	}
	out, code, err = o.Run.Run(ctx, "env", "LC_ALL=C", "DEBIAN_FRONTEND=noninteractive",
		"apt-get", "-q", "-s", "-o", "Debug::NoLocking=1", "upgrade")
	if err != nil {
		return OSStatus{}, fmt.Errorf("apt-get upgrade -s: %w", err)
	}
	if code != 0 {
		return OSStatus{}, aptFailure("apt-get upgrade -s", out)
	}
	pkgs, kept := ParseUpgradeSim(out)
	st := OSStatus{Packages: pkgs, KeptBack: kept}
	st.Job, st.JobLog = o.jobUnit(ctx, osUpgradeUnit)
	st.RebootRequired, st.RebootPkgs = ReadReboot(o.rebootFiles())
	return st, nil
}

var aptLockRe = regexp.MustCompile(`Could not get lock|Unable to acquire the (?:dpkg|apt)|Could not open lock file|dpkg frontend lock|is another process using it|held by process`)

// aptFailure turns a failed apt run into ErrAptBusy (lock held) or a short
// error with the last lines of the output.
func aptFailure(what, out string) error {
	if aptLockRe.MatchString(out) {
		return ErrAptBusy
	}
	return fmt.Errorf("%s failed: %s", what, LastLines(out, 4))
}

// instRe matches "Inst name [current] (new origin [arch])" and, on some
// systems (Raspberry Pi OS), a trailing " []" group after the closing paren:
// "Inst name [current] (new origin [arch]) []". The bracketed current version
// is absent for a new install.
var instRe = regexp.MustCompile(`^Inst (\S+)(?: \[([^\]]*)\])? \((\S+)(?: (.*))?\)(?: \[[^\]]*\])?\s*$`)

var archRe = regexp.MustCompile(`\s*\[[^\]]*\]\s*$`)

// ParseUpgradeSim parses the output of `apt-get -s upgrade`: the packages it
// would install (Inst lines) and the kept back ones.
func ParseUpgradeSim(out string) (pkgs []OSPackage, keptBack []string) {
	pkgs, keptBack = []OSPackage{}, []string{}
	inKept := false
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		if inKept {
			if strings.HasPrefix(line, " ") && strings.TrimSpace(line) != "" {
				keptBack = append(keptBack, strings.Fields(line)...)
				continue
			}
			inKept = false
		}
		switch {
		case strings.HasPrefix(line, "The following packages have been kept back"):
			inKept = true
		case strings.HasPrefix(line, "Inst "):
			m := instRe.FindStringSubmatch(line)
			if m == nil || len(pkgs) >= maxOSPackages {
				continue
			}
			origin := strings.TrimSpace(archRe.ReplaceAllString(m[4], ""))
			pkgs = append(pkgs, OSPackage{
				Name: m[1], From: m[2], To: m[3], Origin: origin,
				Security: strings.Contains(strings.ToLower(origin), "security"),
			})
		}
	}
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].Name < pkgs[j].Name })
	sort.Strings(keptBack)
	return pkgs, keptBack
}

// OSApply starts the OS upgrade in a transient systemd unit, detached from
// the helper and the server so it survives their restart (the aslcp or
// asterisk package may be among the upgraded ones).
func (o *Ops) OSApply(ctx context.Context) (OSStatus, error) {
	o.applyMu.Lock()
	defer o.applyMu.Unlock()
	if o.aptJobRunning(ctx) {
		return OSStatus{}, ErrBusy
	}
	if !o.osCheckMu.TryLock() { // a check holds the apt locks
		return OSStatus{}, ErrBusy
	}
	o.osCheckMu.Unlock()
	out, code, err := o.Run.Run(ctx, osUpgradeArgv()...)
	o.resetJobUnit(osUpgradeUnit)
	if err != nil {
		return OSStatus{}, fmt.Errorf("systemd-run: %w", err)
	}
	if code != 0 {
		return OSStatus{}, fmt.Errorf("systemd-run failed: %s", LastLines(out, 5))
	}
	st := OSStatus{Packages: []OSPackage{}, KeptBack: []string{}, Job: JobRunning}
	st.RebootRequired, st.RebootPkgs = ReadReboot(o.rebootFiles())
	return st, nil
}

// Reboot reboots the machine (systemctl reboot, without waiting). It refuses
// while an upgrade runs.
func (o *Ops) Reboot(ctx context.Context) (OKResult, error) {
	o.applyMu.Lock()
	defer o.applyMu.Unlock()
	if o.aptJobRunning(ctx) {
		return OKResult{}, ErrBusy
	}
	out, code, err := o.Run.Run(ctx, "systemctl", "--no-block", "reboot")
	if err != nil {
		return OKResult{}, fmt.Errorf("systemctl: %w", err)
	}
	if code != 0 {
		return OKResult{}, fmt.Errorf("systemctl reboot failed: %s", LastLines(out, 3))
	}
	return OKResult{OK: true}, nil
}

// AslcpRestart restarts the aslcp service. systemctl is told not to wait, so
// the request is answered before systemd stops the service that asked.
func (o *Ops) AslcpRestart(ctx context.Context) (OKResult, error) {
	out, code, err := o.Run.Run(ctx, "systemctl", "--no-block", "restart", "aslcp.service")
	if err != nil {
		return OKResult{}, fmt.Errorf("systemctl: %w", err)
	}
	if code != 0 {
		return OKResult{}, fmt.Errorf("systemctl restart failed: %s", LastLines(out, 3))
	}
	return OKResult{OK: true}, nil
}
