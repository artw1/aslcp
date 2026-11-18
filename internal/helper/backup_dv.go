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
// manifest. Nothing is redacted. The history copies of the setup page
// (/var/lib/aslcp-dvswitch-history) are not part of it.

// DVRestoreNotInstalled is the step detail and the warning shown when the
// target directory does not exist.
const DVRestoreNotInstalled = "DVSwitch is not installed on this node: install DVSwitch, then restore this part again"

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
	return added, nil
}

func dvHasFiles(m *backup.Manifest) bool {
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

// restoreDVSwitch writes the part's files. Without the target directory
// nothing is written and the step says to install DVSwitch first (installed
// false, no error). Every target is checked before the first write; each
// replaced file gets a history copy, like a change on the setup page.
func (o *Ops) restoreDVSwitch(ctx context.Context, sp *spool, m *backup.Manifest) (detail string, installed bool, err error) {
	var ents []backup.Entry
	for _, rel := range backup.DVSwitchFiles {
		if e := m.Find(backup.DVSwitchPath(rel)); e != nil {
			ents = append(ents, *e)
		}
	}
	if len(ents) == 0 {
		return "", true, nil
	}
	if !o.dvInstalled() {
		return DVRestoreNotInstalled, false, nil
	}
	if o.dvBusy(ctx) {
		return "", true, ErrBusy
	}
	type job struct {
		rel, dest string
		e         backup.Entry
		data      []byte
		cur       fs.FileInfo
		old       []byte
	}
	var jobs []job
	var skipped []string
	for _, e := range ents {
		rel := strings.TrimPrefix(e.Path, backup.PathDVSwitchOpt+"/")
		dest := filepath.Join(o.dvOptDir(), filepath.FromSlash(rel))
		if fi, err := os.Stat(filepath.Dir(dest)); err != nil || !fi.IsDir() {
			skipped = append(skipped, filepath.Dir(rel)+" is not installed")
			continue
		}
		if e.Size > dvIniMaxSize {
			return "", true, fmt.Errorf("%s is too large", rel)
		}
		data, err := sp.read(e.Path, dvIniMaxSize)
		if err != nil {
			return "", true, err
		}
		cur, old, err := dvRestoreCurrent(dest)
		if err != nil {
			return "", true, fmt.Errorf("%s %w", rel, err)
		}
		jobs = append(jobs, job{rel, dest, e, data, cur, old})
	}
	n := 0
	for _, j := range jobs {
		if j.cur != nil && string(j.old) == string(j.data) {
			continue // already the same
		}
		if j.cur != nil {
			if _, err := o.dvSaveHistory(dvBackupID(j.rel), j.old); err != nil {
				return "", true, fmt.Errorf("history copy of %s: %w", j.rel, err)
			}
		}
		parent, err := filepath.EvalSymlinks(filepath.Dir(j.dest))
		if err != nil {
			return "", true, err
		}
		uid, gid := dvOwner(j.e, j.cur)
		mode, _ := backup.ParseMode(j.e.Mode)
		if err := atomicWrite(filepath.Join(parent, filepath.Base(j.dest)), parent, j.data, uid, gid, fs.FileMode(mode&0o777)); err != nil {
			return "", true, fmt.Errorf("%s: %w", j.rel, err)
		}
		n++
	}
	detail = fmt.Sprintf("%d file(s) written", n)
	if len(skipped) > 0 {
		detail += "; skipped: " + strings.Join(dedupe(skipped), ", ")
	}
	return detail, true, nil
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
