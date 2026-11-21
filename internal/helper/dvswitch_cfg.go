package helper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/artw1/aslcp/internal/dvswitch/ini"
)

// DVSwitch credentials and ini files (docs/DVSWITCH.md, M4). Only the typed
// ops below write the DVSwitch inis. The client names a file by enum id, never
// a path; every (file, section, key) it can reach is in dvAllow with its
// validator; values never appear in an error, a log line or a reply (the
// password is write-only).

// Operation names of the M4 ops.
const (
	OpDVSwitchSetup       = "dvswitch-setup"       // read-only view of the editable values
	OpDVSwitchInfo        = "dvswitch-info"        // parsed ABInfo (read-only)
	OpDVSwitchIdentity    = "dvswitch-identity"    // callsign, DMR ID, ESSID everywhere, optional NXDN ID
	OpDVSwitchCredentials = "dvswitch-credentials" // DMR - Other's master address, port, password (dvswitch-network save)
	OpDVSwitchModes       = "dvswitch-modes"       // enable flags
	OpDVSwitchBootMode    = "dvswitch-bootmode"    // internal: ambeMode and ports follow the last mode, gateway Startup blank
	OpDVSwitchRollback    = "dvswitch-rollback"    // newest history copy of one file
	OpDVSwitchRestart     = "dvswitch-restart"     // enum scope and action
	// OpDVSwitchLists (dvswitch_lists.go) is the host and TG lists of the pickers.
)

func isDVConfigOp(op string) bool {
	switch op {
	case OpDVSwitchIdentity, OpDVSwitchCredentials, OpDVSwitchModes, OpDVSwitchBootMode, OpDVSwitchRollback, OpDVSwitchRestart, OpDVSwitchLists, OpDVSwitchTune, OpDVSwitchUSRP, OpDVSwitchPrivate, OpDVSwitchBM, OpDVSwitchNetwork:
		return true
	}
	return false
}

const (
	// DefaultDVOptDir is the parent of /opt/Analog_Bridge and the others.
	DefaultDVOptDir = "/opt"
	// DefaultDVHistoryDir keeps the previous versions (root 0700): like the
	// config history it is not writable by the unprivileged service.
	DefaultDVHistoryDir = "/var/lib/aslcp-dvswitch-history"
	dvHistoryKeep       = 10
	dvIniMaxSize        = 1 << 20
	dvStockPassword     = "passw0rd" // stock MMDVM_Bridge.ini value: counts as "not set"
)

// Errors of the M4 ops. The client maps the texts back (client.go).
var (
	ErrDVInvalid      = errors.New("invalid_field: a value is not allowed")
	ErrDVNotInstalled = errors.New("not_installed: DVSwitch is not installed (its ini files are missing)")
	ErrDVNoHistory    = errors.New("no_history: there is no earlier version of this file")
	ErrDVNoInfo       = errors.New("no_info: Analog_Bridge is not running or has not written its state file")
)

// dvNoChange is the result of a request that carries no values: nothing is
// written, nothing changed and no unit needs a restart.
func dvNoChange() DVApplyResult {
	return DVApplyResult{Files: []string{}, Skipped: []string{}, RestartHint: []string{}, Hashes: map[string]string{}}
}

func dvInvalid(what string) error { return fmt.Errorf("%w (%s)", ErrDVInvalid, what) }

// DVFile ids.
const (
	DVFileAnalogBridge = "analog_bridge"
	DVFileMMDVMBridge  = "mmdvm_bridge"
	DVFileYSFGateway   = "ysfgateway"
	DVFileP25Gateway   = "p25gateway"
	DVFileNXDNGateway  = "nxdngateway"
	// DVFileDVSwitch is MMDVM_Bridge/DVSwitch.ini: only its [STFU] section is
	// written (the BrandMeister connection, dvswitch_stfu.go). Its keys are
	// read case-insensitively by the DVSwitch tools.
	DVFileDVSwitch = "dvswitch"
)

type dvFileDef struct {
	id, rel, unit string
	core          bool // Analog_Bridge and MMDVM_Bridge: a write needs them
}

// dvFiles is the whole file allowlist: id -> path below the opt dir.
var dvFiles = []dvFileDef{
	{DVFileAnalogBridge, "Analog_Bridge/Analog_Bridge.ini", "analog_bridge", true},
	{DVFileMMDVMBridge, "MMDVM_Bridge/MMDVM_Bridge.ini", "mmdvm_bridge", true},
	{DVFileYSFGateway, "YSFGateway/YSFGateway.ini", "ysfgateway", false},
	{DVFileP25Gateway, "P25Gateway/P25Gateway.ini", "p25gateway", false},
	{DVFileNXDNGateway, "NXDNGateway/NXDNGateway.ini", "nxdngateway", false},
	{DVFileDVSwitch, "MMDVM_Bridge/DVSwitch.ini", "stfu", false},
}

func dvFileByID(id string) (dvFileDef, bool) {
	for _, f := range dvFiles {
		if f.id == id {
			return f, true
		}
	}
	return dvFileDef{}, false
}

// DVFileIDs returns the allowlisted file ids in order.
func DVFileIDs() []string {
	ids := make([]string, len(dvFiles))
	for i, f := range dvFiles {
		ids[i] = f.id
	}
	return ids
}

// ---- validators (docs/DVSWITCH.md, Allowlist)

var (
	reDVCall    = regexp.MustCompile(`^[A-Z0-9/]{3,10}$`)
	reDVDigits7 = regexp.MustCompile(`^[0-9]{7}$`)
	reDVDigits9 = regexp.MustCompile(`^[0-9]{9}$`)
	reDVESSID   = regexp.MustCompile(`^[0-9]{2}$`)
	reDVHost    = regexp.MustCompile(`^[A-Za-z0-9.-]{1,253}$`)
	reDVTG      = regexp.MustCompile(`^[0-9]{1,8}$`)
	reDVRefl    = regexp.MustCompile(`^[0-9]{1,5}$`)
	reDVTune    = regexp.MustCompile(`^[A-Za-z0-9.:_-]{1,64}$`)
	reDVYSFID   = regexp.MustCompile(`^[0-9]{5}$`)
)

// dvStockNXDNID is the stock [NXDN] Id of MMDVM_Bridge.ini: it counts as "not set".
const dvStockNXDNID = "12345"

// vNXDNID: digits only, 1-65519 (16-bit; 65520 and above are reserved), no
// leading zeros.
var vNXDNID = vInt(1, 65519)

// vYSFStartup: the YSFGateway Startup value, a 5-digit reflector id or a
// reflector name as in YSFHosts.txt: 1-32 printable characters, inner spaces
// allowed, no ; # = (ini comment and assignment characters), no control
// characters, no leading or trailing space.
func vYSFStartup(s string) bool {
	if reDVYSFID.MatchString(s) {
		return true
	}
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

// dvAmbeModes are the values Analog_Bridge.ini accepts for ambeMode in v1
// (its own comment lists DMR, DMR_IPSC, DSTAR, NXDN, P25, YSFN, YSFW).
var dvAmbeModes = map[string]bool{"DMR": true, "YSFN": true, "YSFW": true, "P25": true, "NXDN": true}

func vMatch(re *regexp.Regexp) func(string) bool { return re.MatchString }

// vBlank: the gateways' [Network] Startup is only ever blanked (DVSwitch
// starts in the last mode, ASLCP re-tunes the last reflector).
func vBlank(s string) bool { return s == "" }
func vOrEmpty(re *regexp.Regexp) func(string) bool {
	return func(s string) bool { return s == "" || re.MatchString(s) }
}
func vInt(lo, hi int) func(string) bool {
	return func(s string) bool {
		n, err := strconv.Atoi(s)
		return err == nil && n >= lo && n <= hi && strconv.Itoa(n) == s
	}
}

// vPassword: printable ASCII 0x21-0x7E, 1-64, none of " # ; (MMDVM_Bridge
// strips # and ; comments after a value and unquotes ").
func vPassword(s string) bool {
	if len(s) < 1 || len(s) > 64 {
		return false
	}
	for _, c := range []byte(s) {
		if c < 0x21 || c > 0x7e || c == '"' || c == '#' || c == ';' {
			return false
		}
	}
	return true
}

type dvKey struct{ file, section, key string }

// dvModeSections: mode id -> the MMDVM_Bridge section and its network section.
var dvModeSections = []struct{ id, section string }{
	{"dmr", "DMR"}, {"ysf", "System Fusion"}, {"p25", "P25"}, {"nxdn", "NXDN"},
}

var dvAllow = func() map[dvKey]func(string) bool {
	m := map[dvKey]func(string) bool{
		{DVFileAnalogBridge, "AMBE_AUDIO", "gatewayDmrId"}: vMatch(reDVDigits7),
		{DVFileAnalogBridge, "AMBE_AUDIO", "repeaterID"}:   vMatch(reDVDigits9),
		{DVFileAnalogBridge, "AMBE_AUDIO", "ambeMode"}:     func(s string) bool { return dvAmbeModes[s] },
		{DVFileAnalogBridge, "AMBE_AUDIO", "txPort"}:       vInt(1024, 65535),
		{DVFileAnalogBridge, "AMBE_AUDIO", "rxPort"}:       vInt(1024, 65535),

		{DVFileAnalogBridge, "USRP", "address"}: func(s string) bool { return s == dvUSRPAddress },
		{DVFileAnalogBridge, "USRP", "rxPort"}:  vInt(1024, 65535),
		{DVFileAnalogBridge, "USRP", "txPort"}:  vInt(1024, 65535),

		{DVFileMMDVMBridge, "General", "Callsign"}:     vMatch(reDVCall),
		{DVFileMMDVMBridge, "General", "Id"}:           vMatch(reDVDigits9),
		{DVFileMMDVMBridge, "NXDN", "Id"}:              vNXDNID,
		{DVFileMMDVMBridge, "Info", "Location"}:        vInfoText(20),
		{DVFileMMDVMBridge, "Info", "Description"}:     vInfoText(19),
		{DVFileMMDVMBridge, "Info", "URL"}:             vInfoURL,
		{DVFileMMDVMBridge, "Info", "RXFrequency"}:     vFreqHz,
		{DVFileMMDVMBridge, "Info", "TXFrequency"}:     vFreqHz,
		{DVFileMMDVMBridge, "Info", "Latitude"}:        vCoord(-90, 90),
		{DVFileMMDVMBridge, "Info", "Longitude"}:       vCoord(-180, 180),
		{DVFileMMDVMBridge, "DMR Network", "Address"}:  vMatch(reDVHost),
		{DVFileMMDVMBridge, "DMR Network", "Port"}:     vInt(1, 65535),
		{DVFileMMDVMBridge, "DMR Network", "Password"}: vPassword,
		{DVFileYSFGateway, "General", "Callsign"}:      vMatch(reDVCall),
		{DVFileYSFGateway, "General", "Id"}:            vMatch(reDVDigits7),
		{DVFileYSFGateway, "Network", "Startup"}:       vBlank,
		{DVFileP25Gateway, "General", "Callsign"}:      vMatch(reDVCall),
		{DVFileP25Gateway, "Network", "Startup"}:       vBlank,
		{DVFileNXDNGateway, "General", "Callsign"}:     vMatch(reDVCall),
		{DVFileNXDNGateway, "Network", "Startup"}:      vBlank,

		{DVFileDVSwitch, dvSTFUSection, "BMAddress"}:   vMatch(reDVHost),
		{DVFileDVSwitch, dvSTFUSection, "BMPort"}:      vInt(1, 65535),
		{DVFileDVSwitch, dvSTFUSection, "BMPassword"}:  vPassword,
		{DVFileDVSwitch, dvSTFUSection, "Address"}:     func(s string) bool { return s == dvSTFULocal },
		{DVFileDVSwitch, dvSTFUSection, "txPort"}:      vInt(1024, 65535),
		{DVFileDVSwitch, dvSTFUSection, "rxPort"}:      vInt(1024, 65535),
		{DVFileDVSwitch, dvSTFUSection, "UserID"}:      vMatch(reDVDigits7),
		{DVFileDVSwitch, dvSTFUSection, "TalkerAlias"}: vTalkerAlias,
		{DVFileDVSwitch, dvSTFUSection, "StartTG"}:     vMatch(reDVTG),
	}
	for _, ms := range dvModeSections {
		m[dvKey{DVFileMMDVMBridge, ms.section, "Enable"}] = vInt(0, 1)
		m[dvKey{DVFileMMDVMBridge, ms.section + " Network", "Enable"}] = vInt(0, 1)
	}
	return m
}()

// DVSwitchRequest is the argument of the M4 ops. Which fields are used
// depends on the op; the helper rejects a request that carries a field its op
// does not take.
type DVSwitchRequest struct {
	// Base: file id -> sha256 of the version the client saw (from the setup
	// view). A mismatch is a conflict. Optional.
	Base   map[string]string `json:"base,omitempty"`
	Author string            `json:"author,omitempty"`

	Identity    *DVIdentity    `json:"identity,omitempty"`
	Credentials *DVCredentials `json:"credentials,omitempty"`
	Modes       *DVModes       `json:"modes,omitempty"`
	// File: rollback, a file id.
	File string `json:"file,omitempty"`
	// Scope: restart, "bridge", "gateways" or "all". Action: "restart"
	// (default) or "start".
	Scope  string `json:"scope,omitempty"`
	Action string `json:"action,omitempty"`
	// Mode: lists, "dmr", "ysf", "p25" or "nxdn"; bootmode, a panel mode
	// (DVTuneModes).
	Mode string `json:"mode,omitempty"`
	// Tune: dvswitch-tune.
	Tune *DVTune `json:"tune,omitempty"`
	// USRP: dvswitch-usrp. Private: dvswitch-private.
	USRP    *DVUSRP    `json:"usrp,omitempty"`
	Private *DVPrivate `json:"private,omitempty"`
	// BM: dvswitch-bm (dvswitch_stfu.go).
	BM *DVBM `json:"bm,omitempty"`
	// Network: dvswitch-network (dvswitch_networks.go).
	Network *DVNetworkReq `json:"network,omitempty"`
}

// DVIdentity: callsign, 7-digit DMR ID, 2-digit ESSID and the optional NXDN
// ID (radioid.net, separate from the DMR ID; empty leaves the file alone).
type DVIdentity struct {
	Callsign string `json:"callsign"`
	DMRID    string `json:"dmrId"`
	ESSID    string `json:"essid"`
	NXDNID   string `json:"nxdnId,omitempty"`
	// Info is the [Info] section of MMDVM_Bridge.ini, what BrandMeister
	// shows for the hotspot. Nil leaves it alone.
	Info *DVInfo `json:"info,omitempty"`
}

// DVInfo: where the hotspot is and what it says about itself. Location and
// Description are required, URL may be empty (an empty URL is written as
// such), FrequencyHz is one frequency written to both RXFrequency and
// TXFrequency (simplex, DMO), Latitude and Longitude are decimal degrees and
// both set or both empty (empty leaves them alone). The text limits are the
// widths of the DMR login packet (Location 20, Description 19, URL 124).
type DVInfo struct {
	Location    string `json:"location"`
	Description string `json:"description"`
	URL         string `json:"url,omitempty"`
	FrequencyHz int64  `json:"frequencyHz"`
	Latitude    string `json:"latitude,omitempty"`
	Longitude   string `json:"longitude,omitempty"`
}

// DVInfoView is the [Info] section as the setup view reports it.
// FrequencyHz is 0 when RX and TX differ (duplex) or are not numbers; RX and
// TX are then shown separately. Stock is true while the file still holds the
// stock iceberg location or frequencies.
type DVInfoView struct {
	Location    string `json:"location"`
	Description string `json:"description"`
	URL         string `json:"url"`
	FrequencyHz int64  `json:"frequencyHz"`
	RXHz        int64  `json:"rxHz"`
	TXHz        int64  `json:"txHz"`
	Latitude    string `json:"latitude"`
	Longitude   string `json:"longitude"`
	Stock       bool   `json:"stock"`
}

var (
	reDVInfoText = regexp.MustCompile(`^[A-Za-z0-9][\x20-\x7e]*$`)
	reDVInfoURL  = regexp.MustCompile(`^https?://[A-Za-z0-9._~:/?&=%+-]{1,116}$`)
	reDVCoord    = regexp.MustCompile(`^-?[0-9]{1,3}(\.[0-9]{1,6})?$`)
)

// vInfoText: 1..max printable ASCII characters, starting with a letter or
// digit, no trailing blank and none of ; # = " (ini comment, assignment and
// quote characters).
func vInfoText(max int) func(string) bool {
	return func(s string) bool {
		if len(s) < 1 || len(s) > max || !reDVInfoText.MatchString(s) || s != strings.TrimRight(s, " ") {
			return false
		}
		return !strings.ContainsAny(s, ";#=\"")
	}
}

// vInfoURL: http or https, at most 124 characters, no spaces, quotes, ; or #.
func vInfoURL(s string) bool { return s == "" || len(s) <= 124 && reDVInfoURL.MatchString(s) }

// vFreqHz: 30 MHz to 3 GHz, whole hertz. int64: 3 GHz overflows int on
// 32-bit (armhf).
func vFreqHz(s string) bool {
	n, err := strconv.ParseInt(s, 10, 64)
	return err == nil && n >= 30_000_000 && n <= 3_000_000_000 && strconv.FormatInt(n, 10) == s
}

func vCoord(lo, hi float64) func(string) bool {
	return func(s string) bool {
		if !reDVCoord.MatchString(s) {
			return false
		}
		f, err := strconv.ParseFloat(s, 64)
		return err == nil && f >= lo && f <= hi
	}
}

// dvInfoChanges validates an Info block and returns the writes.
func dvInfoChanges(in *DVInfo) ([]dvChange, error) {
	if !vInfoText(20)(in.Location) {
		return nil, dvInvalid("identity/info/location")
	}
	if !vInfoText(19)(in.Description) {
		return nil, dvInvalid("identity/info/description")
	}
	if !vInfoURL(in.URL) {
		return nil, dvInvalid("identity/info/url")
	}
	hz := strconv.FormatInt(in.FrequencyHz, 10)
	if !vFreqHz(hz) {
		return nil, dvInvalid("identity/info/frequencyHz")
	}
	ch := []dvChange{
		{DVFileMMDVMBridge, "Info", "Location", in.Location},
		{DVFileMMDVMBridge, "Info", "Description", in.Description},
		{DVFileMMDVMBridge, "Info", "URL", in.URL},
		{DVFileMMDVMBridge, "Info", "RXFrequency", hz},
		{DVFileMMDVMBridge, "Info", "TXFrequency", hz},
	}
	if in.Latitude != "" || in.Longitude != "" {
		if !vCoord(-90, 90)(in.Latitude) {
			return nil, dvInvalid("identity/info/latitude")
		}
		if !vCoord(-180, 180)(in.Longitude) {
			return nil, dvInvalid("identity/info/longitude")
		}
		ch = append(ch, dvChange{DVFileMMDVMBridge, "Info", "Latitude", in.Latitude}, dvChange{DVFileMMDVMBridge, "Info", "Longitude", in.Longitude})
	}
	return ch, nil
}

// DVCredentials: DMR - Other's master (the save of dvswitch-network). Nil
// fields are left alone. The password is write-only. ClearPassword forgets
// it (the stock value goes to the ini, reported as "not set") for masters
// that need no password; it excludes Password.
type DVCredentials struct {
	Address       *string `json:"address,omitempty"`
	Port          *int    `json:"port,omitempty"`
	Password      *string `json:"password,omitempty"`
	ClearPassword bool    `json:"clearPassword,omitempty"`
}

// DVModes: the YSF, P25 and NXDN enable flags; nil fields are left alone.
// The DMR networks are enabled with dvswitch-network and dvswitch-bm.
type DVModes struct {
	YSF  *bool `json:"ysf,omitempty"`
	P25  *bool `json:"p25,omitempty"`
	NXDN *bool `json:"nxdn,omitempty"`
}

// DVApplyResult is the reply of the write ops. It carries no value.
type DVApplyResult struct {
	// Files that changed on disk; Skipped: absent optional files (gateways).
	Files   []string `json:"files"`
	Skipped []string `json:"skipped"`
	// RestartHint: units that must restart to pick the change up.
	RestartHint []string `json:"restartHint"`
	// Hashes of the changed files after the write.
	Hashes map[string]string `json:"hashes"`
	// BMLogin (BrandMeister save only): "logged_in" when STFU's login was
	// seen after the restart (the check ran before STFU was stopped again,
	// if it was), "pending" when it was not seen in time.
	BMLogin string `json:"bmLogin,omitempty"`
	// STFUStopFailed: STFU should be stopped again and could not be.
	STFUStopFailed bool `json:"stfuStopFailed,omitempty"`
}

type dvChange struct{ file, section, key, value string }

// ---- configuration

func (o *Ops) dvOptDir() string     { return orDefault(o.DVOptDir, DefaultDVOptDir) }
func (o *Ops) dvHistoryDir() string { return orDefault(o.DVHistoryDir, DefaultDVHistoryDir) }
func (o *Ops) dvFilePath(f dvFileDef) string {
	return filepath.Join(o.dvOptDir(), filepath.FromSlash(f.rel))
}

// ---- safe file access

// dvFileState is one ini file as read.
type dvFileState struct {
	def     dvFileDef
	present bool
	regular bool
	data    []byte
	info    fs.FileInfo
}

// dvRead reads one allowlisted file: no symlink (Lstat and O_NOFOLLOW), a
// regular file, at most dvIniMaxSize. A missing file is present == false; a
// symlink or other non-regular file is present && !regular (never written).
func (o *Ops) dvRead(f dvFileDef) (dvFileState, error) {
	st := dvFileState{def: f}
	p := o.dvFilePath(f)
	fi, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, err
	}
	st.present = true
	if !fi.Mode().IsRegular() {
		return st, nil
	}
	fd, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return st, err
	}
	defer fd.Close()
	fi2, err := fd.Stat()
	if err != nil {
		return st, err
	}
	if !fi2.Mode().IsRegular() {
		return st, nil
	}
	data, err := io.ReadAll(io.LimitReader(fd, dvIniMaxSize+1))
	if err != nil {
		return st, err
	}
	if len(data) > dvIniMaxSize {
		return st, fmt.Errorf("%s is too large", f.id)
	}
	st.regular, st.data, st.info = true, data, fi2
	return st, nil
}

// dvWrite replaces the file atomically, keeping owner and mode.
func (o *Ops) dvWrite(st dvFileState, data []byte) error {
	p := o.dvFilePath(st.def)
	parent, err := filepath.EvalSymlinks(filepath.Dir(p))
	if err != nil {
		return err
	}
	uid, gid, ok := ownerIDs(st.info)
	if !ok {
		uid, gid = os.Getuid(), os.Getgid()
	}
	return atomicWrite(filepath.Join(parent, filepath.Base(p)), parent, data, uid, gid, st.info.Mode().Perm())
}

// ---- history

const dvStampLayout = "20060102T150405.000000000Z"

var reDVStamp = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}\.[0-9]{9}Z(-[0-9]+)?$`)

func (o *Ops) dvHistDir(id string) string { return filepath.Join(o.dvHistoryDir(), id) }

// dvHistoryNames returns the valid copy names of a file, oldest first.
func (o *Ops) dvHistoryNames(id string) []string {
	ents, err := os.ReadDir(o.dvHistDir(id))
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range ents {
		if e.Type().IsRegular() && reDVStamp.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// dvSaveHistory stores data as the newest copy of file id and keeps the last
// dvHistoryKeep. It returns the copy's name.
func (o *Ops) dvSaveHistory(id string, data []byte) (string, error) {
	dir := o.dvHistDir(id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	// MkdirAll leaves an existing directory's mode alone: close it.
	_ = os.Chmod(o.dvHistoryDir(), 0o700)
	_ = os.Chmod(dir, 0o700)
	stamp := o.now().UTC().Format(dvStampLayout)
	name := stamp
	for i := 1; ; i++ {
		f, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
		if err == nil {
			_, werr := f.Write(data)
			if cerr := f.Close(); werr == nil {
				werr = cerr
			}
			if werr != nil {
				os.Remove(filepath.Join(dir, name))
				return "", werr
			}
			break
		}
		if !errors.Is(err, fs.ErrExist) {
			return "", err
		}
		name = fmt.Sprintf("%s-%d", stamp, i)
	}
	// A suffix sorts after the plain stamp, which is the right order for
	// same-nanosecond copies.
	names := o.dvHistoryNames(id)
	for len(names) > dvHistoryKeep {
		os.Remove(filepath.Join(dir, names[0]))
		names = names[1:]
	}
	return name, nil
}

// ---- apply engine

func (o *Ops) dvBusy(ctx context.Context) bool {
	in, un := o.dvJobs(ctx)
	if in.Job == JobRunning || un.Job == JobRunning {
		return true
	}
	st, _ := o.jobUnit(ctx, dvSTFUJobUnit)
	return st == JobRunning
}

// dvApply validates changes against the allowlist, checks the base hashes,
// edits the files in memory and writes the changed ones (history copy first).
// Nothing is written unless every change is valid and applies. Files that do
// not exist: the core files (Analog_Bridge, MMDVM_Bridge) make the op fail
// with ErrDVNotInstalled, a gateway file is skipped.
func (o *Ops) dvApply(ctx context.Context, changes []dvChange, base map[string]string) (DVApplyResult, error) {
	return o.dvApplyOpt(ctx, changes, base, dvApplyOpts{})
}

// dvApplyOpts: noHistory writes without a history copy (internal writes the
// user did not make: the boot mode, the install defaults; they stay out of
// the rollback list). noBusy skips the busy check: for the steps that run
// inside the install job itself.
type dvApplyOpts struct{ noHistory, noBusy bool }

func (o *Ops) dvApplyOpt(ctx context.Context, changes []dvChange, base map[string]string, opt dvApplyOpts) (DVApplyResult, error) {
	res := DVApplyResult{Files: []string{}, Skipped: []string{}, RestartHint: []string{}, Hashes: map[string]string{}}
	for _, c := range changes {
		ok := false
		if v, found := dvAllow[dvKey{c.file, c.section, c.key}]; found {
			ok = v(c.value)
		}
		if !ok {
			// never the value
			return res, dvInvalid(c.file + "/" + c.section + "/" + c.key)
		}
	}
	for id := range base {
		if _, ok := dvFileByID(id); !ok {
			return res, dvInvalid("base/" + safeID(id))
		}
	}
	o.applyMu.Lock()
	defer o.applyMu.Unlock()
	if !opt.noBusy && o.dvBusy(ctx) {
		return res, ErrBusy
	}

	type work struct {
		st   dvFileState
		f    *ini.File
		out  []byte
		hist string
	}
	var ws []*work
	var conflicts []string
	for _, def := range dvFiles {
		var mine []dvChange
		for _, c := range changes {
			if c.file == def.id {
				mine = append(mine, c)
			}
		}
		if len(mine) == 0 {
			continue
		}
		st, err := o.dvRead(def)
		if err != nil {
			return res, fmt.Errorf("cannot read %s: %w", def.id, err)
		}
		if !st.present || !st.regular {
			if st.present || def.core {
				if st.present {
					return res, dvInvalid(def.id + " is not a regular file")
				}
				return res, ErrDVNotInstalled
			}
			res.Skipped = append(res.Skipped, def.id)
			continue
		}
		if b := base[def.id]; b != "" && b != sha256hex(st.data) {
			conflicts = append(conflicts, def.id)
			continue
		}
		w := &work{st: st, f: ini.Parse(st.data)}
		for _, c := range mine {
			if def.id == DVFileDVSwitch {
				// case-insensitive file: replace the line as it is spelled
				c.key = w.f.KeyFold(c.section, c.key)
			}
			old, exists := w.f.Get(c.section, c.key)
			if !exists && c.value == "" {
				continue // clearing a key that is not there
			}
			if _, err := w.f.Set(c.section, c.key, c.value); err != nil {
				return res, dvInvalid(c.file + "/" + c.section + "/" + c.key)
			}
			if h := dvHint(def, c); h != "" {
				res.RestartHint = addUnit(res.RestartHint, h)
			}
			if def.id == DVFileMMDVMBridge && c.key == "Enable" && old != c.value {
				res.RestartHint = addUnit(res.RestartHint, "analog_bridge")
			}
		}
		w.out = w.f.Bytes()
		ws = append(ws, w)
	}
	if len(conflicts) > 0 {
		return res, &ConflictError{Files: conflicts}
	}

	var written []*work
	revert := func() {
		for _, w := range written {
			_ = o.dvWrite(w.st, w.st.data)
			if w.hist != "" {
				os.Remove(filepath.Join(o.dvHistDir(w.st.def.id), w.hist))
			}
		}
	}
	for _, w := range ws {
		if string(w.out) == string(w.st.data) {
			continue
		}
		if !opt.noHistory {
			hist, err := o.dvSaveHistory(w.st.def.id, w.st.data)
			if err != nil {
				revert()
				return res, fmt.Errorf("cannot keep a copy of %s: %w", w.st.def.id, err)
			}
			w.hist = hist
		}
		if err := o.dvWrite(w.st, w.out); err != nil {
			if w.hist != "" {
				os.Remove(filepath.Join(o.dvHistDir(w.st.def.id), w.hist))
			}
			revert()
			return res, fmt.Errorf("cannot write %s: %w", w.st.def.id, err)
		}
		written = append(written, w)
		res.Files = append(res.Files, w.st.def.id)
		res.Hashes[w.st.def.id] = sha256hex(w.out)
	}
	if len(res.Files) == 0 {
		res.RestartHint = []string{} // nothing changed: nothing to restart
	}
	sortUnits(res.RestartHint)
	return res, nil
}

func safeID(s string) string {
	if len(s) > 20 {
		s = s[:20]
	}
	return strings.Map(func(r rune) rune {
		if r < 0x21 || r > 0x7e {
			return '?'
		}
		return r
	}, s)
}

func dvHint(def dvFileDef, _ dvChange) string { return def.unit }

func addUnit(list []string, u string) []string {
	for _, x := range list {
		if x == u {
			return list
		}
	}
	return append(list, u)
}

func sortUnits(l []string) {
	rank := func(u string) int {
		for i, x := range dvUnits {
			if x == u {
				return i
			}
		}
		return len(dvUnits)
	}
	sort.SliceStable(l, func(i, j int) bool { return rank(l[i]) < rank(l[j]) })
}

// ---- typed ops

func (o *Ops) dvArgs(req *DVSwitchRequest) (*DVSwitchRequest, error) {
	if req == nil {
		return nil, errors.New("invalid request: missing dvswitch field")
	}
	return req, nil
}

func oneOf(n int, set ...bool) bool {
	c := 0
	for _, s := range set {
		if s {
			c++
		}
	}
	return c == n
}

// DVSwitchIdentity writes callsign, DMR ID and ESSID to every place they live
// (Analog_Bridge gatewayDmrId/repeaterID, MMDVM_Bridge Callsign/Id, the
// gateways' Callsign and YSFGateway Id) in one op.
func (o *Ops) DVSwitchIdentity(ctx context.Context, req *DVSwitchRequest) (DVApplyResult, error) {
	req, err := o.dvArgs(req)
	if err != nil {
		return DVApplyResult{}, err
	}
	if !oneOf(1, req.Identity != nil) || req.Credentials != nil || req.Modes != nil {
		return DVApplyResult{}, errors.New("invalid request: identity only")
	}
	id := req.Identity
	if !reDVCall.MatchString(id.Callsign) {
		return DVApplyResult{}, dvInvalid("identity/callsign")
	}
	if !reDVDigits7.MatchString(id.DMRID) {
		return DVApplyResult{}, dvInvalid("identity/dmrId")
	}
	if !reDVESSID.MatchString(id.ESSID) {
		return DVApplyResult{}, dvInvalid("identity/essid")
	}
	if id.NXDNID != "" && !vNXDNID(id.NXDNID) {
		return DVApplyResult{}, dvInvalid("identity/nxdnId")
	}
	full := id.DMRID + id.ESSID
	ch := []dvChange{
		{DVFileAnalogBridge, "AMBE_AUDIO", "gatewayDmrId", id.DMRID},
		{DVFileAnalogBridge, "AMBE_AUDIO", "repeaterID", full},
		{DVFileMMDVMBridge, "General", "Callsign", id.Callsign},
		{DVFileMMDVMBridge, "General", "Id", full},
		{DVFileYSFGateway, "General", "Callsign", id.Callsign},
		{DVFileYSFGateway, "General", "Id", id.DMRID},
		{DVFileP25Gateway, "General", "Callsign", id.Callsign},
		{DVFileNXDNGateway, "General", "Callsign", id.Callsign},
	}
	if id.NXDNID != "" {
		ch = append(ch, dvChange{DVFileMMDVMBridge, "NXDN", "Id", id.NXDNID})
	}
	ch = append(ch, o.dvBMIdentity(id.DMRID)...)
	if id.Info != nil {
		ic, err := dvInfoChanges(id.Info)
		if err != nil {
			return DVApplyResult{}, err
		}
		ch = append(ch, ic...)
	}
	return o.dvApply(ctx, ch, req.Base)
}

// DVSwitchCredentials saves DMR - Other's master address, port and
// password: dvswitch-network save of DMR (kept for the credentials
// endpoint).
func (o *Ops) DVSwitchCredentials(ctx context.Context, req *DVSwitchRequest) (DVApplyResult, error) {
	req, err := o.dvArgs(req)
	if err != nil {
		return DVApplyResult{}, err
	}
	c := req.Credentials
	if c == nil || req.Identity != nil || req.Modes != nil || req.Network != nil {
		return DVApplyResult{}, errors.New("invalid request: credentials only")
	}
	n, _ := DVNetworkByID(DVModeDMR)
	return o.dvNetSaveOp(ctx, n, &DVNetworkReq{ID: DVModeDMR, Action: DVNetSave, Address: c.Address, Port: c.Port, Password: c.Password, ClearPassword: c.ClearPassword}, req.Base)
}

// DVSwitchModes sets the YSF, P25 and NXDN enable flags, each in the mode
// section and its Network section together.
func (o *Ops) DVSwitchModes(ctx context.Context, req *DVSwitchRequest) (DVApplyResult, error) {
	req, err := o.dvArgs(req)
	if err != nil {
		return DVApplyResult{}, err
	}
	m := req.Modes
	if m == nil || req.Identity != nil || req.Credentials != nil || req.Network != nil {
		return DVApplyResult{}, errors.New("invalid request: modes only")
	}
	var ch []dvChange
	for _, e := range []struct {
		id string
		v  *bool
	}{{"ysf", m.YSF}, {"p25", m.P25}, {"nxdn", m.NXDN}} {
		if e.v == nil {
			continue
		}
		val := "0"
		if *e.v {
			val = "1"
		}
		for _, ms := range dvModeSections {
			if ms.id == e.id {
				ch = append(ch,
					dvChange{DVFileMMDVMBridge, ms.section, "Enable", val},
					dvChange{DVFileMMDVMBridge, ms.section + " Network", "Enable", val})
			}
		}
	}
	if len(ch) == 0 {
		// nothing to write: a harmless no-op, not an error
		return dvNoChange(), nil
	}
	return o.dvApply(ctx, ch, req.Base)
}

// DVSwitchRollback restores the newest history copy of one file and removes
// that copy (the next rollback goes one step further back).
func (o *Ops) DVSwitchRollback(ctx context.Context, req *DVSwitchRequest) (DVApplyResult, error) {
	res := DVApplyResult{Files: []string{}, Skipped: []string{}, RestartHint: []string{}, Hashes: map[string]string{}}
	req, err := o.dvArgs(req)
	if err != nil {
		return res, err
	}
	def, ok := dvFileByID(req.File)
	if !ok {
		return res, dvInvalid("file")
	}
	o.applyMu.Lock()
	defer o.applyMu.Unlock()
	if o.dvBusy(ctx) {
		return res, ErrBusy
	}
	st, err := o.dvRead(def)
	if err != nil {
		return res, fmt.Errorf("cannot read %s: %w", def.id, err)
	}
	if !st.present || !st.regular {
		return res, ErrDVNotInstalled
	}
	if b := req.Base[def.id]; b != "" && b != sha256hex(st.data) {
		return res, &ConflictError{Files: []string{def.id}}
	}
	names := o.dvHistoryNames(def.id)
	if len(names) == 0 {
		return res, ErrDVNoHistory
	}
	newest := filepath.Join(o.dvHistDir(def.id), names[len(names)-1])
	fd, err := os.OpenFile(newest, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return res, fmt.Errorf("cannot read the history copy: %w", err)
	}
	data, err := io.ReadAll(io.LimitReader(fd, dvIniMaxSize+1))
	fd.Close()
	if err != nil || len(data) > dvIniMaxSize {
		return res, errors.New("cannot read the history copy")
	}
	if err := o.dvWrite(st, data); err != nil {
		return res, fmt.Errorf("cannot write %s: %w", def.id, err)
	}
	os.Remove(newest)
	res.Files = []string{def.id}
	res.Hashes[def.id] = sha256hex(data)
	res.RestartHint = []string{def.unit}
	return res, nil
}

// ---- setup view

// DVSwitchSetup is the reply of dvswitch-setup: the editable values, whether
// the password is set, the file hashes. No secret value.
type DVSwitchSetup struct {
	Identity DVIdentityView `json:"identity"`
	USRP     DVUSRPView     `json:"usrp"`
	// Private is the record of what ASLCP changed for the private bridge
	// node (nil: none). ReservedPorts are the DVSwitch ports a USRP port must
	// not use.
	Private       *DVPrivateRecord `json:"private"`
	ReservedPorts []int            `json:"reservedPorts"`
	Modes         DVModesView      `json:"modes"`
	// Gateways: which gateway ini files are there (the YSF, P25 and NXDN
	// cards).
	Gateways DVGateways   `json:"gateways"`
	Files    []DVFileView `json:"files"`
	// Networks are the DMR networks' cards, in registry order
	// (dvswitch_networks.go). No secret value.
	Networks []DVNetworkView `json:"networks"`
	// BrandMeister is the STFU connection: the [STFU] values of
	// DVSwitch.ini and the unit state. No secret value.
	BrandMeister DVBMView `json:"brandmeister"`
	// Warnings: non-blocking hints, e.g. NXDN enabled without an NXDN ID.
	Warnings []string `json:"warnings"`
}

// DVIdentityView: Consistent is false when the places that hold the identity
// disagree (somebody edited a file by hand).
type DVIdentityView struct {
	Callsign   string `json:"callsign"`
	DMRID      string `json:"dmrId"`
	ESSID      string `json:"essid"`
	NXDNID     string `json:"nxdnId"` // empty: not set (also the stock 12345)
	Consistent bool   `json:"consistent"`
	// Info is the [Info] section of MMDVM_Bridge.ini.
	Info DVInfoView `json:"info"`
}

// dvStockInfo: the [Info] values of the stock MMDVM_Bridge.ini that put the
// hotspot on an iceberg in the North Atlantic.
const (
	dvStockInfoRX  = 222340000
	dvStockInfoLoc = "Iceberg, North Atlantic"
)

// DVInfoStockWarning is the setup-view warning for the stock [Info] values.
const DVInfoStockWarning = "MMDVM_Bridge still has the stock location and frequencies (an iceberg, 222.34 MHz); BrandMeister shows them. Set your own under Identity"

func dvInfoView(get func(sec, key string) string) DVInfoView {
	atoi := func(s string) int64 { n, _ := strconv.ParseInt(s, 10, 64); return n }
	v := DVInfoView{Location: get("Info", "Location"), Description: get("Info", "Description"), URL: get("Info", "URL"),
		RXHz: atoi(get("Info", "RXFrequency")), TXHz: atoi(get("Info", "TXFrequency")),
		Latitude: get("Info", "Latitude"), Longitude: get("Info", "Longitude")}
	if v.RXHz == v.TXHz {
		v.FrequencyHz = v.RXHz
	}
	v.Stock = v.RXHz == dvStockInfoRX || v.Location == dvStockInfoLoc
	return v
}

// DVDMRBMWarning is the setup-view warning for DMR - Other on a BrandMeister
// master.
const DVDMRBMWarning = "DMR - Other points at a BrandMeister master. BrandMeister is set up under BrandMeister (STFU); point DMR - Other at another network or clear it"

type DVUSRPView struct {
	Address string `json:"address"`
	RxPort  int    `json:"rxPort"`
	TxPort  int    `json:"txPort"`
}

// DVModesView: the Enable flags of MMDVM_Bridge.ini (DMR is on while any
// MMDVM_Bridge DMR network is enabled; informational).
type DVModesView struct {
	DMR  bool `json:"dmr"`
	YSF  bool `json:"ysf"`
	P25  bool `json:"p25"`
	NXDN bool `json:"nxdn"`
}

type DVGatewayView struct {
	Present bool `json:"present"`
}

type DVGateways struct {
	YSF  DVGatewayView `json:"ysf"`
	P25  DVGatewayView `json:"p25"`
	NXDN DVGatewayView `json:"nxdn"`
}

type DVFileView struct {
	ID       string `json:"id"`
	Present  bool   `json:"present"`
	Writable bool   `json:"writable"`
	SHA256   string `json:"sha256"`
	History  int    `json:"history"`
}

// DVSwitchSetup builds the view. Read-only.
func (o *Ops) DVSwitchSetup(ctx context.Context) (DVSwitchSetup, error) {
	v := DVSwitchSetup{Files: []DVFileView{}}
	parsed := map[string]*ini.File{}
	for _, def := range dvFiles {
		st, err := o.dvRead(def)
		if err != nil {
			return v, fmt.Errorf("cannot read %s: %w", def.id, err)
		}
		fv := DVFileView{ID: def.id, Present: st.present, Writable: st.regular, History: len(o.dvHistoryNames(def.id))}
		if st.regular {
			fv.SHA256 = sha256hex(st.data)
			parsed[def.id] = ini.Parse(st.data)
		}
		v.Files = append(v.Files, fv)
	}
	get := func(file, sec, key string) string {
		if f := parsed[file]; f != nil {
			s, _ := f.Get(sec, key)
			return s
		}
		return ""
	}
	atoi := func(s string) int { n, _ := strconv.Atoi(s); return n }

	// Identity: MMDVM_Bridge is the reference; the rest is compared to it.
	call, id := get(DVFileMMDVMBridge, "General", "Callsign"), get(DVFileMMDVMBridge, "General", "Id")
	v.Identity.Callsign = call
	switch len(id) {
	case 9:
		v.Identity.DMRID, v.Identity.ESSID = id[:7], id[7:]
	default:
		v.Identity.DMRID = id
	}
	v.Identity.Consistent = parsed[DVFileMMDVMBridge] != nil && call != "" && len(id) == 9
	chk := func(file, sec, key, want string) {
		if parsed[file] != nil && get(file, sec, key) != want {
			v.Identity.Consistent = false
		}
	}
	chk(DVFileAnalogBridge, "AMBE_AUDIO", "gatewayDmrId", v.Identity.DMRID)
	chk(DVFileAnalogBridge, "AMBE_AUDIO", "repeaterID", id)
	for _, g := range []string{DVFileYSFGateway, DVFileP25Gateway, DVFileNXDNGateway} {
		chk(g, "General", "Callsign", call)
	}
	chk(DVFileYSFGateway, "General", "Id", v.Identity.DMRID)

	v.USRP = DVUSRPView{Address: get(DVFileAnalogBridge, "USRP", "address"), RxPort: atoi(get(DVFileAnalogBridge, "USRP", "rxPort")), TxPort: atoi(get(DVFileAnalogBridge, "USRP", "txPort"))}

	v.Private, _ = ReadDVPrivate(o.dvPrivatePath())
	v.ReservedPorts = dvReservedPorts(func(sec, key string) int { return atoi(get(DVFileAnalogBridge, sec, key)) })

	en := func(sec string) bool {
		return get(DVFileMMDVMBridge, sec, "Enable") == "1" && get(DVFileMMDVMBridge, sec+" Network", "Enable") == "1"
	}
	v.Modes = DVModesView{DMR: en("DMR"), YSF: en("System Fusion"), P25: en("P25"), NXDN: en("NXDN")}
	loaded, unitEnabled := o.dvBMUnit(ctx)
	v.BrandMeister = dvBMView(parsed[DVFileDVSwitch], loaded, unitEnabled, o.dvSTFUStay())
	v.BrandMeister.Enabled = o.dvBMEnabled()
	v.Networks = o.dvNetworkViews(v.BrandMeister)

	gw := func(file string) DVGatewayView { return DVGatewayView{Present: parsed[file] != nil} }
	v.Gateways = DVGateways{YSF: gw(DVFileYSFGateway), P25: gw(DVFileP25Gateway), NXDN: gw(DVFileNXDNGateway)}
	v.Identity.NXDNID = dvNXDNIDView(get(DVFileMMDVMBridge, "NXDN", "Id"))
	v.Identity.Info = dvInfoView(func(sec, key string) string { return get(DVFileMMDVMBridge, sec, key) })
	v.Warnings = dvWarnings(v.Modes, v.Identity)
	for _, n := range v.Networks {
		if n.PointsAtBM && n.Enabled {
			v.Warnings = append(v.Warnings, DVDMRBMWarning)
		}
	}
	return v, nil
}

// dvNXDNIDView reports the stock value and anything invalid as "not set".
func dvNXDNIDView(id string) string {
	if id == dvStockNXDNID || !vNXDNID(id) {
		return ""
	}
	return id
}

// DVNXDNWarning is the setup-view warning for NXDN enabled without an ID.
const DVNXDNWarning = "NXDN needs your NXDN ID from radioid.net"

func dvWarnings(m DVModesView, id DVIdentityView) []string {
	w := []string{}
	if m.NXDN && id.NXDNID == "" {
		w = append(w, DVNXDNWarning)
	}
	if id.Info.Stock {
		w = append(w, DVInfoStockWarning)
	}
	return w
}

var _ = time.Second

// dvTLVPorts are the stock TLV ports of the mode gateways (DMR, YSF, P25,
// NXDN: the in and out port each).
var dvTLVPorts = []int{31100, 31103, 35100, 35103, 34100, 34103, 33100, 33103}

// dvReservedPorts returns the ports a USRP port must avoid: the stock TLV
// ports, STFU's stock TLV ports and the ones Analog_Bridge itself uses
// ([AMBE_AUDIO]).
func dvReservedPorts(get func(sec, key string) int) []int {
	seen := map[int]bool{}
	var out []int
	add := func(p int) {
		if p > 0 && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, p := range dvTLVPorts {
		add(p)
	}
	add(dvSTFUTx)
	add(dvSTFURx)
	for _, k := range []string{"txPort", "rxPort", "ambePort"} {
		add(get("AMBE_AUDIO", k))
	}
	sort.Ints(out)
	return out
}
