package iax2

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"reflect"
	"testing"
)

func TestParseLinkText(t *testing.T) {
	cases := []struct {
		in   string
		want LinkText
	}{
		{"!NEWKEY1!\x00", LinkText{Kind: TextKindNewKey1, Raw: "!NEWKEY1!"}},
		{"!NEWKEY!", LinkText{Kind: TextKindNewKey, Raw: "!NEWKEY!"}},
		{"!IAXKEY! 1", LinkText{Kind: TextKindIAXKey, Raw: "!IAXKEY! 1"}},
		{"!!DISCONNECT!!\x00", LinkText{Kind: TextKindDisconnect, Raw: "!!DISCONNECT!!"}},
		{"L T2000,R2001,C2002,T1997\x00", LinkText{Kind: TextKindLinkList, Raw: "L T2000,R2001,C2002,T1997",
			Links: []LinkEntry{{ModeTransceive, "2000"}, {ModeReceive, "2001"}, {ModeConnecting, "2002"}, {ModeTransceive, "1997"}}}},
		{"L ", LinkText{Kind: TextKindLinkList, Raw: "L "}},
		{"L", LinkText{Kind: TextKindLinkList, Raw: "L"}},
		{"L T2000,X2001,T,Tabc,R1998", LinkText{Kind: TextKindLinkList, Raw: "L T2000,X2001,T,Tabc,R1998",
			Links: []LinkEntry{{ModeTransceive, "2000"}, {ModeReceive, "1998"}}}},
		{"M 2000 0 hello there", LinkText{Kind: TextKindMessage, Raw: "M 2000 0 hello there", From: "2000", To: "0", Body: "hello there"}},
		{"T 2000 1999 CONNECTED", LinkText{Kind: TextKindTelemetry, Raw: "T 2000 1999 CONNECTED", From: "2000", To: "1999", Body: "CONNECTED"}},
		{"M 2000", LinkText{Kind: TextKindOther, Raw: "M 2000"}},
		{"D 2000 1999 12 5", LinkText{Kind: TextKindDTMF, Raw: "D 2000 1999 12 5"}},
		{"G 1234", LinkText{Kind: TextKindGPS, Raw: "G 1234"}},
		{"", LinkText{Kind: TextKindOther}},
		{"hello", LinkText{Kind: TextKindOther, Raw: "hello"}},
	}
	for _, tc := range cases {
		if got := ParseLinkText([]byte(tc.in)); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q: got %+v, want %+v", tc.in, got, tc.want)
		}
	}
}

func TestFormatLinkList(t *testing.T) {
	cases := []struct {
		in   []LinkEntry
		want string
		err  bool
	}{
		{nil, "L ", false},
		{[]LinkEntry{{ModeTransceive, "1997"}}, "L T1997", false},
		{[]LinkEntry{{ModeTransceive, "2000"}, {ModeReceive, "2001"}}, "L T2000,R2001", false},
		{[]LinkEntry{{ModeLocal, "2000"}}, "", true},
		{[]LinkEntry{{ModeTransceive, "20,00"}}, "", true},
	}
	for _, tc := range cases {
		got, err := FormatLinkList(tc.in)
		if (err != nil) != tc.err || got != tc.want {
			t.Errorf("%v: got %q %v", tc.in, got, err)
		}
		if err == nil {
			if back := ParseLinkText([]byte(got)); !reflect.DeepEqual(back.Links, tc.in) {
				t.Errorf("%v: parsed back %v", tc.in, back.Links)
			}
		}
	}
}

type fakeDNS struct {
	srv map[string][]*net.SRV
	a   map[string][]netip.Addr
}

func (f *fakeDNS) LookupSRV(_ context.Context, service, proto, name string) (string, []*net.SRV, error) {
	q := "_" + service + "._" + proto + "." + name
	if r, ok := f.srv[q]; ok {
		return q, r, nil
	}
	return "", nil, &net.DNSError{Err: "no such host", Name: q, IsNotFound: true}
}

func (f *fakeDNS) LookupNetIP(_ context.Context, network, host string) ([]netip.Addr, error) {
	if network != "ip4" {
		return nil, errors.New("unexpected network " + network)
	}
	if r, ok := f.a[host]; ok {
		return r, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
}

func TestResolver(t *testing.T) {
	dns := &fakeDNS{
		srv: map[string][]*net.SRV{
			"_iax._udp.2000.nodes.allstarlink.org": {{Target: "2000.nodes.allstarlink.org.", Port: 4570}},
			"_iax._udp.2002.nodes.allstarlink.org": {{Target: "gone.example.net.", Port: 4569}},
			"_iax._udp.2001.test.example.net":      {{Target: "radio.example.net", Port: 0}},
		},
		a: map[string][]netip.Addr{
			"2000.nodes.allstarlink.org": {netip.MustParseAddr("2001:db8::1"), netip.MustParseAddr("192.0.2.10")},
			"1998.nodes.allstarlink.org": {netip.MustParseAddr("::ffff:203.0.113.5")},
			"radio.example.net":          {netip.MustParseAddr("192.0.2.20")},
		},
	}
	cases := []struct {
		node, domain string
		want         string
		err          error
	}{
		{"2000", "", "192.0.2.10:4570", nil},
		{"1998", "", "203.0.113.5:4569", nil}, // no SRV: A record, default port
		{"2001", "test.example.net.", "192.0.2.20:4569", nil},
		{"2002", "", "", ErrNodeNotFound},
		{"1999", "", "", ErrNodeNotFound},
		{"199", "", "", nil},
		{"19a9", "", "", nil},
	}
	for _, tc := range cases {
		r := &Resolver{DNS: dns, Domain: tc.domain}
		got, err := r.Resolve(context.Background(), tc.node)
		if tc.want == "" {
			if err == nil {
				t.Errorf("%s: no error", tc.node)
			} else if tc.err != nil && !errors.Is(err, tc.err) {
				t.Errorf("%s: err %v, want %v", tc.node, err, tc.err)
			}
			continue
		}
		if err != nil || got.String() != tc.want {
			t.Errorf("%s: got %v %v, want %s", tc.node, got, err, tc.want)
		}
	}
}
