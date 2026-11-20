//go:build !linux

package helper

// StartLogin has no pseudo terminal to use outside Linux.
func StartLogin(rows, cols uint16) (PTY, error) { return nil, errTerminalUnsupported }
