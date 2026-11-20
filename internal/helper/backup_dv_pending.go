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

// Pending DVSwitch settings (docs/BACKUP.md, "Apply" step 5): a restore onto a
// node without DVSwitch stages the part's files here, root-only, instead of
// skipping them. The install unit applies them (dvswitch-step restore-apply)
// once the packages are installed. Nothing in this file logs file contents:
// they hold the DMR password.

const (
	dvPendingDirName  = "restore-pending"
	dvPendingManifest = "manifest.json"
)

// DVRestorePending is the part of dvswitch-status that describes a staged set.
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
}

func (o *Ops) dvPendingDir() string { return filepath.Join(o.dvStateDir(), dvPendingDirName) }

// dvPendingName is the file name of a target inside the pending directory. It
// is derived from the allowlisted relative path, never read from the manifest.
func dvPendingName(rel string) string { return strings.ReplaceAll(rel, "/", "__") }

func dvAllowedRel(rel string) bool {
	for _, r := range backup.DVSwitchFiles {
		if r == rel {
			return true
		}
	}
	return false
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
			return fmt.Errorf("%s is not a DVSwitch part file", it.rel)
		}
		f, err := os.OpenFile(filepath.Join(tmp, dvPendingName(it.rel)), os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
		if err != nil {
			return err
		}
		_, werr := f.Write(it.data)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return werr
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
		return nil, errors.New("pending DVSwitch settings: not a directory")
	}
	raw, err := dvReadNoFollow(filepath.Join(dir, dvPendingManifest), 1<<16)
	if err != nil {
		return nil, err
	}
	var m dvPendingMeta
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("pending DVSwitch settings: %w", err)
	}
	if m.Version != 1 || len(m.Files) == 0 || len(m.Files) > len(backup.DVSwitchFiles) {
		return nil, errors.New("pending DVSwitch settings: unsupported content")
	}
	seen := map[string]bool{}
	for _, f := range m.Files {
		if !dvAllowedRel(f.Rel) || seen[f.Rel] {
			return nil, errors.New("pending DVSwitch settings: unexpected file")
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

// DVSwitchRestoreDiscard deletes the pending set (dvswitch-restore-discard).
// It is refused while an install is writing the files.
func (o *Ops) DVSwitchRestoreDiscard(ctx context.Context) (DVSwitchStatus, error) {
	o.applyMu.Lock()
	defer o.applyMu.Unlock()
	if o.dvBusy(ctx) {
		return DVSwitchStatus{}, ErrBusy
	}
	if err := os.RemoveAll(o.dvPendingDir()); err != nil {
		return DVSwitchStatus{}, fmt.Errorf("cannot remove the pending DVSwitch settings: %w", err)
	}
	return o.DVSwitchStatus(ctx)
}

// dvRestoreApply is the install unit's step: it applies the pending set with
// the safe write path of the restore, logs file names (never contents) to w,
// deletes the pending directory and starts the DVSwitch services. It never
// fails the install: problems are warnings and the set stays for a look.
func (o *Ops) dvRestoreApply(ctx context.Context, w io.Writer) error {
	m, err := o.dvReadPendingMeta()
	if err != nil {
		fmt.Fprintf(w, "warning: the DVSwitch settings saved from your backup cannot be used (%v); they were discarded\n", err)
		_ = os.RemoveAll(o.dvPendingDir())
		return nil
	}
	if m == nil {
		return nil
	}
	if !o.dvInstalled() {
		fmt.Fprintln(w, "warning: DVSwitch is not installed; the saved settings from your backup were not applied")
		return nil
	}
	var items []dvItem
	for _, f := range m.Files {
		data, err := dvReadNoFollow(filepath.Join(o.dvPendingDir(), dvPendingName(f.Rel)), dvIniMaxSize)
		if err != nil {
			fmt.Fprintf(w, "warning: saved DVSwitch settings cannot be applied: %s: %v\n", f.Rel, err)
			return nil
		}
		items = append(items, dvItem{rel: f.Rel, e: backup.Entry{Path: backup.DVSwitchPath(f.Rel), Owner: f.Owner, Group: f.Group, Mode: f.Mode}, data: data})
	}
	fmt.Fprintf(w, "applying the DVSwitch settings saved from your backup of %s\n", m.BackupDate)
	n, skipped, err := o.dvWriteItems(items)
	if err != nil {
		fmt.Fprintf(w, "warning: the saved DVSwitch settings were not fully applied: %v (they are kept; discard them on the DVSwitch page)\n", err)
		return nil
	}
	fmt.Fprintf(w, "%d file(s) written\n", n)
	for _, s := range dedupe(skipped) {
		fmt.Fprintf(w, "skipped: %s\n", s)
	}
	if err := os.RemoveAll(o.dvPendingDir()); err != nil {
		fmt.Fprintf(w, "warning: cannot remove the saved settings: %v\n", err)
	}
	if n > 0 {
		// Start (or restart) the bridges and gateways that are installed: a
		// fresh install leaves them stopped until the setup page saves, and
		// these settings need no saving. stfu stays on demand.
		loaded := map[string]bool{}
		for _, u := range o.dvUnitStates(ctx) {
			if u.Loaded {
				loaded[u.Name] = true
			}
		}
		argv := []string{"systemctl", "restart"}
		for _, u := range dvRestoreRestartUnits {
			if len(loaded) == 0 || loaded[u] {
				argv = append(argv, u+".service")
			}
		}
		if out, code, err := o.Run.Run(ctx, argv...); err != nil || code != 0 {
			fmt.Fprintf(w, "warning: starting the DVSwitch services failed: %s\n", strings.TrimSpace(LastLines(out, 2)+" "+errOr(err)))
		} else {
			fmt.Fprintln(w, "DVSwitch services started")
		}
	}
	return nil
}

// dvRestoreRestartUnits are started after an apply (all but stfu, which runs
// on demand while the panel is in BrandMeister mode).
var dvRestoreRestartUnits = []string{"analog_bridge", "mmdvm_bridge", "ysfgateway", "p25gateway", "nxdngateway"}
