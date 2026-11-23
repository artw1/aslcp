package helper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	dvctl "github.com/artw1/aslcp/internal/dv/ctl"
	"github.com/artw1/aslcp/internal/legacy"
)

// Digital modes: enabling installs the md380-emu vocoder from the third-party
// package repository that ships it, stops the packages of the previous
// digital voice stack and starts aslcp-dv (docs/DV.md). Everything here runs
// fixed command lines: the package list, the paths and the units are
// constants, nothing a client sends reaches a command line or a path.

const (
	// DVKeyURL is the fixed keyring URL and DVKeyFingerprint the only
	// primary key it may hold (rsa4096).
	DVKeyURL         = legacy.KeyURL
	DVKeyFingerprint = "6DDFC33CD5FC678659642DF572147EC1E788D4C3"

	DefaultDVKeyring    = legacy.KeyringPath
	DefaultDVSourceList = legacy.SourcesPath

	dvRepoLine   = legacy.AptRepoURL
	dvSuite      = "bookworm" // the repository uses it on trixie too
	dvKeyMaxSize = 1 << 20
	dvKeyTimeout = 30 * time.Second

	dvEngineUnit = "aslcp-dv"
	dvMD380Unit  = "md380-emu"
	dvMD380Pkg   = "md380-emu"
	dvQemuPkg    = "qemu-user-static"
	// dvDropin is the md380-emu drop-in: the vocoder only talks to the engine.
	dvDropinPath = "/etc/systemd/system/md380-emu.service.d/aslcp.conf"
	dvDropin     = "[Service]\nIPAddressDeny=any\nIPAddressAllow=localhost\n"

	dvJobLines = 200
)

// Steps of the enable unit (the script calls `aslcp-helper dv-step <step>`).
const (
	StepInstallPlan = "install-plan"
	StepConfig      = "config"
	StepCheck       = "check"
)

// dvLegacyUnits are the units of the previous digital voice stack. Enabling
// stops and disables them (they hold the ports the engine uses); the purge
// step removes the packages ASLCP installed (dv_purge.go).
var dvLegacyUnits = legacy.Units

// dvForbiddenDepPrefixes are the packages whose appearance in the install
// simulation stops the job (unexpected_deps).
var dvForbiddenDepPrefixes = []string{"apache2", "libapache2-mod-php", "php", "mosquitto", "nodejs"}

// Errors of the dv ops. The client maps the texts back (client.go).
var (
	ErrDVUnsupported  = errors.New("not_supported: this host is not supported")
	ErrDVClockUnset   = errors.New("clock_unset: the system clock is not set; fix the date and time first")
	ErrDVKeyMismatch  = errors.New("key_mismatch: the repository key does not have the expected fingerprint")
	ErrDVNotEnabled   = errors.New("not_enabled: digital modes are not enabled")
	ErrDVBadRequest   = errors.New("bad_request: invalid request")
	ErrDVUnexpectedDe = errors.New("unexpected_deps: the install would pull in unexpected packages")
)

var dvErrors = []error{ErrDVUnsupported, ErrDVClockUnset, ErrDVKeyMismatch, ErrDVNotEnabled, ErrDVBadRequest, ErrDVUnexpectedDe}

// DVRequest is the dv field of dv-private: Private sets the record, Clear
// removes it; neither reads it.
type DVRequest struct {
	Private *DVPrivateRecord `json:"private,omitempty"`
	Clear   bool             `json:"clear,omitempty"`
	// Network is dv-network's argument (dv_network.go).
	Network *DVNetworkRequest `json:"network,omitempty"`
}

// DVStatus is the reply of dv-status, dv-enable, dv-disable, dv-restart and
// dv-restore-discard. It never carries a secret.
type DVStatus struct {
	Supported      bool   `json:"supported"` // DVPlatform and not ARMv6
	Reason         string `json:"reason,omitempty"`
	Arch           string `json:"arch,omitempty"`
	MD380Installed bool   `json:"md380Installed"`
	Enabled        bool   `json:"enabled"` // aslcp-dv.service is enabled
	Active         bool   `json:"active"`
	MD380Active    bool   `json:"md380Active"`
	Job            string `json:"job,omitempty"` // "", running, failed, done
	// LegacyUnits are the units of the previous stack that still exist.
	LegacyUnits []string `json:"legacyUnits,omitempty"`
}

// DVJob is the state and log of the enable unit (the last 200 lines).
type DVJob struct {
	Job string `json:"job"`
	Log string `json:"log"`
	// At is the Unix time of the job's last journal line (0 when unknown).
	At int64 `json:"at,omitempty"`
}

// DVConfigResult is the reply of dv-config.
type DVConfigResult struct {
	Path    string `json:"path"`
	Changed bool   `json:"changed"`
}

// isDVOp reports whether op may carry the dv field.
func isDVOp(op string) bool { return op == OpDVPrivate || op == OpDVNetwork }

// ---- package rules

func dvForbiddenDep(name string) bool {
	for _, p := range dvForbiddenDepPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// DVInst is one Inst line of an install simulation.
type DVInst struct {
	Name string
	// New: no current version, the package is not installed yet.
	New bool
}

var dvInstRe = regexp.MustCompile(`^Inst (\S+)(?: \[[^\]]*\])?`)

// ParseInstLines returns the Inst lines of `apt-get -s install`.
func ParseInstLines(out string) []DVInst {
	var res []DVInst
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		if !strings.HasPrefix(line, "Inst ") {
			continue
		}
		m := dvInstRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		res = append(res, DVInst{Name: m[1], New: !strings.HasPrefix(strings.TrimPrefix(line, "Inst "+m[1]), " [")})
	}
	return res
}

// UnexpectedDeps returns the Inst names the install guard refuses.
func UnexpectedDeps(inst []DVInst) []string {
	var bad []string
	for _, i := range inst {
		if dvForbiddenDep(i.Name) {
			bad = append(bad, i.Name)
		}
	}
	sort.Strings(bad)
	return bad
}

// protected packages are never recorded as ours.
var dvProtectedRe = regexp.MustCompile(`^(asterisk|allstarlink|asl3|asl-|aslcp)`)

// dvPackages is the install set for arch: the vocoder emulator, and qemu
// user-mode emulation where the host cannot run its armhf binary natively.
func dvPackages(arch string) []string {
	if arch == "armhf" {
		return []string{dvMD380Pkg}
	}
	return []string{dvMD380Pkg, dvQemuPkg}
}

// ---- platform

// dvCPUInfo reads the CPU model lines for the ARMv6 check.
func (o *Ops) dvARMv6() bool {
	data, err := os.ReadFile(orDefault(o.DVCpuInfo, "/proc/cpuinfo"))
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if ok && strings.TrimSpace(k) == "model name" && strings.Contains(v, "(v6l)") {
			return true
		}
	}
	return false
}

func (o *Ops) dvArch(ctx context.Context) string {
	if out, code, err := o.Run.Run(ctx, "dpkg", "--print-architecture"); err == nil && code == 0 {
		return strings.TrimSpace(out)
	}
	return ""
}

// dvSupported combines the platform check with the ARMv6 refusal.
func (o *Ops) dvSupported(arch string) (bool, string) {
	ok, reason := DVPlatform(o.osCodename(), arch)
	if ok && arch == "armhf" && o.dvARMv6() {
		return false, "armv6_unsupported"
	}
	return ok, reason
}

// ---- status

// DVStatus reports what the digital modes page shows. Read-only, runs no apt.
func (o *Ops) DVStatus(ctx context.Context) (DVStatus, error) {
	st := DVStatus{}
	inst, err := o.dpkgInstalled(ctx, []string{dvMD380Pkg})
	if err != nil {
		return st, err
	}
	_, st.MD380Installed = inst[dvMD380Pkg]
	st.Arch = o.dvArch(ctx)
	st.Supported, st.Reason = o.dvSupported(st.Arch)

	units := append([]string{dvEngineUnit, dvMD380Unit}, dvLegacyUnits...)
	for _, u := range o.dvUnitStates(ctx, units) {
		switch u.Name {
		case dvEngineUnit:
			st.Enabled, st.Active = u.Enabled == "enabled", u.Active == "active"
		case dvMD380Unit:
			st.MD380Active = u.Active == "active"
		default:
			if u.Loaded {
				st.LegacyUnits = append(st.LegacyUnits, u.Name)
			}
		}
	}
	j := o.dvJob(ctx)
	switch j.Job {
	case JobRunning:
		st.Job = "running"
	case JobFailed:
		st.Job = "failed"
	case JobSucceeded:
		st.Job = "done"
	}
	return st, nil
}

func (o *Ops) dvJob(ctx context.Context) DVJob {
	var j DVJob
	j.Job, j.Log = o.jobUnit(ctx, dvEnableUnit)
	j.Log = LastLines(j.Log, dvJobLines)
	if j.Job != JobIdle {
		j.At = o.dvJobTime(ctx, dvEnableUnit)
	}
	return j
}

// dvJobTime returns the Unix time of the unit's last journal line, or the
// time the job was started by this helper while it has no line yet.
func (o *Ops) dvJobTime(ctx context.Context, unit string) int64 {
	out, code, err := o.Run.Run(ctx, "journalctl", "-u", unit, "-n", "1", "-o", "short-unix", "-q", "--no-pager")
	if err == nil && code == 0 {
		if f := strings.Fields(out); len(f) > 0 {
			if v, perr := strconv.ParseFloat(f[0], 64); perr == nil && v > 0 {
				return int64(v)
			}
		}
	}
	o.jobMu.Lock()
	t := o.startedAt[unit]
	o.jobMu.Unlock()
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

// DVJob reports the enable job only (cheap, polled while it runs).
func (o *Ops) DVJob(ctx context.Context) (DVJob, error) {
	return o.dvJob(ctx), nil
}

// ---- enable

// dvSourceLine is the apt source: the suite is always bookworm.
func dvSourceLine(keyring string) string {
	return "deb [signed-by=" + keyring + "] " + dvRepoLine + " " + dvSuite + " hamradio\n"
}

// shQuote single-quotes s for /bin/sh.
func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// dvEnableScript is the fixed script of the enable unit. The guard, the
// marker, the config and the check are done by the helper binary (dv-step),
// which is tested in Go; the script only sequences apt and systemctl.
func (o *Ops) dvEnableScript(arch string) string {
	self, sim := shQuote(o.selfPath()), shQuote(o.dvSimPath())
	pkgs := strings.Join(dvPackages(arch), " ")
	apt := "-o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold -o Dpkg::Use-Pty=0"
	b := []string{
		"set -e",
		"rm -f " + legacy.AptListsGlob,
		"apt-get update -q -o DPkg::Lock::Timeout=600 --allow-releaseinfo-change",
		"apt-get -s -q install --no-install-recommends " + pkgs + " > " + sim + " || { cat " + sim + "; exit 1; }",
		self + " dv-step " + StepInstallPlan,
		"apt-get install -y -q -o DPkg::Lock::Timeout=600 --no-install-recommends " + apt + " " + pkgs,
	}
	for _, u := range dvLegacyUnits {
		b = append(b,
			"if systemctl cat "+u+".service >/dev/null 2>&1; then echo 'stopping "+u+"'; systemctl disable --now "+u+".service || true; fi")
	}
	b = append(b,
		"mkdir -p "+shQuote(filepath.Dir(dvDropinPath)),
		"printf '%s' "+shQuote(dvDropin)+" > "+shQuote(dvDropinPath),
		self+" dv-step "+StepConfig,
		"systemctl daemon-reload",
		"systemctl enable --now "+dvMD380Unit+".service "+dvEngineUnit+".service",
		self+" dv-step "+StepCheck,
		// Retry of the upgrade purge; a failure never fails the enable.
		self+" dv-step "+StepPurgeLegacy+" || echo 'warning: removing the previous digital voice stack failed (see above)'",
		"echo 'digital modes enabled'",
	)
	return strings.Join(b, "\n")
}

func dvArgv(unit, script string) []string {
	return []string{"systemd-run", "--no-block", "--unit=" + unit, "--collect", "--property=Type=oneshot",
		"--setenv=DEBIAN_FRONTEND=noninteractive", "--setenv=APT_LISTCHANGES_FRONTEND=none", "--setenv=LC_ALL=C",
		"/bin/sh", "-c", script}
}

// dvHTTPGet is the default fetcher of the keyring: fixed URL given by the
// caller, redirects refused, size capped.
func dvHTTPGet(ctx context.Context, url string) ([]byte, error) {
	c := &http.Client{Timeout: dvKeyTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("keyring download: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, dvKeyMaxSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > dvKeyMaxSize {
		return nil, errors.New("keyring download: too large")
	}
	return data, nil
}

// DVEnable prepares (clock, key, source) and starts the enable job in a
// transient unit. Every refusal happens before anything is written. Running
// it again repairs an install.
func (o *Ops) DVEnable(ctx context.Context) (DVStatus, error) {
	o.applyMu.Lock()
	defer o.applyMu.Unlock()
	if o.aptJobRunning(ctx) {
		return DVStatus{}, ErrBusy
	}
	if !o.osCheckMu.TryLock() {
		return DVStatus{}, ErrBusy
	}
	o.osCheckMu.Unlock()
	st, err := o.DVStatus(ctx)
	if err != nil {
		return st, err
	}
	if !st.Supported {
		return DVStatus{}, fmt.Errorf("%w: %s", ErrDVUnsupported, st.Reason)
	}
	if o.now().Year() < dvMinYear {
		return DVStatus{}, ErrDVClockUnset
	}
	get := o.HTTPGet
	if get == nil {
		get = dvHTTPGet
	}
	raw, err := get(ctx, DVKeyURL)
	if err != nil {
		return DVStatus{}, fmt.Errorf("cannot download the package repository keyring: %w", err)
	}
	bin, err := VerifyDVKeyring(raw)
	if err != nil {
		return DVStatus{}, err
	}
	for _, p := range []string{o.dvKeyring(), o.dvSourceList()} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return DVStatus{}, fmt.Errorf("cannot create %s: %w", filepath.Dir(p), err)
		}
	}
	if err := writeAtomic(o.dvKeyring(), bin); err != nil {
		return DVStatus{}, fmt.Errorf("cannot write the keyring: %w", err)
	}
	if err := writeAtomic(o.dvSourceList(), []byte(dvSourceLine(o.dvKeyring()))); err != nil {
		return DVStatus{}, fmt.Errorf("cannot write the apt source: %w", err)
	}
	if err := os.MkdirAll(o.dvStateDir(), 0o700); err != nil {
		return DVStatus{}, fmt.Errorf("cannot create the state directory: %w", err)
	}
	out, code, err := o.Run.Run(ctx, dvArgv(dvEnableUnit, o.dvEnableScript(st.Arch))...)
	o.resetJobUnit(dvEnableUnit)
	if err != nil {
		return DVStatus{}, fmt.Errorf("systemd-run: %w", err)
	}
	if code != 0 {
		return DVStatus{}, fmt.Errorf("systemd-run failed: %s", LastLines(out, 5))
	}
	st.Job = "running"
	return st, nil
}

// DVDisable stops and disables aslcp-dv. md380-emu and the marker stay.
func (o *Ops) DVDisable(ctx context.Context) (DVStatus, error) {
	o.applyMu.Lock()
	defer o.applyMu.Unlock()
	if o.dvBusy(ctx) {
		return DVStatus{}, ErrBusy
	}
	out, code, err := o.Run.Run(ctx, "systemctl", "disable", "--now", dvEngineUnit+".service")
	if err != nil {
		return DVStatus{}, fmt.Errorf("systemctl: %w", err)
	}
	if code != 0 {
		return DVStatus{}, fmt.Errorf("systemctl disable failed: %s", LastLines(out, 5))
	}
	return o.DVStatus(ctx)
}

// DVRestart restarts aslcp-dv (the fallback when its control socket cannot
// be reached). It refuses while digital modes are not enabled.
func (o *Ops) DVRestart(ctx context.Context) (DVStatus, error) {
	o.applyMu.Lock()
	defer o.applyMu.Unlock()
	if o.dvBusy(ctx) {
		return DVStatus{}, ErrBusy
	}
	st, err := o.DVStatus(ctx)
	if err != nil {
		return st, err
	}
	if !st.Enabled {
		return DVStatus{}, ErrDVNotEnabled
	}
	out, code, err := o.Run.Run(ctx, "systemctl", "restart", dvEngineUnit+".service")
	if err != nil {
		return DVStatus{}, fmt.Errorf("systemctl: %w", err)
	}
	if code != 0 {
		return DVStatus{}, fmt.Errorf("systemctl restart failed: %s", LastLines(out, 5))
	}
	return o.DVStatus(ctx)
}

// ---- steps of the enable unit

// RunDVStep runs one step of the enable unit as root. A non-nil error makes
// the unit fail; its text is printed to w by the caller. Steps of earlier
// versions that may still be called by a job started before an upgrade do
// nothing.
func (o *Ops) RunDVStep(ctx context.Context, step string, w io.Writer) error {
	switch step {
	case StepInstallPlan:
		return o.dvInstallPlan(ctx, w)
	case StepConfig:
		res, err := o.DVConfig(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintf(w, "engine configuration written to %s\n", res.Path)
		return nil
	case StepCheck:
		return o.dvCheck(ctx, w)
	case StepPurgeLegacy:
		return o.PurgeLegacy(ctx, w)
	case "install-check", "install-defaults", "restore-apply":
		fmt.Fprintf(w, "step %s is no longer used\n", step)
		return nil
	}
	return fmt.Errorf("unknown step %q", step)
}

// dvInstallPlan reads the simulation written by the script, refuses a plan
// that pulls in a web server, PHP, a broker or Node.js (unexpected_deps),
// and records the newly installed packages in the marker before anything is
// installed.
func (o *Ops) dvInstallPlan(ctx context.Context, w io.Writer) error {
	data, err := os.ReadFile(o.dvSimPath())
	if err != nil {
		return fmt.Errorf("cannot read the simulation: %w", err)
	}
	inst := ParseInstLines(string(data))
	prev, err := ReadDVMarker(o.dvMarkerPath())
	if err != nil {
		return err
	}
	if bad := UnexpectedDeps(inst); len(bad) > 0 {
		fmt.Fprintf(w, "unexpected_deps: the install would also install %s; nothing was installed\n", strings.Join(bad, ", "))
		if prev == nil { // nothing is installed yet: do not leave the repository behind
			_ = os.Remove(o.dvSourceList())
			_ = os.Remove(o.dvKeyring())
			fmt.Fprintln(w, "the package repository and its key were removed again")
		}
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
	m := MergeDVMarker(prev, newPkgs, o.now())
	if err := WriteDVMarker(o.dvMarkerPath(), m); err != nil {
		return fmt.Errorf("cannot write the install marker: %w", err)
	}
	fmt.Fprintf(w, "recorded %d newly installed packages: %s\n", len(m.Packages), strings.Join(m.Packages, " "))
	_ = os.Remove(o.dvSimPath())
	return nil
}

// dvCheck waits for the engine's control socket and a status with a working
// vocoder. It never fails the job: it prints a warning instead.
func (o *Ops) dvCheck(ctx context.Context, w io.Writer) error {
	wait := o.DVCheckWait
	if wait <= 0 {
		wait = 10 * time.Second
	}
	cl := dvctl.NewClient(orDefault(o.DVSocket, dvctl.SocketPath))
	deadline := time.Now().Add(wait)
	var last error
	for {
		cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		st, err := cl.Status(cctx)
		cancel()
		if err == nil && st.Vocoder.OK {
			fmt.Fprintf(w, "engine %s is running and the vocoder answers\n", st.Version)
			return nil
		}
		if err == nil {
			last = errors.New("the vocoder does not answer yet")
		} else {
			last = err
		}
		if !time.Now().Before(deadline) || ctx.Err() != nil {
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(250 * time.Millisecond):
		}
	}
	fmt.Fprintf(w, "warning: the engine check did not pass within %s: %v\n", wait, last)
	return nil
}

// dvOwnedPackages are the packages a backup restore leaves to the digital
// modes enable job: ours plus the previous stack's install set (an old
// backup may list them).
var dvOwnedPackages = []string{dvMD380Pkg, dvQemuPkg, "analog-bridge", "mmdvm-bridge", "ysfgateway", "p25gateway", "nxdngateway", "stfu"}

// IsDVPackage reports whether name belongs to the digital modes install or
// the previous third-party stack (a prefix match on its vendor name).
func IsDVPackage(name string) bool {
	if legacy.IsLegacyPackage(name) {
		return true
	}
	for _, p := range dvOwnedPackages {
		if p == name {
			return true
		}
	}
	return false
}
