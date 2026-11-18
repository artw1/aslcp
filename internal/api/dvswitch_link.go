package api

import (
	"context"

	"github.com/artw1/aslcp/internal/dvswitch"
	"github.com/artw1/aslcp/internal/rpt"
)

// The link between a local node and the private bridge node (docs/DVSWITCH.md,
// "Link mode"): the Linker the reconciler sends its commands through, the
// view of the link in the snapshot and the hooks of the HTTP handlers.

// dvLinker implements dvswitch.Linker with the same link helpers as the HTTP
// handlers. Its commands are not manual ones: they never reach the hooks.
type dvLinker struct{ s *Server }

func (l dvLinker) Connect(ctx context.Context, local, bridge string) error {
	return l.s.connectLink(ctx, local, bridge, rpt.ConnTransceive, true, false)
}

func (l dvLinker) Disconnect(ctx context.Context, local, bridge string, permanent bool) error {
	return l.s.disconnectLink(ctx, local, bridge, permanent)
}

// Linked reports whether local has bridge among its direct links (a link
// that is still connecting counts: the reconciler must not repeat itself).
func (l dvLinker) Linked(local, bridge string) (linked, known bool) {
	m := l.s.monitor()
	if m == nil || !l.s.isLocalNode(local) {
		return false, false
	}
	st, ok := m.Status(local)
	if !ok {
		return false, false
	}
	for _, k := range st.Links {
		if k.Node == bridge {
			return true, true
		}
	}
	return false, true
}

// initDVLink wires the reconciler to this server.
func (s *Server) initDVLink() {
	r := s.d.DVLink
	if r == nil {
		return
	}
	if r.Linker == nil {
		r.Linker = dvLinker{s}
	}
	if r.Store == nil && s.d.Store != nil {
		r.Store = s.d.Store
	}
	if r.Logger == nil {
		r.Logger = s.d.Logger
	}
	prev := r.OnChange
	r.OnChange = func() {
		if prev != nil {
			prev()
		}
		s.dvLinkChanged()
	}
}

// dvLinkChanged publishes a fresh snapshot after a link command or a pause
// change.
func (s *Server) dvLinkChanged() {
	if s.dvs != nil && s.d.DVSwitch != nil {
		s.dvs.afterCommand()
	}
}

// dvManualDisconnect is the hook of handleDisconnect and handleDisconnectAll.
func (s *Server) dvManualDisconnect(node, target string) {
	if s.d.DVLink != nil && s.d.DVLink.ManualDisconnect(node, target) {
		s.d.Logger.Printf("dvswitch link: paused after a manual disconnect of %s on %s", target, node)
	}
	s.dvLinkChanged()
}

func (s *Server) dvManualConnect(node, target string) {
	if s.d.DVLink != nil {
		s.d.DVLink.ManualConnect(node, target)
	}
	s.dvLinkChanged()
}

// DVLink is the link part of the snapshot. LocalNode is "" when none is
// chosen or known; Linked is false while the monitor does not know it.
type DVLink struct {
	Mode      string `json:"mode"`
	LocalNode string `json:"localNode"`
	Linked    bool   `json:"linked"`
	Paused    bool   `json:"paused"`
}

// dvLinkSettings reads the saved link settings (ondemand when there are none).
func (s *Server) dvLinkSettings() dvswitch.LinkSettings {
	if s.d.Store == nil {
		return dvswitch.LinkSettings{Mode: dvswitch.LinkOnDemand}
	}
	return dvswitch.ReadLinkSettings(s.d.Store)
}

// dvLocalFor picks the local node of the link: the saved one or, for the
// on-demand button, the first local node that is not the bridge node.
func (s *Server) dvLocalFor(set dvswitch.LinkSettings, bridge string) string {
	if set.LocalNode != "" && set.LocalNode != bridge {
		return set.LocalNode
	}
	if set.Mode == dvswitch.LinkPermanent {
		return ""
	}
	if m := s.monitor(); m != nil {
		for _, n := range m.Nodes() {
			if n != bridge {
				return n
			}
		}
	}
	return ""
}

// dvLinkView computes the link of the snapshot for the bridge node.
func (s *Server) dvLinkView(bn DVBridgeNode) DVLink {
	set := s.dvLinkSettings()
	v := DVLink{Mode: set.Mode}
	bridge := set.Bridge
	if bn.Present {
		bridge = bn.Node
	}
	if bridge == "" {
		return v
	}
	v.LocalNode = s.dvLocalFor(set, bridge)
	if v.LocalNode == "" {
		return v
	}
	v.Linked, _ = dvLinker{s}.Linked(v.LocalNode, bridge)
	if set.Mode == dvswitch.LinkPermanent && s.d.Store != nil {
		p, _ := s.d.Store.GetSetting(dvswitch.SettingLinkPaused)
		v.Paused = p == "1"
	}
	return v
}
