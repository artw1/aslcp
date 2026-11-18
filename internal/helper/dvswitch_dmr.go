package helper

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/artw1/aslcp/internal/dvswitch/ini"
)

// DMR login status of MMDVM_Bridge (docs/DVSWITCH.md, "DMR login status").
// The op takes no client path: the log dir is fixed, the files are
// MMDVM_Bridge-YYYY-MM-DD.log (UTC, as in MMDVMHost's Log.cpp which names the
// file and stamps the lines with gmtime). Only the tail of the newest two is
// read, with O_NOFOLLOW, regular files only.

const (
	// OpDVSwitchDMRStatus is the op name.
	OpDVSwitchDMRStatus = "dvswitch-dmr-status"
	// DefaultDVLogDir is the stock [Log] FilePath of MMDVM_Bridge.ini.
	DefaultDVLogDir = "/var/log/mmdvm"
	dvLogTail       = 256 << 10
	dvLogMaxLine    = 4096
)

// DMR login states.
const (
	DMRNotInstalled  = "not_installed"
	DMRDisabled      = "disabled"
	DMRNotRunning    = "not_running"
	DMRIdle          = "idle" // BrandMeister only: stfu is stopped on purpose (the panel is not in BM mode)
	DMRConnecting    = "connecting"
	DMRLoggedIn      = "logged_in"
	DMRUnknown       = "unknown" // running, but the log shows neither a login nor traffic
	DMRLoginFailed   = "login_failed"
	DMRTimedOut      = "timed_out"
	DMRSocketFailed  = "socket_failed"
	DMRMasterClosing = "master_closing"
)

// DVDMRStatus is the reply of dvswitch-dmr-status. It never holds the
// password; digit runs of 7 or more are redacted.
type DVDMRStatus struct {
	State    string `json:"state"`
	Master   string `json:"master,omitempty"`
	At       string `json:"at,omitempty"`
	Since    string `json:"since,omitempty"`
	LastLine string `json:"lastLine,omitempty"`
	// Via is the connection: "mmdvm_bridge" (DMR - Other) or "stfu"
	// (BrandMeister).
	Via string `json:"via,omitempty"`
	// RX is the last transmission from the network (STFU only).
	RX *DVDMRRx `json:"rx,omitempty"`
	// BM is the BrandMeister login (STFU) next to DMR - Other's on the top
	// level: nil when STFU is not installed, State disabled when its unit is
	// not enabled. Set on the top level only.
	BM *DVDMRStatus `json:"bm,omitempty"`
}

func (o *Ops) dvLogDir() string { return orDefault(o.DVLogDir, DefaultDVLogDir) }

var (
	reDVLogName   = regexp.MustCompile(`^MMDVM_Bridge-[0-9]{4}-[0-9]{2}-[0-9]{2}\.log$`)
	reDVLogLine   = regexp.MustCompile(`^[A-Z]: ([0-9]{4}-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2})\.[0-9]{3} (DMR, [\x20-\x7e]{1,300})$`)
	reDVMasterOK  = regexp.MustCompile(`successfully: ([A-Za-z0-9.-]{1,253}:[0-9]{1,5})\s*$`)
	reDVDigitRun  = regexp.MustCompile(`[0-9]{7,}`)
	reDVActiveEnt = regexp.MustCompile(`^(?:[A-Za-z]{3} )?([0-9]{4}-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2}) (?:UTC|GMT)$`)
)

const dvLogTimeLayout = "2006-01-02 15:04:05"

func dvRedact(s string) string { return reDVDigitRun.ReplaceAllString(s, "***") }

// dvVerdict maps a message to a state ("" when it is no login verdict).
func dvVerdict(msg string) string {
	switch {
	case strings.Contains(msg, "Logged into the master successfully"):
		return DMRLoggedIn
	case strings.Contains(msg, "Login to the master has failed"):
		return DMRLoginFailed
	case strings.Contains(msg, "Connection to the master has timed out"):
		return DMRTimedOut
	case strings.Contains(msg, "Socket has failed"):
		return DMRSocketFailed
	case strings.Contains(msg, "Master is closing down"):
		return DMRMasterClosing
	}
	return ""
}

// parseDMRLog scans log text in order and returns the last verdict line at or
// after since.
func parseDMRLog(data []byte, since time.Time, cur *DVDMRStatus) {
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64<<10), dvLogMaxLine)
	for sc.Scan() {
		m := reDVLogLine.FindStringSubmatch(strings.TrimRight(sc.Text(), "\r"))
		if m == nil {
			continue
		}
		st := dvVerdict(m[2])
		if st == "" {
			continue
		}
		at, err := time.ParseInLocation(dvLogTimeLayout, m[1], time.UTC)
		if err != nil || at.Before(since) {
			continue
		}
		cur.State, cur.At, cur.LastLine, cur.Master = st, at.Format(time.RFC3339), dvRedact(m[2]), ""
		if st == DMRLoggedIn {
			if mm := reDVMasterOK.FindStringSubmatch(m[2]); mm != nil {
				cur.Master = dvRedact(mm[1])
			}
		}
	}
}

// dvReadTail reads at most dvLogTail bytes from the end of a regular file
// (never through a symlink). A cut first line is dropped.
func dvReadTail(path string) ([]byte, bool) {
	fd, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, false
	}
	defer fd.Close()
	fi, err := fd.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		return nil, false
	}
	cut := false
	if fi.Size() > dvLogTail {
		if _, err := fd.Seek(fi.Size()-dvLogTail, io.SeekStart); err != nil {
			return nil, false
		}
		cut = true
	}
	data, err := io.ReadAll(io.LimitReader(fd, dvLogTail+1))
	if err != nil {
		return nil, false
	}
	if cut {
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			data = data[i+1:]
		} else {
			data = nil
		}
	}
	return data, true
}

// dvLogFiles returns the two newest regular log files, oldest first.
func (o *Ops) dvLogFiles() []string {
	ents, err := os.ReadDir(o.dvLogDir())
	if err != nil {
		return nil
	}
	var names []string
	for i, e := range ents {
		if i >= 4096 {
			break
		}
		if reDVLogName.MatchString(e.Name()) && e.Type().IsRegular() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // the date in the name sorts as time
	if len(names) > 2 {
		names = names[len(names)-2:]
	}
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = filepath.Join(o.dvLogDir(), n)
	}
	return out
}

// dvUnitStart returns the unit's ActiveState and its start time in UTC (zero
// when unknown).
func (o *Ops) dvUnitStart(ctx context.Context, unit string) (loaded bool, active string, since time.Time) {
	argv := []string{"systemctl", "show", "--timestamp=utc", "-p", "LoadState,ActiveState,ActiveEnterTimestamp", unit}
	out, code, err := o.Run.Run(ctx, argv...)
	if err != nil || code != 0 {
		return false, "", time.Time{}
	}
	p := ParseShow(out)
	if m := reDVActiveEnt.FindStringSubmatch(p["ActiveEnterTimestamp"]); m != nil {
		since, _ = time.ParseInLocation(dvLogTimeLayout, m[1], time.UTC)
	}
	return p["LoadState"] == "loaded", p["ActiveState"], since
}

// DVSwitchDMRStatus reports both DMR logins: DMR - Other (MMDVM_Bridge to
// its DMR master, from the log lines written since the unit started) on the
// top level and BrandMeister (STFU, from STFU.log) in BM.
func (o *Ops) DVSwitchDMRStatus(ctx context.Context) (DVDMRStatus, error) {
	res, err := o.dvMMDVMStatus(ctx)
	if loaded, configured := o.dvBMConfigured(ctx); loaded {
		bm := DVDMRStatus{State: DMRDisabled, Via: DVViaSTFU}
		if configured {
			bm = o.dvSTFUStatus(ctx)
		}
		res.BM = &bm
	}
	return res, err
}

func (o *Ops) dvMMDVMStatus(ctx context.Context) (DVDMRStatus, error) {
	loaded, active, since := o.dvUnitStart(ctx, "mmdvm_bridge.service")
	ini1, err := o.dvRead(dvFiles[1])
	if err != nil || !loaded || !ini1.regular {
		return DVDMRStatus{State: DMRNotInstalled, Via: DVViaMMDVM}, nil
	}
	f := ini.Parse(ini1.data)
	en := func(sec string) bool {
		a, _ := f.Get(sec, "Enable")
		b, _ := f.Get(sec+" Network", "Enable")
		return a == "1" && b == "1"
	}
	if !en("DMR") {
		return DVDMRStatus{State: DMRDisabled, Via: DVViaMMDVM}, nil
	}
	res := DVDMRStatus{State: DMRNotRunning, Via: DVViaMMDVM}
	if !since.IsZero() {
		res.Since = since.Format(time.RFC3339)
	}
	if active != "active" && active != "activating" && active != "reloading" {
		return res, nil
	}
	res.State = DMRConnecting
	if since.IsZero() {
		return res, nil // cannot tell old lines from new ones: no verdict
	}
	for _, p := range o.dvLogFiles() {
		if data, ok := dvReadTail(p); ok {
			parseDMRLog(data, since, &res)
		}
	}
	return res, nil
}
