package helper

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"strings"
)

// Read-only network status of the System tab (plan 5.3): interfaces, link
// state, addresses, default gateways and DNS servers.
const OpNetStatus = "net-status"

func init() {
	registerSysOp(OpNetStatus, func(o *Ops, ctx context.Context, _ *SysRequest) (any, error) {
		return o.NetStatus(ctx), nil
	})
}

// DefaultResolvConf is where DNS servers are read when resolved is not used.
const DefaultResolvConf = "/etc/resolv.conf"

// NetAddr is one address of an interface.
type NetAddr struct {
	Family  string `json:"family"` // inet or inet6
	Addr    string `json:"addr"`
	Prefix  int    `json:"prefix"`
	Scope   string `json:"scope"`
	Dynamic bool   `json:"dynamic"`
}

// NetIface is one network interface.
type NetIface struct {
	Name  string    `json:"name"`
	State string    `json:"state"` // UP, DOWN, UNKNOWN ...
	Kind  string    `json:"kind"`  // ether, loopback, none ...
	MAC   string    `json:"mac"`
	MTU   int       `json:"mtu"`
	Addrs []NetAddr `json:"addrs"`
}

// NetRoute is one default route.
type NetRoute struct {
	Family  string `json:"family"`
	Gateway string `json:"gateway"`
	Dev     string `json:"dev"`
	Metric  int    `json:"metric"`
}

// NetStatus is the response of net-status.
type NetStatus struct {
	Available  bool       `json:"available"`
	Interfaces []NetIface `json:"interfaces"`
	Gateways   []NetRoute `json:"gateways"`
	DNS        []string   `json:"dns"`
	DNSSource  string     `json:"dnsSource"` // resolvectl, resolv.conf or ""
}

type ipAddrJSON struct {
	Ifname    string `json:"ifname"`
	Operstate string `json:"operstate"`
	LinkType  string `json:"link_type"`
	Address   string `json:"address"`
	MTU       int    `json:"mtu"`
	AddrInfo  []struct {
		Family    string `json:"family"`
		Local     string `json:"local"`
		Prefixlen int    `json:"prefixlen"`
		Scope     string `json:"scope"`
		Dynamic   bool   `json:"dynamic"`
	} `json:"addr_info"`
}

// parseIPAddr reads "ip -j addr".
func parseIPAddr(text string) ([]NetIface, error) {
	var raw []ipAddrJSON
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return nil, err
	}
	out := make([]NetIface, 0, len(raw))
	for _, r := range raw {
		it := NetIface{Name: r.Ifname, State: r.Operstate, Kind: r.LinkType, MAC: r.Address, MTU: r.MTU, Addrs: []NetAddr{}}
		for _, a := range r.AddrInfo {
			it.Addrs = append(it.Addrs, NetAddr{Family: a.Family, Addr: a.Local, Prefix: a.Prefixlen, Scope: a.Scope, Dynamic: a.Dynamic})
		}
		out = append(out, it)
	}
	return out, nil
}

type ipRouteJSON struct {
	Dst     string `json:"dst"`
	Gateway string `json:"gateway"`
	Dev     string `json:"dev"`
	Metric  int    `json:"metric"`
}

// parseIPRoute reads "ip -j route" (family "inet") or "ip -j -6 route"
// ("inet6") and returns the default routes that have a gateway.
func parseIPRoute(text, family string) ([]NetRoute, error) {
	var raw []ipRouteJSON
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return nil, err
	}
	out := []NetRoute{}
	for _, r := range raw {
		if r.Dst == "default" && r.Gateway != "" {
			out = append(out, NetRoute{Family: family, Gateway: r.Gateway, Dev: r.Dev, Metric: r.Metric})
		}
	}
	return out, nil
}

// parseResolvConf returns the nameserver addresses of a resolv.conf.
func parseResolvConf(text string) []string {
	out := []string{}
	for _, line := range strings.Split(text, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "nameserver" && net.ParseIP(f[1]) != nil {
			out = append(out, f[1])
		}
	}
	return out
}

// parseResolvectl returns the DNS servers of "resolvectl status" (global
// and per link, without duplicates). The list can continue on indented lines
// that hold one address each.
func parseResolvectl(text string) []string {
	out := []string{}
	seen := map[string]bool{}
	add := func(s string) {
		if net.ParseIP(s) != nil && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	inList := false
	for _, line := range strings.Split(text, "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "DNS Servers:"):
			inList = true
			for _, f := range strings.Fields(strings.TrimPrefix(t, "DNS Servers:")) {
				add(f)
			}
		case inList && net.ParseIP(t) != nil:
			add(t)
		default:
			inList = false
		}
	}
	return out
}

// isStubResolver: the addresses of systemd-resolved's local listeners, which
// say nothing about the real servers.
func isStubResolver(dns []string) bool {
	if len(dns) == 0 {
		return false
	}
	for _, d := range dns {
		if d != "127.0.0.53" && d != "127.0.0.54" {
			return false
		}
	}
	return true
}

// NetStatus reads the network state; it never fails (Available is false when
// "ip" cannot be run).
func (o *Ops) NetStatus(ctx context.Context) NetStatus {
	res := NetStatus{Interfaces: []NetIface{}, Gateways: []NetRoute{}, DNS: []string{}}
	out, code, err := o.Run.Run(ctx, "ip", "-j", "addr")
	if err != nil || code != 0 {
		return res
	}
	ifs, perr := parseIPAddr(out)
	if perr != nil {
		return res
	}
	res.Available, res.Interfaces = true, ifs
	for _, v := range []struct {
		family string
		argv   []string
	}{
		{"inet", []string{"ip", "-j", "route"}},
		{"inet6", []string{"ip", "-j", "-6", "route"}},
	} {
		if out, code, err := o.Run.Run(ctx, v.argv...); err == nil && code == 0 {
			if rs, err := parseIPRoute(out, v.family); err == nil {
				res.Gateways = append(res.Gateways, rs...)
			}
		}
	}
	path := o.ResolvConf
	if path == "" {
		path = DefaultResolvConf
	}
	if b, err := os.ReadFile(path); err == nil {
		res.DNS = parseResolvConf(string(b))
		if len(res.DNS) > 0 {
			res.DNSSource = "resolv.conf"
		}
	}
	if len(res.DNS) == 0 || isStubResolver(res.DNS) {
		if out, code, err := o.Run.Run(ctx, "resolvectl", "status"); err == nil && code == 0 {
			if dns := parseResolvectl(out); len(dns) > 0 {
				res.DNS, res.DNSSource = dns, "resolvectl"
			}
		}
	}
	return res
}
