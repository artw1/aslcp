package susb

import (
	"context"
	"testing"
)

func TestUSRPNode(t *testing.T) {
	c := &fakeConfig{files: map[string]string{"rpt.conf": `
[node-main](!)
type=asl

[2006](node-main)
rxchannel = SimpleUSB/usb

[1999](node-main)
rxchannel = USRP/127.0.0.1:34001:32001
`}}
	if n, ok := USRPNode(context.Background(), c); !ok || n != "1999" {
		t.Fatalf("got %q %v", n, ok)
	}
	c.set("rpt.conf", "[2006]\nrxchannel=SimpleUSB/usb\n")
	if n, ok := USRPNode(context.Background(), c); ok {
		t.Fatalf("found %q", n)
	}
	if _, ok := USRPNode(context.Background(), &fakeConfig{files: map[string]string{}}); ok {
		t.Fatal("no file")
	}
}
