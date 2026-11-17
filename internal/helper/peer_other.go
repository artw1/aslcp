//go:build !linux

package helper

import (
	"errors"
	"net"
)

// PeerUID is only implemented on Linux.
func PeerUID(net.Conn) (uint32, error) {
	return 0, errors.New("peer credentials unsupported on this OS")
}
