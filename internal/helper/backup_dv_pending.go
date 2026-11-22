package helper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/artw1/aslcp/internal/backup"
)

// Pending the previous stack settings (docs/BACKUP.md, "Apply" step 5): a restore onto a
// node without the previous stack stages the part's files here, root-only, instead of
// skipping them. The install unit applies them (dv-step restore-apply)
// once the packages are installed. Nothing in this file logs file contents:
// they hold the DMR password.

const (
	dvPendingDirName  = "restore-pending"
	dvPendingManifest = "manifest.json"
)

// DVRestorePending is the part of dv-status that describes a staged set.
type DVRestorePending struct {
	Pending bool `json:"pending"`
	// BackupDate is the creation time of the backup the set came from.
	BackupDate string `json:"backupDate,omitempty"`
	// Files are the target paths (/opt/...), no content.
	Files []string `json:"files"`
}

type dvPendingFile struct {
	Rel   string `json:"rel"`
	Owner string `json:"owner"`
	Group string `json:"group"`
	Mode  string `json:"mode"`
	Size  int64  `json:"size"`
}

type dvPendingMeta struct {
	Version    int             `json:"version"`
	BackupDate string          `json:"backupDate"`
	Files      []dvPendingFile `json:"files"`
	// Private and Networks are read from sets staged by earlier versions;
	// nothing sets them any more (the record and the settings are written
	// at restore).
	Private  bool `json:"private,omitempty"`
	Networks bool `json:"networks,omitempty"`
}

func (o *Ops) dvPendingDir() string { return filepath.Join(o.dvStateDir(), dvPendingDirName) }

// dvPendingName is the file name of a target inside the pending directory. It
// is derived from the allowlisted relative path, never read from the manifest.
func dvPendingName(rel string) string { return strings.ReplaceAll(rel, "/", "__") }

func dvAllowedRel(rel string) bool {
	for _, r := range backup.DVFiles {
		if r == rel {
			return true
		}
	}
	return false
}

// writeNew creates path (0600, exclusively, no symlink following) with data.
func writeNew(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

// dvStagePending replaces any pending set with items: a new 0700 directory,
// files 0600, then a rename into place.
func (o *Ops) dvStagePending(items []dvItem, date string) error {
	base := o.dvStateDir()
	if err := os.MkdirAll(base, 0o700); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(base, dvPendingDirName+".tmp")
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(tmp)
		}
	}()
	if err := os.Chmod(tmp, 0o700); err != nil {
		return err
	}
	meta := dvPendingMeta{Version: 1, BackupDate: date}
	for _, it := range items {
		if !dvAllowedRel(it.rel) || len(it.data) > dvIniMaxSize {
			return fmt.Errorf("%s is not a digital modes part file", it.rel)
		}
		if err := writeNew(filepath.Join(tmp, dvPendingName(it.rel)), it.data); err != nil {
			return err
		}
		meta.Files = append(meta.Files, dvPendingFile{Rel: it.rel, Owner: it.e.Owner, Group: it.e.Group, Mode: it.e.Mode, Size: int64(len(it.data))})
	}
	raw, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, dvPendingManifest), append(raw, '\n'), 0o600); err != nil {
		return err
	}
	final := o.dvPendingDir()
	if err := os.RemoveAll(final); err != nil {
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		return err
	}
	ok = true
	return nil
}

// dvReadPendingMeta reads and validates the manifest of the pending set;
// (nil, nil) when there is none. The directory must be a real directory (no
// symlink) and every listed file an allowlisted target.
func (o *Ops) dvReadPendingMeta() (*dvPendingMeta, error) {
	dir := o.dvPendingDir()
	fi, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, errors.New("pending digital modes settings: not a directory")
	}
	raw, err := dvReadNoFollow(filepath.Join(dir, dvPendingManifest), 1<<16)
	if err != nil {
		return nil, err
	}
	var m dvPendingMeta
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("pending digital modes settings: %w", err)
	}
	if m.Version != 1 || len(m.Files) == 0 || len(m.Files) > len(backup.DVFiles) {
		return nil, errors.New("pending digital modes settings: unsupported content")
	}
	seen := map[string]bool{}
	for _, f := range m.Files {
		if !dvAllowedRel(f.Rel) || seen[f.Rel] {
			return nil, errors.New("pending digital modes settings: unexpected file")
		}
		seen[f.Rel] = true
	}
	return &m, nil
}

// dvReadNoFollow reads a regular file of at most max bytes without following
// a symlink.
func dvReadNoFollow(p string, max int) ([]byte, error) {
	fd, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer fd.Close()
	fi, err := fd.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(fd, int64(max)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > max {
		return nil, errors.New("too large")
	}
	return data, nil
}

// dvPendingStatus is the status field. An unreadable set reads as none.
func (o *Ops) dvPendingStatus() DVRestorePending {
	st := DVRestorePending{Files: []string{}}
	m, err := o.dvReadPendingMeta()
	if err != nil || m == nil {
		return st
	}
	st.Pending, st.BackupDate = true, m.BackupDate
	for _, f := range m.Files {
		st.Files = append(st.Files, filepath.Join(o.dvOptDir(), filepath.FromSlash(f.Rel)))
	}
	return st
}

// DVRestoreDiscard deletes the pending set (dv-restore-discard). It is
// refused while the enable job is running.
func (o *Ops) DVRestoreDiscard(ctx context.Context) (DVStatus, error) {
	o.applyMu.Lock()
	defer o.applyMu.Unlock()
	if o.dvBusy(ctx) {
		return DVStatus{}, ErrBusy
	}
	if err := os.RemoveAll(o.dvPendingDir()); err != nil {
		return DVStatus{}, fmt.Errorf("cannot remove the pending settings: %w", err)
	}
	return o.DVStatus(ctx)
}
