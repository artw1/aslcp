package helper

import (
	"os"

	"github.com/artw1/aslcp/internal/backup"
)

// The digital modes backup part (docs/BACKUP.md): the record of the private
// bridge node (backup_dv_private.go, dv/state/privatenode.json) and the DMR
// network settings (backup_dv_networks.go, dv/state/networks.json). Nothing is
// redacted. Archives made by 1.x also carry the ini files of the previous
// stack; 2.0 does not restore them (restoreDV says so).

const dvStepName = "Restore digital modes configuration"

// DVRestoreIgnoredDetail is the step detail when the archive carries settings
// of the previous digital voice stack.
const DVRestoreIgnoredDetail = "settings of the previous digital voice stack in the backup are not restored; set up digital modes again"

// dvPartInfo describes the part for backup-info: present when the bridge
// node record or the network settings exist, size the sum of both files.
func (o *Ops) dvPartInfo() BackupPartInfo {
	p := BackupPartInfo{ID: "dv"}
	if fi, err := os.Lstat(o.dvPrivatePath()); err == nil && fi.Mode().IsRegular() && fi.Size() <= dvPrivateMax {
		p.Present = true
		p.Size += fi.Size()
	}
	if fi, err := os.Lstat(o.dvNetworksPath()); err == nil && fi.Mode().IsRegular() && fi.Size() <= dvNetworksMax {
		p.Present = true
		p.Size += fi.Size()
	}
	return p
}

// collectDV adds the part's files to c; it reports whether anything was added.
func (o *Ops) collectDV(c *collector) (bool, error) {
	added := false
	if ok, err := o.collectDVPrivate(c); err != nil {
		return added, err
	} else if ok {
		added = true
	}
	if ok, err := o.collectDVNetworks(c); err != nil {
		return added, err
	} else if ok {
		added = true
	}
	return added, nil
}

func dvHasFiles(m *backup.Manifest) bool {
	return m.FindDVPrivate() != nil || m.FindDVNetworks() != nil
}

// dvHasLegacyFiles reports whether the archive carries ini files of the
// previous stack (made by 1.x).
func dvHasLegacyFiles(m *backup.Manifest) bool {
	for _, rel := range backup.DVFiles {
		if m.FindDV(rel) != nil {
			return true
		}
	}
	return false
}

// restoreDV restores the engine's own state: the bridge node record and the
// DMR network settings. The ini files of the previous stack in an archive
// made by 1.x are ignored, and the step says so.
func (o *Ops) restoreDV(sp *spool, m *backup.Manifest) (detail string, err error) {
	// The bridge node record (dv/state/privatenode.json): kept only when
	// the restored rpt.conf, written by the Asterisk step before this one,
	// has the node's stanza. Older backups have none.
	priv, privNode, privNote := o.dvRestoredPrivate(sp, m)
	// The DMR network settings (dv/state/networks.json): every DMR
	// network mode's settings and enabled state. Older backups have none.
	nets, netsNote := dvRestoredNetworks(sp, m)
	privNote = joinNote(privNote, netsNote)
	if len(priv) > 0 {
		if err := o.dvWritePrivate(priv); err != nil {
			privNote = "the bridge node record could not be restored: " + err.Error()
		} else {
			detail = "bridge node " + privNode + " is managed by ASLCP again"
		}
	}
	if len(nets) > 0 {
		if err := o.dvWriteNetworks(nets); err != nil {
			privNote = joinNote(privNote, "the DMR network settings could not be restored: "+err.Error())
		} else {
			detail = joinNote(detail, "DMR network settings restored")
		}
	}
	detail = joinNote(detail, privNote)
	if dvHasLegacyFiles(m) {
		detail = joinNote(detail, DVRestoreIgnoredDetail)
	}
	return detail, nil
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
