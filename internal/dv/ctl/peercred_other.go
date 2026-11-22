//go:build !linux

package ctl

import (
	"net"
	"os"
)

// peerUID is a development stub: aslcp-dv only ships for Linux, where
// SO_PEERCRED is checked for real. Elsewhere every peer counts as the
// current user.
func peerUID(net.Conn) (int, error) { return os.Getuid(), nil }
