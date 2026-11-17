package api_test

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/schedules"
)

func ny(t *testing.T) *time.Location {
	t.Helper()
	l, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func schedBody(over map[string]any) map[string]any {
	b := map[string]any{"name": "Tuesday net", "enabled": true, "node": localNode, "target": "41234", "action": "connect",
		"mode": "transceive", "permanent": false, "exclusive": false, "days": []int{2}, "date": "", "start": "19:00", "end": "19:05"}
	for k, v := range over {
		b[k] = v
	}
	return b
}

func (e *env) snapshot(role string) schedules.Snapshot {
	e.t.Helper()
	var s schedules.Snapshot
	if c := e.do(role, "GET", "/api/schedules", nil, &s); c != 200 {
		e.t.Fatalf("GET /api/schedules: %d", c)
	}
	return s
}

func TestSchedulesUnavailable(t *testing.T) {
	e := newEnv(t, opts{})
	expectErr(t, e, "viewer", "GET", "/api/schedules", nil, 503, "schedules_unavailable")
	expectErr(t, e, "op", "POST", "/api/schedules", schedBody(nil), 503, "schedules_unavailable")
	expectErr(t, e, "op", "PUT", "/api/schedules/enabled", map[string]any{"enabled": false}, 503, "schedules_unavailable")
	// manual actions work without the feature
	if c := e.do("op", "POST", "/api/nodes/"+localNode+"/disconnect-all", nil, nil); c != 202 {
		t.Fatal(c)
	}
}

func TestSchedulesCRUDAndValidation(t *testing.T) {
	e := newEnv(t, opts{schedules: true})

	snap := e.snapshot("viewer")
	if !snap.Enabled || len(snap.Schedules) != 0 || snap.Timezone != "America/New_York" || len(snap.Upcoming) != 0 {
		t.Fatalf("empty snapshot: %+v", snap)
	}

	var created schedules.Schedule
	if c := e.do("op", "POST", "/api/schedules", schedBody(nil), &created); c != 201 {
		t.Fatalf("create: %d", c)
	}
	if created.ID == 0 || created.Name != "Tuesday net" || !created.Enabled || created.Target != "41234" ||
		created.Start != "19:00" || created.End != "19:05" || len(created.Days) != 1 || created.Days[0] != 2 || created.LastRun != nil {
		t.Fatalf("created: %+v", created)
	}
	path := "/api/schedules/" + itoa(created.ID)

	snap = e.snapshot("viewer")
	if len(snap.Schedules) != 1 || len(snap.Upcoming) != 4 || snap.Upcoming[0].Action != "connect" ||
		snap.Upcoming[0].At.Format(time.RFC3339) != "2026-11-17T19:00:00-05:00" {
		t.Fatalf("snapshot: %+v", snap)
	}

	// full replace: a one-off disconnect (mode and exclusive are normalised away)
	var upd schedules.Schedule
	if c := e.do("op", "PUT", path, schedBody(map[string]any{"action": "disconnect", "end": "", "days": []int{}, "date": "2026-11-18",
		"mode": "monitor", "exclusive": true, "enabled": false, "name": " Evening "}), &upd); c != 200 {
		t.Fatalf("update: %d", c)
	}
	if upd.ID != created.ID || upd.Action != "disconnect" || upd.Date != "2026-11-18" || len(upd.Days) != 0 || upd.Mode != "" || upd.Exclusive ||
		upd.Enabled || upd.Name != "Evening" {
		t.Fatalf("updated: %+v", upd)
	}
	expectErr(t, e, "op", "PUT", "/api/schedules/9999", schedBody(nil), 404, "not_found")

	// validation
	bad := []map[string]any{
		{"target": "N0AAA"},
		{"target": localNode},
		{"target": ""},
		{"days": []int{}, "date": ""},
		{"days": []int{1}, "date": "2026-12-01"},
		{"days": []int{7}},
		{"days": []int{}, "date": "2026-02-30"},
		{"action": "disconnect", "end": "20:00"},
		{"action": "toggle"},
		{"end": "19:00"},
		{"start": "7:00"},
		{"start": "24:00"},
		{"end": "19:60"},
		{"mode": "listen"},
		{"name": strings.Repeat("x", 61)},
	}
	for _, b := range bad {
		expectErr(t, e, "op", "POST", "/api/schedules", schedBody(b), 400, "bad_request")
		expectErr(t, e, "op", "PUT", path, schedBody(b), 400, "bad_request")
	}
	expectErr(t, e, "op", "POST", "/api/schedules", schedBody(map[string]any{"node": "4242"}), 400, "unknown_node")
	expectErr(t, e, "op", "PUT", "/api/schedules/abc", schedBody(nil), 400, "bad_request")
	if got := e.snapshot("op").Schedules; len(got) != 1 {
		t.Fatalf("invalid bodies were stored: %+v", got)
	}

	// midnight-crossing end is valid
	if c := e.do("op", "POST", "/api/schedules", schedBody(map[string]any{"start": "23:00", "end": "01:00"}), nil); c != 201 {
		t.Fatal(c)
	}

	if c := e.do("op", "DELETE", path, nil, nil); c != 204 {
		t.Fatalf("delete: %d", c)
	}
	expectErr(t, e, "op", "DELETE", path, nil, 404, "not_found")
	if got := e.snapshot("op").Schedules; len(got) != 1 {
		t.Fatalf("after delete: %+v", got)
	}
}

func TestSchedulesRoles(t *testing.T) {
	e := newEnv(t, opts{schedules: true})
	var s schedules.Schedule
	if c := e.do("admin", "POST", "/api/schedules", schedBody(nil), &s); c != 201 {
		t.Fatal(c)
	}
	path := "/api/schedules/" + itoa(s.ID)
	for _, role := range []string{"viewer", "op", "admin"} {
		if e.do(role, "GET", "/api/schedules", nil, nil) != 200 {
			t.Fatalf("%s cannot read", role)
		}
	}
	expectErr(t, e, "viewer", "POST", "/api/schedules", schedBody(nil), 403, "forbidden")
	expectErr(t, e, "viewer", "PUT", path, schedBody(nil), 403, "forbidden")
	expectErr(t, e, "viewer", "DELETE", path, nil, 403, "forbidden")
	expectErr(t, e, "viewer", "PUT", "/api/schedules/enabled", map[string]any{"enabled": false}, 403, "forbidden")
	expectErr(t, e, "", "GET", "/api/schedules", nil, 401, "unauthorized")
	if !e.snapshot("viewer").Enabled {
		t.Fatal("viewer changed the switch")
	}
}

func TestSchedulesEnabledSwitch(t *testing.T) {
	e := newEnv(t, opts{schedules: true})
	var snap schedules.Snapshot
	if c := e.do("op", "PUT", "/api/schedules/enabled", map[string]any{"enabled": false}, &snap); c != 200 || snap.Enabled {
		t.Fatalf("%d %+v", c, snap)
	}
	if e.snapshot("viewer").Enabled {
		t.Fatal("not persisted")
	}
	if v, _ := e.st.GetSetting("schedules.enabled"); v != "false" {
		t.Fatalf("setting %q", v)
	}
	if c := e.do("op", "PUT", "/api/schedules/enabled", map[string]any{"enabled": true}, &snap); c != 200 || !snap.Enabled {
		t.Fatalf("%d %+v", c, snap)
	}
	expectErr(t, e, "op", "PUT", "/api/schedules/enabled", map[string]any{}, 400, "bad_request")
}

func TestManualActionsPauseSchedules(t *testing.T) {
	e := newEnv(t, opts{schedules: true})
	cases := []struct {
		name, path string
		body       any
		reason     string
	}{
		{"connect", "/connect", map[string]any{"target": "2000"}, "manual connect 2000 on 1997 by op"},
		{"disconnect", "/disconnect", map[string]any{"target": "50241"}, "manual disconnect 50241 on 1997 by op"},
		{"disconnect-all", "/disconnect-all", nil, "manual disconnect all on 1997 by op"},
	}
	for _, c := range cases {
		e.do("op", "PUT", "/api/schedules/enabled", map[string]any{"enabled": true}, nil)
		if !e.snapshot("op").Enabled {
			t.Fatal("not enabled")
		}
		if code := e.do("op", "POST", "/api/nodes/"+localNode+c.path, c.body, nil); code != 202 {
			t.Fatalf("%s: %d", c.name, code)
		}
		s := e.snapshot("viewer")
		if s.Enabled || s.PausedReason != c.reason || s.PausedAt == nil {
			t.Fatalf("%s: %+v", c.name, s)
		}
	}
	// a rejected command does not pause
	e.do("op", "PUT", "/api/schedules/enabled", map[string]any{"enabled": true}, nil)
	expectErr(t, e, "op", "POST", "/api/nodes/"+localNode+"/connect", map[string]any{"target": "N0AAA"}, 400, "bad_target")
	expectErr(t, e, "viewer", "POST", "/api/nodes/"+localNode+"/disconnect-all", nil, 403, "forbidden")
	if !e.snapshot("op").Enabled {
		t.Fatal("paused by a rejected command")
	}
}

func TestSchedulesEndToEnd(t *testing.T) {
	e := newEnv(t, opts{schedules: true})
	l := ny(t)
	st := e.stream("viewer")
	defer st.cancel()

	var s schedules.Schedule
	if c := e.do("op", "POST", "/api/schedules", schedBody(nil), &s); c != 201 { // Tuesdays 19:00-19:05
		t.Fatal(c)
	}
	st.wait(func(ev sseEvent) bool {
		return ev.name == "schedules" && strings.Contains(ev.data, `"schedules":[{"id":`)
	})

	run := func(at time.Time) []string {
		e.clk.set(at)
		before := len(e.fake.ilinkCommands())
		e.eng.RunOnce(context.Background())
		return e.fake.ilinkCommands()[before:]
	}
	join := func(l []string) string { return strings.Join(l, "|") }

	if got := run(time.Date(2026, 11, 17, 18, 59, 55, 0, l)); len(got) != 0 {
		t.Fatalf("early: %v", got)
	}
	got := run(time.Date(2026, 11, 17, 19, 0, 5, 0, l))
	if join(got) != "rpt cmd 1997 ilink 3 41234" {
		t.Fatalf("start: %v", got)
	}
	snap := e.snapshot("viewer")
	if !snap.Enabled || snap.PausedReason != "" {
		t.Fatalf("the scheduler's own command paused the schedules: %+v", snap)
	}
	if len(snap.Active) != 1 || snap.Active[0].ID != s.ID || snap.Schedules[0].LastResult != "ok" || snap.Schedules[0].LastRun == nil {
		t.Fatalf("snapshot after start: %+v", snap)
	}
	st.wait(func(ev sseEvent) bool {
		return ev.name == "schedules" && strings.Contains(ev.data, `"lastResult":"ok"`)
	})

	// the node now shows the link (the fake node does not follow commands)
	e.setLinks("50241", "41234")
	got = run(time.Date(2026, 11, 17, 19, 5, 5, 0, l))
	if join(got) != "rpt cmd 1997 ilink 1 41234" {
		t.Fatalf("end: %v", got)
	}
	if s := e.snapshot("op"); !s.Enabled || len(s.Active) != 0 {
		t.Fatalf("after end: %+v", s)
	}
}

func TestSchedulesExclusiveAndPermanentCommands(t *testing.T) {
	e := newEnv(t, opts{schedules: true})
	l := ny(t)
	e.setLinks("50241", "1999")
	if c := e.do("op", "POST", "/api/schedules", schedBody(map[string]any{"mode": "monitor", "permanent": true, "exclusive": true, "end": ""}), nil); c != 201 {
		t.Fatal(c)
	}
	e.clk.set(time.Date(2026, 11, 17, 19, 0, 5, 0, l))
	before := len(e.fake.ilinkCommands())
	e.eng.RunOnce(context.Background())
	got := strings.Join(e.fake.ilinkCommands()[before:], "|")
	want := "rpt cmd 1997 ilink 1 50241|rpt cmd 1997 ilink 1 1999|rpt cmd 1997 ilink 12 41234"
	if got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}

func TestSchedulesAlreadyLinkedIsSkipped(t *testing.T) {
	e := newEnv(t, opts{schedules: true})
	l := ny(t)
	e.setLinks("50241", "41234") // linked as transceive by the fake
	if c := e.do("op", "POST", "/api/schedules", schedBody(map[string]any{"end": ""}), nil); c != 201 {
		t.Fatal(c)
	}
	e.clk.set(time.Date(2026, 11, 17, 19, 0, 5, 0, l))
	before := len(e.fake.ilinkCommands())
	e.eng.RunOnce(context.Background())
	if got := e.fake.ilinkCommands()[before:]; len(got) != 0 {
		t.Fatalf("commands: %v", got)
	}
	if r := e.snapshot("op").Schedules[0].LastResult; r != "skipped: already connected" {
		t.Fatalf("result %q", r)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
