package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/hamdb"
	"github.com/artw1/aslcp/internal/nowcalling"
	"github.com/artw1/aslcp/internal/radioid"
)

type hdbSettings map[string]string

func (s hdbSettings) GetSetting(k string) (string, error) { return s[k], nil }
func (s hdbSettings) SetSetting(k, v string) error        { s[k] = v; return nil }

// hamdbRig is a server with a radioid.net list that knows N0CALL, and a hamdb.org stand-in that knows N1CALL.
func hamdbRig(t *testing.T, on bool) (*Server, *int32, context.CancelFunc) {
	t.Helper()
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if r.URL.Path == "/v1/N1CALL/json/aslcp" {
			fmt.Fprint(w, `{"hamdb":{"callsign":{"fname":"PAT","name":"EXAMPLE","addr2":"SPRINGFIELD","state":"XX","country":"United States"},"messages":{"status":"OK"}}}`)
			return
		}
		fmt.Fprint(w, `{"hamdb":{"messages":{"status":"NOT_FOUND"}}}`)
	}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	csv := "RADIO_ID,CALLSIGN,FIRST_NAME,LAST_NAME,CITY,STATE,COUNTRY\n1234567,N0CALL,Alex,Example,Exampleton,XX,Exampleland\n"
	if err := os.WriteFile(filepath.Join(dir, radioid.FileName), []byte(csv), 0o600); err != nil {
		t.Fatal(err)
	}
	rid := radioid.New(radioid.Options{Dir: dir})
	rid.Load()
	set := hdbSettings{}
	if on {
		set[hamdb.SettingKey] = "true"
	}
	hd := hamdb.New(hamdb.Options{Dir: dir, BaseURL: srv.URL, Version: "test", Settings: set})
	s := &Server{d: Deps{RadioID: rid, HamDB: hd}, nc: &ncState{}}
	ctx, cancel := context.WithCancel(context.Background())
	go hd.Run(ctx)
	return s, &hits, cancel
}

func TestNcResolveRadioIDHitSkipsHamDB(t *testing.T) {
	s, hits, cancel := hamdbRig(t, true)
	defer cancel()
	c := &nowcalling.Call{Kind: nowcalling.KindYSF, Call: "N0CALL"}
	s.ncResolve(c)
	time.Sleep(100 * time.Millisecond)
	if c.Name != "Alex Example" || c.Source != "" || atomic.LoadInt32(hits) != 0 {
		t.Fatalf("%+v hits=%d", c, atomic.LoadInt32(hits))
	}
}

func TestNcResolveHamDBFillsInLater(t *testing.T) {
	s, hits, cancel := hamdbRig(t, true)
	defer cancel()
	c := &nowcalling.Call{Kind: nowcalling.KindYSF, Call: "N1CALL"}
	s.ncResolve(c) // never waits: the answer comes later
	if c.Name != "" {
		t.Fatalf("filled at once: %+v", c)
	}
	var got nowcalling.Call
	for i := 0; i < 100; i++ {
		got = nowcalling.Call{Kind: nowcalling.KindYSF, Call: "N1CALL"}
		s.ncResolve(&got)
		if got.Name != "" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got.Name != "Pat Example" || got.First != "Pat" || got.City != "Springfield" || got.State != "XX" || got.Source != "hamdb.org" {
		t.Fatalf("%+v", got)
	}
	if n, p := s.callerPerson("N1CALL"); n != "Pat Example" || p != "Springfield, XX" {
		t.Fatalf("callerPerson %q %q", n, p)
	}
	if atomic.LoadInt32(hits) != 1 {
		t.Fatalf("hits %d", atomic.LoadInt32(hits))
	}
	// DMR and AllStar calls never go to hamdb.org
	for _, k := range []string{nowcalling.KindDMR, nowcalling.KindAllStar} {
		x := &nowcalling.Call{Kind: k, Call: "N2CALL"}
		s.ncResolve(x)
	}
	time.Sleep(100 * time.Millisecond)
	if atomic.LoadInt32(hits) != 1 {
		t.Fatalf("hits %d", atomic.LoadInt32(hits))
	}
}

func TestNcResolveHamDBOffNeverCalls(t *testing.T) {
	s, hits, cancel := hamdbRig(t, false)
	defer cancel()
	c := &nowcalling.Call{Kind: nowcalling.KindM17, Call: "N1CALL"}
	s.ncResolve(c)
	if n, _ := s.callerPerson("N1CALL"); n != "" {
		t.Fatal(n)
	}
	time.Sleep(150 * time.Millisecond)
	if c.Name != "" || atomic.LoadInt32(hits) != 0 {
		t.Fatalf("%+v hits=%d", c, atomic.LoadInt32(hits))
	}
}

func TestTrackerRefillAfterHamDB(t *testing.T) {
	s, _, cancel := hamdbRig(t, true)
	defer cancel()
	tr := nowcalling.New()
	tr.Resolve = s.ncResolve
	s.nc.tr = tr
	var changes int32
	tr.OnChange = func(nowcalling.Snapshot) { atomic.AddInt32(&changes, 1) }
	s.d.HamDB.SetOnChange(s.hamdbChanged)
	tr.UpdateAllStar("1998", nil, true, false, false)
	tr.UpdateDV([]nowcalling.Talk{{Kind: nowcalling.KindYSF, Via: nowcalling.ViaEngine, Call: "N1CALL", Start: time.Now().Add(time.Second)}})
	for i := 0; i < 100; i++ {
		sn := tr.Snapshot()
		if sn.Current != nil && sn.Current.Name == "Pat Example" && atomic.LoadInt32(&changes) > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the talking call never got its name: %+v", tr.Snapshot())
}
