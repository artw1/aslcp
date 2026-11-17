package backupsvc

import (
	"context"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/helper"
	"github.com/artw1/aslcp/internal/store"
)

func newSvc(t *testing.T) (*Service, *store.Store, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ops, err := helper.NewDemoOps(t.TempDir(), map[string]string{"rpt.conf": "[1999]\nrxchannel = SimpleUSB/usb\n"})
	if err != nil {
		t.Fatal(err)
	}
	data := t.TempDir()
	st, err := store.Open(filepath.Join(data, "aslcp.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if _, err := st.CreateUser("alice", "hash", store.RoleAdmin); err != nil {
		t.Fatal(err)
	}
	s := New(Options{Helper: helper.LocalBackup{Ops: ops}, Store: st, DataDir: data, Version: "1.0.0",
		Logger: log.New(io.Discard, "", 0), Hostname: func() string { return "node-a" }, Timezone: func() string { return "UTC" }})
	return s, st, data
}

func TestStagedRestoreAndSwap(t *testing.T) {
	s, st, data := newSvc(t)
	c, err := s.Create(context.Background(), helper.BackupRequest{History: true}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	up, err := s.Upload(context.Background(), c.File)
	if err != nil {
		t.Fatal(err)
	}
	if up.Preview.Backup.Hostname != "node-a" || len(up.Preview.Users) != 1 {
		t.Fatalf("preview %+v", up.Preview)
	}
	if _, err := s.Apply(context.Background(), "nope", nil, "alice"); err != ErrNotFound {
		t.Fatalf("unknown id: %v", err)
	}
	if _, err := s.Apply(context.Background(), up.ID, []string{"rpt.conf"}, "alice"); err == nil {
		t.Fatal("deleting a file that is not offered must fail")
	}
	res, err := s.Apply(context.Background(), up.ID, nil, "alice")
	if err != nil || !res.OK || !res.Restarting {
		t.Fatalf("apply: %+v %v", res, err)
	}
	if _, err := os.Stat(StagedPath(data)); err != nil {
		t.Fatalf("not staged: %v", err)
	}
	r, ok := s.LastResult()
	if !ok || !r.Health.Pending {
		t.Fatalf("result %+v %v", r, ok)
	}

	// the next start swaps the staged database in
	st.Close()
	swapped, err := ApplyStaged(data)
	if err != nil || !swapped {
		t.Fatalf("ApplyStaged: %v %v", swapped, err)
	}
	for _, p := range []string{"aslcp.db", "aslcp.db.pre-restore"} {
		if _, err := os.Stat(filepath.Join(data, p)); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
	if _, err := os.Stat(StagedPath(data)); err == nil {
		t.Error("staged file remains")
	}
	st2, err := store.Open(filepath.Join(data, "aslcp.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	aud, _ := st2.ListAudit(10)
	found := false
	for _, a := range aud {
		found = found || a.Action == "backup.restore"
	}
	if !found {
		t.Errorf("restore audit entry missing: %v", aud)
	}

	// health check completes the pending result
	s2 := New(s.o)
	s2.RunHealth(context.Background(), func(context.Context, []string) []Check {
		return []Check{{Name: "x", OK: true, Detail: "fine"}}
	})
	r, _ = s2.LastResult()
	if r.Health.Pending || !r.Health.OK || len(r.Health.Checks) != 1 {
		t.Fatalf("health %+v", r.Health)
	}
	_ = time.Second
}

func TestApplyStagedRejectsJunk(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "aslcp.db")
	os.WriteFile(live, []byte("live"), 0o600)
	os.WriteFile(StagedPath(dir), []byte("junk junk junk junk junk junk junk junk junk junk"), 0o600)
	swapped, err := ApplyStaged(dir)
	if swapped || err == nil {
		t.Fatalf("junk staged db was accepted: %v %v", swapped, err)
	}
	if b, _ := os.ReadFile(live); string(b) != "live" {
		t.Error("live database touched")
	}
	if swapped, err := ApplyStaged(t.TempDir()); swapped || err != nil {
		t.Errorf("nothing staged: %v %v", swapped, err)
	}
}
