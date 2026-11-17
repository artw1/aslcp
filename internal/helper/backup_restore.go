package helper

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/artw1/aslcp/internal/backup"
)

const (
	maxRestoreConfigFile = 32 << 20
	maxKeepNewListed     = 2000
)

func rndHex() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// resolveOwner maps names from the archive to ids: the name itself, else
// asterisk, else root.
func resolveOwner(owner, group string) (uid, gid int) {
	for _, n := range []string{owner, "asterisk"} {
		if u, err := user.Lookup(n); err == nil {
			if v, err := strconv.Atoi(u.Uid); err == nil {
				uid = v
				break
			}
		}
	}
	for _, n := range []string{group, "asterisk"} {
		if g, err := user.LookupGroup(n); err == nil {
			if v, err := strconv.Atoi(g.Gid); err == nil {
				gid = v
				break
			}
		}
	}
	return uid, gid
}

func entryOwner(e backup.Entry) (int, int, fs.FileMode) {
	uid, gid := resolveOwner(e.Owner, e.Group)
	m, _ := backup.ParseMode(e.Mode)
	return uid, gid, fs.FileMode(m & 0o777)
}

func chownPath(path string, uid, gid int, link bool) error {
	var err error
	if link {
		err = os.Lchown(path, uid, gid)
	} else {
		err = os.Chown(path, uid, gid)
	}
	if err != nil && os.Geteuid() == 0 {
		return err
	}
	return nil
}

// ---------------------------------------------------------------- plan

type plannedWrite struct {
	e    backup.Entry
	rel  string
	dist bool // keep this system's file as <rel>.dpkg-dist first
}

type restorePlan struct {
	files   []RestoreFile
	writes  []plannedWrite
	deletes []string
	dirs    map[string]backup.Entry // rel -> dir entry
	offered map[string]bool
}

func md5File(path string) (string, bool) {
	_, _, md, _, err := hashFile(path)
	return md, err == nil
}

func isDpkgLeftover(rel string) bool {
	for _, s := range []string{".dpkg-dist", ".dpkg-old", ".dpkg-new"} {
		if strings.HasSuffix(rel, s) {
			return true
		}
	}
	return false
}

func (o *Ops) planConfig(m *backup.Manifest, st dpkgState, root string) *restorePlan {
	p := &restorePlan{dirs: map[string]backup.Entry{}, offered: map[string]bool{}}
	inBackup := map[string]bool{}
	prefix := backup.PathAsteriskEtc + "/"
	for _, e := range m.Files {
		if !strings.HasPrefix(e.Path, prefix) {
			if e.Path == backup.PathAsteriskEtc && e.Type == backup.TypeDir {
				p.dirs[""] = e
			}
			continue
		}
		rel := strings.TrimPrefix(e.Path, prefix)
		inBackup[rel] = true
		if e.Type == backup.TypeDir {
			p.dirs[rel] = e
			continue
		}
		dest := filepath.Join(root, filepath.FromSlash(rel))
		_, lerr := os.Lstat(dest)
		exists := lerr == nil
		f := RestoreFile{Path: rel}
		switch {
		case isDpkgLeftover(rel):
			f.Action, f.Note = ActSkip, "belongs to an older package version"
		case e.Class == backup.ClassStock && exists:
			f.Action, f.Note = ActKeepStock, "stock file: this system's version is kept"
		case e.Class == backup.ClassStock:
			f.Action, f.Note = ActRestore, "stock file missing here: restored"
			p.writes = append(p.writes, plannedWrite{e: e, rel: rel})
		default:
			f.Action = ActRestore
			if e.Class == backup.ClassChanged {
				f.Note = "your configuration"
			} else {
				f.Note = "local file"
			}
			pw := plannedWrite{e: e, rel: rel}
			if ci, ok := st.conf[rel]; ok && e.Class == backup.ClassChanged && e.Type == backup.TypeFile &&
				e.StockMD5 != "" && ci.md5 != e.StockMD5 && exists {
				if cur, ok := md5File(dest); ok && cur == ci.md5 {
					f.Action, f.Note = ActRestoreDist, "ASL3 changed the default; review "+rel+".dpkg-dist"
					pw.dist = true
				} else {
					f.Note = "your configuration (ASL3 changed the default, but the local copy is modified: no .dpkg-dist)"
				}
			}
			p.writes = append(p.writes, pw)
		}
		p.files = append(p.files, f)
	}
	for _, ms := range m.MissingStock {
		if backup.ValidPath(ms.Path) != nil || inBackup[ms.Path] {
			continue
		}
		if fi, err := os.Lstat(filepath.Join(root, filepath.FromSlash(ms.Path))); err == nil && !fi.IsDir() {
			p.offered[ms.Path] = true
			p.files = append(p.files, RestoreFile{Path: ms.Path, Action: ActDeleteOptional,
				Note: "deleted on the old node; tick to delete here"})
		}
	}
	n := 0
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || n >= maxKeepNewListed {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if inBackup[rel] || p.offered[rel] || strings.HasPrefix(filepath.Base(rel), ".aslcp-tmp-") {
			return nil
		}
		n++
		p.files = append(p.files, RestoreFile{Path: rel, Action: ActKeepNew, Note: "only on this system: kept"})
		return nil
	})
	sort.SliceStable(p.files, func(i, j int) bool { return p.files[i].Path < p.files[j].Path })
	return p
}

// ---------------------------------------------------------------- spool

type spool struct {
	dir    string
	staged map[string]string
}

func (o *Ops) spoolArchive(in io.Reader) (*spool, *backup.Manifest, error) {
	dir, err := os.MkdirTemp(o.tempDirRoot(), "aslcp-restore-")
	if err != nil {
		return nil, nil, fmt.Errorf("temporary directory: %w", err)
	}
	sp := &spool{dir: dir, staged: map[string]string{}}
	n := 0
	m, err := backup.Read(in, func(e backup.Entry, body io.Reader) error {
		if body == nil {
			return nil
		}
		n++
		name := filepath.Join(dir, "f"+strconv.Itoa(n))
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		_, err = io.Copy(f, body)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
		sp.staged[e.Path] = name
		return nil
	})
	if err != nil {
		os.RemoveAll(dir)
		return nil, nil, err
	}
	return sp, m, nil
}

func (sp *spool) close() { os.RemoveAll(sp.dir) }

func (sp *spool) read(path string, max int64) ([]byte, error) {
	name, ok := sp.staged[path]
	if !ok {
		return nil, fmt.Errorf("%s is not in the backup", path)
	}
	fi, err := os.Stat(name)
	if err != nil {
		return nil, err
	}
	if fi.Size() > max {
		return nil, fmt.Errorf("%s is too large to restore", path)
	}
	return os.ReadFile(name)
}

// ---------------------------------------------------------------- trees

// installTree builds the archived tree archRoot at dest (which must not
// exist). force overrides the owner of every entry when non-nil.
func (sp *spool) installTree(m *backup.Manifest, archRoot, dest string, force *[2]int) error {
	if err := os.Mkdir(dest, 0o700); err != nil {
		return err
	}
	type dirFix struct {
		path     string
		uid, gid int
		mode     fs.FileMode
	}
	var dirs []dirFix
	pre := archRoot + "/"
	for _, e := range m.Files {
		if e.Path != archRoot && !strings.HasPrefix(e.Path, pre) {
			continue
		}
		if backup.ValidPath(e.Path) != nil {
			return fmt.Errorf("bad path %q", e.Path)
		}
		p := dest
		if e.Path != archRoot {
			p = filepath.Join(dest, filepath.FromSlash(strings.TrimPrefix(e.Path, pre)))
		}
		uid, gid, mode := entryOwner(e)
		if force != nil {
			uid, gid = force[0], force[1]
		}
		switch e.Type {
		case backup.TypeDir:
			if p != dest {
				if err := os.Mkdir(p, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
					return err
				}
			}
			dirs = append(dirs, dirFix{p, uid, gid, mode})
		case backup.TypeSymlink:
			if err := os.Symlink(e.Target, p); err != nil {
				return err
			}
			if err := chownPath(p, uid, gid, true); err != nil {
				return err
			}
		case backup.TypeFile:
			src, ok := sp.staged[e.Path]
			if !ok {
				return fmt.Errorf("%s is missing from the archive", e.Path)
			}
			if err := copyStaged(src, p, uid, gid, mode); err != nil {
				return fmt.Errorf("%s: %w", e.Path, err)
			}
		}
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		d := dirs[i]
		if err := chownPath(d.path, d.uid, d.gid, false); err != nil {
			return err
		}
		if err := os.Chmod(d.path, d.mode); err != nil {
			return err
		}
	}
	return nil
}

func copyStaged(src, dst string, uid, gid int, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Chown(uid, gid); err != nil && os.Geteuid() == 0 {
		out.Close()
		return err
	}
	if err := out.Chmod(mode); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// replaceTree replaces dest by the archived tree archRoot: the new tree is
// built beside it and swapped in with renames.
func (sp *spool) replaceTree(m *backup.Manifest, archRoot, dest string, force *[2]int) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	if fi, err := os.Lstat(dest); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symbolic link", dest)
	}
	fresh := dest + ".restore-" + rndHex()
	if err := sp.installTree(m, archRoot, fresh, force); err != nil {
		os.RemoveAll(fresh)
		return err
	}
	old := ""
	if _, err := os.Lstat(dest); err == nil {
		old = dest + ".old-" + rndHex()
		if err := os.Rename(dest, old); err != nil {
			os.RemoveAll(fresh)
			return err
		}
	}
	if err := os.Rename(fresh, dest); err != nil {
		if old != "" {
			_ = os.Rename(old, dest)
		}
		os.RemoveAll(fresh)
		return err
	}
	if old != "" {
		os.RemoveAll(old)
	}
	return nil
}

// ---------------------------------------------------------------- config

type undoRec struct {
	abs, parent string
	existed     bool
	symlink     bool
	data        []byte
	target      string
	uid, gid    int
	mode        fs.FileMode
}

func captureUndo(abs, parent string) (undoRec, error) {
	u := undoRec{abs: abs, parent: parent}
	fi, err := os.Lstat(abs)
	if errors.Is(err, fs.ErrNotExist) {
		return u, nil
	}
	if err != nil {
		return u, err
	}
	u.existed = true
	u.uid, u.gid, _ = ownerIDs(fi)
	u.mode = fi.Mode().Perm()
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		u.symlink = true
		u.target, err = os.Readlink(abs)
	case fi.Mode().IsRegular():
		if fi.Size() > maxRestoreConfigFile {
			return u, fmt.Errorf("%s is too large to replace", filepath.Base(abs))
		}
		u.data, err = os.ReadFile(abs)
	default:
		err = fmt.Errorf("%s is not a regular file", filepath.Base(abs))
	}
	return u, err
}

func (u undoRec) revert(w ConfigWriter) error {
	if !u.existed {
		return os.Remove(u.abs)
	}
	if u.symlink {
		return replaceSymlink(u.abs, u.target, u.uid, u.gid)
	}
	return w(u.abs, u.parent, u.data, u.uid, u.gid, u.mode)
}

func replaceSymlink(abs, target string, uid, gid int) error {
	tmp := abs + ".aslcp-tmp-" + rndHex()
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	if err := chownPath(tmp, uid, gid, true); err != nil {
		os.Remove(tmp)
		return err
	}
	if fi, err := os.Lstat(abs); err == nil && fi.IsDir() {
		os.Remove(tmp)
		return errors.New("destination is a directory")
	}
	if err := os.Rename(tmp, abs); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// ensureDir creates the directories of rel under root (never following a
// symbolic link) and returns the real parent directory.
func ensureDir(root, rel string, dirs map[string]backup.Entry) error {
	if rel == "." || rel == "" {
		return nil
	}
	cur, acc := root, ""
	for _, part := range strings.Split(rel, "/") {
		cur = filepath.Join(cur, part)
		if acc == "" {
			acc = part
		} else {
			acc += "/" + part
		}
		fi, err := os.Lstat(cur)
		if err == nil {
			if !fi.IsDir() {
				return fmt.Errorf("%s is not a directory", acc)
			}
			continue
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		mode := fs.FileMode(0o755)
		var u, g int
		if e, ok := dirs[acc]; ok {
			u, g, mode = entryOwner(e)
		} else {
			u, g = resolveOwner("asterisk", "asterisk")
		}
		if err := os.Mkdir(cur, 0o700); err != nil {
			return err
		}
		if err := chownPath(cur, u, g, false); err != nil {
			return err
		}
		if err := os.Chmod(cur, mode); err != nil {
			return err
		}
	}
	return nil
}

func (o *Ops) applyConfig(sp *spool, p *restorePlan, root string, deletes []string) error {
	w := o.writer()
	var done []undoRec
	rollback := func(cause error) error {
		var fails []string
		for i := len(done) - 1; i >= 0; i-- {
			if err := done[i].revert(w); err != nil {
				fails = append(fails, filepath.Base(done[i].abs))
			}
		}
		if len(fails) > 0 {
			return fmt.Errorf("%w (could not undo: %s; the history has the previous state)", cause, strings.Join(fails, ", "))
		}
		return fmt.Errorf("%w (changes undone)", cause)
	}
	step := func(rel string, f func(abs, parent string) error) error {
		dir := filepath.ToSlash(filepath.Dir(filepath.FromSlash(rel)))
		if err := ensureDir(root, dir, p.dirs); err != nil {
			return err
		}
		parent := filepath.Join(root, filepath.FromSlash(dir))
		if rp, err := filepath.EvalSymlinks(parent); err != nil || rp != parent {
			return fmt.Errorf("%s: directory is not a plain directory", dir)
		}
		abs := filepath.Join(parent, filepath.Base(rel))
		u, err := captureUndo(abs, parent)
		if err != nil {
			return err
		}
		if err := f(abs, parent); err != nil {
			return err
		}
		done = append(done, u)
		return nil
	}
	for _, pw := range p.writes {
		if pw.dist {
			dist := pw.rel + ".dpkg-dist"
			cur := filepath.Join(root, filepath.FromSlash(pw.rel))
			data, err := os.ReadFile(cur)
			fi, err2 := os.Stat(cur)
			if err != nil || err2 != nil {
				return rollback(fmt.Errorf("%s: %v", pw.rel, errors.Join(err, err2)))
			}
			uid, gid, _ := ownerIDs(fi)
			if err := step(dist, func(abs, parent string) error {
				return w(abs, parent, data, uid, gid, fi.Mode().Perm())
			}); err != nil {
				return rollback(fmt.Errorf("writing %s: %w", dist, err))
			}
		}
		uid, gid, mode := entryOwner(pw.e)
		err := step(pw.rel, func(abs, parent string) error {
			if pw.e.Type == backup.TypeSymlink {
				return replaceSymlink(abs, pw.e.Target, uid, gid)
			}
			data, err := sp.read(pw.e.Path, maxRestoreConfigFile)
			if err != nil {
				return err
			}
			return w(abs, parent, data, uid, gid, mode)
		})
		if err != nil {
			return rollback(fmt.Errorf("writing %s: %w", pw.rel, err))
		}
	}
	for _, rel := range deletes {
		err := step(rel, func(abs, _ string) error { return os.Remove(abs) })
		if err != nil {
			return rollback(fmt.Errorf("deleting %s: %w", rel, err))
		}
	}
	return nil
}

// ---------------------------------------------------------------- misc parts

func (o *Ops) restoreAstDB(sp *spool, e backup.Entry) error {
	name, ok := sp.staged[backup.PathAstDB]
	if !ok {
		return errors.New("AstDB is not in the archive")
	}
	if err := sqliteCheck(name); err != nil {
		return fmt.Errorf("the archived AstDB is damaged: %w", err)
	}
	dest := o.astDB()
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	uid, gid, mode := entryOwner(e)
	tmp := dest + ".restore-" + rndHex()
	if err := copyStaged(name, tmp, uid, gid, mode); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		os.Remove(tmp)
		return err
	}
	for _, s := range []string{"-wal", "-shm", "-journal"} {
		_ = os.Remove(dest + s)
	}
	return nil
}

func (o *Ops) restoreSingle(sp *spool, path, dest string, uid, gid int, mode fs.FileMode) error {
	data, err := sp.read(path, maxRestoreConfigFile)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(dest))
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(parent, filepath.Base(dest)), parent, data, uid, gid, mode)
}

func (o *Ops) restoreSounds(sp *spool, m *backup.Manifest) (string, error) {
	paths := map[string]string{"var-lib": orDefault(o.SoundsDirs[0], DefaultSoundsVar), "usr-share": orDefault(o.SoundsDirs[1], DefaultSoundsUsr)}
	roots := map[string]string{"var-lib": backup.PathSoundsVar, "usr-share": backup.PathSoundsUsr}
	seen := map[string]bool{}
	var done []string
	for _, id := range m.Parts.Sounds {
		dest, ok := paths[id]
		if !ok {
			continue
		}
		if rp, err := filepath.EvalSymlinks(dest); err == nil {
			dest = rp
		} else if rp, err := filepath.EvalSymlinks(filepath.Dir(dest)); err == nil {
			dest = filepath.Join(rp, filepath.Base(dest))
		}
		if seen[dest] {
			continue
		}
		seen[dest] = true
		if err := sp.replaceTree(m, roots[id], dest, nil); err != nil {
			return "", fmt.Errorf("%s: %w", id, err)
		}
		done = append(done, id)
	}
	return strings.Join(done, ", "), nil
}

func parseSA818Port(data []byte) string {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if v, ok := strings.CutPrefix(line, "CURRENT_PORT="); ok {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}

// ---------------------------------------------------------------- main

func restoreDate(m *backup.Manifest) string { return m.Created.UTC().Format("2006-01-02 15:04") }

// BackupRestore validates the archive read from in completely, then (unless
// req.DryRun) applies it. Errors before the first change are returned as
// errors; failures while applying are reported in the result.
func (o *Ops) BackupRestore(ctx context.Context, req *RestoreRequest, in io.Reader) (RestoreResult, error) {
	if req == nil {
		req = &RestoreRequest{}
	}
	o.applyMu.Lock()
	defer o.applyMu.Unlock()

	sp, m, err := o.spoolArchive(in)
	if err != nil {
		return RestoreResult{}, err
	}
	defer sp.close()

	root, err := filepath.EvalSymlinks(o.configRoot())
	if err != nil {
		return RestoreResult{}, errors.New("the Asterisk configuration directory does not exist")
	}
	st := o.dpkgState(ctx)
	plan := o.planConfig(m, st, root)
	res := RestoreResult{DryRun: req.DryRun, Files: plan.files, Steps: []RestoreStep{},
		ASL3Version: st.asl3(), AsteriskVersion: st.asterisk(), Arch: o.arch(ctx), Packages: st.packages()}
	osi := o.osInfo(st)
	res.OSID, res.OSCodename, res.OSName = osi.ID, osi.Codename, osi.Pretty
	if res.Packages == nil {
		res.Packages = []backup.Package{}
	}
	if data, err := sp.read(backup.PathSA818, 1<<20); err == nil {
		res.SA818Port = parseSA818Port(data)
		if strings.HasPrefix(res.SA818Port, "/dev/") {
			_, err := os.Stat(res.SA818Port)
			res.SA818PortPresent = err == nil
		}
	}
	if m.Parts.DVSwitch && dvHasFiles(m) && !o.dvInstalled() {
		res.DVSwitchNotInstalled = true
	}
	seen := map[string]bool{}
	var deletes []string
	for _, d := range req.DeleteMissing {
		if !plan.offered[d] {
			return RestoreResult{}, fmt.Errorf("%s is not a file this restore offers to delete", d)
		}
		if !seen[d] {
			seen[d] = true
			deletes = append(deletes, d)
		}
	}
	if req.DryRun {
		res.OK = true
		return res, nil
	}

	fail := func(name string, err error) (RestoreResult, error) {
		res.Steps = append(res.Steps, RestoreStep{Name: name, OK: false, Detail: err.Error()})
		res.Error = name + ": " + err.Error()
		return res, nil
	}
	okStep := func(name, detail string) {
		res.Steps = append(res.Steps, RestoreStep{Name: name, OK: true, Detail: detail})
	}

	stopped := false
	defer func() {
		if stopped {
			// Never leave the node without Asterisk, whatever happened.
			_, _, _ = o.Run.Run(context.WithoutCancel(ctx), "systemctl", "start", "asterisk")
		}
	}()
	stopped = true
	if out, code, err := o.Run.Run(ctx, "systemctl", "stop", "asterisk"); err != nil || code != 0 {
		stopped = false
		return fail("Stop Asterisk", fmt.Errorf("could not stop Asterisk: %s", strings.TrimSpace(LastLines(out, 2)+" "+errOr(err))))
	}
	okStep("Stop Asterisk", "")

	date := restoreDate(m)
	if m.Parts.History {
		hist := o.historyDir()
		force := [2]int{os.Geteuid(), os.Getegid()}
		if err := sp.replaceTree(m, backup.PathHistory, hist, &force); err != nil {
			return fail("Restore config history", err)
		}
		_ = os.Chmod(hist, 0o700)
		okStep("Restore config history", "config history restored")
	}
	r, err := o.prepareRepo(ctx)
	if err != nil {
		return fail("Snapshot current /etc/asterisk to history", err)
	}
	if err := r.commitIfChanged(ctx, "Before restore from backup "+date, "aslcp"); err != nil {
		return fail("Snapshot current /etc/asterisk to history", err)
	}
	okStep("Snapshot current /etc/asterisk to history", "current configuration committed to the history")

	if err := o.applyConfig(sp, plan, root, deletes); err != nil {
		return fail("Restore /etc/asterisk", err)
	}
	dist := 0
	for _, f := range plan.files {
		if f.Action == ActRestoreDist {
			dist++
		}
	}
	okStep("Restore /etc/asterisk", fmt.Sprintf("%d files restored, %d .dpkg-dist copies, %d deleted", len(plan.writes), dist, len(deletes)))

	if m.Parts.AstDB {
		e := m.Find(backup.PathAstDB)
		if e == nil {
			return fail("Replace AstDB", errors.New("missing entry"))
		}
		if err := o.restoreAstDB(sp, *e); err != nil {
			return fail("Replace AstDB", err)
		}
		okStep("Replace AstDB", "")
	}
	if m.Parts.Allmon3 {
		if err := sp.replaceTree(m, backup.PathAllmon3, o.allmonDir(), nil); err != nil {
			return fail("Replace Allmon3 config", err)
		}
		okStep("Replace Allmon3 config", "")
	}
	if len(m.Parts.Sounds) > 0 {
		d, err := o.restoreSounds(sp, m)
		if err != nil {
			return fail("Replace custom sounds", err)
		}
		okStep("Replace custom sounds", d)
	}
	if m.Parts.DVSwitch {
		d, _, err := o.restoreDVSwitch(ctx, sp, m)
		if err != nil {
			return fail(dvStepName, err)
		}
		if d != "" {
			okStep(dvStepName, d)
		}
	}
	if m.Parts.SA818 {
		if err := o.restoreSingle(sp, backup.PathSA818, o.sa818Conf(), 0, 0, 0o644); err != nil {
			return fail("Restore SA818 settings", err)
		}
		okStep("Restore SA818 settings", "")
	}
	if e := m.Find(backup.PathAslcpConf); e != nil {
		uid, gid, mode := entryOwner(*e)
		if err := o.restoreSingle(sp, backup.PathAslcpConf, o.aslcpConf(), uid, gid, mode&0o664); err != nil {
			return fail("Restore ASLCP settings", err)
		}
		okStep("Restore ASLCP settings", "")
	}
	if m.Channel == ChannelStable || m.Channel == ChannelBeta {
		if _, err := o.setChannelLocked(ctx, m.Channel); err != nil {
			res.Steps = append(res.Steps, RestoreStep{Name: "Set update channel", OK: false, Detail: err.Error()})
		} else {
			okStep("Set update channel", m.Channel)
		}
	}
	if err := r.commitIfChanged(ctx, "Restored from backup "+date, "aslcp"); err != nil {
		res.Steps = append(res.Steps, RestoreStep{Name: "Commit restored configuration to history", OK: false, Detail: err.Error()})
	} else {
		okStep("Commit restored configuration to history", "restored configuration committed to the history")
	}
	stopped = false
	if out, code, err := o.Run.Run(ctx, "systemctl", "start", "asterisk"); err != nil || code != 0 {
		return fail("Start Asterisk", fmt.Errorf("%s", strings.TrimSpace(LastLines(out, 2)+" "+errOr(err))))
	}
	okStep("Start Asterisk", "")
	res.OK = true
	return res, nil
}

func errOr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
