package iax2

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
)

// DefaultNodeDomain is the AllStarLink DNS zone that publishes nodes.
const DefaultNodeDomain = "nodes.allstarlink.org"

// DefaultPort is the standard IAX2 port, used when a node has no SRV
// record. The client never binds it by default.
const DefaultPort = 4569

// ErrNodeNotFound means DNS has no usable record for the node.
var ErrNodeNotFound = errors.New("iax2: node not found in DNS")

// DNS is the resolver interface Resolver needs; *net.Resolver satisfies it.
type DNS interface {
	LookupSRV(ctx context.Context, service, proto, name string) (string, []*net.SRV, error)
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// Resolver maps an AllStar node number to the IAX2 address app_rpt would
// dial: the SRV record _iax._udp.<node>.<domain> and the IPv4 address of its
// target, falling back to the A record of <node>.<domain> on port 4569 when
// there is no SRV record.
type Resolver struct {
	DNS    DNS    // default net.DefaultResolver
	Domain string // default DefaultNodeDomain
}

// Resolve returns the node's address.
func (r *Resolver) Resolve(ctx context.Context, node string) (netip.AddrPort, error) {
	if len(node) < 4 || !allDigits(node) {
		return netip.AddrPort{}, fmt.Errorf("iax2: resolve %q: not a node number", node)
	}
	dns := r.DNS
	if dns == nil {
		dns = net.DefaultResolver
	}
	domain := strings.TrimSuffix(r.Domain, ".")
	if domain == "" {
		domain = DefaultNodeDomain
	}
	name := node + "." + domain

	_, srvs, srvErr := dns.LookupSRV(ctx, "iax", "udp", name)
	if srvErr == nil && len(srvs) > 0 {
		// app_rpt takes the first record; net sorts them by priority and
		// weight already.
		target := strings.TrimSuffix(srvs[0].Target, ".")
		ip, err := firstIPv4(ctx, dns, target)
		if err != nil {
			return netip.AddrPort{}, fmt.Errorf("iax2: resolve node %s: SRV target %s: %w", node, target, err)
		}
		port := srvs[0].Port
		if port == 0 {
			port = DefaultPort
		}
		return netip.AddrPortFrom(ip, port), nil
	}
	if ctx.Err() != nil {
		return netip.AddrPort{}, fmt.Errorf("iax2: resolve node %s: %w", node, ctx.Err())
	}
	ip, err := firstIPv4(ctx, dns, name)
	if err != nil {
		return netip.AddrPort{}, fmt.Errorf("iax2: resolve node %s: %w", node, err)
	}
	return netip.AddrPortFrom(ip, DefaultPort), nil
}

func firstIPv4(ctx context.Context, dns DNS, host string) (netip.Addr, error) {
	ips, err := dns.LookupNetIP(ctx, "ip4", host)
	if err != nil {
		var de *net.DNSError
		if errors.As(err, &de) && de.IsNotFound {
			return netip.Addr{}, ErrNodeNotFound
		}
		return netip.Addr{}, err
	}
	for _, ip := range ips {
		if ip.Unmap().Is4() {
			return ip.Unmap(), nil
		}
	}
	return netip.Addr{}, ErrNodeNotFound
}
