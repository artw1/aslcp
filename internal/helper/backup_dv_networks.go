package helper

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/artw1/aslcp/internal/backup"
)

// The DMR network settings in the backup (docs/BACKUP.md): networks.json of
// the state directory, archive path dv/state/networks.json. It holds
// every DMR network mode's settings and enabled state, so a restore brings
// back the networks that are not the one in [DMR Network] too. Passwords are
// stored as they are, like the ini files of the part (the archive is
// root-only); nothing here logs the content.

// collectDVNetworks adds networks.json to c when it is usable. Without the
// file nothing is added: a node still on the 1.3 state has its settings in
// the ini files of the part.
func (o *Ops) collectDVNetworks(c *collector) (bool, error) {
	p := o.dvNetworksPath()
	rel := backup.PathDVNetworks
	dvNetMu.Lock()
	defer dvNetMu.Unlock()
	fi, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil || !fi.Mode().IsRegular() {
		c.skip(rel, "not a regular file")
		return false, nil
	}
	if fi.Size() > dvNetworksMax {
		c.skip(rel, "too large")
		return false, nil
	}
	data, err := dvReadNoFollow(p, dvNetworksMax)
	if err != nil {
		c.skip(rel, "unreadable")
		return false, nil
	}
	if _, err := parseDVNetworks(data); err != nil {
		c.skip(rel, "not valid")
		return false, nil
	}
	n, sum, _, fi2, err := hashFile(p)
	if err != nil {
		c.skip(rel, "unreadable")
		return false, nil
	}
	e := entryFor(rel, backup.TypeFile, fi2)
	e.Size, e.SHA256 = n, sum
	if err := c.add(srcEntry{e: e, src: p}); err != nil {
		return false, err
	}
	return true, nil
}

// dvRestoredNetworks reads, validates and re-encodes the settings of the
// backup; nil when there are none. Unusable settings are dropped with the
// reason in note, never an error.
func dvRestoredNetworks(sp *spool, m *backup.Manifest) (data []byte, note string) {
	e := m.FindDVNetworks()
	if e == nil {
		return nil, ""
	}
	raw, err := sp.read(e.Path, dvNetworksMax)
	if err != nil {
		return nil, "the DMR network settings in the backup are too large or unreadable and were not restored"
	}
	out, err := dvReencodeNetworks(raw)
	if err != nil {
		return nil, "the DMR network settings in the backup are not valid and were not restored"
	}
	return out, ""
}

func dvReencodeNetworks(raw []byte) ([]byte, error) {
	st, err := parseDVNetworks(raw)
	if err != nil {
		return nil, err
	}
	out, err := encodeDVNetworks(st)
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// dvWriteNetworks writes the settings root-only (0600) into the state
// directory (0700), atomically, never following a symlink.
func (o *Ops) dvWriteNetworks(data []byte) error {
	dvNetMu.Lock()
	defer dvNetMu.Unlock()
	dir := o.dvStateDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(parent, dvNetworksFile), parent, data, 0, 0, 0o600); err != nil {
		return err
	}
	return nil
}
