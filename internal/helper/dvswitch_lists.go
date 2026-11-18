package helper

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"
)

// Host and TG lists for the pickers (docs/DVSWITCH.md, "Host and TG lists").
// The files are DVSwitch's own copies in the host dir (refreshed daily,
// downloaded from pistar.uk). The client names a mode, never a path; each mode
// maps to one fixed file name. Every file is untrusted data: it is opened
// with O_NOFOLLOW, must be a regular file, is size capped, and every field is
// checked before it reaches a reply. A DMR password in DMR_Hosts.txt is never
// read into a value.

// OpDVSwitchLists returns the entries of one list (read-only).
const OpDVSwitchLists = "dvswitch-lists"

// ErrDVNoList: the list file of the mode is missing or unreadable.
var ErrDVNoList = errors.New("no_list: the list file is not available on this node")

// Caps. dvListFileMax bounds the read, DVListMax the number of items returned.
const (
	dvListFileMax = 4 << 20
	// DVListMax is the most items one reply carries (the real lists hold
	// roughly 1,500 DMR, 1,700 YSF, 500 P25 and 450 NXDN entries).
	DVListMax = 5000
)

// DVListModes are the accepted modes, in display order.
var DVListModes = []string{"dmr", "ysf", "p25", "nxdn"}

// dvListFiles maps a mode to its file below the host dir. DMR_Hosts.txt is the
// Pi-Star name; DVSwitch's own files have no underscore.
var dvListFiles = map[string]string{
	"dmr":  "DMR_Hosts.txt",
	"ysf":  "YSFHosts.txt",
	"p25":  "P25Hosts.txt",
	"nxdn": "NXDNHosts.txt",
}

// ValidDVListMode reports whether m is one of dmr, ysf, p25, nxdn.
func ValidDVListMode(m string) bool { _, ok := dvListFiles[m]; return ok }

// DVListItem is one selectable entry. Value is what gets written: the master
// address (dmr), the 5-digit reflector id (ysf) or the TG (p25, nxdn). Detail
// is the description or network id. Address and Port say where it connects.
type DVListItem struct {
	Value   string `json:"value"`
	Name    string `json:"name"`
	Detail  string `json:"detail,omitempty"`
	Address string `json:"address"`
	Port    int    `json:"port"`
}

// DVList is the reply of the lists op. ETag is a hash of the file content.
type DVList struct {
	Mode      string       `json:"mode"`
	Items     []DVListItem `json:"items"`
	Truncated bool         `json:"truncated,omitempty"`
	ETag      string       `json:"etag"`
}

// cleanText keeps printable text only and shortens it to n runes.
func cleanText(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > n {
		s = string([]rune(s)[:n])
	}
	return s
}

func listPort(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	return n, err == nil && n >= 1 && n <= 65535
}

// eachListLine calls f for every line that is neither blank nor a comment.
func eachListLine(data []byte, f func(line string)) {
	for _, l := range strings.Split(string(data), "\n") {
		l = strings.TrimSpace(strings.TrimSuffix(l, "\r"))
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		f(l)
	}
}

// ParseDMRHosts reads DMR_Hosts.txt. Pi-Star's format is five whitespace
// separated fields: name, network id, address, password, port. A four field
// line (name, address, password, port) is accepted too. The password is
// skipped, not stored. Loopback entries (the DMRGateway internals) and
// malformed lines are skipped.
func ParseDMRHosts(data []byte) []DVListItem {
	var out []DVListItem
	eachListLine(data, func(l string) {
		f := strings.Fields(l)
		var name, id, host, port string
		switch len(f) {
		case 5:
			name, id, host, port = f[0], f[1], f[2], f[4]
		case 4:
			name, host, port = f[0], f[1], f[3]
		default:
			return
		}
		p, ok := listPort(port)
		if !ok || !reDVHost.MatchString(host) || strings.HasPrefix(host, "127.") || name == "" {
			return
		}
		it := DVListItem{Value: host, Name: cleanText(name, 64), Address: host, Port: p}
		if id != "" && strings.Trim(id, "0") != "" && len(id) <= 10 && strings.Trim(id, "0123456789") == "" {
			it.Detail = "ID " + id
		}
		out = append(out, it)
	})
	return out
}

// ParseYSFHosts reads YSFHosts.txt: id;name;description;address;port;... with
// a 5-digit id (more fields follow and are ignored).
func ParseYSFHosts(data []byte) []DVListItem {
	var out []DVListItem
	eachListLine(data, func(l string) {
		f := strings.Split(l, ";")
		if len(f) < 5 {
			return
		}
		id, host := strings.TrimSpace(f[0]), strings.TrimSpace(f[3])
		p, ok := listPort(strings.TrimSpace(f[4]))
		if !ok || !reDVYSFID.MatchString(id) || !reDVHost.MatchString(host) {
			return
		}
		name := cleanText(f[1], 64)
		if name == "" {
			name = "Reflector " + id
		}
		out = append(out, DVListItem{Value: id, Name: name, Detail: cleanText(f[2], 96), Address: host, Port: p})
	})
	return out
}

// ParseTGHosts reads P25Hosts.txt and NXDNHosts.txt: TG, address, port,
// whitespace separated. The label is "P25" or "NXDN".
func ParseTGHosts(data []byte, label string) []DVListItem {
	var out []DVListItem
	eachListLine(data, func(l string) {
		f := strings.Fields(l)
		if len(f) < 3 {
			return
		}
		p, ok := listPort(f[2])
		if !ok || !reDVRefl.MatchString(f[0]) || !reDVHost.MatchString(f[1]) {
			return
		}
		out = append(out, DVListItem{Value: f[0], Name: label + " TG " + f[0], Address: f[1], Port: p})
	})
	return out
}

func parseDVList(mode string, data []byte) []DVListItem {
	switch mode {
	case "dmr":
		return ParseDMRHosts(data)
	case "ysf":
		return ParseYSFHosts(data)
	case "p25":
		return ParseTGHosts(data, "P25")
	default:
		return ParseTGHosts(data, "NXDN")
	}
}

// dvReadList reads the fixed file of a mode: no symlink, regular file, capped.
func (o *Ops) dvReadList(mode string) ([]byte, error) {
	name, ok := dvListFiles[mode]
	if !ok {
		return nil, dvInvalid("mode")
	}
	fd, err := os.OpenFile(filepath.Join(o.dvHostDir(), name), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, ErrDVNoList
	}
	defer fd.Close()
	fi, err := fd.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		return nil, ErrDVNoList
	}
	data, err := io.ReadAll(io.LimitReader(fd, dvListFileMax+1))
	if err != nil || len(data) > dvListFileMax {
		return nil, ErrDVNoList
	}
	return bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")), nil
}

// DVSwitchLists returns the entries of one list, at most DVListMax.
func (o *Ops) DVSwitchLists(_ context.Context, req *DVSwitchRequest) (DVList, error) {
	req, err := o.dvArgs(req)
	if err != nil {
		return DVList{}, err
	}
	data, err := o.dvReadList(req.Mode)
	if err != nil {
		return DVList{}, err
	}
	sum := sha256.Sum256(data)
	l := DVList{Mode: req.Mode, Items: parseDVList(req.Mode, data), ETag: hex.EncodeToString(sum[:8])}
	if len(l.Items) > DVListMax {
		l.Items, l.Truncated = l.Items[:DVListMax], true
	}
	if l.Items == nil {
		l.Items = []DVListItem{}
	}
	return l, nil
}

// ysfIDForTune maps ABInfo's last YSF tune ("host:port") to a reflector id
// through YSFHosts.txt. A 5-digit tune is already an id. ok is false when
// nothing matches.
func (o *Ops) ysfIDForTune(tune string) (string, bool) {
	if reDVYSFID.MatchString(tune) {
		return tune, true
	}
	i := strings.LastIndex(tune, ":")
	if i < 0 {
		return "", false
	}
	port, ok := listPort(tune[i+1:])
	if !ok {
		return "", false
	}
	host := tune[:i]
	data, err := o.dvReadList("ysf")
	if err != nil {
		return "", false
	}
	for _, it := range ParseYSFHosts(data) {
		if it.Port == port && strings.EqualFold(it.Address, host) {
			return it.Value, true
		}
	}
	return "", false
}
