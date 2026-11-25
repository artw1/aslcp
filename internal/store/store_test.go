package store

import (
	"errors"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/legacy"
	"github.com/artw1/aslcp/internal/schedules"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestMigrationsIdempotent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "m.db")
	s, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	v, _ := s.Version()
	if v != len(migrations) {
		t.Fatalf("version %d", v)
	}
	s.CreateUser("a", "h", RoleAdmin)
	s.Close()
	s, err = Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if n, _ := s.CountUsers(); n != 1 {
		t.Fatal("data lost")
	}
	if v2, _ := s.Version(); v2 != v {
		t.Fatal("version changed")
	}
}

func TestUsers(t *testing.T) {
	s := open(t)
	u, err := s.CreateUser("Alice", "hash", RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser("alice", "x", RoleViewer); !errors.Is(err, ErrExists) {
		t.Fatalf("want ErrExists got %v", err)
	}
	got, err := s.UserByName("ALICE")
	if err != nil || got.ID != u.ID || got.PWHash != "hash" {
		t.Fatal(got, err)
	}
	r, h := RoleOperator, "h2"
	if err := s.UpdateUser(u.ID, &r, &h); err != nil {
		t.Fatal(err)
	}
	got, _ = s.UserByID(u.ID)
	if got.Role != RoleOperator || got.PWHash != "h2" {
		t.Fatal(got)
	}
	if n, _ := s.CountAdmins(); n != 0 {
		t.Fatal("admins", n)
	}
	l, _ := s.ListUsers()
	if len(l) != 1 || l[0].PWHash != "" {
		t.Fatal(l)
	}
	if _, err := s.UserByName("nobody"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := s.UpdateUser(999, &r, nil); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := s.DeleteUser(u.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUser(u.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestSessions(t *testing.T) {
	s := open(t)
	u, _ := s.CreateUser("bob", "h", RoleViewer)
	now := time.Now()
	if err := s.CreateSession("th", u.ID, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	se, uu, err := s.SessionByHash("th", now)
	if err != nil || uu.Username != "bob" || se.UserID != u.ID {
		t.Fatal(err, uu)
	}
	if _, _, err := s.SessionByHash("th", now.Add(2*time.Hour)); !errors.Is(err, ErrNotFound) {
		t.Fatal("expired session valid")
	}
	if err := s.TouchSession("th", now, now.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.SessionByHash("th", now.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	s.CreateSession("old", u.ID, now.Add(-time.Hour))
	if n, _ := s.DeleteExpiredSessions(now); n != 1 {
		t.Fatal(n)
	}
	s.CreateSession("other", u.ID, now.Add(time.Hour))
	s.DeleteUserSessions(u.ID, "th")
	if _, _, err := s.SessionByHash("other", now); !errors.Is(err, ErrNotFound) {
		t.Fatal("other survived")
	}
	if _, _, err := s.SessionByHash("th", now); err != nil {
		t.Fatal("kept session removed")
	}
	// cascade
	s.DeleteUser(u.ID)
	if _, _, err := s.SessionByHash("th", now); !errors.Is(err, ErrNotFound) {
		t.Fatal("cascade failed")
	}
}

func TestFavorites(t *testing.T) {
	s := open(t)
	a, _ := s.AddFavorite("1000", "A", "")
	b, _ := s.AddFavorite("2000", "B", "Hubs")
	c, _ := s.AddFavorite("3000", "C", "Hubs")
	if a.Position != 0 || b.Position != 1 || c.Position != 2 {
		t.Fatal(a, b, c)
	}
	l, _ := s.ListFavorites()
	if len(l) != 3 || l[0].ID != a.ID || l[1].ID != b.ID {
		t.Fatal(l)
	}
	if err := s.ReorderFavorites([]int64{c.ID, b.ID}); err != nil {
		t.Fatal(err)
	}
	l, _ = s.ListFavorites()
	// group "" first (a), then Hubs: c, b
	if l[0].ID != a.ID || l[1].ID != c.ID || l[2].ID != b.ID {
		t.Fatal(l)
	}
	lbl := "Renamed"
	f, err := s.UpdateFavorite(a.ID, nil, &lbl, nil, nil)
	if err != nil || f.Label != "Renamed" || f.Node != "1000" {
		t.Fatal(f, err)
	}
	if err := s.ReorderFavorites([]int64{12345}); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	if err := s.DeleteFavorite(a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetFavorite(a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestSettings(t *testing.T) {
	s := open(t)
	if _, err := s.GetSetting("k"); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	s.SetSetting("k", "1")
	s.SetSetting("k", "2")
	if v, _ := s.GetSetting("k"); v != "2" {
		t.Fatal(v)
	}
}

func TestActivityListAndPrune(t *testing.T) {
	s := open(t)
	now := time.Now().UTC().Truncate(time.Second)
	add := func(node, link string, ago time.Duration, d time.Duration) {
		t.Helper()
		if err := s.AddActivity(Activity{Node: node, Link: link, Start: now.Add(-ago), Duration: d, Kerchunk: d < 1500*time.Millisecond}); err != nil {
			t.Fatal(err)
		}
	}
	add("1", "100", 8*24*time.Hour, 5*time.Second) // too old
	add("1", "101", 3*time.Hour, 4*time.Second)
	add("2", "local", 2*time.Hour, 1200*time.Millisecond)
	add("1", "102", time.Hour, 9*time.Second)

	got, err := s.ListActivity(nil, 10)
	if err != nil || len(got) != 4 || got[0].Link != "102" || got[3].Link != "100" {
		t.Fatalf("%+v %v", got, err)
	}
	if !got[1].Kerchunk || got[1].Duration != 1200*time.Millisecond || got[1].Link != "local" {
		t.Fatalf("%+v", got[1])
	}
	if got, _ := s.ListActivity([]string{"2"}, 10); len(got) != 1 || got[0].Node != "2" {
		t.Fatalf("%+v", got)
	}
	if got, _ := s.ListActivity(nil, 2); len(got) != 2 {
		t.Fatalf("limit: %+v", got)
	}

	if n, err := s.PruneActivity(now.Add(-7*24*time.Hour), 0); err != nil || n != 1 {
		t.Fatalf("age prune: %d %v", n, err)
	}
	// cap: keep the newest 2
	if n, err := s.PruneActivity(now.Add(-7*24*time.Hour), 2); err != nil || n != 1 {
		t.Fatalf("cap prune: %d %v", n, err)
	}
	got, _ = s.ListActivity(nil, 10)
	if len(got) != 2 || got[0].Link != "102" || got[1].Link != "local" {
		t.Fatalf("%+v", got)
	}
}

func TestAudit(t *testing.T) {
	s := open(t)
	for _, a := range []string{"config.apply", "config.read"} {
		if err := s.AddAudit(Audit{Username: "admin", Action: a, Detail: `{"path":"rpt.conf"}`}); err != nil {
			t.Fatal(err)
		}
	}
	l, err := s.ListAudit(10)
	if err != nil || len(l) != 2 || l[0].Action != "config.read" || l[0].Username != "admin" || l[0].Time.IsZero() {
		t.Fatalf("%+v %v", l, err)
	}
	if l, _ := s.ListAudit(1); len(l) != 1 {
		t.Fatal("limit")
	}
}

func TestAuditPage(t *testing.T) {
	s := open(t)
	for _, e := range [][2]string{{"admin", "system.hostname"}, {"op", "radio.save"}, {"admin", "system.reboot"}, {"admin", "config.apply"}, {"admin", "sys_tem%"}} {
		if err := s.AddAudit(Audit{Username: e[0], Action: e[1]}); err != nil {
			t.Fatal(err)
		}
	}
	all, _ := s.ListAuditPage(AuditFilter{})
	if len(all) != 5 || all[0].Action != "sys_tem%" {
		t.Fatalf("%+v", all)
	}
	// paging by id: no overlap, no gap, newest first
	p1, _ := s.ListAuditPage(AuditFilter{Limit: 2})
	p2, _ := s.ListAuditPage(AuditFilter{Limit: 2, BeforeID: p1[1].ID})
	p3, _ := s.ListAuditPage(AuditFilter{Limit: 2, BeforeID: p2[1].ID})
	if len(p1) != 2 || len(p2) != 2 || len(p3) != 1 || p1[1].ID <= p2[0].ID || p2[1].ID <= p3[0].ID {
		t.Fatalf("%+v %+v %+v", p1, p2, p3)
	}
	// a new entry does not shift the next page
	_ = s.AddAudit(Audit{Username: "admin", Action: "new"})
	if q, _ := s.ListAuditPage(AuditFilter{Limit: 2, BeforeID: p1[1].ID}); q[0].ID != p2[0].ID {
		t.Fatal("page shifted")
	}
	if l, _ := s.ListAuditPage(AuditFilter{User: "op"}); len(l) != 1 || l[0].Action != "radio.save" {
		t.Fatalf("%+v", l)
	}
	if l, _ := s.ListAuditPage(AuditFilter{ActionPrefix: "system."}); len(l) != 2 {
		t.Fatalf("%+v", l)
	}
	// wildcards in the prefix are literal
	if l, _ := s.ListAuditPage(AuditFilter{ActionPrefix: "sys_"}); len(l) != 1 || l[0].Action != "sys_tem%" {
		t.Fatalf("%+v", l)
	}
	if l, _ := s.ListAuditPage(AuditFilter{ActionPrefix: "%"}); len(l) != 0 {
		t.Fatalf("%+v", l)
	}
}

func TestSchedules(t *testing.T) {
	s := open(t)
	if v, _ := s.Version(); v < 4 {
		t.Fatalf("schema version %d", v)
	}
	l, err := s.ListSchedules()
	if err != nil || len(l) != 0 {
		t.Fatalf("empty list: %v %v", l, err)
	}
	a, err := s.AddSchedule(schedules.Schedule{Name: "Net", Enabled: true, Node: "1997", Target: "41234", Action: "connect",
		Mode: "monitor", Permanent: true, Exclusive: true, Days: []int{2, 4}, Start: "19:00", End: "20:30"})
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == 0 || a.Name != "Net" || !a.Enabled || !a.Permanent || !a.Exclusive || a.Mode != "monitor" ||
		len(a.Days) != 2 || a.Days[0] != 2 || a.Days[1] != 4 || a.Start != "19:00" || a.End != "20:30" || a.LastRun != nil {
		t.Fatalf("added: %+v", a)
	}
	b, err := s.AddSchedule(schedules.Schedule{Node: "1997", Target: "50241", Action: "disconnect", Days: []int{}, Date: "2026-12-01", Start: "07:00"})
	if err != nil {
		t.Fatal(err)
	}
	if b.Date != "2026-12-01" || len(b.Days) != 0 || b.Days == nil || b.Enabled {
		t.Fatalf("once: %+v", b)
	}
	a.Name, a.Days, a.End = "Net 2", []int{0}, ""
	u, err := s.UpdateSchedule(a)
	if err != nil || u.Name != "Net 2" || len(u.Days) != 1 || u.End != "" {
		t.Fatalf("update: %+v %v", u, err)
	}
	if _, err := s.UpdateSchedule(schedules.Schedule{ID: 999, Node: "1", Target: "2", Action: "connect", Start: "00:00"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing: %v", err)
	}
	at := time.Unix(1_800_000_000, 0)
	if err := s.SetScheduleResult(a.ID, at, "ok"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetScheduleEnabled(b.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetScheduleEnabled(999, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("enable missing: %v", err)
	}
	g, _ := s.GetSchedule(a.ID)
	if g.LastRun == nil || !g.LastRun.Equal(at) || g.LastResult != "ok" {
		t.Fatalf("result: %+v", g)
	}
	if g, _ := s.GetSchedule(b.ID); !g.Enabled {
		t.Fatal("enable did not stick")
	}
	if err := s.DeleteSchedule(a.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSchedule(a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
	if _, err := s.GetSchedule(a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get deleted: %v", err)
	}
	if l, _ := s.ListSchedules(); len(l) != 1 {
		t.Fatalf("list: %v", l)
	}
}

func TestFavoritePins(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a, _ := st.AddFavorite("1000", "A", "")
	d, _ := st.AddDVFavorite("YSF", "12345", "Room")
	if a.Pinned || d.Pinned {
		t.Fatal("new favourites are not pinned")
	}
	yes := true
	if f, err := st.UpdateFavorite(a.ID, nil, nil, nil, &yes); err != nil || !f.Pinned {
		t.Fatalf("%+v %v", f, err)
	}
	if f, err := st.UpdateDVFavorite(d.ID, nil, nil, &yes); err != nil || !f.Pinned {
		t.Fatalf("%+v %v", f, err)
	}
	lbl := "B"
	if f, _ := st.UpdateFavorite(a.ID, nil, &lbl, nil, nil); !f.Pinned {
		t.Fatal("an edit without pinned keeps the pin")
	}
	fl, _ := st.ListFavorites()
	dl, _ := st.ListDVFavorites()
	if !fl[0].Pinned || !dl[0].Pinned {
		t.Fatalf("lists: %+v %+v", fl, dl)
	}
}

func TestDVFavorites(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a, err := st.AddDVFavorite("YSF", "12345", "Room A")
	if err != nil || a.Sort != 0 {
		t.Fatalf("add: %+v %v", a, err)
	}
	b, _ := st.AddDVFavorite("YSF", "54321", "Room B")
	if b.Sort != 1 {
		t.Fatalf("sort %d", b.Sort)
	}
	if _, err := st.AddDVFavorite("YSF", "12345", "again"); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err := st.AddDVFavorite("P25", "12345", "same value other mode"); err != nil {
		t.Fatal(err)
	}
	n := "Renamed"
	s0 := 5
	if f, err := st.UpdateDVFavorite(a.ID, &n, &s0, nil); err != nil || f.Name != n || f.Sort != 5 {
		t.Fatalf("update: %+v %v", f, err)
	}
	l, _ := st.ListDVFavorites()
	if len(l) != 3 || l[0].Mode != "P25" || l[1].ID != b.ID || l[2].ID != a.ID {
		t.Fatalf("list order: %+v", l)
	}
	if err := st.DeleteDVFavorite(a.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteDVFavorite(a.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
	for i := 0; i < MaxDVFavoritesPerMode; i++ {
		if _, err := st.AddDVFavorite("NXDN", strconv.Itoa(i+1), "x"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.AddDVFavorite("NXDN", "9000", "x"); !errors.Is(err, ErrLimit) {
		t.Fatalf("limit: %v", err)
	}
}

// A 1.x database holds the favourites table and the settings keys under the
// previous stack's names; opening it moves them to the dv names.
func TestMigrateOldDVNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// Turn the fresh database back into a 1.x one.
	_, err = s.db.Exec(`ALTER TABLE dv_favorites DROP COLUMN pinned;
		ALTER TABLE favorites DROP COLUMN pinned;
		ALTER TABLE dv_favorites RENAME TO ` + legacy.OldFavoritesTable + `;
		DELETE FROM schema_version WHERE version>5;
		INSERT INTO settings(k,v) VALUES ('` + legacy.OldSettingsPrefix + `node','1999'), ('` + legacy.OldSettingsPrefix + `resume','{"a":1}'), ('dv.resume','new'), ('other','x');
		INSERT INTO ` + legacy.OldFavoritesTable + `(mode,value,name,sort,created) VALUES ('YSF','00001','Test',0,1)`)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if v, err := s.GetSetting("dv.node"); err != nil || v != "1999" {
		t.Errorf("dv.node = %q, %v", v, err)
	}
	if v, _ := s.GetSetting("dv.resume"); v != "new" {
		t.Errorf("existing dv.resume overwritten: %q", v)
	}
	if v, _ := s.GetSetting("other"); v != "x" {
		t.Errorf("other = %q", v)
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM settings WHERE k LIKE ?`, legacy.OldSettingsPrefix+"%").Scan(&n); err != nil || n != 0 {
		t.Errorf("old keys left: %d %v", n, err)
	}
	favs, err := s.ListDVFavorites()
	if err != nil || len(favs) != 1 || favs[0].Name != "Test" {
		t.Errorf("favourites %+v %v", favs, err)
	}
}

func TestDVFavoritesM17(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	// a favourite is a reflector and module; two modules are two favourites
	for _, v := range []string{"M17-XXX C", "M17-XXX D"} {
		if f, err := st.AddDVFavorite("M17", v, "Test"); err != nil || f.Mode != "M17" || f.Value != v {
			t.Fatalf("%+v %v", f, err)
		}
	}
	if _, err := st.AddDVFavorite("M17", "M17-XXX C", "again"); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate: %v", err)
	}
}
