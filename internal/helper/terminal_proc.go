package helper

import (
	"encoding/binary"
	"strconv"
	"strings"
)

// Pure parsers used to find the Linux user of a terminal session. login runs
// as the session leader of the PTY; after PAM it starts the user's shell in
// the same session, and (when it is built to) records the user in utmp.

// parseProcStat returns the pid, the session id and the state letter of
// /proc/<pid>/stat. The command name is in parentheses and may itself contain
// spaces and parentheses, so the fields are counted from the last ')'.
func parseProcStat(text string) (pid, sid int, state byte, ok bool) {
	i := strings.IndexByte(text, ' ')
	j := strings.LastIndexByte(text, ')')
	if i < 1 || j < i {
		return 0, 0, 0, false
	}
	pid, err := strconv.Atoi(text[:i])
	if err != nil {
		return 0, 0, 0, false
	}
	// After the name: state ppid pgrp session ...
	f := strings.Fields(text[j+1:])
	if len(f) < 4 || len(f[0]) != 1 {
		return 0, 0, 0, false
	}
	sid, err = strconv.Atoi(f[3])
	if err != nil {
		return 0, 0, 0, false
	}
	return pid, sid, f[0][0], true
}

// parseProcUID returns the real uid of /proc/<pid>/status.
func parseProcUID(text string) (uint32, bool) {
	for _, line := range strings.Split(text, "\n") {
		if rest, ok := strings.CutPrefix(line, "Uid:"); ok {
			f := strings.Fields(rest)
			if len(f) == 0 {
				return 0, false
			}
			n, err := strconv.ParseUint(f[0], 10, 32)
			if err != nil {
				return 0, false
			}
			return uint32(n), true
		}
	}
	return 0, false
}

// utmp record layout (glibc, the same on 32 and 64 bit): type int16 at 0,
// line [32] at 8, user [32] at 44, 384 bytes in all.
const (
	utmpRecSize  = 384
	utmpLineOff  = 8
	utmpUserOff  = 44
	utmpUserLen  = 32
	utmpLineLen  = 32
	utmpUserProc = 7 // USER_PROCESS
)

// utmpUser returns the user of the last USER_PROCESS record whose line is
// line (for example "pts/3"), or "".
func utmpUser(data []byte, line string) string {
	var user string
	for off := 0; off+utmpRecSize <= len(data); off += utmpRecSize {
		rec := data[off : off+utmpRecSize]
		if int16(binary.LittleEndian.Uint16(rec[0:2])) != utmpUserProc && int16(binary.BigEndian.Uint16(rec[0:2])) != utmpUserProc {
			continue
		}
		if cstr(rec[utmpLineOff:utmpLineOff+utmpLineLen]) != line {
			continue
		}
		user = cstr(rec[utmpUserOff : utmpUserOff+utmpUserLen])
	}
	return user
}

func cstr(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}
