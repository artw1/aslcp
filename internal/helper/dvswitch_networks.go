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
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/artw1/aslcp/internal/dvswitch/ini"
)

// DMR networks (docs/DVSWITCH.md, "DMR networks"). Each DMR network is its
// own panel mode with its own card, settings and enabled state: BrandMeister
// through STFU, and the networks MMDVM_Bridge logs in to itself (TGIF, DMR -
// Other). The MMDVM_Bridge networks share [DMR Network] of
// MMDVM_Bridge.ini, which holds the one the panel last switched to; the
// settings of every network live in the helper's state file networks.json
// (root 0600), which is part of a backup.
//
// A new network is one more entry in DVNetworks (and in web/src/lib/
// dvnetworks.ts, which mirrors it) plus its tests.

// DVModeTGIF is the panel mode of TGIF (DVNetworks).
const DVModeTGIF = "TGIF"

// How a network's master is found (DVNetwork.Master).
const (
	// DVMasterFixed: read-only, from DMR_Hosts.txt (the row named HostName,
	// else the first row starting with HostPrefix), else DefaultHost.
	DVMasterFixed = "fixed"
	// DVMasterUser: typed in on the card (address, port), with a picker of
	// the DMR_Hosts.txt rows no other network claims.
	DVMasterUser = "user"
)

// DVNetwork is one DMR network of the registry.
type DVNetwork struct {
	ID    string `json:"id"`    // the panel mode and the resume key
	Label string `json:"label"` // what the card and the panel call it
	Via   string `json:"via"`   // DVViaSTFU or DVViaMMDVM
	// Master: DVMasterFixed, DVMasterUser, or "" (BrandMeister: its own card
	// and op, dvswitch-bm).
	Master string `json:"master,omitempty"`
	// HostName and HostPrefix name the DMR_Hosts.txt rows that belong to
	// this network (any case); Domains are host name suffixes that do.
	HostName   string   `json:"-"`
	HostPrefix string   `json:"-"`
	Domains    []string `json:"-"`
	// DefaultHost and DefaultPort: the master when DMR_Hosts.txt has no row.
	DefaultHost string `json:"-"`
	DefaultPort int    `json:"-"`
	// Fields are the settings the card saves: "address", "port", "password".
	Fields []string `json:"fields"`
	// CatchAll: the network of any master no other entry claims (its picker
	// leaves out the rows the others claim).
	CatchAll bool `json:"-"`
}

// DVNetworks is the registry, in display order.
var DVNetworks = []DVNetwork{
	{ID: DVModeBM, Label: "BrandMeister", Via: DVViaSTFU, HostPrefix: "BM_", Domains: dvBMDomains, Fields: []string{}},
	{ID: DVModeTGIF, Label: "TGIF", Via: DVViaMMDVM, Master: DVMasterFixed, HostName: "TGIF_Network", HostPrefix: "TGIF",
		DefaultHost: DefaultDVTGIFHost, DefaultPort: DefaultDVTGIFPort, Fields: []string{"password"}},
	{ID: DVModeDMR, Label: "DMR - Other", Via: DVViaMMDVM, Master: DVMasterUser, Fields: []string{"address", "port", "password"}, CatchAll: true},
}

const (
	// DefaultDVTGIFHost and DefaultDVTGIFPort: TGIF's public master, used
	// when DMR_Hosts.txt has no TGIF row.
	DefaultDVTGIFHost = "tgif.network"
	DefaultDVTGIFPort = 62031
)

// Errors of the network op (mapped back by the client, client.go).
var (
	ErrDVNetMaster        = errors.New("net_master: this master belongs to another DMR network; set it up on that network's card")
	ErrDVNetNotConfigured = errors.New("net_not_configured: save this DMR network's settings first")
	ErrDVNetFailed        = errors.New("net_failed: the DMR network could not be switched")
)

func dvNetworkIDs() []string {
	out := make([]string, 0, len(DVNetworks))
	for _, n := range DVNetworks {
		out = append(out, n.ID)
	}
	return out
}

// DVNetworkByID finds a registry entry.
func DVNetworkByID(id string) (DVNetwork, bool) {
	for _, n := range DVNetworks {
		if n.ID == id {
			return n, true
		}
	}
	return DVNetwork{}, false
}

// DVNetworkLabel is the label of a panel mode: the registry's for a DMR
// network, else the mode itself.
func DVNetworkLabel(mode string) string {
	if n, ok := DVNetworkByID(mode); ok {
		return n.Label
	}
	return mode
}

func (n DVNetwork) has(field string) bool {
	for _, f := range n.Fields {
		if f == field {
			return true
		}
	}
	return false
}

// ClaimsRow: the DMR_Hosts.txt row belongs to this network.
func (n DVNetwork) ClaimsRow(it DVListItem) bool {
	if n.HostName != "" && strings.EqualFold(it.Name, n.HostName) {
		return true
	}
	p := n.HostPrefix
	return p != "" && len(it.Name) >= len(p) && strings.EqualFold(it.Name[:len(p)], p)
}

// claimsName: the host belongs to this network by name (a domain or the
// default master).
func (n DVNetwork) claimsName(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	if h == "" {
		return false
	}
	if n.DefaultHost != "" && h == strings.ToLower(n.DefaultHost) {
		return true
	}
	for _, d := range n.Domains {
		if h == d || strings.HasSuffix(h, "."+d) {
			return true
		}
	}
	return false
}

// DVNetworkClaim is the id of the network (not a catch-all) a master host
// belongs to, from its name or the DMR_Hosts.txt rows; "" when none claims
// it. rows may be nil (by name only).
func DVNetworkClaim(host string, rows []DVListItem) string {
	if host == "" {
		return ""
	}
	for _, n := range DVNetworks {
		if !n.CatchAll && n.claimsName(host) {
			return n.ID
		}
	}
	for _, n := range DVNetworks {
		if n.CatchAll {
			continue
		}
		for _, it := range rows {
			if n.ClaimsRow(it) && strings.EqualFold(it.Address, host) {
				return n.ID
			}
		}
	}
	return ""
}

// DVNetworkHost picks a fixed network's master from a DMR_Hosts.txt: the
// row named HostName, else the first row starting with HostPrefix. Comment
// lines never match (ParseDMRHosts skips them).
func DVNetworkHost(n DVNetwork, data []byte) (DVListItem, bool) {
	var first *DVListItem
	for _, it := range ParseDMRHosts(data) {
		if n.HostName != "" && strings.EqualFold(it.Name, n.HostName) {
			return it, true
		}
		if first == nil && n.HostPrefix != "" && n.ClaimsRow(it) {
			c := it
			first = &c
		}
	}
	if first != nil {
		return *first, true
	}
	return DVListItem{}, false
}

// dvNetClaim is DVNetworkClaim with this node's DMR_Hosts.txt.
func (o *Ops) dvNetClaim(host string) string {
	if host == "" {
		return ""
	}
	if id := DVNetworkClaim(host, nil); id != "" {
		return id
	}
	data, err := o.dvReadList("dmr")
	if err != nil {
		return ""
	}
	return DVNetworkClaim(host, ParseDMRHosts(data))
}

// dvIsBMHost reports whether host is a BrandMeister server: by name, or the
// address of a BM_ entry of DMR_Hosts.txt.
func (o *Ops) dvIsBMHost(host string) bool { return o.dvNetClaim(host) == DVModeBM }

// dvNetMaster is a fixed network's master: from DMR_Hosts.txt, else the
// default. source is "hosts" or "default", name the row's name.
func (o *Ops) dvNetMaster(n DVNetwork) (addr string, port int, source, name string) {
	if data, err := o.dvReadList("dmr"); err == nil {
		if it, ok := DVNetworkHost(n, data); ok && vHostname(it.Address) {
			p := it.Port
			if p == 0 {
				p = n.DefaultPort
			}
			return it.Address, p, "hosts", it.Name
		}
	}
	return n.DefaultHost, n.DefaultPort, "default", ""
}

// ---- state (networks.json)

const (
	dvNetworksFile = "networks.json"
	dvNetworksMax  = 64 << 10
	// DVStateNetworks is the id the write ops report in DVApplyResult.Files
	// when only the helper's network state changed (no ini file).
	DVStateNetworks = "networks"
	// legacy state files (1.3): read once to seed networks.json
	dvSTFUStayFile  = "stfu-stay"
	dvBMEnabledFile = "bm-enabled"
)

// dvNetState is one network's settings. The password is stored as typed
// ("" is none: the stock value goes to the ini).
type dvNetState struct {
	Enabled  bool   `json:"enabled"`
	Address  string `json:"address,omitempty"`
	Port     int    `json:"port,omitempty"`
	Password string `json:"password,omitempty"`
}

// dvNetworksState is networks.json: every DMR network's settings and
// enabled state, and BrandMeister's "stay logged in". BrandMeister's own
// settings stay in [STFU] of DVSwitch.ini.
type dvNetworksState struct {
	Version  int                   `json:"version"`
	STFUStay bool                  `json:"stfuStay,omitempty"`
	Networks map[string]dvNetState `json:"networks"`
}

// dvNetMu serialises the read-modify-write of networks.json. It is held
// only around the file, never while taking applyMu (a restore holds applyMu
// and then takes it). dvNetOpMu serialises the dvswitch-network ops, which
// write the state and then the ini.
var dvNetMu, dvNetOpMu sync.Mutex

func (o *Ops) dvNetworksPath() string { return filepath.Join(o.dvStateDir(), dvNetworksFile) }

func dvEmptyNetworks() dvNetworksState {
	return dvNetworksState{Version: 1, Networks: map[string]dvNetState{}}
}

// parseDVNetworks reads networks.json strictly: version 1, valid values.
// Networks this version does not know are dropped (a newer backup).
func parseDVNetworks(data []byte) (dvNetworksState, error) {
	bad := errors.New("the DMR network settings are unreadable")
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
	for id, s := range st.Networks {
		n, ok := DVNetworkByID(id)
		if !ok {
			continue
		}
		if s.Address != "" && (!n.has("address") || !vMatch(reDVHost)(s.Address)) ||
			s.Port != 0 && (!n.has("port") || s.Port < 1 || s.Port > 65535) ||
			s.Password != "" && (!n.has("password") || !vPassword(s.Password)) {
			return dvNetworksState{}, bad
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

// dvNetLoad reads the state (the caller holds dvNetMu, or only reads). A
// missing or unreadable file is seeded from the 1.3 state: the stfu-stay
// and bm-enabled files and the network [DMR Network] holds.
func (o *Ops) dvNetLoad() dvNetworksState {
	fd, err := os.OpenFile(o.dvNetworksPath(), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err == nil {
		data, rerr := io.ReadAll(io.LimitReader(fd, dvNetworksMax+1))
		fd.Close()
		if rerr == nil {
			if st, perr := parseDVNetworks(data); perr == nil {
				return st
			}
		}
	}
	return o.dvNetSeed()
}

func (o *Ops) dvNetSeed() dvNetworksState {
	st := dvEmptyNetworks()
	exists := func(name string) bool { _, err := os.Stat(filepath.Join(o.dvStateDir(), name)); return err == nil }
	st.STFUStay = exists(dvSTFUStayFile)
	if exists(dvBMEnabledFile) {
		st.Networks[DVModeBM] = dvNetState{Enabled: true}
	}
	def, _ := dvFileByID(DVFileMMDVMBridge)
	if fs, err := o.dvRead(def); err == nil && fs.regular {
		f := ini.Parse(fs.data)
		get := func(sec, k string) string { v, _ := f.Get(sec, k); return v }
		addr := get("DMR Network", "Address")
		port, _ := strconv.Atoi(get("DMR Network", "Port"))
		pw := get("DMR Network", "Password")
		if pw == dvStockPassword || !vPassword(pw) {
			pw = ""
		}
		on := get("DMR", "Enable") == "1" && get("DMR Network", "Enable") == "1"
		if vMatch(reDVHost)(addr) {
			switch id := o.dvMMDVMNetwork(addr); {
			case o.dvIsBMHost(addr):
			case id == DVModeDMR:
				if port < 1 || port > 65535 {
					port = 0
				}
				st.Networks[id] = dvNetState{Enabled: on, Address: addr, Port: port, Password: pw}
			default:
				st.Networks[id] = dvNetState{Enabled: on, Password: pw}
			}
		}
	}
	return st
}

// dvNetSave writes the state (the caller holds dvNetMu). The 1.3 state
// files go once it is written.
func (o *Ops) dvNetSave(st dvNetworksState) error {
	if err := os.MkdirAll(o.dvStateDir(), 0o700); err != nil {
		return err
	}
	data, err := encodeDVNetworks(st)
	if err != nil {
		return err
	}
	if err := writeStateFile(o.dvNetworksPath(), data, 0o600); err != nil {
		return err
	}
	for _, name := range []string{dvSTFUStayFile, dvBMEnabledFile} {
		os.Remove(filepath.Join(o.dvStateDir(), name))
	}
	return nil
}

// dvNetGet reads the state under the lock.
func (o *Ops) dvNetGet() dvNetworksState {
	dvNetMu.Lock()
	defer dvNetMu.Unlock()
	return o.dvNetLoad()
}

// dvNetUpdate changes the state under the lock.
func (o *Ops) dvNetUpdate(fn func(*dvNetworksState)) error {
	dvNetMu.Lock()
	defer dvNetMu.Unlock()
	st := o.dvNetLoad()
	fn(&st)
	return o.dvNetSave(st)
}

// dvNetSetOne replaces one network's state.
func (o *Ops) dvNetSetOne(id string, s dvNetState) error {
	return o.dvNetUpdate(func(st *dvNetworksState) { st.Networks[id] = s })
}

// dvNetworksReset writes a fresh state: every network disabled, nothing
// stored (a fresh install; a restore staged by a backup runs after it).
func (o *Ops) dvNetworksReset() error {
	dvNetMu.Lock()
	defer dvNetMu.Unlock()
	return o.dvNetSave(dvEmptyNetworks())
}

// dvBMEnabled reports whether BrandMeister is enabled in ASLCP (the card's
// Enable). Off by default: without it the panel has no BM mode and ASLCP
// never starts stfu; the [STFU] settings stay as they are.
func (o *Ops) dvBMEnabled() bool { return o.dvNetGet().Networks[DVModeBM].Enabled }

func (o *Ops) dvBMEnabledSet(on bool) error {
	return o.dvNetUpdate(func(st *dvNetworksState) {
		s := st.Networks[DVModeBM]
		s.Enabled = on
		st.Networks[DVModeBM] = s
	})
}

// dvSTFUStay reports the "stay logged in outside BM mode" setting.
func (o *Ops) dvSTFUStay() bool { return o.dvNetGet().STFUStay }

func (o *Ops) dvSTFUStaySet(on bool) error {
	return o.dvNetUpdate(func(st *dvNetworksState) { st.STFUStay = on })
}

// dvNetConfigured: the network has what it needs to log in. A fixed master
// always has one (the password is optional); a typed-in master needs its
// address and port.
func dvNetConfigured(n DVNetwork, s dvNetState) bool {
	if n.Master == DVMasterUser {
		return s.Address != "" && s.Port != 0
	}
	return n.Master == DVMasterFixed
}

// dvMMDVMAnyEnabled: an MMDVM_Bridge network is enabled, so its [DMR] and
// [DMR Network] Enable flags are on.
func dvMMDVMAnyEnabled(st dvNetworksState) bool {
	for _, n := range DVNetworks {
		if n.Via == DVViaMMDVM && st.Networks[n.ID].Enabled {
			return true
		}
	}
	return false
}

// dvMMDVMNetwork is the MMDVM_Bridge network a [DMR Network] master belongs
// to: the fixed network that claims it, else the catch-all (DMR - Other).
func (o *Ops) dvMMDVMNetwork(addr string) string {
	if id := o.dvNetClaim(addr); id != "" {
		if n, _ := DVNetworkByID(id); n.Via == DVViaMMDVM {
			return id
		}
	}
	for _, n := range DVNetworks {
		if n.Via == DVViaMMDVM && n.CatchAll {
			return n.ID
		}
	}
	return DVModeDMR
}

// dvDMRNetwork reads [DMR Network] of MMDVM_Bridge.ini.
func (o *Ops) dvDMRNetwork() (addr string, port int, pw string, ok bool) {
	def, _ := dvFileByID(DVFileMMDVMBridge)
	st, err := o.dvRead(def)
	if err != nil || !st.regular {
		return "", 0, "", false
	}
	f := ini.Parse(st.data)
	get := func(k string) string { v, _ := f.Get("DMR Network", k); return v }
	port, _ = strconv.Atoi(get("Port"))
	return get("Address"), port, get("Password"), true
}

// dvMMDVMLive is the network [DMR Network] holds: the one the panel last
// switched to.
func (o *Ops) dvMMDVMLive() string {
	addr, _, _, _ := o.dvDMRNetwork()
	return o.dvMMDVMNetwork(addr)
}

// dvNetChanges are the [DMR Network] values of network n: its master and
// password (no saved password is the stock one). A typed-in master that is
// not set leaves the ini value alone.
func (o *Ops) dvNetChanges(n DVNetwork, s dvNetState) []dvChange {
	var ch []dvChange
	add := func(k, v string) { ch = append(ch, dvChange{DVFileMMDVMBridge, "DMR Network", k, v}) }
	addr, port := s.Address, s.Port
	if n.Master == DVMasterFixed {
		addr, port, _, _ = o.dvNetMaster(n)
	}
	if addr != "" {
		add("Address", addr)
	}
	if port != 0 {
		add("Port", strconv.Itoa(port))
	}
	pw := s.Password
	if pw == "" {
		pw = dvStockPassword
	}
	add("Password", pw)
	return ch
}

// dvNetDiffers: the ini does not hold these values.
func (o *Ops) dvNetDiffers(ch []dvChange) bool {
	addr, port, pw, ok := o.dvDMRNetwork()
	if !ok {
		return true
	}
	cur := map[string]string{"Address": addr, "Port": strconv.Itoa(port), "Password": pw}
	for _, c := range ch {
		if cur[c.key] != c.value {
			return true
		}
	}
	return false
}

// ---- the op

// OpDVSwitchNetwork saves, enables or disables an MMDVM_Bridge DMR network
// (TGIF, DMR - Other). BrandMeister has its own op, dvswitch-bm.
const OpDVSwitchNetwork = "dvswitch-network"

// Actions of dvswitch-network.
const (
	DVNetSave    = "save"
	DVNetEnable  = "enable"
	DVNetDisable = "disable"
)

// DVNetworkReq is the argument of dvswitch-network. Save takes the fields
// of the network (DVNetwork.Fields); nil leaves a value alone; the password
// is write-only, ClearPassword forgets it (excludes Password). Enable and
// Disable take no other field.
type DVNetworkReq struct {
	ID            string  `json:"id"`
	Action        string  `json:"action"`
	Address       *string `json:"address,omitempty"`
	Port          *int    `json:"port,omitempty"`
	Password      *string `json:"password,omitempty"`
	ClearPassword bool    `json:"clearPassword,omitempty"`
}

// DVSwitchNetwork runs dvswitch-network. Settings go to networks.json; the
// ini is written only when the network saved is the one [DMR Network]
// holds. Enable and Disable change that network's flag only, and MMDVM_Bridge's
// [DMR] and [DMR Network] Enable follow: on while any MMDVM_Bridge network
// is enabled. Disable keeps the settings.
func (o *Ops) DVSwitchNetwork(ctx context.Context, req *DVSwitchRequest) (DVApplyResult, error) {
	req, err := o.dvArgs(req)
	if err != nil {
		return DVApplyResult{}, err
	}
	nr := req.Network
	if nr == nil || req.Identity != nil || req.Credentials != nil || req.Modes != nil || req.Tune != nil || req.BM != nil || req.USRP != nil || req.Private != nil {
		return DVApplyResult{}, errors.New("invalid request: network only")
	}
	n, ok := DVNetworkByID(nr.ID)
	if !ok || n.Via != DVViaMMDVM {
		return DVApplyResult{}, dvInvalid("network/id")
	}
	switch nr.Action {
	case DVNetSave:
		return o.dvNetSaveOp(ctx, n, nr, req.Base)
	case DVNetEnable, DVNetDisable:
		if nr.Address != nil || nr.Port != nil || nr.Password != nil || nr.ClearPassword {
			return DVApplyResult{}, dvInvalid("network/action")
		}
		return o.dvNetEnableOp(ctx, n, nr.Action == DVNetEnable, req.Base)
	}
	return DVApplyResult{}, dvInvalid("network/action")
}

func (o *Ops) dvNetSaveOp(ctx context.Context, n DVNetwork, nr *DVNetworkReq, base map[string]string) (DVApplyResult, error) {
	if nr.Password != nil && nr.ClearPassword {
		return DVApplyResult{}, dvInvalid("network/password")
	}
	if nr.Address != nil && !n.has("address") || nr.Port != nil && !n.has("port") || (nr.Password != nil || nr.ClearPassword) && !n.has("password") {
		return DVApplyResult{}, dvInvalid("network/fields")
	}
	if nr.Address != nil {
		if !vMatch(reDVHost)(*nr.Address) {
			return DVApplyResult{}, dvInvalid("network/address")
		}
		switch id := o.dvNetClaim(*nr.Address); {
		case id == DVModeBM:
			return DVApplyResult{}, ErrDVBMMaster
		case id != "" && id != n.ID:
			return DVApplyResult{}, ErrDVNetMaster
		}
	}
	if nr.Port != nil && (*nr.Port < 1 || *nr.Port > 65535) {
		return DVApplyResult{}, dvInvalid("network/port")
	}
	if nr.Password != nil && !vPassword(*nr.Password) {
		return DVApplyResult{}, dvInvalid("network/password")
	}
	dvNetOpMu.Lock()
	defer dvNetOpMu.Unlock()
	before := o.dvNetGet().Networks[n.ID]
	next := before
	if nr.Address != nil {
		next.Address = *nr.Address
	}
	if nr.Port != nil {
		next.Port = *nr.Port
	}
	if nr.Password != nil {
		next.Password = *nr.Password
	}
	if nr.ClearPassword {
		next.Password = ""
	}
	if next == before {
		return dvNoChange(), nil
	}
	live := o.dvMMDVMLive() == n.ID
	if err := o.dvNetSetOne(n.ID, next); err != nil {
		return DVApplyResult{}, fmt.Errorf("%w (state)", ErrDVNetFailed)
	}
	if !live {
		res := dvNoChange()
		res.Files = []string{DVStateNetworks}
		return res, nil
	}
	// the network [DMR Network] holds: the ini follows
	res, err := o.dvApply(ctx, o.dvNetChanges(n, next), base)
	if err != nil {
		_ = o.dvNetSetOne(n.ID, before)
		return res, err
	}
	res.Files = append(res.Files, DVStateNetworks)
	return res, nil
}

func (o *Ops) dvNetEnableOp(ctx context.Context, n DVNetwork, on bool, base map[string]string) (DVApplyResult, error) {
	dvNetOpMu.Lock()
	defer dvNetOpMu.Unlock()
	before := o.dvNetGet().Networks[n.ID]
	if on && !dvNetConfigured(n, before) {
		return DVApplyResult{}, ErrDVNetNotConfigured
	}
	next := before
	next.Enabled = on
	res := dvNoChange()
	var st dvNetworksState
	if err := o.dvNetUpdate(func(s *dvNetworksState) {
		s.Networks[n.ID] = next
		st = *s
	}); err != nil {
		return DVApplyResult{}, fmt.Errorf("%w (state)", ErrDVNetFailed)
	}
	if next != before {
		res.Files = []string{DVStateNetworks}
	}
	// MMDVM_Bridge's DMR is on while any of its networks is enabled
	want := "0"
	if dvMMDVMAnyEnabled(st) {
		want = "1"
	}
	def, _ := dvFileByID(DVFileMMDVMBridge)
	fs, err := o.dvRead(def)
	if err != nil || !fs.regular {
		return res, nil // nothing to align (DVSwitch not installed)
	}
	f := ini.Parse(fs.data)
	a, _ := f.Get("DMR", "Enable")
	b, _ := f.Get("DMR Network", "Enable")
	if a == want && b == want {
		return res, nil
	}
	r2, err := o.dvApply(ctx, []dvChange{
		{DVFileMMDVMBridge, "DMR", "Enable", want},
		{DVFileMMDVMBridge, "DMR Network", "Enable", want},
	}, base)
	if err != nil {
		if next != before {
			_ = o.dvNetSetOne(n.ID, before)
		}
		return r2, err
	}
	r2.Files = append(r2.Files, res.Files...)
	return r2, nil
}

// ---- switching the live network (dvswitch-tune)

// dvNetEnsure makes [DMR Network] hold network n before the panel switches
// to it: when the ini differs from n's settings they are written (no history
// copy: not a user change), mmdvm_bridge alone restarts and the login is
// awaited. It reports restarted and pending (the login was not seen in
// time). The caller holds the tune lock.
func (o *Ops) dvNetEnsure(ctx context.Context, n DVNetwork) (restarted, pending bool, err error) {
	s := o.dvNetGet().Networks[n.ID]
	ch := o.dvNetChanges(n, s)
	if !o.dvNetDiffers(ch) {
		return false, false, nil
	}
	if _, err := o.dvApplyOpt(ctx, ch, nil, dvApplyOpts{noHistory: true}); err != nil {
		return false, false, err
	}
	if !o.dvSystemctl(ctx, "restart", "mmdvm_bridge.service") {
		return false, false, fmt.Errorf("%w (mmdvm_bridge)", ErrDVNetFailed)
	}
	ok := o.dvWaitLogin(ctx, func() bool {
		st, _ := o.dvMMDVMStatus(ctx)
		return st.State == DMRLoggedIn
	})
	return true, !ok, nil
}

// ---- setup view

// DVNetworkView is one DMR network's card. Address and Port: the typed-in
// master, or the fixed one (Source "hosts" with the row's Name, or
// "default"). The password only as PasswordSet. Live: [DMR Network] holds
// this network. PointsAtBM: a typed-in master that is BrandMeister's (saved,
// or hand-edited into [DMR Network] while it is this network's).
type DVNetworkView struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Via         string `json:"via"`
	Master      string `json:"master,omitempty"`
	Address     string `json:"address"`
	Port        int    `json:"port"`
	Source      string `json:"source,omitempty"`
	Name        string `json:"name,omitempty"`
	PasswordSet bool   `json:"passwordSet"`
	Enabled     bool   `json:"enabled"`
	Configured  bool   `json:"configured"`
	Live        bool   `json:"live"`
	PointsAtBM  bool   `json:"pointsAtBm,omitempty"`
}

// dvNetworkViews builds the cards; BrandMeister's Enabled and Configured
// come from bm (its details are DVSwitchSetup.BrandMeister).
func (o *Ops) dvNetworkViews(bm DVBMView) []DVNetworkView {
	st := o.dvNetGet()
	iniAddr, _, _, _ := o.dvDMRNetwork()
	live := o.dvMMDVMNetwork(iniAddr)
	out := make([]DVNetworkView, 0, len(DVNetworks))
	for _, n := range DVNetworks {
		s := st.Networks[n.ID]
		v := DVNetworkView{ID: n.ID, Label: n.Label, Via: n.Via, Master: n.Master, Enabled: s.Enabled}
		switch {
		case n.Via == DVViaSTFU:
			v.Enabled, v.Configured = bm.Enabled, bm.Configured
		case n.Master == DVMasterFixed:
			v.Address, v.Port, v.Source, v.Name = o.dvNetMaster(n)
		default:
			v.Address, v.Port = s.Address, s.Port
			// the saved master, or a hand-edited one in [DMR Network]
			v.PointsAtBM = vHostname(s.Address) && o.dvIsBMHost(s.Address) ||
				live == n.ID && vHostname(iniAddr) && o.dvIsBMHost(iniAddr)
		}
		if n.Via == DVViaMMDVM {
			v.PasswordSet = dvPasswordSet(s.Password)
			v.Configured = dvNetConfigured(n, s)
			v.Live = live == n.ID
		}
		out = append(out, v)
	}
	return out
}
