package helper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/artw1/aslcp/internal/backup"
)

// The DVSwitch backup part (docs/BACKUP.md): the allowlisted ini files and
// dvsm.macro, stored as dvswitch/opt/<rel> with owner, group and mode in the
// manifest, and the record of the private bridge node (backup_dv_private.go,
// dvswitch/state/privatenode.json). Nothing is redacted. The history copies of the setup page
// (/var/lib/aslcp-dvswitch-history) are not part of it.

// DVRestorePendingDetail is the step detail when DVSwitch is not installed
// and the part was staged instead of written.
const DVRestorePendingDetail = "DVSwitch is not installed: its settings were saved and will be applied when you install DVSwitch"

const dvStepName = "Restore DVSwitch configuration"

// dvBackupID is the history id of a DVSwitch part file: the ini ids of the
// setup page, "dvsm_macro" for the macro file.
func dvBackupID(rel string) string {
	for _, f := range dvFiles {
		if f.rel == rel {
			return f.id
		}
	}
	return "dvsm_macro"
}

// dvInstalled reports whether the core directory of DVSwitch exists.
func (o *Ops) dvInstalled() bool {
	fi, err := os.Stat(filepath.Join(o.dvOptDir(), "Analog_Bridge"))
	return err == nil && fi.IsDir()
}

// dvPartInfo describes the part for backup-info: present when DVSwitch is
// installed, size the sum of the files that would be included.
func (o *Ops) dvPartInfo() BackupPartInfo {
	p := BackupPartInfo{ID: "dvswitch"}
	if !o.dvInstalled() {
		return p
	}
	p.Present = true
	for _, rel := range backup.DVSwitchFiles {
		if fi, err := os.Lstat(filepath.Join(o.dvOptDir(), filepath.FromSlash(rel))); err == nil && fi.Mode().IsRegular() {
			p.Size += fi.Size()
		}
	}
	if fi, err := os.Lstat(o.dvPrivatePath()); err == nil && fi.Mode().IsRegular() && fi.Size() <= dvPrivateMax {
		p.Size += fi.Size()
	}
	return p
}

// collectDVSwitch adds the part's files to c. Symlinks, non-regular and
// oversized files are skipped (and said so); a missing file is not an error
// (a gateway may not be installed). It reports whether anything was added.
func (o *Ops) collectDVSwitch(c *collector) (bool, error) {
	added := false
	for _, rel := range backup.DVSwitchFiles {
		p := filepath.Join(o.dvOptDir(), filepath.FromSlash(rel))
		fi, err := os.Lstat(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil || !fi.Mode().IsRegular() {
			c.skip(rel, "not a regular file")
			continue
		}
		if fi.Size() > dvIniMaxSize {
			c.skip(rel, "too large")
			continue
		}
		n, sum, _, fi2, err := hashFile(p)
		if err != nil {
			c.skip(rel, "unreadable")
			continue
		}
		e := entryFor(backup.DVSwitchPath(rel), backup.TypeFile, fi2)
		e.Size, e.SHA256 = n, sum
		if err := c.add(srcEntry{e: e, src: p}); err != nil {
			return added, err
		}
		added = true
	}
	if ok, err := o.collectDVPrivate(c); err != nil {
		return added, err
	} else if ok {
		added = true
	}
	return added, nil
}

func dvHasFiles(m *backup.Manifest) bool {
	if m.Find(backup.PathDVSwitchPrivate) != nil {
		return true
	}
	for _, rel := range backup.DVSwitchFiles {
		if m.Find(backup.DVSwitchPath(rel)) != nil {
			return true
		}
	}
	return false
}

// dvOwner resolves the owner of a restored file: the names of the manifest
// when they exist on this node, else the owner of the file being replaced,
// else root.
func dvOwner(e backup.Entry, cur fs.FileInfo) (int, int) {
	uid, gid := 0, 0
	if cur != nil {
		if u, g, ok := ownerIDs(cur); ok {
			uid, gid = u, g
		}
	}
	if u, err := user.Lookup(e.Owner); err == nil {
		if v, err := strconv.Atoi(u.Uid); err == nil {
			uid = v
		}
	}
	if g, err := user.LookupGroup(e.Group); err == nil {
		if v, err := strconv.Atoi(g.Gid); err == nil {
			gid = v
		}
	}
	return uid, gid
}

// dvRestoreCurrent reads the file being replaced with the rules of the setup
// page: Lstat and O_NOFOLLOW, regular files only. A missing file is not an
// error (cur == nil, no data).
func dvRestoreCurrent(p string) (fs.FileInfo, []byte, error) {
	fi, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, nil, errors.New("is a symbolic link or not a regular file; not replaced")
	}
	fd, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, nil, err
	}
	defer fd.Close()
	fi2, err := fd.Stat()
	if err != nil || !fi2.Mode().IsRegular() {
		return nil, nil, errors.New("is a symbolic link or not a regular file; not replaced")
	}
	data, err := io.ReadAll(io.LimitReader(fd, dvIniMaxSize+1))
	if err != nil {
		return nil, nil, err
	}
	if len(data) > dvIniMaxSize {
		return nil, nil, errors.New("is too large; not replaced")
	}
	return fi2, data, nil
}

// dvItem is one file of the part: its relative path below /opt, the manifest
// entry (owner, group, mode) and the content.
type dvItem struct {
	rel  string
	e    backup.Entry
	data []byte
}

// restoreDVSwitch writes the part's files. Without the target directory
// (DVSwitch not installed) the files are not written to /opt: they are staged
// in the root-only pending directory and applied when DVSwitch is installed
// (installed false, no error). Every target is checked before the first
// write; each replaced file gets a history copy, like a change on the setup
// page. date is the backup's date, kept with a pending set.
func (o *Ops) restoreDVSwitch(ctx context.Context, sp *spool, m *backup.Manifest, date string) (detail string, installed bool, err error) {
	var items []dvItem
	for _, rel := range backup.DVSwitchFiles {
		e := m.Find(backup.DVSwitchPath(rel))
		if e == nil {
			continue
		}
		if e.Size > dvIniMaxSize {
			return "", true, fmt.Errorf("%s is too large", rel)
		}
		data, err := sp.read(e.Path, dvIniMaxSize)
		if err != nil {
			return "", true, err
		}
		items = append(items, dvItem{rel, *e, data})
	}
	// The bridge node record (dvswitch/state/privatenode.json): kept only when
	// the restored rpt.conf, written by the Asterisk step before this one,
	// has the node's stanza. Older backups have none.
	priv, privNode, privNote := o.dvRestoredPrivate(sp, m)
	if len(items) == 0 && (len(priv) == 0 || !o.dvInstalled()) {
		return privNote, true, nil
	}
	if !o.dvInstalled() {
		if err := o.dvStagePending(items, date, priv); err != nil {
			return "", false, fmt.Errorf("cannot save the DVSwitch settings: %w", err)
		}
		detail = DVRestorePendingDetail
		if len(priv) > 0 {
			detail += "; the bridge node record is saved too"
		}
		return joinNote(detail, privNote), false, nil
	}
	if o.dvBusy(ctx) {
		return "", true, ErrBusy
	}
	n, skipped, err := o.dvWriteItems(items)
	if err != nil {
		return "", true, err
	}
	detail = fmt.Sprintf("%d file(s) written", n)
	if len(skipped) > 0 {
		detail += "; skipped: " + strings.Join(dedupe(skipped), ", ")
	}
	if len(priv) > 0 {
		if err := o.dvWritePrivate(priv); err != nil {
			privNote = "the bridge node record could not be restored: " + err.Error()
		} else {
			detail += "; bridge node " + privNode + " is managed by ASLCP again"
		}
	}
	detail = joinNote(detail, privNote)
	if n > 0 {
		// A node installed with DVSwitch has the services stopped until
		// set up; the restored settings are the set-up.
		if err := o.dvStartRestored(ctx); err != nil {
			detail += "; starting the DVSwitch services failed: " + err.Error()
		} else {
			detail += "; DVSwitch services started"
		}
	}
	return detail, true, nil
}

// dvWriteItems is the safe write path shared by the restore step and the
// apply of a pending set: every target is checked before the first write
// (a symlink or non-regular file refuses everything), a file whose own
// directory is missing is skipped, each replaced file first gets a history
// copy, then is replaced atomically with the owner, group and mode of the
// entry. Identical files are left alone. It returns the number written and
// the skipped notes.
func (o *Ops) dvWriteItems(items []dvItem) (int, []string, error) {
	type job struct {
		dvItem
		dest string
		cur  fs.FileInfo
		old  []byte
	}
	var jobs []job
	var skipped []string
	for _, it := range items {
		dest := filepath.Join(o.dvOptDir(), filepath.FromSlash(it.rel))
		if fi, err := os.Stat(filepath.Dir(dest)); err != nil || !fi.IsDir() {
			skipped = append(skipped, filepath.Dir(it.rel)+" is not installed")
			continue
		}
		cur, old, err := dvRestoreCurrent(dest)
		if err != nil {
			return 0, nil, fmt.Errorf("%s %w", it.rel, err)
		}
		jobs = append(jobs, job{it, dest, cur, old})
	}
	n := 0
	for _, j := range jobs {
		if j.cur != nil && string(j.old) == string(j.data) {
			continue // already the same
		}
		if j.cur != nil {
			if _, err := o.dvSaveHistory(dvBackupID(j.rel), j.old); err != nil {
				return n, skipped, fmt.Errorf("history copy of %s: %w", j.rel, err)
			}
		}
		parent, err := filepath.EvalSymlinks(filepath.Dir(j.dest))
		if err != nil {
			return n, skipped, err
		}
		uid, gid := dvOwner(j.e, j.cur)
		mode, _ := backup.ParseMode(j.e.Mode)
		if err := atomicWrite(filepath.Join(parent, filepath.Base(j.dest)), parent, j.data, uid, gid, fs.FileMode(mode&0o777)); err != nil {
			return n, skipped, fmt.Errorf("%s: %w", j.rel, err)
		}
		n++
	}
	return n, skipped, nil
}

// joinNote appends note to a step detail.
func joinNote(detail, note string) string {
	if note == "" {
		return detail
	}
	if detail == "" {
		return note
	}
	return detail + "; " + note
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
