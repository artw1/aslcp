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
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/artw1/aslcp/internal/dvpanel"
	"github.com/artw1/aslcp/internal/legacy"
)

// Code shared by the digital-modes ops (dv.go, dv_config.go) and the backup
// part (backup_dv*.go): the install marker, the platform check, the state
// directory paths, the state files (networks.json, privatenode.json) and the
// history of the legacy ini files a backup restore replaces.

const (
	dvEnableUnit = "aslcp-dv-enable"

	// DefaultDVStateDir is root-owned (0700) and, like the config history,
	// deliberately not below /var/lib/aslcp (the unprivileged user's), and
	// not /var/lib/aslcp-dv either (the engine's own StateDirectory). 1.x
	// installs keep it under another name; MigrateDVState renames it.
	DefaultDVStateDir   = "/var/lib/aslcp-dvstate"
	DefaultDVEngineDir  = "/var/lib/aslcp-dv"
	DefaultDVEngineUser = "aslcp-dv"
	// DefaultDVHostListsDir is where the panel keeps the downloaded host lists.
	DefaultDVHostListsDir = "/var/lib/aslcp/hostlists"

	dvMarkerFile = "install.json"    // the ASLCP-installed marker
	dvSimFile    = "install-sim.txt" // simulated install, input of install-plan
	dvMinYear    = 2026              // clock check (apt rejects a Release file from the future)
	dvMaxNames   = 400

	dvNetworksFile  = "networks.json"
	dvNetworksMax   = 64 << 10
	dvPrivateFile   = "privatenode.json"
	dvPrivateMax    = 64 << 10
	dvUSRPAddress   = "127.0.0.1"
	dvDefaultRxPort = 34001
	dvDefaultTxPort = 32001
)

var (
	reDVNodeNum = regexp.MustCompile(`^[0-9]{1,7}$`)
	reDVHost    = regexp.MustCompile(`^[A-Za-z0-9.-]{1,253}$`)
	dvNameRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9+.\-]{0,99}$`)
)

// MigrateDVState is the one-time move of the state directory of a 1.x install
// to its current name: when the old directory exists and the new one does not,
// it is renamed (same filesystem, so atomic). It holds the bridge node record
// and the DMR network settings of 1.4.0, which the panel keeps using. Nothing
// is merged and nothing is overwritten; an old directory that stays beside an
// existing new one is left alone. It returns the renames it did. The ini
// history of the previous stack is not moved (PurgeLegacy deletes it).
func (o *Ops) MigrateDVState() ([]string, error) {
	oldDir, newDir := orDefault(o.DVOldStateDir, legacy.OldStateDir), o.dvStateDir()
	if oldDir == newDir {
		return nil, nil
	}
	if fi, err := os.Lstat(oldDir); err != nil || !fi.IsDir() {
		return nil, nil
	}
	if _, err := os.Lstat(newDir); err == nil {
		return nil, nil
	}
	if err := os.Rename(oldDir, newDir); err != nil {
		return nil, fmt.Errorf("move %s to %s: %w", oldDir, newDir, err)
	}
	return []string{oldDir + " -> " + newDir}, nil
}

// ---- paths and clock

func (o *Ops) dvKeyring() string      { return orDefault(o.DVKeyring, DefaultDVKeyring) }
func (o *Ops) dvSourceList() string   { return orDefault(o.DVSourceList, DefaultDVSourceList) }
func (o *Ops) dvStateDir() string     { return orDefault(o.DVStateDir, DefaultDVStateDir) }
func (o *Ops) dvEngineDir() string    { return orDefault(o.DVEngineDir, DefaultDVEngineDir) }
func (o *Ops) dvHostListsDir() string { return orDefault(o.DVHostListsDir, DefaultDVHostListsDir) }
func (o *Ops) dvEngineUser() string   { return orDefault(o.DVEngineUser, DefaultDVEngineUser) }
func (o *Ops) dvMarkerPath() string   { return filepath.Join(o.dvStateDir(), dvMarkerFile) }
func (o *Ops) dvSimPath() string      { return filepath.Join(o.dvStateDir(), dvSimFile) }
func (o *Ops) dvPrivatePath() string {
	return filepath.Join(o.dvStateDir(), dvPrivateFile)
}
func (o *Ops) dvNetworksPath() string { return filepath.Join(o.dvStateDir(), dvNetworksFile) }
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

// DVReasonArmhf is the DVStatus reason on a 32-bit (armhf) system; the web UI
// shows a message for it (reinstall with a 64-bit OS).
const DVReasonArmhf = "armhf_unsupported"

// DVReasonText is the reason as a sentence for the command line and logs.
func DVReasonText(reason string) string {
	if reason == DVReasonArmhf {
		return "32-bit (armhf) systems are not supported: ASL3 has no armhf packages; reinstall with Raspberry Pi OS Lite (64-bit)"
	}
	return reason
}

// DVPlatform decides whether digital modes can be enabled: host codename
// bookworm or trixie, architecture amd64 or arm64. armhf (32-bit) is refused
// with the reason DVReasonArmhf: ASL3 ships no armhf packages.
func DVPlatform(codename, arch string) (bool, string) {
	if codename != "bookworm" && codename != "trixie" {
		return false, fmt.Sprintf("Debian %q is not supported (bookworm and trixie are)", codename)
	}
	switch arch {
	case "amd64", "arm64":
		return true, ""
	case "armhf":
		return false, DVReasonArmhf
	}
	return false, fmt.Sprintf("architecture %q is not supported (amd64 and arm64 are)", arch)
}

// ---- install marker

// DVMarker is the root-owned record of an ASLCP install of the vocoder
// packages; its existence is what makes the install "ASLCP-installed".
type DVMarker struct {
	Version     int    `json:"version"`
	InstalledAt string `json:"installedAt"`
	// WgetPreinstalled is kept for markers written by earlier versions.
	WgetPreinstalled bool `json:"wgetPreinstalled"`
	// Packages are the packages the simulation showed as newly installed
	// (Inst lines without a current version): the set members that were
	// missing and their dependencies.
	Packages []string `json:"packages"`
}

// ReadDVMarker reads the marker; (nil, nil) when there is none. A marker that
// cannot be parsed or holds an invalid package name is an error (it is never
// half-trusted).
func ReadDVMarker(path string) (*DVMarker, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m DVMarker
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("install marker: %w", err)
	}
	if m.Version != 1 || len(m.Packages) > dvMaxNames {
		return nil, errors.New("install marker: unsupported content")
	}
	for _, p := range m.Packages {
		if !dvNameRe.MatchString(p) {
			return nil, errors.New("install marker: invalid package name")
		}
	}
	return &m, nil
}

// WriteDVMarker writes the marker atomically, root-only (0600) in a 0700
// directory.
func WriteDVMarker(path string, m DVMarker) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	sort.Strings(m.Packages)
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return writeStateFile(path, append(data, '\n'), 0o600)
}

// MergeDVMarker builds the marker of an install run. newPkgs are the packages
// this run's simulation shows as newly installed; an earlier marker (a
// repeated or resumed install) keeps its packages and its install time.
func MergeDVMarker(prev *DVMarker, newPkgs []string, now time.Time) DVMarker {
	m := DVMarker{Version: 1, InstalledAt: now.UTC().Format(time.RFC3339)}
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
	for p := range set {
		m.Packages = append(m.Packages, p)
	}
	sort.Strings(m.Packages)
	if m.Packages == nil {
		m.Packages = []string{}
	}
	return m
}

func writeStateFile(path string, data []byte, mode os.FileMode) error {
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
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
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

// ---- units

// DVUnit is the state of one unit.
type DVUnit struct {
	Name    string `json:"name"`
	Loaded  bool   `json:"loaded"`
	Active  string `json:"active"`
	Enabled string `json:"enabled"`
}

// dvUnitStates reads the state of units with one systemctl call.
func (o *Ops) dvUnitStates(ctx context.Context, units []string) []DVUnit {
	res := make([]DVUnit, 0, len(units))
	argv := []string{"systemctl", "show", "-p", "Id,LoadState,ActiveState,UnitFileState"}
	for _, u := range units {
		argv = append(argv, u+".service")
	}
	out, _, err := o.Run.Run(ctx, argv...)
	byName := map[string]DVUnit{}
	if err == nil {
		for _, block := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n\n") {
			p := ParseShow(block)
			id := strings.TrimSuffix(p["Id"], ".service")
			if id == "" {
				continue
			}
			byName[id] = DVUnit{Name: id, Loaded: p["LoadState"] == "loaded", Active: p["ActiveState"], Enabled: p["UnitFileState"]}
		}
	}
	for _, u := range units {
		if st, ok := byName[u]; ok {
			res = append(res, st)
		} else {
			res = append(res, DVUnit{Name: u, Active: "unknown"})
		}
	}
	return res
}

// dpkgInstalled returns name -> version for the installed ones among names.
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

// dvBusy reports whether the enable job (or any apt job) is running: the
// backup restore refuses to touch the part then.
func (o *Ops) dvBusy(ctx context.Context) bool {
	st, _ := o.jobUnit(ctx, dvEnableUnit)
	return st == JobRunning
}

// ---- DMR network state (networks.json)

// dvNetState is one network's settings. The password is stored as typed;
// it is never logged and never leaves the helper (dv-network reports only
// whether one is set).
type dvNetState struct {
	Enabled  bool              `json:"enabled"`
	Address  string            `json:"address,omitempty"`
	Port     int               `json:"port,omitempty"`
	Password string            `json:"password,omitempty"`
	Options  map[string]string `json:"options,omitempty"`
}

// dvIdentity is the node's DMR identity (rendered into the engine config).
type dvIdentity struct {
	Callsign   string `json:"callsign,omitempty"`
	DMRID      uint32 `json:"dmrId,omitempty"`
	RepeaterID uint32 `json:"repeaterId,omitempty"`
}

// dvNetworksState is networks.json: every network's settings and enabled
// state, and the identity. The file is carried in a backup and read by
// dv-config. A 1.4.0 file (enabled, address, port, password per network, and
// the unused stfuStay) reads as it is: the new fields are optional.
type dvNetworksState struct {
	Version  int                   `json:"version"`
	STFUStay bool                  `json:"stfuStay,omitempty"` // 1.x, kept so the file round-trips
	Identity *dvIdentity           `json:"identity,omitempty"`
	Networks map[string]dvNetState `json:"networks"`
}

// dvNetMu serialises the read-modify-write of networks.json.
var dvNetMu sync.Mutex

func dvEmptyNetworks() dvNetworksState {
	return dvNetworksState{Version: 1, Networks: map[string]dvNetState{}}
}

// parseDVNetworks reads networks.json strictly: version 1, valid values.
// Networks this version does not know are dropped (a newer backup).
func parseDVNetworks(data []byte) (dvNetworksState, error) {
	bad := errors.New("the network settings are unreadable")
	if len(data) > dvNetworksMax {
		return dvNetworksState{}, bad
	}
	var st dvNetworksState
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&st); err != nil || st.Version != 1 {
		return dvNetworksState{}, bad
	}
	out := dvEmptyNetworks()
	out.STFUStay = st.STFUStay
	if id := st.Identity; id != nil {
		v, err := dvpanel.ValidateIdentity(dvpanel.IdentityState{Callsign: id.Callsign, DMRID: id.DMRID, RepeaterID: id.RepeaterID})
		if err != nil {
			return dvNetworksState{}, bad
		}
		if v.Callsign != "" || v.DMRID != 0 || v.RepeaterID != 0 {
			out.Identity = &dvIdentity{Callsign: v.Callsign, DMRID: v.DMRID, RepeaterID: v.RepeaterID}
		}
	}
	for id, s := range st.Networks {
		sp, ok := dvpanel.NetSpecFor(id)
		if !ok {
			// a network this version does not know, and LOOP: the echo test
			// has no settings and is always on, but an earlier version could
			// store an entry for it (its Enable switch); dropped silently
			continue
		}
		if !sp.Port {
			// a port stored while the network had a port setting (BrandMeister
			// before 2.0) is dropped: the engine uses its default
			s.Port = 0
		}
		if !sp.Address && dvpanel.RetiredAddress[id] {
			// YSF and P25 stored a reflector address and port before they
			// became enable-only: dropped, the reflector is tuned instead
			s.Address = ""
		}
		if s.Address != "" && (!sp.Address || !dvpanel.ValidAddress(s.Address)) ||
			s.Port != 0 && !dvpanel.ValidNetPort(s.Port) ||
			s.Password != "" && (!sp.Password || !dvpanel.ValidPassword(s.Password)) {
			return dvNetworksState{}, bad
		}
		if len(s.Options) > 0 {
			// options a mode no longer has (P25 unit ID) are dropped
			for k := range dvpanel.RetiredOptions[id] {
				delete(s.Options, k)
			}
			o, err := dvpanel.CleanOptions(id, s.Options)
			if err != nil {
				return dvNetworksState{}, bad
			}
			s.Options = o
		} else {
			s.Options = nil
		}
		out.Networks[id] = s
	}
	return out, nil
}

func encodeDVNetworks(st dvNetworksState) ([]byte, error) {
	st.Version = 1
	if st.Networks == nil {
		st.Networks = map[string]dvNetState{}
	}
	return json.MarshalIndent(st, "", "  ")
}

// dvNetLoad reads networks.json; a missing or unreadable file is the empty
// state.
func (o *Ops) dvNetLoad() dvNetworksState {
	fd, err := os.OpenFile(o.dvNetworksPath(), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return dvEmptyNetworks()
	}
	data, rerr := io.ReadAll(io.LimitReader(fd, dvNetworksMax+1))
	fd.Close()
	if rerr == nil {
		if st, perr := parseDVNetworks(data); perr == nil {
			return st
		}
	}
	return dvEmptyNetworks()
}
