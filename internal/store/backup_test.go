package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSnapshotInspectMigrate(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.CreateUser("alice", "hash", RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddFavorite("1999", "Hub", ""); err != nil {
		t.Fatal(err)
	}
	u, _ := st.UserByName("alice")
	if err := st.CreateSession("tok", u.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	snap := filepath.Join(dir, "snap.db")
	if err := st.Snapshot(snap); err != nil {
		t.Fatal(err)
	}
	in, err := Inspect(snap)
	if err != nil {
		t.Fatal(err)
	}
	if in.Version != SchemaVersion() || len(in.Users) != 1 || in.Users[0].Username != "alice" || in.Favorites != 1 {
		t.Fatalf("inspection %+v", in)
	}
	// no sessions in the snapshot
	st2, err := Open(snap)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st2.SessionByHash("tok", time.Now()); err == nil {
		t.Error("session survived the snapshot")
	}
	st2.Close()

	if _, err := Inspect(filepath.Join(dir, "missing.db")); err == nil {
		t.Error("missing file accepted")
	}
	junk := filepath.Join(dir, "junk.db")
	os.WriteFile(junk, []byte("not sqlite at all, just text padding padding padding padding"), 0o600)
	if _, err := Inspect(junk); err == nil {
		t.Error("junk accepted")
	}
	if err := MigrateFile(snap, &Audit{Username: "alice", Action: "backup.restore", Detail: "{}"}); err != nil {
		t.Fatal(err)
	}
}
