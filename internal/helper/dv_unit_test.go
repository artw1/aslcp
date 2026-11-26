package helper

import (
	"os"
	"strings"
	"testing"
)

// The node reaches DNS and the masters and reflectors on the internet: its
// unit must not carry an IP allow or deny list.
func TestNodeServiceUnitHasNoIPFilter(t *testing.T) {
	b, err := os.ReadFile("../../packaging/systemd/aslcp-node.service")
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"IPAddressDeny", "IPAddressAllow"} {
		if strings.Contains(string(b), k) {
			t.Errorf("aslcp-node.service must not contain %s", k)
		}
	}
}
