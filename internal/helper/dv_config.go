package helper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/artw1/aslcp/internal/astconf"
	dvctl "github.com/artw1/aslcp/internal/dv/ctl"
	"github.com/artw1/aslcp/internal/dvpanel"
)

// The engine's config.json (docs/DV.md): rendered by the helper only, from the
// private node record, the node's rpt.conf stanza and networks.json, written
// atomically for the engine's own user, mode 0600.

const dvConfigFile = "config.json"

func (o *Ops) dvConfigPath() string { return filepath.Join(o.dvEngineDir(), dvConfigFile) }

// dvUSRPPorts reads the node's rxchannel (USRP/127.0.0.1:<engine listens>:
// <engine sends to>); ok is false when the stanza or the channel is missing
// or not a chan_usrp channel.
func dvUSRPPorts(rpt []byte, node string) (listen, peer int, ok bool) {
	f, err := astconf.Parse("rpt.conf", rpt)
	if err != nil {
		return 0, 0, false
	}
	ss := f.Section(node)
	if len(ss) != 1 || ss[0].Template {
		return 0, 0, false
	}
	es := ss[0].Get("rxchannel")
	if len(es) == 0 {
		return 0, 0, false
	}
	parts := strings.Split(strings.TrimSpace(es[len(es)-1].Value), "/")
	if len(parts) != 2 || !strings.EqualFold(parts[0], "usrp") {
		return 0, 0, false
	}
	hp := strings.Split(parts[1], ":")
	if len(hp) != 3 || hp[0] != dvUSRPAddress {
		return 0, 0, false
	}
	l, err1 := strconv.Atoi(hp[1])
	p, err2 := strconv.Atoi(hp[2])
	if err1 != nil || err2 != nil || !validDVPort(l) || !validDVPort(p) || l == p {
		return 0, 0, false
	}
	return l, p, true
}

// renderDVConfig builds the engine config: the parrot, every network this
// build carries natively (disabled until set up) and, from networks.json, the
// settings of the networks that are not in this build yet (carried so the
// operator's settings are not lost). The identity comes from networks.json.
func renderDVConfig(listen, peer int, nets dvNetworksState) dvctl.Config {
	cfg := dvctl.Config{
		Version: dvctl.ConfigVersion,
		USRP:    dvctl.USRPConfig{Listen: fmt.Sprintf("%s:%d", dvUSRPAddress, listen), Peer: fmt.Sprintf("%s:%d", dvUSRPAddress, peer)},
		Vocoder: dvctl.VocoderConfig{MD380: "127.0.0.1:2470", TimeoutMS: 60},
		Networks: []dvctl.NetConfig{
			{Mode: dvctl.ModeLoop, Enabled: true},
		},
	}
	if id := nets.Identity; id != nil {
		cfg.Identity = dvctl.Identity{Callsign: id.Callsign, DMRID: id.DMRID, RepeaterID: id.RepeaterID}
	}
	for _, sp := range dvpanel.NetSpecs {
		s, ok := nets.Networks[sp.Mode]
		if !ok && !dvpanel.NativeMode(sp.Mode) {
			continue
		}
		nc := dvctl.NetConfig{Mode: sp.Mode, Enabled: s.Enabled, Address: s.Address, Port: s.Port, Password: s.Password}
		if len(s.Options) > 0 {
			nc.Options = s.Options
		}
		cfg.Networks = append(cfg.Networks, nc)
	}
	return cfg
}

// DVConfig renders the engine's config.json. The private node's ports come
// from its rpt.conf stanza; without a node the engine defaults apply, so a
// fresh enable works before the node exists.
func (o *Ops) DVConfig(ctx context.Context) (DVConfigResult, error) {
	o.applyMu.Lock()
	defer o.applyMu.Unlock()
	return o.dvConfigLocked()
}

func (o *Ops) dvConfigLocked() (DVConfigResult, error) {
	res := DVConfigResult{Path: o.dvConfigPath()}
	listen, peer := dvDefaultRxPort, dvDefaultTxPort
	if rec, err := ReadDVPrivate(o.dvPrivatePath()); err == nil && rec != nil {
		if cur, err := o.ConfigRead("rpt.conf"); err == nil {
			if l, p, ok := dvUSRPPorts([]byte(cur.Content), rec.Node); ok {
				listen, peer = l, p
			}
		}
	}
	data, err := json.MarshalIndent(renderDVConfig(listen, peer, o.dvNetLoad()), "", "  ")
	if err != nil {
		return res, err
	}
	data = append(data, '\n')
	u, err := user.Lookup(o.dvEngineUser())
	if err != nil {
		return res, fmt.Errorf("the engine user %q does not exist: %w", o.dvEngineUser(), err)
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	dir := o.dvEngineDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return res, err
	}
	// the directory belongs to the engine (StateDirectory); a fresh one is
	// handed over, an existing one is left alone
	_ = os.Chown(dir, uid, gid)
	if old, err := os.ReadFile(res.Path); err == nil && bytes.Equal(old, data) {
		_ = os.Chown(res.Path, uid, gid)
		_ = os.Chmod(res.Path, 0o600)
		return res, nil
	}
	if err := atomicWrite(res.Path, dir, data, uid, gid, 0o600); err != nil {
		return res, fmt.Errorf("cannot write the engine configuration: %w", err)
	}
	res.Changed = true
	return res, nil
}
