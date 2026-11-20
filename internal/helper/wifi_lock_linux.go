//go:build linux

package helper

import (
	"os"
	"syscall"
)

// flockFile takes (lock true, without blocking) or releases an exclusive
// flock on f.
func flockFile(f *os.File, lock bool) error {
	if !lock {
		return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	}
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}
