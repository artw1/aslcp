package api

import (
	"testing"

	"github.com/artw1/aslcp/internal/rpt"
)

// The dv snapshot's link.linked comes from the monitor, so a change of a
// node's links must trigger a rebuild; keying or flag changes must not.
func TestLinkStateChanged(t *testing.T) {
	yes := []rpt.Changes{
		{LinksAdded: []string{"1999"}}, {LinksRemoved: []string{"1999"}},
		{Initial: true}, {Recovered: true}, {Stale: true}, {Added: true}, {Removed: true}, {Resync: true},
	}
	for i, c := range yes {
		if !linkStateChanged(c) {
			t.Errorf("case %d: link state change not reported", i)
		}
	}
	for i, c := range []rpt.Changes{{}, {RxKeyed: true}, {TxKeyed: true}, {Network: true}, {Flags: true}} {
		if linkStateChanged(c) {
			t.Errorf("no-link case %d reported", i)
		}
	}
}
