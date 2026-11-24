package helper

import (
	"context"
	"errors"
	"fmt"
	"os/user"
	"strings"

	"github.com/artw1/aslcp/internal/dvpanel"
)

// dv-network: the per-network settings and the identity of the digital modes
// (docs/DV.md, "Settings"). The op reads or changes networks.json in the
// state directory, renders the engine's config.json again (dv-config) and
// returns the new settings. A password is write-only: no reply carries it and
// no log line mentions it.

// ErrDVNetIncomplete: the network cannot be enabled yet (a setting or the
// identity is missing). The client maps the text back.
var ErrDVNetIncomplete = errors.New("net_incomplete: the network settings are not complete")

func init() { dvErrors = append(dvErrors, ErrDVNetIncomplete) }

// DVIdentityView is the identity as the op reports and sets it.
type DVIdentityView struct {
	Callsign   string `json:"callsign"`
	DMRID      uint32 `json:"dmrId"`
	RepeaterID uint32 `json:"repeaterId,omitempty"`
}

// DVNetworkRequest is the dv.network field of dv-network. With neither Mode
// nor Identity it reads. With Identity it sets the identity. With Mode it
// updates that network: a nil field is kept, Options (when not nil) replaces
// all options, ClearPassword removes the password. Options has no omitempty on
// purpose: an empty map ({}) means "clear every option" and must survive the
// round trip through JSON, only a nil map (null) means "keep".
type DVNetworkRequest struct {
	Mode          string              `json:"mode,omitempty"`
	Enabled       *bool               `json:"enabled,omitempty"`
	Address       *string             `json:"address,omitempty"`
	Port          *int                `json:"port,omitempty"`
	Password      *string             `json:"password,omitempty"`
	ClearPassword bool                `json:"clearPassword,omitempty"`
	Options       map[string]string   `json:"options"`
	Identity      *DVIdentityView     `json:"identity,omitempty"`
	Audio         *dvpanel.AudioState `json:"audio,omitempty"` // sets the audio settings
}

// DVNetworkView is one network's settings without its password.
type DVNetworkView struct {
	Mode        string            `json:"mode"`
	Enabled     bool              `json:"enabled"`
	Address     string            `json:"address,omitempty"`
	Port        int               `json:"port,omitempty"`
	PasswordSet bool              `json:"passwordSet"`
	Options     map[string]string `json:"options"`
}

// DVNetworkReply is the reply of dv-network. Rendered: the engine's config
// was written (false before digital modes are enabled, when the engine user
// does not exist yet). Changed: the file content differs from before.
type DVNetworkReply struct {
	Identity DVIdentityView     `json:"identity"`
	Audio    dvpanel.AudioState `json:"audio"`
	Networks []DVNetworkView    `json:"networks"`
	Rendered bool               `json:"rendered"`
	Changed  bool               `json:"changed"`
}

func dvNetViews(st dvNetworksState) []DVNetworkView {
	out := make([]DVNetworkView, 0, len(dvpanel.NetSpecs))
	for _, sp := range dvpanel.NetSpecs {
		s := st.Networks[sp.Mode]
		o := map[string]string{}
		for k, v := range s.Options {
			o[k] = v
		}
		out = append(out, DVNetworkView{Mode: sp.Mode, Enabled: s.Enabled, Address: s.Address, Port: s.Port, PasswordSet: s.Password != "", Options: o})
	}
	return out
}

func dvAudioView(st dvNetworksState) dvpanel.AudioState {
	if st.Audio == nil {
		return dvpanel.DefaultAudio()
	}
	return *st.Audio
}

func dvIdentityView(st dvNetworksState) DVIdentityView {
	if st.Identity == nil {
		return DVIdentityView{}
	}
	return DVIdentityView{Callsign: st.Identity.Callsign, DMRID: st.Identity.DMRID, RepeaterID: st.Identity.RepeaterID}
}

func dvBad(f string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrDVBadRequest, fmt.Sprintf(f, a...))
}

// DVNetwork runs dv-network.
func (o *Ops) DVNetwork(ctx context.Context, req *DVRequest) (DVNetworkReply, error) {
	var r *DVNetworkRequest
	if req != nil {
		r = req.Network
	}
	o.applyMu.Lock()
	defer o.applyMu.Unlock()
	st := o.dvNetLoad()
	if r == nil || r.Mode == "" && r.Identity == nil && r.Audio == nil {
		return DVNetworkReply{Identity: dvIdentityView(st), Audio: dvAudioView(st), Networks: dvNetViews(st)}, nil
	}
	n := 0
	for _, set := range []bool{r.Mode != "", r.Identity != nil, r.Audio != nil} {
		if set {
			n++
		}
	}
	if n > 1 {
		return DVNetworkReply{}, dvBad("set the identity, the audio or one network, not several at once")
	}
	if r.Audio != nil {
		if err := dvpanel.ValidateAudio(*r.Audio); err != nil {
			return DVNetworkReply{}, dvBad("%v", err)
		}
		st.Audio = nil
		if *r.Audio != dvpanel.DefaultAudio() {
			a := *r.Audio
			st.Audio = &a
		}
	} else if r.Identity != nil {
		id, err := dvpanel.ValidateIdentity(dvpanel.IdentityState{Callsign: r.Identity.Callsign, DMRID: r.Identity.DMRID, RepeaterID: r.Identity.RepeaterID})
		if err != nil {
			return DVNetworkReply{}, dvBad("%v", err)
		}
		if id.Callsign == "" && id.DMRID == 0 {
			st.Identity = nil
			// no identity: a network that needs one cannot stay enabled
			for m, s := range st.Networks {
				if sp, ok := dvpanel.NetSpecFor(m); ok && sp.NeedsIdentity && s.Enabled {
					s.Enabled = false
					st.Networks[m] = s
				}
			}
		} else {
			st.Identity = &dvIdentity{Callsign: id.Callsign, DMRID: id.DMRID, RepeaterID: id.RepeaterID}
		}
	} else {
		sp, ok := dvpanel.NetSpecFor(r.Mode)
		if !ok {
			return DVNetworkReply{}, dvBad("unknown mode")
		}
		u, err := dvpanel.CheckUpdate(r.Mode, dvpanel.NetUpdate{Enabled: r.Enabled, Address: r.Address, Port: r.Port,
			Password: r.Password, ClearPassword: r.ClearPassword, Options: r.Options})
		if err != nil {
			return DVNetworkReply{}, dvBad("%v", err)
		}
		s := st.Networks[r.Mode]
		if u.Address != nil {
			s.Address = *u.Address
		}
		if u.Port != nil {
			s.Port = *u.Port
		}
		if u.ClearPassword {
			s.Password = ""
		}
		if u.Password != nil && *u.Password != "" {
			s.Password = *u.Password
		}
		if u.Options != nil {
			if len(u.Options) == 0 {
				s.Options = nil
			} else {
				s.Options = u.Options
			}
		}
		if u.Enabled != nil {
			s.Enabled = *u.Enabled
		}
		// erasing the NXDN ID of an enabled NXDN network turns it off; turning it
		// on without an ID is refused below
		if sp.Mode == dvpanel.ModeNXDN && s.Enabled && strings.TrimSpace(s.Options["nxdn_id"]) == "" && (u.Enabled == nil || !*u.Enabled) {
			s.Enabled = false
		}
		if s.Enabled {
			id := dvpanel.IdentityState{}
			if st.Identity != nil {
				id = dvpanel.IdentityState{Callsign: st.Identity.Callsign, DMRID: st.Identity.DMRID}
			}
			if why := dvpanel.EnableBlock(sp, dvpanel.NetState{Enabled: true, Address: s.Address, Port: s.Port, PasswordSet: s.Password != "", Options: s.Options}, id); why != "" {
				return DVNetworkReply{}, fmt.Errorf("%w: %s", ErrDVNetIncomplete, why)
			}
		}
		if st.Networks == nil {
			st.Networks = map[string]dvNetState{}
		}
		st.Networks[r.Mode] = s
	}
	data, err := encodeDVNetworks(st)
	if err != nil {
		return DVNetworkReply{}, err
	}
	if err := o.dvWriteNetworks(append(data, '\n')); err != nil {
		return DVNetworkReply{}, fmt.Errorf("cannot write the network settings: %w", err)
	}
	rep := DVNetworkReply{Identity: dvIdentityView(st), Audio: dvAudioView(st), Networks: dvNetViews(st)}
	res, err := o.dvConfigLocked()
	var unknown user.UnknownUserError
	switch {
	case err == nil:
		rep.Rendered, rep.Changed = true, res.Changed
	case errors.As(err, &unknown):
		// digital modes are not enabled yet: the enable job renders the config
	default:
		return rep, fmt.Errorf("the settings were saved, but the engine configuration could not be written: %w", err)
	}
	return rep, nil
}
