package helper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ABInfo reading and the restart/start op (docs/DVSWITCH.md, M4).

const (
	dvInfoMax        = 64 << 10
	dvInfoMaxEntries = 64
	// DefaultDVProcDir is where /proc/<pid>/root lives.
	DefaultDVProcDir = "/proc"
)

func (o *Ops) dvProcDir() string { return orDefault(o.DVProcDir, DefaultDVProcDir) }

var reDVPrivTune = regexp.MustCompile(`^[0-9]{1,8}#$`)

var reDVInfoName = regexp.MustCompile(`^ABInfo_[0-9]{1,5}\.json$`)

// abInfoFile is the strict shape of Analog_Bridge's ABInfo_<port>.json: every
// value is a string, an unknown or mistyped field refuses the whole file.
type abInfoFile struct {
	AB struct {
		Version string `json:"version"`
		Date    string `json:"date"`
	} `json:"ab"`
	DV3000 struct {
		IP        string `json:"ip"`
		Port      string `json:"port"`
		UseSerial string `json:"use_serial"`
	} `json:"dv3000"`
	UseFallback string `json:"use_fallback"`
	UseEmulator string `json:"use_emulator"`
	Mute        string `json:"mute"`
	USRP        struct {
		IP     string `json:"ip"`
		RxPort string `json:"rx_port"`
		TxPort string `json:"tx_port"`
		Ping   string `json:"ping"`
		ToPCM  struct {
			Shape string `json:"shape"`
			Gain  string `json:"gain"`
		} `json:"to_pcm"`
		ToAmbe struct {
			Shape string `json:"shape"`
			Gain  string `json:"gain"`
		} `json:"to_ambe"`
	} `json:"usrp"`
	TLV struct {
		IP       string `json:"ip"`
		TxPort   string `json:"tx_port"`
		RxPort   string `json:"rx_port"`
		AmbeSize string `json:"ambe_size"`
		AmbeMode string `json:"ambe_mode"`
	} `json:"tlv"`
	Digital struct {
		GW   string `json:"gw"`
		Rpt  string `json:"rpt"`
		TG   string `json:"tg"`
		TS   string `json:"ts"`
		CC   string `json:"cc"`
		Call string `json:"call"`
	} `json:"digital"`
	LastTune string `json:"last_tune"`
}

// DVSwitchInfo is the live state of Analog_Bridge. Found is false when the
// bridge does not run or has not written the file. Values that do not fit
// their validator are returned empty rather than echoed.
type DVSwitchInfo struct {
	Found      bool   `json:"found"`
	Version    string `json:"version"`
	Mode       string `json:"mode"`
	TG         string `json:"tg"`
	Slot       string `json:"slot"`
	ColorCode  string `json:"colorCode"`
	Tune       string `json:"tune"`
	Mute       string `json:"mute"`
	GatewayID  string `json:"gatewayId"`
	RepeaterID string `json:"repeaterId"`
	Updated    string `json:"updated"`
	// Started is analog_bridge's ActiveEnterTimestamp (RFC 3339, UTC); empty
	// when the unit does not run or systemd gave no usable time. It is set
	// even when Found is false (the bridge runs but has no state file yet).
	Started string `json:"started,omitempty"`
	// Callers is who is talking and who was (the info op only; nil from the
	// plain read). It holds callsigns: only logged-in roles may see it.
	Callers *DVCallers `json:"callers,omitempty"`
	// Private: last_tune is a DMR private call ("9990#"). TG is then the
	// number that was called, not a talkgroup.
	Private bool `json:"private,omitempty"`
	// Panel is the panel's mode: BM or DMR (both Mode DMR, told apart by
	// Analog_Bridge's TLV ports), YSF, P25 or NXDN (dvPanel).
	Panel string `json:"panel,omitempty"`

	// tlvRx is the TLV port Analog_Bridge listens on (not sent to clients).
	tlvRx int
	// tlvTx is the TLV port Analog_Bridge sends to (the partner's rxPort).
	tlvTx int
}

func keepIf(ok func(string) bool, s string) string {
	if ok(s) {
		return s
	}
	return ""
}

// ParseABInfo decodes and checks the content of one ABInfo file (the same
// parser the helper op uses; exported for tests of the callers).
func ParseABInfo(data []byte) (DVSwitchInfo, error) { return parseABInfo(data) }

// parseABInfo decodes and checks one ABInfo file.
func parseABInfo(data []byte) (DVSwitchInfo, error) {
	var f abInfoFile
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return DVSwitchInfo{}, errors.New("ABInfo is not in the expected format")
	}
	if dec.More() {
		return DVSwitchInfo{}, errors.New("ABInfo is not in the expected format")
	}
	tune := f.LastTune
	private := reDVPrivTune.MatchString(tune)
	if private || !vOrEmpty(reDVTune)(tune) {
		tune = "" // a private call is not a target: Private says so
	}
	tlvRx := 0
	if vInt(1024, 65535)(f.TLV.RxPort) {
		tlvRx, _ = strconv.Atoi(f.TLV.RxPort)
	}
	tlvTx := 0
	if vInt(1024, 65535)(f.TLV.TxPort) {
		tlvTx, _ = strconv.Atoi(f.TLV.TxPort)
	}
	return DVSwitchInfo{
		tlvRx:      tlvRx,
		tlvTx:      tlvTx,
		Private:    private,
		Found:      true,
		Version:    keepIf(regexp.MustCompile(`^[0-9A-Za-z._-]{1,20}$`).MatchString, f.AB.Version),
		Mode:       keepIf(func(s string) bool { return dvAmbeModes[s] }, f.TLV.AmbeMode),
		TG:         keepIf(reDVTG.MatchString, f.Digital.TG),
		Slot:       keepIf(vInt(1, 2), f.Digital.TS),
		ColorCode:  keepIf(vInt(0, 15), f.Digital.CC),
		Tune:       tune,
		Mute:       keepIf(func(s string) bool { return s == "ON" || s == "OFF" }, f.Mute),
		GatewayID:  keepIf(reDVDigits7.MatchString, f.Digital.GW),
		RepeaterID: keepIf(reDVDigits9.MatchString, f.Digital.Rpt),
	}, nil
}

// dvUnit returns the main PID (0: not running) and the start time of the
// analog_bridge unit (zero when unknown). The time comes from systemd as
// "@<unix seconds>" (--timestamp=unix); anything else leaves it unknown.
func (o *Ops) dvUnit(ctx context.Context) (int, time.Time) {
	out, _, err := o.Run.Run(ctx, "systemctl", "show", "-p", "MainPID", "-p", "ActiveEnterTimestamp", "--timestamp=unix", "analog_bridge.service")
	if err != nil {
		return 0, time.Time{}
	}
	pid := 0
	var started time.Time
	for _, l := range strings.Split(out, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(l), "=")
		if !ok {
			continue
		}
		switch k {
		case "MainPID":
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
				pid = n
			}
		case "ActiveEnterTimestamp":
			if sec, err := strconv.ParseInt(strings.TrimPrefix(strings.TrimSpace(v), "@"), 10, 64); err == nil && strings.HasPrefix(strings.TrimSpace(v), "@") && sec > 0 {
				started = time.Unix(sec, 0).UTC()
			}
		}
	}
	return pid, started
}

// DVSwitchInfo reads ABInfo through /proc/<MainPID>/root/tmp (Analog_Bridge's
// unit has PrivateTmp, its /tmp is not the host's). The newest ABInfo_*.json
// wins: the file name follows the USRP rxPort. The file is opened with
// O_NOFOLLOW, must be a regular file and at most 64 KiB.
func (o *Ops) DVSwitchInfo(ctx context.Context) (DVSwitchInfo, error) {
	pid, started := o.dvUnit(ctx)
	if pid == 0 {
		return DVSwitchInfo{}, nil
	}
	info, err := o.dvReadInfo(pid)
	if info.Found {
		o.dvPanel(&info)
	}
	if !started.IsZero() {
		info.Started = started.Format(time.RFC3339)
	}
	return info, err
}

// dvReadInfo reads the newest ABInfo file of the bridge with this PID. A
// missing file is not an error: the result is the zero value.
func (o *Ops) dvReadInfo(pid int) (DVSwitchInfo, error) {
	dir := filepath.Join(o.dvProcDir(), strconv.Itoa(pid), "root", "tmp")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return DVSwitchInfo{}, nil
	}
	type cand struct {
		path string
		mod  time.Time
	}
	var cands []cand
	for i, e := range ents {
		if i >= dvInfoMaxEntries*16 {
			break
		}
		if !reDVInfoName.MatchString(e.Name()) || !e.Type().IsRegular() {
			continue
		}
		fi, err := e.Info()
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		cands = append(cands, cand{filepath.Join(dir, e.Name()), fi.ModTime()})
	}
	if len(cands) == 0 {
		return DVSwitchInfo{}, nil
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].mod.After(cands[j].mod) })
	fd, err := os.OpenFile(cands[0].path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return DVSwitchInfo{}, nil
	}
	defer fd.Close()
	fi, err := fd.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		return DVSwitchInfo{}, nil
	}
	data, err := io.ReadAll(io.LimitReader(fd, dvInfoMax+1))
	if err != nil {
		return DVSwitchInfo{}, nil
	}
	if len(data) > dvInfoMax {
		return DVSwitchInfo{}, errors.New("ABInfo is too large")
	}
	info, err := parseABInfo(data)
	if err != nil {
		return DVSwitchInfo{}, err
	}
	info.Updated = fi.ModTime().UTC().Format(time.RFC3339)
	return info, nil
}

// ---- restart / start

// DVRestartResult lists what was acted on. After a restart DVSwitch starts in
// the last mode (the boot mode follows it, dvswitch_bootmode.go); with the
// resume switch on, the server re-tunes the last talkgroup or reflector.
type DVRestartResult struct {
	Action  string         `json:"action"`
	Units   []string       `json:"units"`
	Skipped []string       `json:"skipped"`
	States  []DVSwitchUnit `json:"states"`
}

var dvScopes = map[string][]string{
	"dmr":      {"mmdvm_bridge"},
	"bridge":   {"analog_bridge", "mmdvm_bridge"},
	"gateways": {"ysfgateway", "p25gateway", "nxdngateway"},
	"all":      {"analog_bridge", "mmdvm_bridge", "ysfgateway", "p25gateway", "nxdngateway"},
}

// DVSwitchRestart restarts or starts the units of a scope that exist.
func (o *Ops) DVSwitchRestart(ctx context.Context, req *DVSwitchRequest) (DVRestartResult, error) {
	res := DVRestartResult{Units: []string{}, Skipped: []string{}, States: []DVSwitchUnit{}}
	req, err := o.dvArgs(req)
	if err != nil {
		return res, err
	}
	action := req.Action
	if action == "" {
		action = "restart"
	}
	if action != "restart" && action != "start" {
		return res, dvInvalid("action")
	}
	res.Action = action
	units, ok := dvScopes[req.Scope]
	if !ok {
		return res, dvInvalid("scope")
	}
	if req.Scope == "dmr" || req.Scope == "all" {
		// only when stfu is supposed to be running: on demand it is stopped
		// outside BM mode, and a restart must not log it in
		if _, configured := o.dvBMConfigured(ctx); configured && o.dvSTFUWanted(ctx) {
			units = append(append([]string{}, units...), dvSTFUUnit)
		}
	}
	o.applyMu.Lock()
	defer o.applyMu.Unlock()
	if o.dvBusy(ctx) {
		return res, ErrBusy
	}
	loaded := map[string]bool{}
	for _, u := range o.dvUnitStates(ctx) {
		loaded[u.Name] = u.Loaded
	}
	argv := []string{"systemctl", action}
	for _, u := range units {
		if loaded[u] {
			argv = append(argv, u+".service")
			res.Units = append(res.Units, u)
		} else {
			res.Skipped = append(res.Skipped, u)
		}
	}
	if len(res.Units) == 0 {
		return res, ErrDVNotInstalled
	}
	if out, code, err := o.Run.Run(ctx, argv...); err != nil || code != 0 {
		_ = out // command output may name paths; keep it out of the reply
		return res, fmt.Errorf("systemctl %s failed", action)
	}
	res.States = o.dvUnitStates(ctx)
	return res, nil
}
