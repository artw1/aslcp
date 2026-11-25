package api

import (
	"time"

	"github.com/artw1/aslcp/internal/nodelist"
	"github.com/artw1/aslcp/internal/rpt"
	"github.com/artw1/aslcp/internal/store"
)

// UserJSON is the API User object.
type UserJSON struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

func userJSON(u store.User) UserJSON { return UserJSON{ID: u.ID, Username: u.Username, Role: u.Role} }

// LinkJSON is the API Link object.
type LinkJSON struct {
	Node       string         `json:"node"`
	Kind       string         `json:"kind"`
	Info       *nodelist.Info `json:"info"`
	IP         string         `json:"ip"`
	Direction  string         `json:"direction"`
	Connected  int64          `json:"connected"`
	State      string         `json:"state"`
	Mode       string         `json:"mode"`
	Keyed      bool           `json:"keyed"`
	SinceKey   *int64         `json:"sinceKey"`
	SinceUnkey *int64         `json:"sinceUnkey"`
}

// NetNodeJSON is one entry of NodeStatus.network.
type NetNodeJSON struct {
	Node string         `json:"node"`
	Mode string         `json:"mode"`
	Kind string         `json:"kind"`
	Info *nodelist.Info `json:"info"`
}

// NodeStatusJSON is the API NodeStatus object.
type NodeStatusJSON struct {
	Node  string         `json:"node"`
	Info  *nodelist.Info `json:"info"`
	Stale bool           `json:"stale"`
	// StaleReason is the last poll error while Stale (admin/operator only).
	StaleReason string          `json:"staleReason,omitempty"`
	RxKeyed     bool            `json:"rxKeyed"`
	TxKeyed     bool            `json:"txKeyed"`
	ExtTxKeyed  bool            `json:"extTxKeyed"`
	NumLinks    int             `json:"numLinks"`
	Links       []LinkJSON      `json:"links"`
	Network     []NetNodeJSON   `json:"network"`
	Flags       map[string]bool `json:"flags"`
	Updated     string          `json:"updated"`
}

// FavoriteJSON is the API Favorite object.
type FavoriteJSON struct {
	ID       int64          `json:"id"`
	Node     string         `json:"node"`
	Label    string         `json:"label"`
	Group    string         `json:"group"`
	Position int            `json:"position"`
	Pinned   bool           `json:"pinned"`
	Info     *nodelist.Info `json:"info"`
}

func secsPtr(d *time.Duration) *int64 {
	if d == nil {
		return nil
	}
	v := int64(*d / time.Second)
	return &v
}

func (s *Server) info(node string) *nodelist.Info {
	if s.d.Nodelist == nil {
		return nil
	}
	if in, ok := s.d.Nodelist.Lookup(node); ok {
		return &in
	}
	return nil
}

func flagsJSON(f rpt.Flags) map[string]bool {
	return map[string]bool{
		"txDisabled":        f.TxDisabled,
		"totDisabled":       f.TotDisabled,
		"linkFunsDisabled":  f.LinkFunsDisabled,
		"autopatchDisabled": f.AutopatchDisabled,
		"schedulerDisabled": f.SchedulerDisabled,
		"userFunsDisabled":  f.UserFunsDisabled,
		"alternateTail":     f.AlternateTail,
		"noIncomingConns":   f.NoIncomingConns,
		"parrot":            f.ParrotEnabled,
	}
}

// nodeJSON converts a monitor status. priv selects admin/operator view
// (link IPs included).
func (s *Server) nodeJSON(st rpt.NodeStatus, priv bool) NodeStatusJSON {
	out := NodeStatusJSON{
		Node: st.Node, Info: s.info(st.Node), Stale: st.Stale,
		RxKeyed: st.RxKeyed, TxKeyed: st.TxKeyed, ExtTxKeyed: st.ExtTxKeyed,
		NumLinks: st.NumLinks, Flags: flagsJSON(st.Flags),
		Links: make([]LinkJSON, 0, len(st.Links)), Network: make([]NetNodeJSON, 0, len(st.Network)),
	}
	if !st.Updated.IsZero() {
		out.Updated = st.Updated.UTC().Format(time.RFC3339)
	}
	if priv && st.Stale {
		out.StaleReason = st.Err
	}
	for _, l := range st.Links {
		mode := l.Mode.String()
		if l.State == rpt.StateConnecting {
			mode = "C"
		}
		lj := LinkJSON{
			Node: l.Node, Kind: l.Kind.String(), Info: s.info(l.Node), Direction: string(l.Direction),
			Connected: int64(l.Connected / time.Second), State: l.State, Mode: mode, Keyed: l.Keyed,
			SinceKey: secsPtr(l.SinceKey), SinceUnkey: secsPtr(l.SinceUnkey),
		}
		if priv {
			lj.IP = l.IP
		}
		out.Links = append(out.Links, lj)
	}
	for _, n := range st.Network {
		out.Network = append(out.Network, NetNodeJSON{Node: n.Node, Mode: n.Mode.String(), Kind: n.Kind.String(), Info: s.info(n.Node)})
	}
	return out
}

func (s *Server) favJSON(f store.Favorite) FavoriteJSON {
	return FavoriteJSON{ID: f.ID, Node: f.Node, Label: f.Label, Group: f.Group, Position: f.Position, Pinned: f.Pinned, Info: s.info(f.Node)}
}

func (s *Server) favoritesJSON() ([]FavoriteJSON, error) {
	fs, err := s.d.Store.ListFavorites()
	if err != nil {
		return nil, err
	}
	out := make([]FavoriteJSON, len(fs))
	for i, f := range fs {
		out[i] = s.favJSON(f)
	}
	return out, nil
}
