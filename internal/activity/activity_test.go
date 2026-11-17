package activity

import (
	"io"
	"log"
	"path/filepath"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/rpt"
	"github.com/artw1/aslcp/internal/store"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func setup(t *testing.T) (*Recorder, *store.Store, *clock) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	r := New(st, log.New(io.Discard, "", 0))
	r.Now = c.now
	return r, st, c
}

func upd(node string, rx bool, links ...rpt.Link) rpt.Update {
	return rpt.Update{Node: node, Status: rpt.NodeStatus{Node: node, RxKeyed: rx, Links: links}}
}

func TestRecordsLinkAndLocalKeyups(t *testing.T) {
	r, st, c := setup(t)
	var got []store.Activity
	r.OnRecord = func(a store.Activity) { got = append(got, a) }

	l := func(node string, keyed bool) rpt.Link {
		return rpt.Link{Node: node, Mode: rpt.ModeTransceive, Keyed: keyed}
	}
	r.Process(upd("1000", false, l("2000", false)))
	r.Process(upd("1000", false, l("2000", true)))
	c.t = c.t.Add(5 * time.Second)
	r.Process(upd("1000", true, l("2000", true))) // local RX starts
	c.t = c.t.Add(3 * time.Second)
	r.Process(upd("1000", true, l("2000", false))) // link ends after 8s
	c.t = c.t.Add(1200 * time.Millisecond)
	r.Process(upd("1000", false, l("2000", false))) // local RX ends: 4.2s

	rows, _ := st.ListActivity(nil, 10)
	if len(rows) != 2 || len(got) != 2 {
		t.Fatalf("%+v", rows)
	}
	byLink := map[string]store.Activity{}
	for _, a := range rows {
		byLink[a.Link] = a
	}
	if a := byLink["2000"]; a.Duration != 8*time.Second || a.Kerchunk || a.Node != "1000" {
		t.Fatalf("%+v", a)
	}
	if a := byLink[Local]; a.Duration != 4200*time.Millisecond || a.Kerchunk {
		t.Fatalf("%+v", a)
	}
}

func TestKerchunkGlitchStaleAndRemovedLink(t *testing.T) {
	r, st, c := setup(t)
	link := func(keyed bool) rpt.Link { return rpt.Link{Node: "3000", Mode: rpt.ModeTransceive, Keyed: keyed} }
	// 1s keyup: recorded and flagged
	r.Process(upd("1", false, link(true)))
	c.t = c.t.Add(time.Second)
	r.Process(upd("1", false, link(false)))
	// 200ms glitch: ignored
	r.Process(upd("1", false, link(true)))
	c.t = c.t.Add(200 * time.Millisecond)
	r.Process(upd("1", false, link(false)))
	// stale in the middle: dropped
	r.Process(upd("1", false, link(true)))
	c.t = c.t.Add(5 * time.Second)
	r.Process(rpt.Update{Node: "1", Status: rpt.NodeStatus{Node: "1", Stale: true}})
	c.t = c.t.Add(5 * time.Second)
	r.Process(upd("1", false, link(false)))
	// link disappears while keyed: counts as the end
	r.Process(upd("1", false, link(true)))
	c.t = c.t.Add(4 * time.Second)
	r.Process(upd("1", false))
	// local monitor links are not network audio
	r.Process(upd("1", false, rpt.Link{Node: "9", Mode: rpt.ModeLocalMonitor, Keyed: true}))
	c.t = c.t.Add(4 * time.Second)
	r.Process(upd("1", false))

	rows, _ := st.ListActivity(nil, 10)
	if len(rows) != 2 {
		t.Fatalf("%+v", rows)
	}
	if rows[0].Duration != 4*time.Second || rows[0].Kerchunk {
		t.Fatalf("%+v", rows[0])
	}
	if rows[1].Duration != time.Second || !rows[1].Kerchunk {
		t.Fatalf("%+v", rows[1])
	}
}

func TestRetentionCleanup(t *testing.T) {
	r, st, c := setup(t)
	old := c.t.Add(-8 * 24 * time.Hour)
	_ = st.AddActivity(store.Activity{Node: "1", Link: "5", Start: old, Duration: 3 * time.Second})
	_ = st.AddActivity(store.Activity{Node: "1", Link: "6", Start: c.t.Add(-time.Hour), Duration: 3 * time.Second})
	r.Prune()
	rows, _ := st.ListActivity(nil, 10)
	if len(rows) != 1 || rows[0].Link != "6" {
		t.Fatalf("%+v", rows)
	}
}
