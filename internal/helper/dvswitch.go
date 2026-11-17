package helper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// DVSwitch install and full uninstall (docs/DVSWITCH.md, M3). Everything
// here runs fixed command lines: the package list, the paths and the units
// are constants, nothing a client sends reaches a command line or a path.

const (
	dvInstallUnit   = "aslcp-dvswitch-install"
	dvUninstallUnit = "aslcp-dvswitch-uninstall"

	// DVKeyURL is the fixed keyring URL and DVKeyFingerprint the only
	// primary key it may hold (rsa4096, "ASL Repository").
	DVKeyURL         = "https://dvswitch.org/DVSwitch_Repository/dvswitch-keyring.gpg"
	DVKeyFingerprint = "6DDFC33CD5FC678659642DF572147EC1E788D4C3"

	DefaultDVKeyring    = "/usr/share/keyrings/dvswitch-keyring.gpg"
	DefaultDVSourceList = "/etc/apt/sources.list.d/dvswitch.list"
	// DefaultDVStateDir is root-owned (0700) and, like the config history,
	// deliberately not below /var/lib/aslcp (the unprivileged user's).
	DefaultDVStateDir = "/var/lib/aslcp-dvswitch"
	DefaultDVHostDir  = "/var/lib/mmdvm"

	dvMarkerFile  = "install.json"    // the ASLCP-installed marker
	dvSimFile     = "install-sim.txt" // simulated install, input of install-plan
	dvPurgeFile   = "purge.list"      // output of uninstall-plan, input of apt-get purge
	dvWget        = "wget"            //
	dvMinYear     = 2026              // clock check (apt rejects a Release file from the future)
	dvKeyMaxSize  = 1 << 20           //
	dvKeyTimeout  = 30 * time.Second  //
	dvStepTimeout = 10 * time.Minute  // one apt simulation in a job step
	dvSuite       = "bookworm"        // upstream uses it on trixie too
	dvMaxNames    = 400               //
)

// dvInstallPackages is the constant install set (docs/DVSWITCH.md, decision
// 7). It is never a parameter.
const dvInstallPackages = "dvswitch-base analog-bridge mmdvm-bridge md380-emu ysfgateway p25gateway nxdngateway wget"

// DVSwitchPackages returns the install set.
func DVSwitchPackages() []string { return strings.Fields(dvInstallPackages) }

// dvServerPkg is the metapackage of a hand install (detected, never
// installed or removed by ASLCP).
const dvServerPkg = "dvswitch-server"

// dvUnits are the units reported by the status and stopped by the uninstall.
var dvUnits = []string{"analog_bridge", "mmdvm_bridge", "netcheck", "ysfgateway", "p25gateway", "nxdngateway", "md380-emu"}

// dvHostFiles are the host files in the host dir that must be non-empty
// after an install (DVSM_Update.sh exits 0 even when its downloads fail).
var dvHostFiles = []string{"DMRIds.dat", "YSFHosts.txt", "P25Hosts.txt", "NXDNHosts.txt"}

// Fixed uninstall paths. The only pattern is the ABInfo glob.
var (
	dvUninstallDirs = []string{
		"/var/lib/mmdvm", "/var/lib/dvswitch", "/var/log/dvswitch", "/var/log/mmdvm",
		"/opt/Analog_Bridge", "/opt/MMDVM_Bridge",
	}
	dvUninstallFiles = []string{"/etc/cron.d/dvswitch", "/etc/cron.daily/dvswitch-log-cleanup"}
)

const dvABInfoGlob = "/tmp/ABInfo_*.json"

// DVSwitchUninstallPaths returns every path the uninstall removes (the
// confirm dialog lists them): leftover directories and files, the ABInfo
// glob, the apt source and the keyring (the last two are Ops paths, so they
// are given as passed).
func (o *Ops) DVSwitchUninstallPaths() []string {
	out := append([]string{}, dvUninstallDirs...)
	out = append(out, dvUninstallFiles...)
	return append(out, dvABInfoGlob, o.dvSourceList(), o.dvKeyring())
}

// Errors of the dvswitch ops. The client maps the texts back (client.go).
var (
	ErrDVAlreadyInstalled = errors.New("already_installed: DVSwitch is already installed")
	ErrDVUnsupported      = errors.New("unsupported_platform: this host is not supported")
	ErrDVClockUnset       = errors.New("clock_unset: the system clock is not set; fix the date and time first")
	ErrDVKeyMismatch      = errors.New("key_mismatch: the DVSwitch repository key does not have the expected fingerprint")
	ErrDVOutsideASLCP     = errors.New("not_aslcp_installed: DVSwitch was installed outside ASLCP; ASLCP only removes what it installed")
)

var dvErrors = []error{ErrDVAlreadyInstalled, ErrDVUnsupported, ErrDVClockUnset, ErrDVKeyMismatch, ErrDVOutsideASLCP, ErrDVInvalid, ErrDVNotInstalled, ErrDVNoHistory, ErrDVNoInfo}

// DVSwitchPkg is one package of the install set.
type DVSwitchPkg struct {
	Name      string `json:"name"`
	Installed bool   `json:"installed"`
	Version   string `json:"version,omitempty"`
}

// DVSwitchUnit is the state of one unit.
type DVSwitchUnit struct {
	Name    string `json:"name"`
	Loaded  bool   `json:"loaded"`
	Active  string `json:"active"`
	Enabled string `json:"enabled"`
}

// DVSwitchHostFile is one host file of the post-install check.
type DVSwitchHostFile struct {
	Name    string `json:"name"`
	Present bool   `json:"present"`
	Size    int64  `json:"size"`
}

// DVSwitchMarker is the root-owned record of an ASLCP install; its
// existence is what makes an install "ASLCP-installed".
type DVSwitchMarker struct {
	Version     int    `json:"version"`
	InstalledAt string `json:"installedAt"`
	// WgetPreinstalled: wget was already installed before the first install
	// job, so the uninstall keeps it.
	WgetPreinstalled bool `json:"wgetPreinstalled"`
	// Packages are the packages the simulation showed as newly installed
	// (Inst lines without a current version): the set members that were
	// missing and their dependencies, qemu-user-static among them.
	Packages []string `json:"packages"`
}

// DVSwitchJob is the state and log of one job.
type DVSwitchJob struct {
	Job string `json:"job"`
	Log string `json:"log"`
}

// DVSwitchJobs is the reply of dvswitch-job.
type DVSwitchJobs struct {
	Install   DVSwitchJob `json:"install"`
	Uninstall DVSwitchJob `json:"uninstall"`
}

// DVSwitchStatus is the reply of dvswitch-status, dvswitch-install and
// dvswitch-uninstall. It never carries a secret.
type DVSwitchStatus struct {
	Installed bool `json:"installed"`
	// InstalledBy: "aslcp" (marker present), "external" (installed, no
	// marker) or "" (not installed).
	InstalledBy       string `json:"installedBy"`
	Version           string `json:"version"`
	Arch              string `json:"arch"`
	OSCodename        string `json:"osCodename"`
	Supported         bool   `json:"supported"`
	UnsupportedReason string `json:"unsupportedReason,omitempty"`

	RepoPresent    bool   `json:"repoPresent"`
	KeyPresent     bool   `json:"keyPresent"`
	KeyOK          bool   `json:"keyOk"`
	KeyFingerprint string `json:"keyFingerprint,omitempty"`

	Packages    []DVSwitchPkg      `json:"packages"`
	Units       []DVSwitchUnit     `json:"units"`
	HostFiles   []DVSwitchHostFile `json:"hostFiles"`
	HostFilesOK bool               `json:"hostFilesOk"`

	Marker *DVSwitchMarker `json:"marker"`

	CanInstall   bool `json:"canInstall"`
	CanUninstall bool `json:"canUninstall"`
	// What the uninstall targets, for the confirm dialog. Packages still
	// needed by something else are skipped at run time (the job log says so).
	UninstallPackages []string `json:"uninstallPackages"`
	UninstallPaths    []string `json:"uninstallPaths"`
	UninstallUnits    []string `json:"uninstallUnits"`

	Install   DVSwitchJob `json:"install"`
	Uninstall DVSwitchJob `json:"uninstall"`
}

// ---- configuration defaults

func (o *Ops) dvKeyring() string    { return orDefault(o.DVKeyring, DefaultDVKeyring) }
func (o *Ops) dvSourceList() string { return orDefault(o.DVSourceList, DefaultDVSourceList) }
func (o *Ops) dvStateDir() string   { return orDefault(o.DVStateDir, DefaultDVStateDir) }
func (o *Ops) dvHostDir() string    { return orDefault(o.DVHostDir, DefaultDVHostDir) }
func (o *Ops) dvMarkerPath() string { return filepath.Join(o.dvStateDir(), dvMarkerFile) }
func (o *Ops) dvSimPath() string    { return filepath.Join(o.dvStateDir(), dvSimFile) }
func (o *Ops) dvPurgePath() string  { return filepath.Join(o.dvStateDir(), dvPurgeFile) }
func (o *Ops) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

func (o *Ops) selfPath() string {
	if o.SelfPath != "" {
		return o.SelfPath
	}
	if p, err := os.Executable(); err == nil {
		return p
	}
	return "/usr/libexec/aslcp/aslcp-helper"
}

// dvSourceLine is the apt source: the suite is always bookworm, as upstream.
func dvSourceLine(keyring string) string {
	return "deb [signed-by=" + keyring + "] http://dvswitch.org/DVSwitch_Repository " + dvSuite + " hamradio\n"
}

// ---- scripts

// shQuote single-quotes s for /bin/sh.
func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// dvInstallScript is the fixed script of the install unit. The guard and the
// marker are written by the helper binary (dvswitch-step), which is tested in
// Go; the script only sequences apt.
func (o *Ops) dvInstallScript() string {
	self, sim := shQuote(o.selfPath()), shQuote(o.dvSimPath())
	apt := "-o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold -o Dpkg::Use-Pty=0"
	return strings.Join([]string{
		"set -e",
		"rm -f /var/lib/apt/lists/dvswitch.org*",
		"apt-get update -q --allow-releaseinfo-change",
		"apt-get -s -q install --no-install-recommends " + dvInstallPackages + " > " + sim + " || { cat " + sim + "; exit 1; }",
		self + " dvswitch-step install-plan",
		"apt-get install -y -q --no-install-recommends " + apt + " " + dvInstallPackages,
		self + " dvswitch-step install-check",
		"echo 'dvswitch install finished'",
	}, "\n")
}

// dvUninstallScript is the fixed script of the uninstall unit. The plan step
// runs the simulation and aborts (unexpected_removals) before anything is
// stopped or removed. The marker is removed last.
func (o *Ops) dvUninstallScript() string {
	self, purge := shQuote(o.selfPath()), shQuote(o.dvPurgePath())
	var b []string
	b = append(b, "set -e", self+" dvswitch-step uninstall-plan")
	for _, u := range dvUnits {
		b = append(b, "systemctl disable --now "+u+".service || true")
	}
	b = append(b,
		"if [ -s "+purge+" ]; then",
		"  apt-get purge -y -q -o Dpkg::Use-Pty=0 $(cat "+purge+")",
		"fi",
		"rm -f -- "+shQuote(o.dvSourceList())+" "+shQuote(o.dvKeyring()),
		"rm -f /var/lib/apt/lists/dvswitch.org*",
		"apt-get update -q --allow-releaseinfo-change || echo 'warning: apt-get update failed'",
	)
	for _, d := range dvUninstallDirs {
		b = append(b, "rm -rf -- "+shQuote(d))
	}
	for _, f := range dvUninstallFiles {
		b = append(b, "rm -f -- "+shQuote(f))
	}
	b = append(b,
		"rm -f "+dvABInfoGlob,
		// The ini edits and their history copies (M4): the purge removes the
		// inis, the history dir is ours.
		"rm -rf -- "+shQuote(o.dvHistoryDir()),
		// The comment below is part of the script on purpose (visible in the
		// unit): M2 must hook its revert in here.
		"# TODO(M2): revert what the private node and the USRP link changed",
		"# (rpt.conf node stanza, modules.conf chan_usrp) before the marker goes.",
		"rm -f -- "+purge,
		"rm -f -- "+shQuote(o.dvMarkerPath()),
		"echo 'dvswitch uninstall finished'",
	)
	return strings.Join(b, "\n")
}

func dvArgv(unit, script string) []string {
	return []string{"systemd-run", "--no-block", "--unit=" + unit, "--collect", "--property=Type=oneshot",
		"--setenv=DEBIAN_FRONTEND=noninteractive", "--setenv=APT_LISTCHANGES_FRONTEND=none", "--setenv=LC_ALL=C",
		"/bin/sh", "-c", script}
}

func (o *Ops) dvInstallArgv() []string   { return dvArgv(dvInstallUnit, o.dvInstallScript()) }
func (o *Ops) dvUninstallArgv() []string { return dvArgv(dvUninstallUnit, o.dvUninstallScript()) }

// ---- package name rules

var dvNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9+.\-]{0,99}$`)

// dvProtectedRe matches the packages an uninstall must never touch.
var dvProtectedRe = regexp.MustCompile(`^(asterisk|allstarlink|asl3|asl-|aslcp)`)

// dvForbiddenDeps are the packages whose appearance in the install
// simulation stops the job (unexpected_deps).
var dvForbiddenDepPrefixes = []string{"apache2", "libapache2-mod-php", "php", "mosquitto", "nodejs"}

func dvForbiddenDep(name string) bool {
	for _, p := range dvForbiddenDepPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// ---- simulation parsing

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

// ParseRemovals returns the package names of the Remv and Purg lines of
// `apt-get -s purge`, sorted and de-duplicated.
func ParseRemovals(out string) []string {
	seen := map[string]bool{}
	var res []string
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		if !strings.HasPrefix(line, "Remv ") && !strings.HasPrefix(line, "Purg ") {
			continue
		}
		f := strings.Fields(line)
		if len(f) >= 2 && !seen[f[1]] {
			seen[f[1]] = true
			res = append(res, f[1])
		}
	}
	sort.Strings(res)
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

// ---- marker

// ReadDVMarker reads the marker; (nil, nil) when there is none. A marker that
// cannot be parsed or holds an invalid package name is an error (it is never
// half-trusted).
func ReadDVMarker(path string) (*DVSwitchMarker, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m DVSwitchMarker
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("dvswitch marker: %w", err)
	}
	if m.Version != 1 || len(m.Packages) > dvMaxNames {
		return nil, errors.New("dvswitch marker: unsupported content")
	}
	for _, p := range m.Packages {
		if !dvNameRe.MatchString(p) {
			return nil, errors.New("dvswitch marker: invalid package name")
		}
	}
	return &m, nil
}

// WriteDVMarker writes the marker atomically, root-only (0600) in a 0700
// directory.
func WriteDVMarker(path string, m DVSwitchMarker) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	sort.Strings(m.Packages)
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmp, ok := f.Name(), false
	defer func() {
		if !ok {
			os.Remove(tmp)
		}
	}()
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	ok = true
	return nil
}

// MergeDVMarker builds the marker of an install run. new are the packages
// this run's simulation shows as newly installed; an earlier marker (a
// repeated or resumed install) keeps its packages, its install time and its
// wget fact (wget is installed by then, which says nothing about before).
func MergeDVMarker(prev *DVSwitchMarker, newPkgs []string, wgetPreinstalled bool, now time.Time) DVSwitchMarker {
	m := DVSwitchMarker{Version: 1, InstalledAt: now.UTC().Format(time.RFC3339), WgetPreinstalled: wgetPreinstalled}
	set := map[string]bool{}
	if prev != nil {
		m.InstalledAt, m.WgetPreinstalled = prev.InstalledAt, prev.WgetPreinstalled
		for _, p := range prev.Packages {
			set[p] = true
		}
	}
	for _, p := range newPkgs {
		set[p] = true
	}
	if m.WgetPreinstalled {
		delete(set, dvWget)
	}
	for p := range set {
		m.Packages = append(m.Packages, p)
	}
	sort.Strings(m.Packages)
	if m.Packages == nil {
		m.Packages = []string{}
	}
	return m
}

// ---- status

// dpkgStatus returns name -> version for the installed ones among names.
func (o *Ops) dpkgInstalled(ctx context.Context, names []string) (map[string]string, error) {
	res := map[string]string{}
	if len(names) == 0 {
		return res, nil
	}
	argv := append([]string{"dpkg-query", "-W", "-f=${Package}\t${db:Status-Status}\t${Version}\n"}, names...)
	out, _, err := o.Run.Run(ctx, argv...)
	if err != nil {
		return nil, fmt.Errorf("dpkg-query: %w", err)
	}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\t")
		if len(f) == 3 && f[1] == "installed" {
			res[f[0]] = f[2]
		}
	}
	return res, nil
}

func (o *Ops) osCodename() string {
	data, err := os.ReadFile(o.osRelease())
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		if v, ok := strings.CutPrefix(line, "VERSION_CODENAME="); ok {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}

// DVPlatform decides whether Install is offered: host codename bookworm or
// trixie, architecture amd64, arm64 or armhf (i386 is in the repository but
// ASLCP does not ship it).
func DVPlatform(codename, arch string) (bool, string) {
	if codename != "bookworm" && codename != "trixie" {
		return false, fmt.Sprintf("Debian %q is not supported (bookworm and trixie are)", codename)
	}
	switch arch {
	case "amd64", "arm64", "armhf":
		return true, ""
	}
	return false, fmt.Sprintf("architecture %q is not supported (amd64, arm64 and armhf are)", arch)
}

// DVSwitchStatus reports everything the DVSwitch tab shows. Read-only, runs
// no apt.
func (o *Ops) DVSwitchStatus(ctx context.Context) (DVSwitchStatus, error) {
	st := DVSwitchStatus{Packages: []DVSwitchPkg{}, Units: []DVSwitchUnit{}, HostFiles: []DVSwitchHostFile{}}
	names := append(DVSwitchPackages(), dvServerPkg)
	inst, err := o.dpkgInstalled(ctx, names)
	if err != nil {
		return st, err
	}
	anySet := false
	complete := true
	for _, n := range DVSwitchPackages() {
		v, ok := inst[n]
		st.Packages = append(st.Packages, DVSwitchPkg{Name: n, Installed: ok, Version: v})
		if ok && n != dvWget {
			anySet = true
		}
		if !ok {
			complete = false
		}
	}
	_, server := inst[dvServerPkg]
	_, base := inst["dvswitch-base"]
	_, ab := inst["analog-bridge"]
	st.Installed = server || base && ab
	anySet = anySet || server
	complete = complete || server
	st.Version = inst["dvswitch-base"]
	if st.Version == "" {
		st.Version = inst[dvServerPkg]
	}

	if out, code, err := o.Run.Run(ctx, "dpkg", "--print-architecture"); err == nil && code == 0 {
		st.Arch = strings.TrimSpace(out)
	}
	st.OSCodename = o.osCodename()
	st.Supported, st.UnsupportedReason = DVPlatform(st.OSCodename, st.Arch)

	if data, err := os.ReadFile(o.dvSourceList()); err == nil {
		st.RepoPresent = strings.Contains(string(data), "dvswitch.org")
	}
	if data, err := os.ReadFile(o.dvKeyring()); err == nil {
		st.KeyPresent = true
		if fps, err := KeyringFingerprints(data); err == nil && len(fps) > 0 {
			st.KeyFingerprint = fps[0]
			st.KeyOK = len(fps) == 1 && fps[0] == dvPinnedFP
		}
	}

	st.Units = o.dvUnitStates(ctx)

	for _, n := range dvHostFiles {
		hf := DVSwitchHostFile{Name: n}
		if fi, err := os.Stat(filepath.Join(o.dvHostDir(), n)); err == nil && fi.Mode().IsRegular() {
			hf.Present, hf.Size = true, fi.Size()
		}
		st.HostFiles = append(st.HostFiles, hf)
	}
	st.HostFilesOK = len(st.HostFiles) > 0
	for _, hf := range st.HostFiles {
		if !hf.Present || hf.Size == 0 {
			st.HostFilesOK = false
		}
	}

	marker, merr := ReadDVMarker(o.dvMarkerPath())
	if merr != nil {
		marker = nil // an unreadable marker is not trusted: uninstall is not offered
	}
	st.Marker = marker
	switch {
	case marker != nil && st.Installed:
		st.InstalledBy = "aslcp"
	case marker != nil:
		st.InstalledBy = "aslcp"
	case st.Installed:
		st.InstalledBy = "external"
	}
	st.Install, st.Uninstall = o.dvJobs(ctx)
	running := st.Install.Job == JobRunning || st.Uninstall.Job == JobRunning || o.aptJobRunning(ctx)
	st.CanInstall = st.Supported && !running && !server &&
		(marker == nil && merr == nil && !anySet || marker != nil && !complete)
	st.CanUninstall = marker != nil && !running
	st.UninstallPackages, st.UninstallPaths, st.UninstallUnits = []string{}, o.DVSwitchUninstallPaths(), append([]string{}, dvUnits...)
	if marker != nil {
		st.UninstallPackages = dvPurgeCandidates(marker)
	}
	return st, nil
}

// dvPurgeCandidates is the package set plus the recorded dependencies,
// without wget when it was there before the install, without anything
// protected.
func dvPurgeCandidates(m *DVSwitchMarker) []string {
	seen := map[string]bool{}
	var res []string
	add := func(p string) {
		if seen[p] || !dvNameRe.MatchString(p) || dvProtectedRe.MatchString(p) || p == dvWget && m.WgetPreinstalled {
			return
		}
		seen[p] = true
		res = append(res, p)
	}
	for _, p := range DVSwitchPackages() {
		add(p)
	}
	for _, p := range m.Packages {
		add(p)
	}
	return res
}

func (o *Ops) dvUnitStates(ctx context.Context) []DVSwitchUnit {
	units := make([]DVSwitchUnit, 0, len(dvUnits))
	argv := []string{"systemctl", "show", "-p", "Id,LoadState,ActiveState,UnitFileState"}
	for _, u := range dvUnits {
		argv = append(argv, u+".service")
	}
	out, _, err := o.Run.Run(ctx, argv...)
	byName := map[string]DVSwitchUnit{}
	if err == nil {
		for _, block := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n\n") {
			p := ParseShow(block)
			id := strings.TrimSuffix(p["Id"], ".service")
			if id == "" {
				continue
			}
			byName[id] = DVSwitchUnit{Name: id, Loaded: p["LoadState"] == "loaded", Active: p["ActiveState"], Enabled: p["UnitFileState"]}
		}
	}
	for _, u := range dvUnits {
		if st, ok := byName[u]; ok {
			units = append(units, st)
		} else {
			units = append(units, DVSwitchUnit{Name: u, Active: "unknown"})
		}
	}
	return units
}

func (o *Ops) dvJobs(ctx context.Context) (DVSwitchJob, DVSwitchJob) {
	var in, un DVSwitchJob
	in.Job, in.Log = o.jobUnit(ctx, dvInstallUnit)
	un.Job, un.Log = o.jobUnit(ctx, dvUninstallUnit)
	return in, un
}

// DVSwitchJob reports the two jobs only (cheap, polled while one runs).
func (o *Ops) DVSwitchJob(ctx context.Context) (DVSwitchJobs, error) {
	in, un := o.dvJobs(ctx)
	return DVSwitchJobs{Install: in, Uninstall: un}, nil
}

// ---- install

// httpGet is the default fetcher of the keyring: fixed URL given by the
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

// DVSwitchInstall prepares (clock, key, source) and starts the install in a
// transient unit. Every refusal happens before anything is written.
func (o *Ops) DVSwitchInstall(ctx context.Context) (DVSwitchStatus, error) {
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
	case !st.Supported:
		return DVSwitchStatus{}, fmt.Errorf("%w: %s", ErrDVUnsupported, st.UnsupportedReason)
	case !st.CanInstall:
		return DVSwitchStatus{}, ErrDVAlreadyInstalled
	}
	if o.now().Year() < dvMinYear {
		return DVSwitchStatus{}, ErrDVClockUnset
	}
	get := o.HTTPGet
	if get == nil {
		get = dvHTTPGet
	}
	raw, err := get(ctx, DVKeyURL)
	if err != nil {
		return DVSwitchStatus{}, fmt.Errorf("cannot download the DVSwitch keyring: %w", err)
	}
	bin, err := VerifyDVKeyring(raw)
	if err != nil {
		return DVSwitchStatus{}, err
	}
	for _, p := range []string{o.dvKeyring(), o.dvSourceList()} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return DVSwitchStatus{}, fmt.Errorf("cannot create %s: %w", filepath.Dir(p), err)
		}
	}
	if err := writeAtomic(o.dvKeyring(), bin); err != nil {
		return DVSwitchStatus{}, fmt.Errorf("cannot write the keyring: %w", err)
	}
	if err := writeAtomic(o.dvSourceList(), []byte(dvSourceLine(o.dvKeyring()))); err != nil {
		return DVSwitchStatus{}, fmt.Errorf("cannot write the apt source: %w", err)
	}
	if err := os.MkdirAll(o.dvStateDir(), 0o700); err != nil {
		return DVSwitchStatus{}, fmt.Errorf("cannot create the state directory: %w", err)
	}
	out, code, err := o.Run.Run(ctx, o.dvInstallArgv()...)
	o.resetJobUnit(dvInstallUnit)
	if err != nil {
		return DVSwitchStatus{}, fmt.Errorf("systemd-run: %w", err)
	}
	if code != 0 {
		return DVSwitchStatus{}, fmt.Errorf("systemd-run failed: %s", LastLines(out, 5))
	}
	st.Install = DVSwitchJob{Job: JobRunning}
	st.CanInstall, st.CanUninstall = false, false
	return st, nil
}

// DVSwitchUninstall starts the full uninstall in a transient unit. It
// refuses when ASLCP did not install DVSwitch (no marker).
func (o *Ops) DVSwitchUninstall(ctx context.Context) (DVSwitchStatus, error) {
	o.applyMu.Lock()
	defer o.applyMu.Unlock()
	if o.aptJobRunning(ctx) {
		return DVSwitchStatus{}, ErrBusy
	}
	if !o.osCheckMu.TryLock() {
		return DVSwitchStatus{}, ErrBusy
	}
	o.osCheckMu.Unlock()
	marker, err := ReadDVMarker(o.dvMarkerPath())
	if err != nil {
		return DVSwitchStatus{}, fmt.Errorf("cannot read the install marker: %w", err)
	}
	if marker == nil {
		return DVSwitchStatus{}, ErrDVOutsideASLCP
	}
	st, err := o.DVSwitchStatus(ctx)
	if err != nil {
		return st, err
	}
	if err := os.MkdirAll(o.dvStateDir(), 0o700); err != nil {
		return DVSwitchStatus{}, err
	}
	out, code, err := o.Run.Run(ctx, o.dvUninstallArgv()...)
	o.resetJobUnit(dvUninstallUnit)
	if err != nil {
		return DVSwitchStatus{}, fmt.Errorf("systemd-run: %w", err)
	}
	if code != 0 {
		return DVSwitchStatus{}, fmt.Errorf("systemd-run failed: %s", LastLines(out, 5))
	}
	st.Uninstall = DVSwitchJob{Job: JobRunning}
	st.CanInstall, st.CanUninstall = false, false
	return st, nil
}
