//go:build !linux

package helper

import (
	"fmt"
	"os"
)

// RunTermHost has no pseudo terminal to use outside Linux.
func RunTermHost(args []string) int {
	fmt.Fprintln(os.Stderr, "term-host needs Linux")
	return 1
}
