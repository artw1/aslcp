//go:build !unix

package helper

import "io/fs"

func ownerIDs(fs.FileInfo) (int, int, bool) { return 0, 0, false }
