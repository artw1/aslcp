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
// BM (BrandMeister through STFU) and DMR (DMR - Other through MMDVM_Bridge)
// are both DMR on the air.
var DVTuneModes = []string{"BM", "DMR", "YSF", "P25", "NXDN"}

// DVTune is the argument of dvswitch-tune.
//
// ModeOnly switches the mode and sends no tune (Tune must be empty); a node
// already in the mode gets no command at all.
type DVTune struct {
	Mode     string `json:"mode"`
	Tune     string `json:"tune"`
	ModeOnly bool   `json:"modeOnly,omitempty"`
}

// DVUnlinkTune is the tune value that unlinks the mode (dvswitch.sh): BM and
// DMR 4000, YSF disconnect, P25 and NXDN 9999. "" for an unknown mode.
func DVUnlinkTune(mode string) string {
	switch mode {
	case "BM", "DMR":
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
	// Left is the mode a switch left and unlinked first (its unlink value
	// went to the outgoing partner before the repoint); "" when nothing was
	// left or it was already unlinked. LeftFailed: that unlink could not be
	// sent (the switch went ahead all the same).
	Left       string `json:"left,omitempty"`
	LeftFailed bool   `json:"leftFailed,omitempty"`
}

// dvLinkedTune is what the bridge state shows the panel mode tuned to: for
// BM and DMR the last tune or else the talkgroup, for the others the last
// tune. "" when unknown.
func dvLinkedTune(mode string, info DVSwitchInfo) string {
	if info.Tune != "" || !DVDMRFamily(mode) {
		return info.Tune
	}
	return info.TG
}

// DVNormMode maps ABInfo's ambe_mode to a tune mode: YSFN and YSFW are YSF.
// "" means a mode this feature does not handle (DMR_IPSC, DSTAR, ...). BM
// (never an ambe_mode; DVPanelMode tells it from DMR) maps to itself.
func DVNormMode(m string) string {
	switch m {
	case "BM", "DMR", "P25", "NXDN":
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
	case "BM", "DMR":
		return tune == "4000"
	case "YSF":
		return tune == "disconnect"
	case "P25", "NXDN":
		return tune == "9999"
	}
	return false
}

// DVPrivateMark is the suffix DVSwitch takes on a DMR ID for a private call
// ("9990#"; docs/DVSWITCH.md, "Private call"). Only a DMR tune may carry it.
const DVPrivateMark = "#"

// DVParrotID is the BrandMeister Parrot, which answers private calls only.
const DVParrotID = "9990"

// DVPrivateTune reports whether tune is a DMR private call ("9990#") in BM or
// DMR: a valid DMR tune with the private mark. It is a one-off call, never a place to
// resume to.
func DVPrivateTune(mode, tune string) bool {
	return DVDMRFamily(mode) && strings.HasSuffix(tune, DVPrivateMark) && DVValidTune(mode, tune)
}

var reDVHostPort = regexp.MustCompile(`^[A-Za-z0-9.-]{1,253}:[0-9]{1,5}$`)

// DVValidTune reports whether tune is allowed for the mode. These are the
// startup-value validators (a TG number for DMR, with a trailing "#" for a
// private call, P25 and NXDN; for YSF a
// reflector id, a name as in YSFHosts.txt, or host:port) and the unlink word
// "disconnect". Nothing starts with "-" or holds "=", so a value can neither
// look like an option nor smuggle a second TLV key.
func DVValidTune(mode, tune string) bool {
	if tune == "" || strings.HasPrefix(tune, "-") || strings.Contains(tune, "=") {
		return false
	}
	switch mode {
	case "BM", "DMR":
		return reDVTG.MatchString(strings.TrimSuffix(tune, DVPrivateMark))
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

// dvSwitchCmds is the mode switch sequence to the partner whose DVSwitch.ini
// ports are tx and rx: Analog_Bridge parks its listener, changes the AMBE
// mode, sends to the partner's rxPort and listens on its txPort.
func (o *Ops) dvSwitchCmds(mode string, tx, rx int) []string {
	park := o.DVParkPort
	if park == 0 {
		park = dvTLVSwitchPort
	}
	return []string{"rxport=" + strconv.Itoa(park), "ambeMode=" + mode, "txport=" + strconv.Itoa(rx), "rxport=" + strconv.Itoa(tx), "info"}
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
	// BM goes to STFU's [STFU] ports, DMR to MMDVM_Bridge's [DMR] ports; both
	// are ambeMode=DMR. Analog_Bridge in DMR on the other DMR connection's
	// ports switches over (DVPanelMode).
	section, ambe := t.Mode, t.Mode
	if t.Mode == DVModeBM {
		section, ambe = dvSTFUSection, DVModeDMR
	}
	port := info.tlvRx
	if cur := DVPanelMode(info); cur != t.Mode {
		tx, rx, err := o.dvModePorts(section)
		if err != nil {
			return res, err
		}
		// Unlink the mode being left while Analog_Bridge still points at its
		// partner: BrandMeister drops STFU's subscription, a reflector the
		// link, while nobody listens. Its remembered tune stays (the tracker
		// only reads ABInfo, which this does not rewrite), so switching back
		// re-tunes it. A failed send is reported; the switch goes ahead.
		if lt := dvLinkedTune(cur, info); cur != "" && lt != "" && !DVUnlinkValue(cur, lt) {
			res.Left = cur
			if p, err := o.dvTLVSend(ctx, port, []string{"txTg=" + DVUnlinkTune(cur)}); err != nil {
				res.LeftFailed = true
			} else {
				port = p
			}
		}
		cmds = append(cmds, o.dvSwitchCmds(ambe, tx, rx)...)
		res.Switched = true
	}
	if !t.ModeOnly {
		cmds = append(cmds, "txTg="+value, "info")
	}
	if len(cmds) == 0 {
		res.Confirmed = true // already in the mode, nothing to send
		return res, nil
	}
	if _, err := o.dvTLVSend(ctx, port, cmds); err != nil {
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
	if now, err := o.DVSwitchInfo(ctx); err == nil && now.Found && DVPanelMode(now) == t.Mode {
		res.Confirmed = t.ModeOnly || now.Tune == value || DVDMRFamily(t.Mode) && now.TG == value
		if DVPrivateTune(t.Mode, value) {
			res.Confirmed = now.Private || now.TG == strings.TrimSuffix(value, DVPrivateMark)
		}
	}
	return res, nil
}
