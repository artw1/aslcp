package helper

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/artw1/aslcp/internal/astconf"
	"github.com/artw1/aslcp/internal/backup"
)

// The record of the private bridge node in the backup (docs/BACKUP.md). It
// decides whether ASLCP manages the node (renumber, remove, uninstall), and
// the restored rpt.conf has the stanza again, so the record has to come with
// it. It is not under /opt: the archive path is dv/state/privatenode.json
// and the target is the helper's state directory. Nothing here logs the
// record: only the node number goes into a step detail.

// dvPrivateStaged is the name of the record inside a pending set (next to the
// ini files, whose names always contain "__", and manifest.json).
const dvPrivateStaged = dvPrivateFile

// collectDVPrivate adds the record to c when there is a usable one. A symlink
// or non-regular file, an oversized, unreadable or invalid record is skipped
// and said so; no record is not an error. It reports whether it was added.
func (o *Ops) collectDVPrivate(c *collector) (bool, error) {
	p := o.dvPrivatePath()
	rel := backup.PathDVPrivate
	fi, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil || !fi.Mode().IsRegular() {
		c.skip(rel, "not a regular file")
		return false, nil
	}
	if fi.Size() > dvPrivateMax {
		c.skip(rel, "too large")
		return false, nil
	}
	data, err := dvReadNoFollow(p, dvPrivateMax)
	if err != nil {
		c.skip(rel, "unreadable")
		return false, nil
	}
	if rec, err := parseDVPrivate(data); err != nil || !rec.valid() {
		c.skip(rel, "not a valid record")
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

// dvRestoredPrivate reads, validates and re-encodes the record of the backup.
// data is nil when there is nothing to restore (an older backup without it
// changes nothing).
// A record that cannot be used is dropped with the reason in note; it is never
// an error, the rest of the restore does not depend on it.
func (o *Ops) dvRestoredPrivate(sp *spool, m *backup.Manifest) (data []byte, node, note string) {
	e := m.FindDVPrivate()
	if e == nil {
		return nil, "", ""
	}
	raw, err := sp.read(e.Path, dvPrivateMax)
	if err != nil {
		return nil, "", "the bridge node record in the backup is too large or unreadable and was not restored"
	}
	rec, err := parseDVPrivate(raw)
	if err != nil || !rec.valid() {
		return nil, "", "the bridge node record in the backup is not valid and was not restored"
	}
	if !o.dvRestoredHasStanza(rec.Node) {
		return nil, "", "the bridge node record was not restored: the restored rpt.conf has no node " + rec.Node
	}
	out, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return nil, "", "the bridge node record in the backup was not restored"
	}
	return append(out, '\n'), rec.Node, ""
}

// dvRestoredHasStanza reports whether rpt.conf on disk, as the restore wrote
// it, has the stanza [node]. A file that cannot be read says no.
func (o *Ops) dvRestoredHasStanza(node string) bool {
	cur, err := o.ConfigRead("rpt.conf")
	if err != nil {
		return false
	}
	return astconf.HasNodeStanza([]byte(cur.Content), node)
}

// dvWritePrivate writes the record root-only (0600) into the state
// directory (0700), atomically, never following a symlink.
func (o *Ops) dvWritePrivate(data []byte) error {
	dir := o.dvStateDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(parent, dvPrivateFile), parent, data, 0, 0, 0o600)
}
