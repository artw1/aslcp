package helper

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"io"
	"io/fs"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/artw1/aslcp/internal/backup"
)

// bkRunner runs git for real and answers everything else from the fake.
type bkRunner struct{ fake *fakeRunner }

func (r bkRunner) Run(ctx context.Context, argv ...string) (string, int, error) {
	if len(argv) > 0 && argv[0] == "env" {
		return ExecRunner{}.Run(ctx, argv...)
	}
	return r.fake.Run(ctx, argv...)
}

func md5hex(s string) string {
	h := md5.Sum([]byte(s)) //nolint:gosec
	return hex.EncodeToString(h[:])
}

type bkNode struct {
	t    *testing.T
	o    *Ops
	base string
	fake *fakeRunner
}

// newBkNode builds a node in temp roots; ver is the asl3-asterisk-config
// version and conf the conffiles dpkg knows (rel path -> md5).
func newBkNode(t *testing.T, ver string, conf map[string]string) *bkNode {
	t.Helper()
	needGit(t)
	base := t.TempDir()
	n := &bkNode{t: t, base: base}
	rules := map[string]resp{
		"systemctl stop asterisk":           {},
		"systemctl start asterisk":          {},
		"dpkg --print-architecture":         {out: "arm64\n"},
		"dpkg-query -W -f=${Version} aslcp": {out: "2.0.0"},
		"dpkg-query -W -f=${Package}\\t":    {out: "asl3-asterisk-config\t" + ver + "\tii \nasl3\t" + ver + "\tii \nasterisk-core\t20.1\tii \nvim\t9\tii \nallmon3\t1.0\trc \n"},
	}
	var sb strings.Builder
	sb.WriteString("asl3-asterisk-config " + ver + "\n")
	for p, m := range conf {
		sb.WriteString(" /etc/asterisk/" + p + " " + m + "\n")
	}
	rules["dpkg-query -W -f=${Package} ${Version}"] = resp{out: sb.String()}
	idleJob(rules)
	n.fake = &fakeRunner{rules: rules}
	n.o = &Ops{
		Run:          bkRunner{n.fake},
		ConfigRoot:   filepath.Join(base, "etc-asterisk"),
		HistoryDir:   filepath.Join(base, "var", "hist"),
		AstDBPath:    filepath.Join(base, "lib", "astdb.sqlite3"),
		SoundsDirs:   [2]string{filepath.Join(base, "lib", "sounds-custom"), filepath.Join(base, "share", "sounds-custom")},
		SA818Conf:    filepath.Join(base, "etc", "sa818.conf"),
		AllmonDir:    filepath.Join(base, "etc", "allmon3"),
		AslcpConf:    filepath.Join(base, "etc", "aslcp", "aslcp.conf"),
		DVHistoryDir: filepath.Join(base, "var", "dv-history"),
		DVStateDir:   filepath.Join(base, "var", "dv-state"),
		SourceList:   filepath.Join(base, "etc", "aslcp.list"),
		TempDir:      base,
		NewFileOwner: func() (int, int, fs.FileMode) {
			return os.Getuid(), os.Getgid(), 0o640
		},
	}
	for _, d := range []string{"etc-asterisk/custom", "etc", "lib", "var"} {
		if err := os.MkdirAll(filepath.Join(base, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return n
}

func (n *bkNode) put(rel, content string, mode fs.FileMode) {
	n.t.Helper()
	p := filepath.Join(n.base, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		n.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		n.t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		n.t.Fatal(err)
	}
}

func (n *bkNode) get(rel string) string {
	n.t.Helper()
	b, err := os.ReadFile(filepath.Join(n.base, rel))
	if err != nil {
		n.t.Fatal(err)
	}
	return string(b)
}

func (n *bkNode) astdb(rows ...string) {
	n.t.Helper()
	db, err := sql.Open("sqlite", n.o.astDB())
	if err != nil {
		n.t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec("CREATE TABLE astdb (key VARCHAR(256), value VARCHAR(256))"); err != nil {
		n.t.Fatal(err)
	}
	for _, r := range rows {
		if _, err := db.Exec("INSERT INTO astdb VALUES (?, 'v')", r); err != nil {
			n.t.Fatal(err)
		}
	}
}

// archive runs backup-create and assembles the gzip archive like the server.
func (n *bkNode) archive(req BackupRequest) []byte {
	n.t.Helper()
	return n.archiveExtra(req, nil)
}

func (n *bkNode) archiveExtra(req BackupRequest, extra map[string]string) []byte {
	n.t.Helper()
	var raw bytes.Buffer
	if _, err := n.o.BackupCreate(context.Background(), &req, &raw); err != nil {
		n.t.Fatal(err)
	}
	bs, err := ReadBackupStream(&raw, nil)
	if err != nil {
		n.t.Fatal(err)
	}
	return assembleArchiveExtra(n.t, bs, extra)
}

func assembleArchive(t *testing.T, bs *BackupStream) []byte {
	t.Helper()
	return assembleArchiveExtra(t, bs, nil)
}

// assembleArchiveExtra is assembleArchive plus extra top-level files (the
// ones the server adds, such as aslcp.conf), given as path -> content.
func assembleArchiveExtra(t *testing.T, bs *BackupStream, extra map[string]string) []byte {
	t.Helper()
	h := bs.Header
	order := append([]backup.Entry(nil), h.Entries...)
	files := append([]backup.Entry(nil), h.Entries...)
	for p, c := range extra {
		sum := sha256.Sum256([]byte(c))
		files = append(files, backup.Entry{Path: p, Type: backup.TypeFile, Size: int64(len(c)),
			SHA256: hex.EncodeToString(sum[:]), Owner: "root", Group: "aslcp", Mode: "0640"})
	}
	m := &backup.Manifest{Format: 1, Created: time.Date(2026, 3, 4, 5, 6, 0, 0, time.UTC), CreatedBy: "admin",
		ASLCPVersion: "2.0.0", ASL3Version: h.ASL3Version, AsteriskVersion: h.AsteriskVersion, Hostname: "node1",
		Timezone: "UTC", Arch: h.Arch, Nodes: h.Nodes, Channel: h.Channel, Packages: h.Packages, Parts: h.Parts,
		MissingStock: h.MissingStock, Skipped: h.Skipped, Files: files}
	var out bytes.Buffer
	w, err := backup.NewWriter(&out, m)
	if err != nil {
		t.Fatal(err)
	}
	// read the system part's tar, feed the entries to the writer
	tr := newTarFeeder(t, bs.Body)
	for _, e := range order {
		if err := w.Add(e.Path, tr.next(e)); err != nil {
			t.Fatalf("%s: %v", e.Path, err)
		}
	}
	for p, c := range extra {
		if err := w.Add(p, strings.NewReader(c)); err != nil {
			t.Fatalf("%s: %v", p, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func restoreInto(t *testing.T, n *bkNode, arc []byte, req RestoreRequest) (RestoreResult, error) {
	t.Helper()
	return n.o.BackupRestore(context.Background(), &req, bytes.NewReader(arc))
}

func TestBackupRestoreRoundTripAndClassification(t *testing.T) {
	const (
		stockExtOld = "stock ext v1\n"
		stockExtNew = "stock ext v2\n"
		stockRptOld = "stock rpt v1\n"
		stockRptNew = "stock rpt v2\n"
		stockGone   = "stock gone\n"
	)
	src := newBkNode(t, "1.0", map[string]string{
		"extensions.conf": md5hex(stockExtOld), "rpt.conf": md5hex(stockRptOld), "gone.conf": md5hex(stockGone)})
	src.put("etc-asterisk/extensions.conf", stockExtOld, 0o640)
	src.put("etc-asterisk/rpt.conf", "[1999]\nmy=edit\n", 0o640)
	src.put("etc-asterisk/custom/x.conf", "secret=1\n", 0o600)
	src.put("etc-asterisk/extensions.conf.dpkg-old", "old\n", 0o640)
	if err := os.Symlink("x.conf", filepath.Join(src.base, "etc-asterisk/custom/link.conf")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(src.base, "etc-asterisk/custom/evil.conf")); err != nil {
		t.Fatal(err)
	}
	src.astdb("a/1", "a/2")
	src.put("lib/sounds-custom/hello.ul", "snd", 0o644)
	src.put("etc/allmon3/allmon3.ini", "[x]\n", 0o640)
	src.put("etc/sa818.conf", "CURRENT_BAND=VHF\nCURRENT_PORT=/dev/nonexistent-aslcp-test\n", 0o644)
	src.put("etc/aslcp.list", "deb https://example.invalid/aslcp beta main\n", 0o644)
	// history
	if _, err := src.o.prepareRepo(context.Background()); err != nil {
		t.Fatal(err)
	}
	arc := src.archive(BackupRequest{History: true, Allmon3: true, Sounds: true})

	m, err := backup.Read(bytes.NewReader(arc), nil)
	if err != nil {
		t.Fatal(err)
	}
	cls := map[string]string{}
	for _, e := range m.Files {
		cls[e.Path] = e.Class
	}
	for p, want := range map[string]string{
		"asterisk/etc/extensions.conf": backup.ClassStock, "asterisk/etc/rpt.conf": backup.ClassChanged,
		"asterisk/etc/custom/x.conf": backup.ClassLocal,
	} {
		if cls[p] != want {
			t.Errorf("%s class %q want %q", p, cls[p], want)
		}
	}
	if _, ok := cls["asterisk/etc/custom/evil.conf"]; ok {
		t.Error("symlink leaving the tree must not be archived")
	}
	if len(m.Skipped) == 0 || len(m.MissingStock) != 0 && m.MissingStock[0].Path != "gone.conf" {
		t.Errorf("skipped %v missing %v", m.Skipped, m.MissingStock)
	}
	if len(m.MissingStock) != 1 || m.MissingStock[0].Path != "gone.conf" {
		t.Errorf("missing stock %v", m.MissingStock)
	}
	if len(m.Packages) != 3 || m.Arch != "arm64" || m.ASL3Version != "1.0" || m.Channel != "beta" {
		t.Errorf("header %+v", m)
	}
	for _, p := range m.Packages {
		if p.Name == "vim" || p.Name == "allmon3" {
			t.Errorf("package %s must not be listed", p.Name)
		}
	}

	// a newer image: stock files differ, a new file, gone.conf is stock here
	dst := newBkNode(t, "2.0", map[string]string{
		"extensions.conf": md5hex(stockExtNew), "rpt.conf": md5hex(stockRptNew), "gone.conf": md5hex(stockGone)})
	dst.put("etc-asterisk/extensions.conf", stockExtNew, 0o640)
	dst.put("etc-asterisk/rpt.conf", stockRptNew, 0o640)
	dst.put("etc-asterisk/gone.conf", stockGone, 0o640)
	dst.put("etc-asterisk/newfile.conf", "new\n", 0o640)
	dst.put("lib/astdb.sqlite3", "garbage", 0o600)
	dst.put("etc/aslcp.list", "deb https://example.invalid/aslcp stable main\n", 0o644)

	pre, err := restoreInto(t, dst, arc, RestoreRequest{DryRun: true})
	if err != nil || !pre.OK || !pre.DryRun {
		t.Fatalf("%+v %v", pre, err)
	}
	act := map[string]string{}
	for _, f := range pre.Files {
		act[f.Path] = f.Action
	}
	for p, want := range map[string]string{
		"extensions.conf": ActKeepStock, "rpt.conf": ActRestoreDist, "custom/x.conf": ActRestore, "custom/link.conf": ActRestore,
		"newfile.conf": ActKeepNew, "gone.conf": ActDeleteOptional, "extensions.conf.dpkg-old": ActSkip,
	} {
		if act[p] != want {
			t.Errorf("%s: action %q want %q (%v)", p, act[p], want, act)
		}
	}
	if pre.ASL3Version != "2.0" || pre.SA818Port != "/dev/nonexistent-aslcp-test" || pre.SA818PortPresent {
		t.Errorf("%+v", pre)
	}
	if dst.get("etc-asterisk/rpt.conf") != stockRptNew || dst.fake.called("systemctl stop") != 0 {
		t.Fatal("dry run changed something")
	}
	if _, err := restoreInto(t, dst, arc, RestoreRequest{DeleteMissing: []string{"newfile.conf"}}); err == nil {
		t.Fatal("deleting a file that was not offered must be refused")
	}
	if dst.get("etc-asterisk/newfile.conf") != "new\n" {
		t.Fatal("refused request changed something")
	}

	res, err := restoreInto(t, dst, arc, RestoreRequest{DeleteMissing: []string{"gone.conf"}})
	if err != nil || !res.OK {
		t.Fatalf("%+v %v", res, err)
	}
	if dst.get("etc-asterisk/extensions.conf") != stockExtNew {
		t.Error("stock file must keep the new version")
	}
	if dst.get("etc-asterisk/rpt.conf") != "[1999]\nmy=edit\n" || dst.get("etc-asterisk/rpt.conf.dpkg-dist") != stockRptNew {
		t.Error("changed file / .dpkg-dist")
	}
	if dst.get("etc-asterisk/custom/x.conf") != "secret=1\n" {
		t.Error("local file")
	}
	if fi, _ := os.Stat(filepath.Join(dst.base, "etc-asterisk/custom/x.conf")); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode())
	}
	if l, err := os.Readlink(filepath.Join(dst.base, "etc-asterisk/custom/link.conf")); err != nil || l != "x.conf" {
		t.Errorf("symlink %q %v", l, err)
	}
	if _, err := os.Stat(filepath.Join(dst.base, "etc-asterisk/gone.conf")); err == nil {
		t.Error("gone.conf must be deleted")
	}
	if dst.get("etc-asterisk/newfile.conf") != "new\n" {
		t.Error("new file must be kept")
	}
	if _, err := os.Stat(filepath.Join(dst.base, "etc-asterisk/extensions.conf.dpkg-old")); err == nil {
		t.Error("dpkg leftovers must not be restored")
	}
	db, err := sql.Open("sqlite", dst.o.astDB())
	if err != nil {
		t.Fatal(err)
	}
	var cnt int
	if err := db.QueryRow("SELECT count(*) FROM astdb").Scan(&cnt); err != nil || cnt != 2 {
		t.Errorf("astdb rows %d %v", cnt, err)
	}
	db.Close()
	if fi, _ := os.Stat(dst.o.astDB()); fi.Mode().Perm() != 0o644 && fi.Mode().Perm() != 0o600 {
		t.Errorf("astdb mode %v", fi.Mode())
	}
	if dst.get("lib/sounds-custom/hello.ul") != "snd" || dst.get("etc/allmon3/allmon3.ini") != "[x]\n" ||
		!strings.HasPrefix(dst.get("etc/sa818.conf"), "CURRENT_BAND=VHF") {
		t.Error("parts not restored")
	}
	if !strings.Contains(dst.get("etc/aslcp.list"), " beta ") {
		t.Errorf("channel %q", dst.get("etc/aslcp.list"))
	}
	if dst.fake.called("systemctl stop asterisk") != 1 || dst.fake.called("systemctl start asterisk") != 1 {
		t.Errorf("asterisk calls %v", dst.fake.calls)
	}
	// history: restored and committed
	r := &repo{o: dst.o, hist: dst.o.historyDir(), root: dst.o.configRoot()}
	out, err := r.git(context.Background(), "log", "--format=%s")
	if err != nil || !strings.Contains(out, "Restored from backup 2026-03-04 05:06") || !strings.Contains(out, "Before restore from backup") {
		t.Errorf("history %q %v", out, err)
	}
	var names []string
	for _, s := range res.Steps {
		names = append(names, s.Name)
		if !s.OK {
			t.Errorf("step %+v", s)
		}
	}
	if strings.Join(names, ",") != "Stop Asterisk,Restore config history,Snapshot current /etc/asterisk to history,Restore /etc/asterisk,Replace AstDB,Replace Allmon3 config,Replace custom sounds,Restore SA818 settings,Set update channel,Commit restored configuration to history,Start Asterisk" {
		t.Errorf("steps %v", names)
	}
}

func TestBackupRestoreOwnerAndModeRoot(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to chown")
	}
	u, err := user.Lookup("nobody")
	if err != nil {
		t.Skip("no nobody user")
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	src := newBkNode(t, "1.0", nil)
	src.put("etc-asterisk/custom/x.conf", "a\n", 0o640)
	if err := os.Chown(filepath.Join(src.base, "etc-asterisk/custom/x.conf"), uid, gid); err != nil {
		t.Fatal(err)
	}
	arc := src.archive(BackupRequest{})
	dst := newBkNode(t, "1.0", nil)
	if res, err := restoreInto(t, dst, arc, RestoreRequest{}); err != nil || !res.OK {
		t.Fatalf("%+v %v", res, err)
	}
	fi, _ := os.Stat(filepath.Join(dst.base, "etc-asterisk/custom/x.conf"))
	gotU, gotG, _ := ownerIDs(fi)
	if gotU != uid || gotG != gid || fi.Mode().Perm() != 0o640 {
		t.Errorf("owner %d:%d mode %v", gotU, gotG, fi.Mode())
	}
}

func TestBackupRestoreFailureRollsBackAndStartsAsterisk(t *testing.T) {
	src := newBkNode(t, "1.0", nil)
	src.put("etc-asterisk/a.conf", "A-backup\n", 0o640)
	src.put("etc-asterisk/b.conf", "B-backup\n", 0o640)
	arc := src.archive(BackupRequest{})
	dst := newBkNode(t, "1.0", nil)
	dst.put("etc-asterisk/a.conf", "A-here\n", 0o640)
	dst.put("etc-asterisk/b.conf", "B-here\n", 0o640)
	real := atomicWrite
	calls := 0
	dst.o.ConfigWrite = func(abs, parent string, data []byte, uid, gid int, mode fs.FileMode) error {
		calls++
		if calls == 2 {
			return os.ErrPermission
		}
		return real(abs, parent, data, uid, gid, mode)
	}
	res, err := restoreInto(t, dst, arc, RestoreRequest{})
	if err != nil || res.OK || res.Error == "" {
		t.Fatalf("%+v %v", res, err)
	}
	if dst.get("etc-asterisk/a.conf") != "A-here\n" || dst.get("etc-asterisk/b.conf") != "B-here\n" {
		t.Error("the configuration must be written back")
	}
	if dst.fake.called("systemctl start asterisk") != 1 {
		t.Errorf("asterisk must be started again: %v", dst.fake.calls)
	}
}

func TestBackupRestoreRejectsBadArchiveBeforeChanging(t *testing.T) {
	dst := newBkNode(t, "1.0", nil)
	dst.put("etc-asterisk/a.conf", "A\n", 0o640)
	for name, data := range map[string][]byte{"empty": nil, "junk": []byte("not a gzip file at all")} {
		if _, err := restoreInto(t, dst, data, RestoreRequest{}); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if dst.fake.called("systemctl") != 0 {
		t.Errorf("asterisk touched: %v", dst.fake.calls)
	}
}

func TestBackupInfo(t *testing.T) {
	n := newBkNode(t, "1.0", nil)
	n.put("lib/sounds-custom/a.ul", "12345", 0o644)
	info, err := n.o.BackupInfo(context.Background())
	if err != nil || len(info.Parts) != 4 || info.SA818 {
		t.Fatalf("%+v %v", info, err)
	}
	for _, p := range info.Parts {
		if p.ID == "sounds" && (!p.Present || p.Size != 5) {
			t.Errorf("%+v", p)
		}
		if (p.ID == "allmon3" || p.ID == "dv") && p.Present {
			t.Errorf("%+v", p)
		}
	}
}

// tarFeeder hands out the entries of the helper's tar stream in header order.
type tarFeeder struct {
	t  *testing.T
	tr *tar.Reader
}

func newTarFeeder(t *testing.T, r io.Reader) *tarFeeder { return &tarFeeder{t, tar.NewReader(r)} }

func (f *tarFeeder) next(e backup.Entry) io.Reader {
	f.t.Helper()
	h, err := f.tr.Next()
	if err != nil {
		f.t.Fatalf("tar stream ended before %s: %v", e.Path, err)
	}
	if h.Name != e.Path && h.Name != e.Path+"/" {
		f.t.Fatalf("tar entry %q, header says %q", h.Name, e.Path)
	}
	return f.tr
}

func TestBackupHeaderOSInfo(t *testing.T) {
	n := newBkNode(t, "3.10.8-1.deb13", map[string]string{})
	n.put("etc/os-release", "ID=debian\nVERSION_CODENAME=trixie\nPRETTY_NAME=\"Debian GNU/Linux 13 (trixie)\"\n", 0o644)
	n.o.OSRelease = filepath.Join(n.base, "etc", "os-release")
	var raw bytes.Buffer
	if _, err := n.o.BackupCreate(context.Background(), &BackupRequest{}, &raw); err != nil {
		t.Fatal(err)
	}
	bs, err := ReadBackupStream(&raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	if h := bs.Header; h.OSID != "debian" || h.OSCodename != "trixie" || h.OSName != "Debian GNU/Linux 13 (trixie)" {
		t.Errorf("header %+v", h)
	}

	// No os-release: the codename comes from the .debNN suffix.
	n.o.OSRelease = filepath.Join(n.base, "etc", "missing")
	raw.Reset()
	if _, err := n.o.BackupCreate(context.Background(), &BackupRequest{}, &raw); err != nil {
		t.Fatal(err)
	}
	if bs, err = ReadBackupStream(&raw, nil); err != nil {
		t.Fatal(err)
	}
	if h := bs.Header; h.OSID != "" || h.OSCodename != "trixie" {
		t.Errorf("fallback header %+v", h)
	}
}

// The helper socket protocol (client request line, then the archive on the
// same connection) must work end to end; demo mode calls Ops in-process and
// never exercised this path. The client's JSON encoder ends the request with
// a newline that must not be read as the first byte of the archive.
func TestBackupOverSocketRoundTrip(t *testing.T) {
	src := newBkNode(t, "1.0", nil)
	src.put("etc-asterisk/rpt.conf", "[1999]\nmy=edit\n", 0o640)
	src.put("etc-asterisk/custom/x.conf", "secret=1\n", 0o600)
	src.astdb("a/1")
	arc := src.archive(BackupRequest{})

	peer := func(net.Conn) (uint32, error) { return 1000, nil }

	// restore side: a second node behind a real unix socket
	dst := newBkNode(t, "1.0", nil)
	dst.put("etc-asterisk/rpt.conf", "[1999]\nold\n", 0o640)
	cl, _ := startServer(t, dst.o, peer, []uint32{1000})
	ctx := context.Background()

	res, err := cl.BackupRestore(ctx, RestoreRequest{DryRun: true}, bytes.NewReader(arc))
	if err != nil {
		t.Fatalf("dry run over socket: %v", err)
	}
	if !res.OK || !res.DryRun || len(res.Files) == 0 {
		t.Fatalf("dry run result %+v", res)
	}
	if dst.get("etc-asterisk/rpt.conf") != "[1999]\nold\n" {
		t.Error("dry run changed files")
	}

	// a garbage upload is still rejected with the real reason
	if _, err := cl.BackupRestore(ctx, RestoreRequest{DryRun: true}, strings.NewReader("junk")); err == nil || !strings.Contains(err.Error(), "gzip") {
		t.Errorf("junk upload: %v", err)
	}

	// real restore
	res, err = cl.BackupRestore(ctx, RestoreRequest{}, bytes.NewReader(arc))
	if err != nil || !res.OK {
		t.Fatalf("restore over socket: %+v %v", res, err)
	}
	if dst.get("etc-asterisk/rpt.conf") != "[1999]\nmy=edit\n" || dst.get("etc-asterisk/custom/x.conf") != "secret=1\n" {
		t.Error("restore did not write the archived files")
	}

	// create side over the socket (header line, then tar), then restore that
	// archive over the socket too
	cc, _ := startServer(t, src.o, peer, []uint32{1000})
	bs, err := cc.BackupCreate(ctx, BackupRequest{})
	if err != nil {
		t.Fatalf("create over socket: %v", err)
	}
	arc2 := assembleArchive(t, bs)
	bs.Close()
	dst2 := newBkNode(t, "1.0", nil)
	c2, _ := startServer(t, dst2.o, peer, []uint32{1000})
	if res, err = c2.BackupRestore(ctx, RestoreRequest{DryRun: true}, bytes.NewReader(arc2)); err != nil || !res.OK {
		t.Fatalf("create->restore over socket: %+v %v", res, err)
	}
}

func TestSkipLeadingSpace(t *testing.T) {
	for _, in := range []string{"\n\x1f\x8bdata", " \t\r\n\x1f\x8bdata", "\x1f\x8bdata"} {
		got, err := io.ReadAll(&skipLeadingSpace{r: iotestOneByte(strings.NewReader(in))})
		if err != nil || string(got) != "\x1f\x8bdata" {
			t.Errorf("%q -> %q %v", in, got, err)
		}
	}
	if got, err := io.ReadAll(&skipLeadingSpace{r: strings.NewReader("\n\n")}); err != nil || len(got) != 0 {
		t.Errorf("only space: %q %v", got, err)
	}
}

func iotestOneByte(r io.Reader) io.Reader { return iotest.OneByteReader(r) }
