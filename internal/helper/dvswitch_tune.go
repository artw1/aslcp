package helper

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/artw1/aslcp/internal/dvswitch/ini"
)

// Mode switch and tune (docs/DVSWITCH.md, "Tune and resume"). The helper
// sends the same TLV remote commands as dvswitch.sh to Analog_Bridge's TLV
// port; it does not run the script (see the docs for why).

// OpDVSwitchTune switches the mode if needed and tunes (admin and operator;
// the API gates the roles).
const OpDVSwitchTune = "dvswitch-tune"

// ErrDVNoPorts: DVSwitch.ini has no usable TXPort/RXPort for the mode.
var ErrDVNoPorts = errors.New("no_ports: DVSwitch.ini has no ports for this mode")

// DVTuneModes are the modes dvswitch-tune takes, in display order.
var DVTuneModes = []string{"DMR", "YSF", "P25", "NXDN"}

// DVTune is the argument of dvswitch-tune.
//
// ModeOnly switches the mode and sends no tune (Tune must be empty); a node
// already in the mode gets no command at all.
type DVTune struct {
	Mode     string `json:"mode"`
	Tune     string `json:"tune"`
	ModeOnly bool   `json:"modeOnly,omitempty"`
}

// DVUnlinkTune is the tune value that unlinks the mode (dvswitch.sh): DMR
// 4000, YSF disconnect, P25 and NXDN 9999. "" for an unknown mode.
func DVUnlinkTune(mode string) string {
	switch mode {
	case "DMR":
		return "4000"
	case "YSF":
		return "disconnect"
	case "P25", "NXDN":
		return "9999"
	}
	return ""
}

// DVTuneResult is the reply. Confirmed: ABInfo shows the mode and tune after
// the commands (best effort: Analog_Bridge may need a moment).
type DVTuneResult struct {
	Mode      string `json:"mode"`
	Switched  bool   `json:"switched"`
	Confirmed bool   `json:"confirmed"`
}

// DVNormMode maps ABInfo's ambe_mode to a tune mode: YSFN and YSFW are YSF.
// "" means a mode this feature does not handle (DMR_IPSC, DSTAR, ...).
func DVNormMode(m string) string {
	switch m {
	case "DMR", "P25", "NXDN":
		return m
	case "YSFN", "YSFW", "YSF":
		return "YSF"
	}
	return ""
}

// DVUnlinkValue is the tune value that disconnects a mode (dvswitch.sh): it is
// not a place to resume to.
func DVUnlinkValue(mode, tune string) bool {
	switch mode {
	case "DMR":
		return tune == "4000"
	case "YSF":
		return tune == "disconnect"
	case "P25", "NXDN":
		return tune == "9999"
	}
	return false
}

var reDVHostPort = regexp.MustCompile(`^[A-Za-z0-9.-]{1,253}:[0-9]{1,5}$`)

// DVValidTune reports whether tune is allowed for the mode. These are the
// startup-value validators (a TG number for DMR, P25 and NXDN; for YSF a
// reflector id, a name as in YSFHosts.txt, or host:port) and the unlink word
// "disconnect". Nothing starts with "-" or holds "=", so a value can neither
// look like an option nor smuggle a second TLV key.
func DVValidTune(mode, tune string) bool {
	if tune == "" || strings.HasPrefix(tune, "-") || strings.Contains(tune, "=") {
		return false
	}
	switch mode {
	case "DMR":
		return reDVTG.MatchString(tune)
	case "P25", "NXDN":
		return reDVRefl.MatchString(tune)
	case "YSF":
		if reDVHostPort.MatchString(tune) {
			return vInt(1, 65535)(tune[strings.LastIndex(tune, ":")+1:])
		}
		return vYSFStartup(tune)
	}
	return false
}

// dvYSFTarget turns a YSF tune into what Analog_Bridge takes: host:port. An id
// or a name is looked up in YSFHosts.txt; "disconnect" and host:port pass.
func (o *Ops) dvYSFTarget(tune string) (string, bool) {
	if tune == "disconnect" || reDVHostPort.MatchString(tune) {
		return tune, true
	}
	data, err := o.dvReadList("ysf")
	if err != nil {
		return "", false
	}
	for _, it := range ParseYSFHosts(data) {
		if it.Value == tune || it.Name == tune {
			return it.Address + ":" + strconv.Itoa(it.Port), true
		}
	}
	return "", false
}

const (
	dvTLVTag          = 0x05 // TLV tag of a remote command (dvswitch.sh remoteControlCommand)
	dvTLVSwitchPort   = 30000
	dvDefaultTLVDelay = 100 * time.Millisecond
	dvDefaultSettle   = 400 * time.Millisecond
	// dvDefaultPortPoll and dvDefaultPortWait: how often and how long ABInfo is
	// read for the new tlv.rx_port after an rxport= command.
	dvDefaultPortPoll = 100 * time.Millisecond
	dvDefaultPortWait = 2 * time.Second
	dvIniMax          = 1 << 20
)

// lockTune takes the one-sequence-at-a-time token; a second request waits
// (bounded by ctx) instead of failing.
func (o *Ops) lockTune(ctx context.Context) (func(), error) {
	o.tuneOnce.Do(func() { o.tuneSem = make(chan struct{}, 1) })
	select {
	case o.tuneSem <- struct{}{}:
		return func() { <-o.tuneSem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func dvSleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// dvWaitPort polls ABInfo (the read of DVSwitchInfo) until tlv.rx_port is
// want. It reports whether the port showed up in time; the caller sends to
// want either way, since that is what Analog_Bridge was told to listen on.
func (o *Ops) dvWaitPort(ctx context.Context, want int) (bool, error) {
	poll, wait := o.DVPortPoll, o.DVPortWait
	if poll <= 0 {
		poll = dvDefaultPortPoll
	}
	if wait <= 0 {
		wait = dvDefaultPortWait
	}
	deadline := time.Now().Add(wait)
	for {
		if info, err := o.DVSwitchInfo(ctx); err == nil && info.Found && info.tlvRx == want {
			return true, nil
		}
		if !time.Now().Add(poll).Before(deadline) {
			return false, nil
		}
		if err := dvSleep(ctx, poll); err != nil {
			return false, err
		}
	}
}

// dvTLVSend sends the commands in order, starting at port. Analog_Bridge
// moves its listener on every rxport=N (dvswitch.sh waits 1 s and re-reads
// tlv.rx_port after each), so after one the helper waits for ABInfo to show
// N and sends the rest there; if it never does, to N all the same. Each port
// gets a fresh unconnected socket, so an ICMP refusal from a port Analog_Bridge
// has left cannot fail a later write. A failed write right after a port change
// is retried once. It returns the port the last command went to.
func (o *Ops) dvTLVSend(ctx context.Context, port int, cmds []string) (int, error) {
	delay := o.DVTLVDelay
	if delay == 0 {
		delay = dvDefaultTLVDelay
	}
	var conn *net.UDPConn
	closeConn := func() {
		if conn != nil {
			conn.Close()
			conn = nil
		}
	}
	defer closeConn()
	open := func() error {
		closeConn()
		c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
		if err != nil {
			return errors.New("cannot reach Analog_Bridge")
		}
		conn = c
		return nil
	}
	if err := open(); err != nil {
		return port, err
	}
	changed := false // the previous command was an rxport=
	for i, c := range cmds {
		if len(c) == 0 || len(c) > 255 {
			return port, errors.New("bad TLV command")
		}
		pkt := append([]byte{dvTLVTag, byte(len(c))}, c...)
		dst := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}
		if _, err := conn.WriteToUDP(pkt, dst); err != nil {
			if !changed {
				return port, errors.New("cannot reach Analog_Bridge")
			}
			// right after a port change: re-read the port and retry once
			if _, werr := o.dvWaitPort(ctx, port); werr != nil {
				return port, werr
			}
			if err := open(); err != nil {
				return port, err
			}
			if _, err := conn.WriteToUDP(pkt, dst); err != nil {
				return port, errors.New("cannot reach Analog_Bridge")
			}
		}
		changed = false
		if n, ok := strings.CutPrefix(c, "rxport="); ok {
			np, err := strconv.Atoi(n)
			if err != nil || np < 1 || np > 65535 {
				return port, errors.New("bad TLV command")
			}
			port, changed = np, true
			if _, err := o.dvWaitPort(ctx, port); err != nil {
				return port, err
			}
			if err := open(); err != nil {
				return port, err
			}
		} else if i < len(cmds)-1 {
			if err := dvSleep(ctx, delay); err != nil {
				return port, err
			}
		}
	}
	return port, nil
}

// dvModePorts reads txPort and rxPort of [mode] in DVSwitch.ini. Keys are
// matched case-insensitively (the stock file spells them txPort/rxPort and
// dvswitch.sh reads it with configparser).
func (o *Ops) dvModePorts(mode string) (tx, rx int, err error) {
	fd, err := os.OpenFile(filepath.Join(o.dvOptDir(), "MMDVM_Bridge", "DVSwitch.ini"), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return 0, 0, ErrDVNoPorts
	}
	defer fd.Close()
	if fi, err := fd.Stat(); err != nil || !fi.Mode().IsRegular() {
		return 0, 0, ErrDVNoPorts
	}
	data, err := io.ReadAll(io.LimitReader(fd, dvIniMax+1))
	if err != nil || len(data) > dvIniMax {
		return 0, 0, ErrDVNoPorts
	}
	f := ini.Parse(data)
	get := func(k string) int {
		v, _ := f.GetFold(mode, k)
		if !vInt(1024, 65535)(v) {
			return 0
		}
		n, _ := strconv.Atoi(v)
		return n
	}
	tx, rx = get("TXPort"), get("RXPort")
	if tx == 0 || rx == 0 {
		return 0, 0, ErrDVNoPorts
	}
	return tx, rx, nil
}

// DVSwitchTune switches Analog_Bridge to the mode (only if it is not in it
// already) and tunes. The commands mirror dvswitch.sh: a mode switch is
// rxport=30000, ambeMode=<mode>, txport=<DVSwitch.ini RXPort>,
// rxport=<DVSwitch.ini TXPort>; a tune is txTg=<value>; each is followed by
// info so Analog_Bridge rewrites ABInfo. Unlike the script, which sends one
// command per process, the helper follows Analog_Bridge's listener: see
// dvTLVSend. One command sequence at a time across the helper (lockTune); the
// ABInfo read that decides "already in this mode" is made inside the lock.
func (o *Ops) DVSwitchTune(ctx context.Context, req *DVSwitchRequest) (DVTuneResult, error) {
	req, err := o.dvArgs(req)
	if err != nil {
		return DVTuneResult{}, err
	}
	t := req.Tune
	if t == nil || req.Identity != nil || req.Credentials != nil || req.Modes != nil || req.Startup != nil {
		return DVTuneResult{}, errors.New("invalid request: tune only")
	}
	ok := false
	for _, m := range DVTuneModes {
		ok = ok || m == t.Mode
	}
	if !ok {
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
		if value, ok = o.dvYSFTarget(t.Tune); !ok {
			return DVTuneResult{}, dvInvalid("tune/value")
		}
	}
	unlock, err := o.lockTune(ctx)
	if err != nil {
		return DVTuneResult{}, err
	}
	defer unlock()
	// Read inside the lock: a sequence ahead of this one may have just changed
	// the mode, and its port.
	info, err := o.DVSwitchInfo(ctx)
	if err != nil {
		return DVTuneResult{}, err
	}
	if !info.Found || info.tlvRx == 0 {
		return DVTuneResult{}, ErrDVNoInfo
	}
	res := DVTuneResult{Mode: t.Mode}
	var cmds []string
	if DVNormMode(info.Mode) != t.Mode {
		tx, rx, err := o.dvModePorts(t.Mode)
		if err != nil {
			return res, err
		}
		park := o.DVParkPort
		if park == 0 {
			park = dvTLVSwitchPort
		}
		cmds = append(cmds, "rxport="+strconv.Itoa(park), "ambeMode="+t.Mode, "txport="+strconv.Itoa(rx), "rxport="+strconv.Itoa(tx), "info")
		res.Switched = true
	}
	if !t.ModeOnly {
		cmds = append(cmds, "txTg="+value, "info")
	}
	if len(cmds) == 0 {
		res.Confirmed = true // already in the mode, nothing to send
		return res, nil
	}
	if _, err := o.dvTLVSend(ctx, info.tlvRx, cmds); err != nil {
		return res, err
	}
	settle := o.DVTLVSettle
	if settle == 0 {
		settle = dvDefaultSettle
	}
	select {
	case <-ctx.Done():
		return res, nil
	case <-time.After(settle):
	}
	if now, err := o.DVSwitchInfo(ctx); err == nil && now.Found && DVNormMode(now.Mode) == t.Mode {
		res.Confirmed = t.ModeOnly || now.Tune == value || t.Mode == "DMR" && now.TG == value
	}
	return res, nil
}
