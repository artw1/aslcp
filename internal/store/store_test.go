package store

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
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
	f, err := s.UpdateFavorite(a.ID, nil, &lbl, nil)
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
