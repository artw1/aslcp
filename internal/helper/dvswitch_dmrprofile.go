package helper

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// DMR profiles: TGIF and DMR - Other (docs/DVSWITCH.md, "TGIF"). Both are
// MMDVM_Bridge's own DMR network, so they share [DMR Network] of
// MMDVM_Bridge.ini and only one of them is on at a time. The ini holds the
// active profile; the other one is kept in a root-only state file of the
// helper (dmr-profiles.json, 0600) and written back to the ini when it is
// enabled, so switching never loses an address or a password. The file is
// helper state, not configuration: it is not in a backup (like the history
// copies, it holds a password); the active profile is in MMDVM_Bridge.ini,
// which is.

// DMR profile ids.
const (
	DVProfileOther = "other"
	DVProfileTGIF  = "tgif"
)

const (
	dvDMRProfilesFile = "dmr-profiles.json"
	// DVStateDMRProfiles is the id the write ops report in DVApplyResult.Files
	// when only the stored (inactive) profile changed.
	DVStateDMRProfiles = "dmr_profiles"
	// DefaultDVTGIFHost and DefaultDVTGIFPort: TGIF's public master, used
	// when DMR_Hosts.txt has no TGIF row.
	DefaultDVTGIFHost = "tgif.network"
	DefaultDVTGIFPort = 62031
	dvTGIFName        = "TGIF_Network"
)

// dvDMRProfile is one stored profile. TGIF only stores a password: its
// master comes from DMR_Hosts.txt.
type dvDMRProfile struct {
	Address  string `json:"address,omitempty"`
	Port     int    `json:"port,omitempty"`
	Password string `json:"password,omitempty"`
}

type dvDMRProfiles struct {
	Active string       `json:"active"`
	TGIF   dvDMRProfile `json:"tgif"`
	Other  dvDMRProfile `json:"other"`
}

// dvProfileMu serialises the read-modify-write of the profile file.
var dvProfileMu sync.Mutex

func (o *Ops) dvDMRProfilesPath() string { return filepath.Join(o.dvStateDir(), dvDMRProfilesFile) }

// dvDMRProfiles reads the state; a missing or unreadable file is DMR - Other
// active with nothing stored.
func (o *Ops) dvDMRProfiles() dvDMRProfiles {
	p := dvDMRProfiles{Active: DVProfileOther}
	data, err := os.ReadFile(o.dvDMRProfilesPath())
	if err != nil || len(data) > 64<<10 {
		return p
	}
	var in dvDMRProfiles
	if json.Unmarshal(data, &in) != nil {
		return p
	}
	if in.Active != DVProfileTGIF {
		in.Active = DVProfileOther
	}
	return in
}

func (o *Ops) dvDMRProfilesSave(p dvDMRProfiles) error {
	if err := os.MkdirAll(o.dvStateDir(), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return writeStateFile(o.dvDMRProfilesPath(), data, 0o600)
}

// DVTGIFView is the TGIF card. Address and Port come from DMR_Hosts.txt (the
// row named TGIF_Network, else the first row whose name starts with TGIF);
// Source is "hosts" then, or "default" (tgif.network:62031) when the list has
// no such row. Name is the row's name. The password only as PasswordSet.
type DVTGIFView struct {
	Address     string `json:"address"`
	Port        int    `json:"port"`
	Source      string `json:"source"`
	Name        string `json:"name,omitempty"`
	PasswordSet bool   `json:"passwordSet"`
	Enabled     bool   `json:"enabled"`
}

// DVTGIFHost picks TGIF's master from a DMR_Hosts.txt: the row named
// TGIF_Network (any case), else the first row whose name starts with TGIF.
// Comment lines never match (ParseDMRHosts skips them).
func DVTGIFHost(data []byte) (DVListItem, bool) {
	var first *DVListItem
	for _, it := range ParseDMRHosts(data) {
		if strings.EqualFold(it.Name, dvTGIFName) {
			return it, true
		}
		if first == nil && len(it.Name) >= 4 && strings.EqualFold(it.Name[:4], "TGIF") {
			c := it
			first = &c
		}
	}
	if first != nil {
		return *first, true
	}
	return DVListItem{}, false
}

// dvTGIFMaster is TGIF's master: from DMR_Hosts.txt, else the default.
func (o *Ops) dvTGIFMaster() (addr string, port int, source, name string) {
	if data, err := o.dvReadList("dmr"); err == nil {
		if it, ok := DVTGIFHost(data); ok && vHostname(it.Address) {
			return it.Address, it.Port, "hosts", it.Name
		}
	}
	return DefaultDVTGIFHost, DefaultDVTGIFPort, "default", ""
}

func (o *Ops) dvTGIFView(pw string) DVTGIFView {
	addr, port, src, name := o.dvTGIFMaster()
	return DVTGIFView{Address: addr, Port: port, Source: src, Name: name, PasswordSet: dvPasswordSet(pw)}
}

// dvDMRNetwork reads [DMR Network] of MMDVM_Bridge.ini as a profile.
func (o *Ops) dvDMRNetwork() (dvDMRProfile, error) {
	f, err := o.dvBMRead(false)
	if err != nil {
		return dvDMRProfile{}, err
	}
	get := func(k string) string { v, _ := f.mm.Get("DMR Network", k); return v }
	port, _ := strconv.Atoi(get("Port"))
	return dvDMRProfile{Address: get("Address"), Port: port, Password: get("Password")}, nil
}

// dvProfileChanges are the [DMR Network] writes that make p the live one.
// Empty fields of a stored profile leave the ini value alone, except the
// password: no saved password is the stock one.
func dvProfileChanges(p dvDMRProfile) []dvChange {
	var ch []dvChange
	add := func(k, v string) { ch = append(ch, dvChange{DVFileMMDVMBridge, "DMR Network", k, v}) }
	if p.Address != "" {
		add("Address", p.Address)
	}
	if p.Port != 0 {
		add("Port", strconv.Itoa(p.Port))
	}
	pw := p.Password
	if pw == "" {
		pw = dvStockPassword
	}
	add("Password", pw)
	return ch
}

// dvCredentialsStored updates the profile that is not active: the state file
// only, the ini is not touched. Values are checked like the ini writes.
func (o *Ops) dvCredentialsStored(c *DVCredentials, profile string) (DVApplyResult, error) {
	p := o.dvDMRProfiles()
	slot := &p.Other
	if profile == DVProfileTGIF {
		slot = &p.TGIF
	}
	before := *slot
	if c.Address != nil {
		if !vMatch(reDVHost)(*c.Address) {
			return DVApplyResult{}, dvInvalid("DMR Network/Address")
		}
		if o.dvIsBMHost(*c.Address) {
			return DVApplyResult{}, ErrDVBMMaster
		}
		slot.Address = *c.Address
	}
	if c.Port != nil {
		if *c.Port < 1 || *c.Port > 65535 {
			return DVApplyResult{}, dvInvalid("DMR Network/Port")
		}
		slot.Port = *c.Port
	}
	if c.Password != nil {
		if !vPassword(*c.Password) {
			return DVApplyResult{}, dvInvalid("DMR Network/Password")
		}
		slot.Password = *c.Password
	}
	if c.ClearPassword {
		slot.Password = ""
	}
	if *slot == before {
		return dvNoChange(), nil
	}
	if err := o.dvDMRProfilesSave(p); err != nil {
		return DVApplyResult{}, errors.New("cannot store the DMR profile")
	}
	res := dvNoChange()
	res.Files = []string{DVStateDMRProfiles}
	return res, nil
}

// dvSwitchProfile makes profile the live one: the ini's [DMR Network] is
// stored as the profile it held, the new profile is written to the ini with
// enable (the two Enable flags of DMR, from DVSwitchModes) and the state
// records the new active profile. A failure after the ini write puts the
// ini back.
func (o *Ops) dvSwitchProfile(ctx context.Context, profile string, enable []dvChange, base map[string]string) (DVApplyResult, error) {
	p := o.dvDMRProfiles()
	live, err := o.dvDMRNetwork()
	if err != nil {
		return DVApplyResult{}, err
	}
	var next dvDMRProfile
	if p.Active == DVProfileTGIF {
		// TGIF's address is not stored: it comes from the host list
		p.TGIF = dvDMRProfile{Password: live.Password}
	} else {
		p.Other = live
	}
	if profile == DVProfileTGIF {
		addr, port, _, _ := o.dvTGIFMaster()
		next = dvDMRProfile{Address: addr, Port: port, Password: p.TGIF.Password}
	} else {
		next = p.Other
	}
	if p.TGIF.Password == dvStockPassword {
		p.TGIF.Password = ""
	}
	// Store the snapshot first: if anything below fails, the state still
	// matches the ini (same active profile, its values stored too).
	if err := o.dvDMRProfilesSave(p); err != nil {
		return DVApplyResult{}, errors.New("cannot store the DMR profile")
	}
	res, err := o.dvApply(ctx, append(dvProfileChanges(next), enable...), base)
	if err != nil {
		return res, err
	}
	p.Active = profile
	if err := o.dvDMRProfilesSave(p); err != nil {
		o.dvBMUndo(ctx, res.Files)
		return DVApplyResult{}, errors.New("cannot store the DMR profile")
	}
	res.DMRProfileChanged = true
	res.RestartHint = addUnit(res.RestartHint, "mmdvm_bridge")
	sortUnits(res.RestartHint)
	if len(res.Files) == 0 {
		res.Files = []string{DVStateDMRProfiles}
	}
	return res, nil
}

// dvDMRProfilesReset forgets both stored profiles (a fresh install).
func (o *Ops) dvDMRProfilesReset() error {
	if err := os.Remove(o.dvDMRProfilesPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
