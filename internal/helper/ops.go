package helper

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/artw1/aslcp/internal/config"
)

const (
	pkgName     = "aslcp"
	upgradeUnit = "aslcp-upgrade"
	fwService   = "aslcp"
	logLines    = 50

	// DefaultSourceList is the apt source written by install.sh.
	DefaultSourceList = "/etc/apt/sources.list.d/aslcp.list"
)

// Ops implements the operations on top of a Runner.
type Ops struct {
	Run Runner
	// SourceList is the aslcp apt source file whose suite is the update
	// channel (default DefaultSourceList).
	SourceList string

	// ConfigRoot is the directory the config-* ops edit (default
	// /etc/asterisk) and HistoryDir the git history repository (default
	// DefaultHistoryDir); both are injectable for tests.
	ConfigRoot string
	HistoryDir string
	// StrictHistoryParents makes the history directory refuse a parent chain
	// that is not root-owned and closed to group/others (set by NewOps).
	StrictHistoryParents bool
	// ConfigWrite replaces the atomic file writer (tests inject failures).
	ConfigWrite ConfigWriter
	// NewFileOwner overrides the owner/mode of new files (default
	// asterisk:asterisk 0640 if that user and group exist, else root 0644).
	NewFileOwner func() (uid, gid int, mode fs.FileMode)

	// Backup and restore locations (defaults below); injectable for tests.
	AstDBPath  string    // default DefaultAstDB
	SoundsDirs [2]string // custom sounds: var-lib, usr-share (defaults below)
	SA818Conf  string    // default DefaultSA818Conf
	AllmonDir  string    // default DefaultAllmonDir
	AslcpConf  string    // default DefaultAslcpConf (restored; read for the web port)
	ResolvConf string    // default DefaultResolvConf (net-status)
	OSRelease  string    // default DefaultOSRelease
	// Svc holds the paths and timing of the services switch and the port 80
	// move (sys_svc.go, sys_webport.go); the zero value means the defaults.
	Svc SvcPaths
	// Host info fallbacks of host-status (injectable for tests): the
	// static hostname file, the kernel hostname and the PATH lookup.
	HostnameFile string
	GetHostname  func() (string, error)
	LookPath     func(string) (string, error)
	// TempDir is where an uploaded archive is staged during a restore
	// (default /var/tmp).
	TempDir string

	// applyMu serialises apply requests so that two concurrent requests
	// cannot both observe "not running".
	applyMu sync.Mutex

	jobMu     sync.Mutex
	cache     map[string]jobCache // by unit
	startedAt map[string]time.Time

	// RebootFile and RebootPkgsFile are the Debian reboot-required markers
	// (defaults below); injectable for tests.
	// Wifi holds the WiFi manager paths (sys_wifi.go); all optional.
	Wifi WifiPaths

	RebootFile     string
	RebootPkgsFile string
	// OSCheckTimeout bounds apt-get update of os-check (default 5 minutes).
	OSCheckTimeout time.Duration
	osCheckMu      sync.Mutex

	// DVSwitch install (dvswitch.go); defaults are the DefaultDV* constants.
	DVKeyring    string
	DVSourceList string
	DVStateDir   string
	DVHostDir    string
	// DVSwitch config (dvswitch_cfg.go): the dir holding Analog_Bridge and
	// the others, the history dir and the /proc root for ABInfo.
	DVOptDir     string
	DVHistoryDir string
	DVProcDir    string
	// DVTLVDelay spaces the TLV datagrams of one command (default 100 ms),
	// DVTLVSettle is the wait before the result is read back (default 400 ms).
	// After an rxport= command the helper polls ABInfo every DVPortPoll
	// (default 100 ms) for up to DVPortWait (default 2 s) until Analog_Bridge
	// reports the new listener port. DVParkPort is the port a mode switch
	// parks Analog_Bridge on first (default 30000).
	DVTLVDelay  time.Duration
	DVTLVSettle time.Duration
	DVPortPoll  time.Duration
	DVPortWait  time.Duration
	DVParkPort  int
	// tuneSem holds one token while a tune or mode sequence runs (a channel,
	// not a mutex, so that a waiter can give up with its context).
	tuneOnce sync.Once
	tuneSem  chan struct{}
	// DVLogDir holds MMDVM_Bridge-YYYY-MM-DD.log (default /var/log/mmdvm).
	DVLogDir string
	// DVSTFULog is STFU's log (default DefaultDVSTFULog); bmMu serialises
	// the BrandMeister save and clear (dvswitch_stfu.go).
	DVSTFULog string
	// DVSTFUDropin is the systemd drop-in that gives STFU umask 0077
	// (default DefaultDVSTFUDropin).
	DVSTFUDropin string
	bmMu         sync.Mutex
	// DVSTFULoginWait (default 10 s) and DVSTFULoginPoll (default 250 ms)
	// bound the wait for STFU's login when ASLCP starts it on demand;
	// DVSleep replaces the real sleep (tests).
	DVSTFULoginWait time.Duration
	DVSTFULoginPoll time.Duration
	DVSleep         func(ctx context.Context, d time.Duration)
	// DVBridgeLogDir holds Analog_Bridge.log (default /var/log/dvswitch).
	DVBridgeLogDir string
	// dvIDs caches DMR ID to callsign lookups (dvswitch_idlookup.go).
	dvIDs dvIDCache
	// SelfPath is the helper binary the install and uninstall units call for
	// their steps (default: this executable).
	SelfPath string
	// HTTPGet fetches the keyring (tests inject; default: fixed-URL client,
	// redirects refused, 1 MiB cap). Now is the clock check's time source.
	HTTPGet func(ctx context.Context, url string) ([]byte, error)
	Now     func() time.Time
}

// jobCache remembers the classification of a finished job.
type jobCache struct {
	valid           bool
	key, state, log string
}

// NewOps returns Ops using the real command runner.
func NewOps() *Ops { return &Ops{Run: ExecRunner{}, StrictHistoryParents: true} }

// Handle dispatches one request and returns the response value.
func (o *Ops) Handle(ctx context.Context, op string) (any, error) {
	return o.HandleRequest(ctx, Request{Op: op})
}

// HandleRequest dispatches a full request. Only the config-* operations
// accept the config field.
func (o *Ops) HandleRequest(ctx context.Context, req Request) (any, error) {
	op := req.Op
	if req.Terminal != nil {
		// the terminal ops are served by Server.handleTerminal, never from here.
		return nil, errors.New("invalid request: unexpected terminal field")
	}
	if isSysOp(op) {
		if req.Config != nil || req.DVSwitch != nil || req.Backup != nil || req.Restore != nil || req.Diag != nil {
			return nil, errors.New("invalid request: unexpected field")
		}
		return o.handleSys(ctx, req)
	}
	if req.Sys != nil {
		return nil, errors.New("invalid request: unexpected sys field")
	}
	if req.Diag != nil {
		return nil, errors.New("invalid request: unexpected diag field")
	}
	if isConfigOp(op) {
		return o.handleConfig(ctx, req)
	}
	if req.Config != nil {
		return nil, errors.New("invalid request: unexpected config field")
	}
	if req.DVSwitch != nil && !isDVConfigOp(op) {
		return nil, errors.New("invalid request: unexpected dvswitch field")
	}
	if req.Backup != nil && op != OpBackupCreate || req.Restore != nil && op != OpBackupRestore {
		return nil, errors.New("invalid request: unexpected field")
	}
	switch op {
	case OpDVSwitchSetup:
		return o.DVSwitchSetup(ctx)
	case OpDVSwitchInfo:
		return o.dvInfoOp(ctx)
	case OpDVSwitchDMRStatus:
		return o.DVSwitchDMRStatus(ctx)
	case OpDVSwitchIdentity:
		return o.DVSwitchIdentity(ctx, req.DVSwitch)
	case OpDVSwitchCredentials:
		return o.DVSwitchCredentials(ctx, req.DVSwitch)
	case OpDVSwitchModes:
		return o.DVSwitchModes(ctx, req.DVSwitch)
	case OpDVSwitchStartup:
		return o.DVSwitchStartup(ctx, req.DVSwitch)
	case OpDVSwitchRollback:
		return o.DVSwitchRollback(ctx, req.DVSwitch)
	case OpDVSwitchRestart:
		return o.DVSwitchRestart(ctx, req.DVSwitch)
	case OpDVSwitchLists:
		return o.DVSwitchLists(ctx, req.DVSwitch)
	case OpDVSwitchTune:
		return o.DVSwitchTune(ctx, req.DVSwitch)
	case OpDVSwitchUSRP:
		return o.DVSwitchUSRP(ctx, req.DVSwitch)
	case OpDVSwitchPrivate:
		return o.DVSwitchPrivate(ctx, req.DVSwitch)
	case OpDVSwitchBM:
		return o.DVSwitchBM(ctx, req.DVSwitch)
	case OpDVSwitchSTFUInstall:
		return o.DVSwitchSTFUInstall(ctx)
	case OpBackupInfo:
		return o.BackupInfo(ctx)
	case OpAsteriskRestart:
		return o.AsteriskRestart(ctx)
	case OpUpdateStatus:
		return o.UpdateStatus(ctx)
	case OpUpdateCheck:
		return o.UpdateCheck(ctx)
	case OpUpdateApply:
		return o.UpdateApply(ctx)
	case OpChannelStable:
		return o.SetChannel(ctx, ChannelStable)
	case OpChannelBeta:
		return o.SetChannel(ctx, ChannelBeta)
	case OpOSCheck:
		return o.OSCheck(ctx)
	case OpOSStatus:
		return o.OSStatus(ctx)
	case OpOSApply:
		return o.OSApply(ctx)
	case OpReboot:
		return o.Reboot(ctx)
	case OpAslcpRestart:
		return o.AslcpRestart(ctx)
	case OpDVSwitchStatus:
		return o.DVSwitchStatus(ctx)
	case OpDVSwitchJob:
		return o.DVSwitchJob(ctx)
	case OpDVSwitchInstall:
		return o.DVSwitchInstall(ctx)
	case OpDVSwitchUninstall:
		return o.DVSwitchUninstall(ctx)
	case OpDVSwitchRestoreDiscard:
		return o.DVSwitchRestoreDiscard(ctx)
	case OpFirewallStatus:
		return o.FirewallStatus(ctx)
	case OpFirewallOpen:
		return o.FirewallOpen(ctx)
	}
	return nil, fmt.Errorf("unknown op")
}

// UpdateStatus reports the installed version (dpkg-query only) and the state
// of the upgrade job. It never runs apt: loading apt's package cache takes
// many seconds on a Raspberry Pi 3. Candidate and Upgradable stay empty; the
// server decides from latest.json.
func (o *Ops) UpdateStatus(ctx context.Context) (Status, error) {
	st, err := o.installed(ctx)
	if err != nil {
		return st, err
	}
	st.Job, st.JobLog = o.job(ctx)
	return st, nil
}

func (o *Ops) installed(ctx context.Context) (Status, error) {
	var st Status
	out, code, err := o.Run.Run(ctx, "dpkg-query", "-W", "-f=${Version}", pkgName)
	if err != nil {
		return st, fmt.Errorf("dpkg-query: %w", err)
	}
	if code == 0 {
		st.Installed = strings.TrimSpace(out)
	}
	st.Channel, _ = readChannel(o.sourceList())
	return st, nil
}

func (o *Ops) sourceList() string {
	if o.SourceList != "" {
		return o.SourceList
	}
	return DefaultSourceList
}

// sourceLine matches an aslcp apt source line: deb [options] URL SUITE ...
// where SUITE is one of the two channels. Group 1 is everything before the
// suite, 2 the suite, 3 the rest of the line.
var sourceLine = regexp.MustCompile(`^(\s*deb\s+(?:\[[^\]\n]*\]\s+)?\S+\s+)(stable|beta)(\s.*|)$`)

// readChannel returns the suite of the first aslcp line of the source file.
func readChannel(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if m := sourceLine.FindStringSubmatch(line); m != nil {
			return m[2], nil
		}
	}
	return "", errors.New("no aslcp source line found")
}

// SetChannel rewrites only the suite word of the aslcp line(s) of the source
// file (atomically, mode 0644) and returns the new status. It refuses while
// an upgrade runs. channel must be ChannelStable or ChannelBeta.
func (o *Ops) SetChannel(ctx context.Context, channel string) (Status, error) {
	if channel != ChannelStable && channel != ChannelBeta {
		return Status{}, errors.New("invalid channel")
	}
	o.applyMu.Lock()
	defer o.applyMu.Unlock()
	return o.setChannelLocked(ctx, channel)
}

// setChannelLocked is SetChannel with applyMu already held.
func (o *Ops) setChannelLocked(ctx context.Context, channel string) (Status, error) {
	if o.aptJobRunning(ctx) {
		return Status{}, ErrBusy
	}
	path := o.sourceList()
	data, err := os.ReadFile(path)
	if err != nil {
		return Status{}, fmt.Errorf("cannot read the apt source: %w", err)
	}
	lines := strings.Split(string(data), "\n")
	found := false
	for i, line := range lines {
		if m := sourceLine.FindStringSubmatch(line); m != nil {
			lines[i] = m[1] + channel + m[3]
			found = true
		}
	}
	if !found {
		return Status{}, errors.New("no aslcp line found in the apt source")
	}
	if out := strings.Join(lines, "\n"); out != string(data) {
		if err := writeAtomic(path, []byte(out)); err != nil {
			return Status{}, fmt.Errorf("cannot write the apt source: %w", err)
		}
	}
	return o.installed(ctx)
}

// writeAtomic replaces path with data (temp file in the same directory,
// mode 0644, then rename). The temp name does not end in .list, so apt
// ignores it.
func writeAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			os.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(0o644); err != nil {
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

// justStarted is how long after update-apply a unit that systemd does not
// list yet is still reported as running.
const justStarted = 3 * time.Second

// job reads the state and log of the detached upgrade unit. systemctl show
// is cheap and runs every time; the journal is read only while the job runs,
// or once per finished job (the classification is cached by the unit's
// InvocationID / start time; a collected unit has neither, so the cache is
// dropped when a job is seen running or started).
func (o *Ops) job(ctx context.Context) (state, log string) { return o.jobUnit(ctx, upgradeUnit) }

// jobUnit is job for one of the two upgrade units (upgradeUnit,
// osUpgradeUnit).
func (o *Ops) jobUnit(ctx context.Context, unit string) (state, log string) {
	o.jobMu.Lock()
	defer o.jobMu.Unlock()
	// systemctl show on a unit that is not loaded makes systemd try to load it
	// from disk, which logs "Failed to open /run/systemd/transient/...: No such
	// file or directory" to the journal on every poll. list-units only
	// enumerates loaded units, so ask it first and skip show when absent (an
	// empty property set is what show reports for a missing unit anyway).
	list, lcode, err := o.Run.Run(ctx, "systemctl", "list-units", "--all", "--plain", "--no-legend", "--full", unit+".service")
	if err != nil {
		return JobIdle, ""
	}
	props := map[string]string{}
	if lcode != 0 || strings.Contains(list, unit+".service") {
		show, _, err := o.Run.Run(ctx, "systemctl", "show", unit, "-p", "ActiveState,Result,InvocationID,ExecMainStartTimestamp")
		if err != nil {
			return JobIdle, ""
		}
		props = ParseShow(show)
	}
	key := props["InvocationID"]
	if key == "" {
		key = props["ExecMainStartTimestamp"]
	}
	running := JobStateFrom(props, "") == JobRunning
	if running {
		o.setCache(unit, jobCache{})
	} else if c := o.cache[unit]; c.valid && c.key == key {
		return c.state, c.log
	} else if key == "" && time.Since(o.startedAt[unit]) < justStarted {
		return JobRunning, ""
	}
	out, code, err := o.Run.Run(ctx, "journalctl", "-u", unit, "-n", fmt.Sprint(logLines), "-o", "cat", "-a", "--no-pager")
	if err != nil || code != 0 {
		out = ""
	}
	log = LastLines(cleanLogUnit(out, unit), logLines)
	state = jobStateFrom(props, out, unit)
	if !running && err == nil && code == 0 {
		o.setCache(unit, jobCache{valid: true, key: key, state: state, log: log})
	}
	return state, log
}

// setCache stores the cache entry of unit (jobMu held).
func (o *Ops) setCache(unit string, c jobCache) {
	if o.cache == nil {
		o.cache = map[string]jobCache{}
	}
	o.cache[unit] = c
}

func (o *Ops) resetJob() { o.resetJobUnit(upgradeUnit) }

func (o *Ops) resetJobUnit(unit string) {
	o.jobMu.Lock()
	o.setCache(unit, jobCache{})
	if o.startedAt == nil {
		o.startedAt = map[string]time.Time{}
	}
	o.startedAt[unit] = time.Now()
	o.jobMu.Unlock()
}

// UpdateCheck is kept for compatibility with older servers. It used to run
// `apt-get update`; it now returns the same cheap status as UpdateStatus
// (the server checks latest.json itself).
func (o *Ops) UpdateCheck(ctx context.Context) (Status, error) { return o.UpdateStatus(ctx) }

// ErrBusy is returned when an upgrade is already running.
var ErrBusy = errors.New("an update is already running")

// upgradeScript is the fixed script of the upgrade unit; nothing a client
// sends reaches it. One unit does the (scoped) index refresh and the install,
// so the first apt run is not paid twice. Quiet, no recommends, no changelog
// reader (APT_LISTCHANGES_FRONTEND=none is set in the unit environment).
const upgradeScript = "apt-get update -o DPkg::Lock::Timeout=600 -o Dir::Etc::sourcelist=sources.list.d/aslcp.list -o Dir::Etc::sourceparts=- -o APT::Get::List-Cleanup=0 -q && " +
	"apt-get install -y -q -o DPkg::Lock::Timeout=600 --only-upgrade --no-install-recommends -o Dpkg::Options::=--force-confold -o Dpkg::Use-Pty=0 aslcp"

// upgradeArgv is the systemd-run command line of update-apply.
func upgradeArgv() []string {
	// --no-block: return as soon as the unit is queued. Without it systemd-run
	// waits for a oneshot unit to finish, so the helper would hold the apply
	// request open for the whole upgrade, and the postinst (which stops the
	// helper) would wait for the helper while the helper waits for the upgrade.
	return []string{"systemd-run", "--no-block", "--unit=" + upgradeUnit, "--collect", "--property=Type=oneshot",
		"--setenv=DEBIAN_FRONTEND=noninteractive", "--setenv=APT_LISTCHANGES_FRONTEND=none",
		"/bin/sh", "-c", upgradeScript}
}

// UpdateApply starts the upgrade in a transient systemd unit, detached from
// the helper and the server so it survives their restart.
func (o *Ops) UpdateApply(ctx context.Context) (Status, error) {
	o.applyMu.Lock()
	defer o.applyMu.Unlock()
	if o.aptJobRunning(ctx) {
		return Status{}, ErrBusy
	}
	out, code, err := o.Run.Run(ctx, upgradeArgv()...)
	o.resetJob()
	if err != nil {
		return Status{}, fmt.Errorf("systemd-run: %w", err)
	}
	if code != 0 {
		return Status{}, fmt.Errorf("systemd-run failed: %s", LastLines(out, 5))
	}
	st, err := o.installed(ctx)
	if err != nil {
		return st, err
	}
	// The unit was just started; report it as running even if systemd has not
	// registered it yet (no journal read: the log is still empty).
	st.Job = JobRunning
	return st, nil
}

// defaultPort is the port the packaged firewalld service "aslcp" covers.
const defaultPort = 8080

// webPort reads the port from the [server] listen setting of aslcp.conf. The
// port is never taken from the request: the operations stay argument-free.
func (o *Ops) webPort() (int, error) {
	c, err := config.Load(o.aslcpConf())
	if err != nil {
		return 0, err
	}
	return config.ListenPort(c.Listen)
}

// fwQuery succeeds when a firewall-cmd --query-* call exits 0, in the runtime
// configuration and then (a rule added with --permanent) the permanent one.
func (o *Ops) fwQuery(ctx context.Context, query string) bool {
	for _, args := range [][]string{{"firewall-cmd", query}, {"firewall-cmd", "--permanent", query}} {
		if _, code, err := o.Run.Run(ctx, args...); err == nil && code == 0 {
			return true
		}
	}
	return false
}

// FirewallStatus reports firewalld state for the configured web port: open
// when the aslcp service (port 8080 only) or the port itself is enabled.
func (o *Ops) FirewallStatus(ctx context.Context) (Firewall, error) {
	var fw Firewall
	out, code, err := o.Run.Run(ctx, "firewall-cmd", "--state")
	if err != nil {
		return fw, nil // firewall-cmd not installed
	}
	fw.Present = true
	fw.Running = code == 0 && strings.TrimSpace(out) == "running"
	port, perr := o.webPort()
	if perr != nil {
		return fw, perr
	}
	fw.Port = port
	if fw.Running {
		if port == defaultPort {
			fw.ServiceEnabled = o.fwQuery(ctx, "--query-service="+fwService)
		}
		if !fw.ServiceEnabled {
			fw.PortOpen = o.fwQuery(ctx, fmt.Sprintf("--query-port=%d/tcp", port))
		}
		fw.Open = fw.ServiceEnabled || fw.PortOpen
	}
	return fw, nil
}

// FirewallOpen opens the configured web port permanently and reloads: the
// aslcp service for 8080, else --add-port=<port>/tcp.
func (o *Ops) FirewallOpen(ctx context.Context) (Firewall, error) {
	fw, err := o.FirewallStatus(ctx)
	if !fw.Present {
		return fw, errors.New("firewalld is not installed")
	}
	if !fw.Running {
		return fw, errors.New("firewalld is not running")
	}
	if err != nil {
		return fw, err
	}
	add := "--add-service=" + fwService
	if fw.Port != defaultPort {
		add = fmt.Sprintf("--add-port=%d/tcp", fw.Port)
	}
	// Reload first so firewalld knows the service definition shipped by the
	// package, then add the rule permanently and reload again.
	for _, args := range [][]string{{"firewall-cmd", "--reload"}, {"firewall-cmd", "--permanent", add}, {"firewall-cmd", "--reload"}} {
		out, code, err := o.Run.Run(ctx, args...)
		if err != nil || code != 0 {
			return fw, fmt.Errorf("firewall-cmd %s failed: %s", args[1], LastLines(out, 3))
		}
	}
	return o.FirewallStatus(ctx)
}

// callTimeout bounds one request (apt-get update can be slow on a Pi).
const callTimeout = 150 * time.Second
