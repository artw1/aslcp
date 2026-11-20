//go:build !linux

package helper

import "os"

// flockFile is a no-op away from Linux (the helper only runs there); the
// in-process mutex still serialises the ops.
func flockFile(*os.File, bool) error { return nil }
