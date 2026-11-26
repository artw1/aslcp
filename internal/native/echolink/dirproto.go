package echolink

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

// Directory protocol, as used by EchoLink clients on TCP port 5200. Each
// request uses a fresh TCP connection that the server closes after replying.
//
// Logon: "l" CALLSIGN 0xAC 0xAC PASSWORD "\r" STATUS VERSION "(" HH:MM ")\r"
// LOCATION "\r", answered with "OK..." on success. STATUS is ONLINE, BUSY or
// OFF-V (log off).
//
// Station list: "s", answered with "@@@\n", a count line, then four lines
// per station (callsign, description, node number, IP address) and "+++".
// The description ends in "[STATUS HH:MM]". Some servers end lines with
// "\r\n"; both are accepted.

// Logon status words.
const (
	statusOnline  = "ONLINE"
	statusBusy    = "BUSY"
	statusOffline = "OFF-V"
)

// clientVersion is the version string sent after the status word. The
// directory shows it but does not check it, as far as is known.
const clientVersion = "3.40"

const (
	maxLogonReply = 1 << 10
	maxListBytes  = 8 << 20
	maxFieldLen   = 255
)

// Errors returned by directory requests.
var (
	// ErrBadLogon means the server refused the logon, for example because of
	// a wrong password or an unvalidated callsign.
	ErrBadLogon = errors.New("echolink: directory refused logon")
	// ErrBadReply means the server's reply could not be parsed.
	ErrBadReply = errors.New("echolink: unexpected directory reply")
	// ErrNotFound means a station is not in the station list.
	ErrNotFound = errors.New("echolink: station not found")
)

// cleanField removes the separators the logon format cannot carry and caps
// the length.
func cleanField(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '\r' || r == '\n' || r == 0xAC || r < 0x20 {
			return -1
		}
		return r
	}, s)
	if len(s) > maxFieldLen {
		s = s[:maxFieldLen]
	}
	return s
}

// logonCommand builds a logon request.
func logonCommand(callsign, password, status, location string, now time.Time) []byte {
	var b bytes.Buffer
	b.WriteByte('l')
	b.WriteString(cleanField(strings.ToUpper(callsign)))
	b.Write([]byte{0xAC, 0xAC})
	b.WriteString(cleanField(password))
	b.WriteByte('\r')
	b.WriteString(status)
	b.WriteString(clientVersion)
	b.WriteString("(" + now.Format("15:04") + ")\r")
	b.WriteString(cleanField(location))
	b.WriteByte('\r')
	return b.Bytes()
}

// readLogonReply reads the server's answer to a logon. It returns as soon as
// the answer is known, without waiting for the server to close.
func readLogonReply(r io.Reader) error {
	var buf []byte
	tmp := make([]byte, 256)
	for {
		n, err := r.Read(tmp)
		buf = append(buf, tmp[:n]...)
		reply := strings.TrimLeft(string(buf), " \r\n")
		switch {
		case strings.HasPrefix(reply, "OK"):
			return nil
		case strings.HasPrefix(reply, "BAD"), strings.HasPrefix(reply, "NO"):
			// The server's text may echo our input; keep only the first word.
			word := reply
			if i := strings.IndexAny(word, " \r\n"); i >= 0 {
				word = word[:i]
			}
			return fmt.Errorf("%w: %q", ErrBadLogon, truncate(word, 16))
		case len(reply) >= 3:
			return fmt.Errorf("%w: %q", ErrBadReply, truncate(reply, 32))
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				if reply == "" {
					return fmt.Errorf("%w: empty", ErrBadReply)
				}
				return fmt.Errorf("%w: %q", ErrBadReply, reply)
			}
			return fmt.Errorf("echolink: read logon reply: %w", err)
		}
		if len(buf) > maxLogonReply {
			return fmt.Errorf("%w: reply too long", ErrBadReply)
		}
	}
}

// parseStationList parses a station list reply. Malformed records are
// skipped; a reply without the "@@@" header is an error.
func parseStationList(r io.Reader) ([]Station, error) {
	sc := bufio.NewScanner(io.LimitReader(r, maxListBytes))
	sc.Buffer(make([]byte, 0, 4096), 64<<10)
	next := func() (string, bool) {
		if !sc.Scan() {
			return "", false
		}
		return strings.TrimRight(sc.Text(), "\r\x00"), true
	}
	line, ok := next()
	if !ok {
		if err := sc.Err(); err != nil {
			return nil, fmt.Errorf("echolink: read station list: %w", err)
		}
		return nil, fmt.Errorf("%w: empty station list", ErrBadReply)
	}
	if strings.TrimSpace(line) != "@@@" {
		return nil, fmt.Errorf("%w: station list header %q", ErrBadReply, truncate(line, 16))
	}
	countLine, ok := next()
	if !ok {
		return nil, fmt.Errorf("%w: missing station count", ErrBadReply)
	}
	count, err := strconv.Atoi(strings.TrimSpace(countLine))
	if err != nil || count < 0 {
		return nil, fmt.Errorf("%w: station count %q", ErrBadReply, truncate(countLine, 16))
	}
	list := make([]Station, 0, min(count, 20000))
	for {
		call, ok := next()
		if !ok || strings.TrimSpace(call) == "+++" {
			break
		}
		desc, ok1 := next()
		idLine, ok2 := next()
		ipLine, ok3 := next()
		if !ok1 || !ok2 || !ok3 {
			break
		}
		id, err := strconv.Atoi(strings.TrimSpace(idLine))
		if err != nil || id <= 0 {
			continue
		}
		addr, err := netip.ParseAddr(strings.TrimSpace(ipLine))
		if err != nil || !addr.Is4() {
			continue
		}
		s := Station{
			Callsign: normalizeTarget(call),
			ID:       id,
			Addr:     addr,
		}
		s.Location, s.Status, s.Time = splitDescription(desc)
		list = append(list, s)
	}
	if err := sc.Err(); err != nil {
		return list, fmt.Errorf("echolink: read station list: %w", err)
	}
	return list, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
