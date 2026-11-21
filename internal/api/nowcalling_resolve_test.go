package api

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/nodelist"
	"github.com/artw1/aslcp/internal/nowcalling"
	"github.com/artw1/aslcp/internal/radioid"
)

// An AllStar call keeps the node's callsign, description and location even when
// the radioid.net list knows the callsign as a person.
func TestNcResolveAllStarIgnoresRadioID(t *testing.T) {
	dir := t.TempDir()
	csv := "RADIO_ID,CALLSIGN,FIRST_NAME,LAST_NAME,CITY,STATE,COUNTRY\n1234567,N0CALL,Alex,Example,Springfield,Illinois,United States\n"
	if err := os.WriteFile(filepath.Join(dir, radioid.FileName), []byte(csv), 0o600); err != nil {
		t.Fatal(err)
	}
	rid := radioid.New(radioid.Options{Dir: dir})
	rid.Load()
	if _, ok := rid.ByCallsign("N0CALL"); !ok {
		t.Fatal("test setup: radioid list did not load")
	}
	nl := nodelist.New(nodelist.Options{})
	nl.Set([]nodelist.Info{{Node: "1997", Callsign: "N0CALL", Description: "Example hub", Location: "Example Hill"}}, "test", time.Now())
	s := &Server{d: Deps{Nodelist: nl, RadioID: rid}, nc: &ncState{}}

	c := &nowcalling.Call{Kind: nowcalling.KindAllStar, Node: "1997"}
	s.ncResolve(c)
	if c.Call != "N0CALL" || c.Desc != "Example hub" || c.Location != "Example Hill" {
		t.Fatalf("node info lost: %+v", c)
	}
	if c.First != "" || c.Name != "" || c.City != "" || c.State != "" || c.Country != "" {
		t.Fatalf("person info leaked into an AllStar call: %+v", c)
	}

	// A YSF call with the same callsign still gets the person.
	y := &nowcalling.Call{Kind: nowcalling.KindYSF, Call: "N0CALL"}
	s.ncResolve(y)
	if y.First != "Alex" || y.City != "Springfield" {
		t.Fatalf("YSF lookup broke: %+v", y)
	}
}
