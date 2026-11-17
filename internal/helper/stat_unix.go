//go:build unix

package helper

import (
	"io/fs"
	"syscall"
)

// ownerIDs returns the numeric owner of fi.
func ownerIDs(fi fs.FileInfo) (uid, gid int, ok bool) {
	if st, k := fi.Sys().(*syscall.Stat_t); k {
		return int(st.Uid), int(st.Gid), true
	}
	return 0, 0, false
}
