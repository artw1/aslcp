package iax2

import (
	"fmt"
	"strings"
)

// app_rpt link text strings (app_rpt.h).
const (
	TextNewKey     = "!NEWKEY!"
	TextNewKey1    = "!NEWKEY1!"
	TextIAXKey     = "!IAXKEY!"
	TextDisconnect = "!!DISCONNECT!!"
)

// LinkMode is the one-letter mode prefix in an app_rpt link list.
type LinkMode byte

// Link list modes.
const (
	ModeTransceive LinkMode = 'T'
	ModeReceive    LinkMode = 'R'
	ModeConnecting LinkMode = 'C'
	ModeLocal      LinkMode = 'L'
)

// LinkEntry is one node in a link list.
type LinkEntry struct {
	Mode LinkMode
	Node string // node number; 3xxxxxx for EchoLink nodes
}

// String renders the entry as app_rpt writes it, e.g. "T2000".
func (e LinkEntry) String() string { return string(rune(e.Mode)) + e.Node }

// TextKind classifies a link text message.
type TextKind int

// Link text kinds. Only those an end-user node acts on are parsed in full.
const (
	TextKindOther      TextKind = iota // anything else, kept raw
	TextKindNewKey                     // !NEWKEY!: peer keys with RADIO_KEY/UNKEY
	TextKindNewKey1                    // !NEWKEY1!: voice presence keys the link
	TextKindIAXKey                     // legacy, ignored
	TextKindDisconnect                 // peer asks us to drop the link
	TextKindLinkList                   // "L <list>"
	TextKindMessage                    // "M <src> <dest> <text>"
	TextKindTelemetry                  // "T <src> <dest> ..."
	TextKindDTMF                       // "D ..." (not acted on)
	TextKindGPS                        // "G ..." (not acted on)
)

// LinkText is a parsed link text message.
type LinkText struct {
	Kind  TextKind
	Raw   string
	Links []LinkEntry // TextKindLinkList
	From  string      // TextKindMessage, TextKindTelemetry
	To    string      // TextKindMessage, TextKindTelemetry
	Body  string      // TextKindMessage, TextKindTelemetry: the rest
}

// ParseLinkText parses the payload of a TEXT frame. Trailing NULs, which
// chan_iax2 includes, are removed.
func ParseLinkText(payload []byte) LinkText {
	s := strings.TrimRight(string(payload), "\x00")
	t := LinkText{Kind: TextKindOther, Raw: s}
	switch {
	case s == TextNewKey:
		t.Kind = TextKindNewKey
	case s == TextNewKey1:
		t.Kind = TextKindNewKey1
	case strings.HasPrefix(s, TextIAXKey):
		t.Kind = TextKindIAXKey
	case s == TextDisconnect:
		t.Kind = TextKindDisconnect
	case s == "":
	case s[0] == 'L':
		t.Kind = TextKindLinkList
		if len(s) >= 2 {
			t.Links = ParseLinkList(s[2:])
		}
	case s[0] == 'M' || s[0] == 'T':
		f := strings.Fields(s)
		if len(f) < 3 {
			return t
		}
		t.Kind = TextKindMessage
		if s[0] == 'T' {
			t.Kind = TextKindTelemetry
		}
		t.From, t.To = f[1], f[2]
		t.Body = strings.Join(f[3:], " ")
	case s[0] == 'D':
		t.Kind = TextKindDTMF
	case s[0] == 'G':
		t.Kind = TextKindGPS
	}
	return t
}

// ParseLinkList parses the body of an "L" message, e.g. "T2000,R2001".
// Malformed entries are skipped.
func ParseLinkList(s string) []LinkEntry {
	var out []LinkEntry
	for _, item := range strings.Split(strings.TrimSpace(s), ",") {
		item = strings.TrimSpace(item)
		if len(item) < 2 {
			continue
		}
		m := LinkMode(item[0])
		switch m {
		case ModeTransceive, ModeReceive, ModeConnecting, ModeLocal:
		default:
			continue
		}
		if !allDigits(item[1:]) {
			continue
		}
		out = append(out, LinkEntry{Mode: m, Node: item[1:]})
	}
	return out
}

// FormatLinkList renders an "L" message. An empty list gives "L ", which is
// what app_rpt sends when nothing else is linked.
func FormatLinkList(entries []LinkEntry) (string, error) {
	var b strings.Builder
	b.WriteString("L ")
	for i, e := range entries {
		switch e.Mode {
		case ModeTransceive, ModeReceive, ModeConnecting:
		default:
			return "", fmt.Errorf("iax2: bad link mode %q", rune(e.Mode))
		}
		if !allDigits(e.Node) {
			return "", fmt.Errorf("iax2: bad link node %q", e.Node)
		}
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(e.String())
	}
	return b.String(), nil
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
