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
}

func keepIf(ok func(string) bool, s string) string {
	if ok(s) {
		return s
	}
	return ""
}

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
	if !vOrEmpty(reDVTune)(tune) {
		tune = ""
	}
	return DVSwitchInfo{
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

// dvMainPID returns the main PID of the analog_bridge unit (0: not running).
func (o *Ops) dvMainPID(ctx context.Context) int {
	out, _, err := o.Run.Run(ctx, "systemctl", "show", "-p", "MainPID", "analog_bridge.service")
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(out), "MainPID=")))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// DVSwitchInfo reads ABInfo through /proc/<MainPID>/root/tmp (Analog_Bridge's
// unit has PrivateTmp, its /tmp is not the host's). The newest ABInfo_*.json
// wins: the file name follows the USRP rxPort. The file is opened with
// O_NOFOLLOW, must be a regular file and at most 64 KiB.
func (o *Ops) DVSwitchInfo(ctx context.Context) (DVSwitchInfo, error) {
	pid := o.dvMainPID(ctx)
	if pid == 0 {
		return DVSwitchInfo{}, nil
	}
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

// DVRestartResult lists what was acted on. A restart resets the running mode
// and talkgroup to the startup values in the ini files.
type DVRestartResult struct {
	Action  string         `json:"action"`
	Units   []string       `json:"units"`
	Skipped []string       `json:"skipped"`
	States  []DVSwitchUnit `json:"states"`
}

var dvScopes = map[string][]string{
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
