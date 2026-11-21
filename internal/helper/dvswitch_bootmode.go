package helper

import (
	"context"
	"errors"
	"io"
	"strconv"

	"github.com/artw1/aslcp/internal/dvswitch/ini"
)

// The boot mode (docs/DVSWITCH.md, "Boot mode"). There are no startup modes
// to set: DVSwitch starts in the last mode. When the server's resume tracker
// sees a new last mode it calls dvswitch-bootmode, which writes
// Analog_Bridge.ini [AMBE_AUDIO] ambeMode and the TLV ports of that mode (BM
// starts as DMR on [DMR]'s ports: STFU is on demand) and blanks the gateways'
// [Network] Startup (the resume re-tunes the last reflector). It writes only
// what differs, keeps no history copy (it is not a user change: the
// rollback list stays the user's), restarts nothing and never touches txTg.

// dvBootAmbe: panel mode -> ambeMode and DVSwitch.ini section.
var dvBootAmbe = map[string][2]string{
	DVModeBM:  {"DMR", "DMR"},
	DVModeDMR: {"DMR", "DMR"},
	"YSF":     {"YSFN", "YSF"},
	"P25":     {"P25", "P25"},
	"NXDN":    {"NXDN", "NXDN"},
}

// dvBlankStartup blanks [Network] Startup of the three gateways (a missing
// file or key is skipped).
var dvBlankStartup = []dvChange{
	{DVFileYSFGateway, "Network", "Startup", ""},
	{DVFileP25Gateway, "Network", "Startup", ""},
	{DVFileNXDNGateway, "Network", "Startup", ""},
}

// DVSwitchBootMode makes DVSwitch start in mode (req.Mode, a panel mode).
func (o *Ops) DVSwitchBootMode(ctx context.Context, req *DVSwitchRequest) (DVApplyResult, error) {
	req, err := o.dvArgs(req)
	if err != nil {
		return DVApplyResult{}, err
	}
	if req.Identity != nil || req.Credentials != nil || req.Modes != nil || req.Tune != nil || req.USRP != nil || req.Private != nil || req.BM != nil || len(req.Base) > 0 {
		return DVApplyResult{}, errors.New("invalid request: boot mode only")
	}
	m, ok := dvBootAmbe[req.Mode]
	if !ok {
		return DVApplyResult{}, dvInvalid("mode")
	}
	tx, rx, err := o.dvModePorts(m[1])
	if err != nil {
		return DVApplyResult{}, err
	}
	ambe := m[0]
	if ambe == "YSFN" {
		// a YSF node set to wide mode by hand keeps it
		if cur := o.dvAmbeMode(); cur == "YSFW" {
			ambe = cur
		}
	}
	// Analog_Bridge sends to the partner's rxPort and listens on its txPort
	// (as the mode switch, dvSwitchCmds).
	ch := append([]dvChange{
		{DVFileAnalogBridge, "AMBE_AUDIO", "ambeMode", ambe},
		{DVFileAnalogBridge, "AMBE_AUDIO", "txPort", strconv.Itoa(rx)},
		{DVFileAnalogBridge, "AMBE_AUDIO", "rxPort", strconv.Itoa(tx)},
	}, dvBlankStartup...)
	res, err := o.dvApplyOpt(ctx, ch, nil, dvApplyOpts{noHistory: true})
	if err != nil {
		return res, err
	}
	res.RestartHint = []string{} // takes effect at the next start; nothing restarts now
	return res, nil
}

// dvAmbeMode reads [AMBE_AUDIO] ambeMode ("" when unreadable).
func (o *Ops) dvAmbeMode() string {
	def, _ := dvFileByID(DVFileAnalogBridge)
	st, err := o.dvRead(def)
	if err != nil || !st.regular {
		return ""
	}
	v, _ := ini.Parse(st.data).Get("AMBE_AUDIO", "ambeMode")
	return v
}

// StepInstallDefaults sets the defaults of a fresh install: every mode off
// (all Enable flags of MMDVM_Bridge.ini 0), BrandMeister disabled in ASLCP,
// no stored DMR profile, the gateways' Startup blank. Each mode is turned on
// with its card's Enable. It runs before restore-apply, so settings staged
// by a backup restore still win.
const StepInstallDefaults = "install-defaults"

func (o *Ops) dvInstallDefaults(ctx context.Context, w io.Writer) error {
	var ch []dvChange
	for _, ms := range dvModeSections {
		ch = append(ch,
			dvChange{DVFileMMDVMBridge, ms.section, "Enable", "0"},
			dvChange{DVFileMMDVMBridge, ms.section + " Network", "Enable", "0"})
	}
	ch = append(ch, dvBlankStartup...)
	// inside the install job: no busy check, and not a user change
	if _, err := o.dvApplyOpt(ctx, ch, nil, dvApplyOpts{noHistory: true, noBusy: true}); err != nil {
		io.WriteString(w, "install-defaults: the modes could not be turned off: "+err.Error()+"\n")
	} else {
		io.WriteString(w, "install-defaults: all modes are off; enable them on the DVSwitch page\n")
	}
	_ = o.dvBMEnabledSet(false)
	_ = o.dvDMRProfilesReset()
	return nil // never fails the install
}

// DVModeOn says whether a panel mode is on, as its card shows it: BM when
// BrandMeister is enabled and configured, the others when MMDVM_Bridge
// enables them (DMR: either DMR profile).
func DVModeOn(v DVSwitchSetup, mode string) bool {
	switch mode {
	case DVModeBM:
		return v.BrandMeister.Configured && v.BrandMeister.Enabled
	case DVModeDMR:
		return v.Modes.DMR
	case "YSF":
		return v.Modes.YSF
	case "P25":
		return v.Modes.P25
	case "NXDN":
		return v.Modes.NXDN
	}
	return false
}
