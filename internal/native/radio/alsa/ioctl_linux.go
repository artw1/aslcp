package alsa

import (
	"unsafe"

	"golang.org/x/sys/unix"
)

// ioctl issues a request whose argument is the pointer p (nil for none).
func ioctl(fd int, req uintptr, p unsafe.Pointer) error {
	_, _, e := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), req, uintptr(p))
	if e != 0 {
		return e
	}
	return nil
}
