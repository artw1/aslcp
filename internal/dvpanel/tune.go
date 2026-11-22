package dvpanel

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Panel modes in display order. LOOP is the engine's own parrot (the loopback
// test); BM, TGIF and DMR are the DMR networks, which carry talkgroups, private
// calls and a slot.
const (
	ModeLoop = "LOOP"
	ModeBM   = "BM"
	ModeTGIF = "TGIF"
	ModeDMR  = "DMR"
	ModeYSF  = "YSF"
	ModeP25  = "P25"
	ModeNXDN = "NXDN"
)

// PanelModes are the modes the panel handles, in display order.
var PanelModes = []string{ModeLoop, ModeBM, ModeTGIF, ModeDMR, ModeYSF, ModeP25, ModeNXDN}

// PrivateMark is the suffix a DMR ID takes for a private call ("9990#").
// Only a DMR tune may carry it.
const PrivateMark = "#"

// ParrotID is the DMR ID of the parrot, which answers private calls only. The
// loopback mode takes it as its one tune.
const ParrotID = "9990"

var (
	reTG    = regexp.MustCompile(`^[0-9]{1,8}$`)
	reRefl  = regexp.MustCompile(`^[0-9]{1,5}$`)
	reHost  = regexp.MustCompile(`^[A-Za-z0-9.-]{1,253}$`)
	reYSFID = regexp.MustCompile(`^[0-9]{5}$`)

	// a value of this shape is a host:port and must pass hostPort, never a name
	reHostPortLike = regexp.MustCompile(`^[A-Za-z0-9.-]{1,253}:[0-9]{1,5}$`)
)

// NormMode returns mode when it is a panel mode, else "".
func NormMode(mode string) string {
	for _, m := range PanelModes {
		if m == mode {
			return m
		}
	}
	return ""
}

// DMRFamily reports whether mode is one of the DMR networks.
func DMRFamily(mode string) bool {
	return mode == ModeBM || mode == ModeTGIF || mode == ModeDMR
}

// UnlinkTune is the tune value that disconnects a mode: 4000 for the DMR
// networks, "disconnect" for YSF and LOOP, 9999 for P25 and NXDN. "" for an
// unknown mode.
func UnlinkTune(mode string) string {
	switch {
	case DMRFamily(mode):
		return "4000"
	case mode == ModeYSF || mode == ModeLoop:
		return "disconnect"
	case mode == ModeP25 || mode == ModeNXDN:
		return "9999"
	}
	return ""
}

// UnlinkValue reports whether tune disconnects the mode: it is not a place
// to resume to.
func UnlinkValue(mode, tune string) bool {
	u := UnlinkTune(mode)
	return u != "" && tune == u
}

// PrivateTune reports whether tune is a DMR private call ("9990#"): a valid
// DMR tune with the private mark. It is a one-off call, never a place to
// resume to.
func PrivateTune(mode, tune string) bool {
	return DMRFamily(mode) && strings.HasSuffix(tune, PrivateMark) && ValidTune(mode, tune)
}

// ysfName is a reflector name as in the YSF host list: 1-32 printable
// characters, no ; # =, no leading or trailing space.
func ysfName(s string) bool {
	if s == "" || s != strings.TrimSpace(s) || utf8.RuneCountInString(s) > 32 {
		return false
	}
	for _, r := range s {
		if r == utf8.RuneError || !unicode.IsPrint(r) || r == ';' || r == '#' || r == '=' {
			return false
		}
	}
	return true
}

// hostPort reports whether s is host:port with a port of 1-65535.
func hostPort(s string) bool {
	i := strings.LastIndex(s, ":")
	if i < 1 || !reHost.MatchString(s[:i]) {
		return false
	}
	p := s[i+1:]
	if len(p) < 1 || len(p) > 5 || (len(p) > 1 && p[0] == '0') {
		return false
	}
	n := 0
	for _, c := range []byte(p) {
		if c < '0' || c > '9' {
			return false
		}
		n = n*10 + int(c-'0')
	}
	return n >= 1 && n <= 65535
}

// ValidTune reports whether tune is allowed for the mode: a talkgroup number
// for the DMR networks (with a trailing "#" for a private call), a number of
// up to five digits for P25 and NXDN, for YSF a reflector id, a name or
// host:port, and the parrot ID for LOOP. The unlink value of the mode is
// valid too. Nothing starts with "-" or holds "=", so a value can neither
// look like an option nor smuggle a second key.
func ValidTune(mode, tune string) bool {
	if tune == "" || strings.HasPrefix(tune, "-") || strings.Contains(tune, "=") {
		return false
	}
	switch {
	case mode == ModeLoop:
		return tune == ParrotID || tune == UnlinkTune(mode)
	case DMRFamily(mode):
		return reTG.MatchString(strings.TrimSuffix(tune, PrivateMark))
	case mode == ModeP25 || mode == ModeNXDN:
		return reRefl.MatchString(tune)
	case mode == ModeYSF:
		if reHostPortLike.MatchString(tune) {
			return hostPort(tune)
		}
		return reYSFID.MatchString(tune) || ysfName(tune)
	}
	return false
}
