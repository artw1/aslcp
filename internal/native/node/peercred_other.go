//go:build !linux

package node

import (
	"errors"
	"net"
)

// peerUID is not available off Linux; the node runs on Linux only.
func peerUID(net.Conn) (int, error) { return -1, errors.New("peer credentials need Linux") }
