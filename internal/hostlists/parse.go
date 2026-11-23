package hostlists

import (
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Max is the most items one list carries (the real lists hold roughly 1,300
// DMR, 1,700 YSF, 500 P25 and 450 NXDN entries).
const Max = 5000

var (
	reHost  = regexp.MustCompile(`^[A-Za-z0-9.-]{1,253}$`)
	rePass  = regexp.MustCompile(`^[\x21-\x7e]{1,64}$`)
	reYSFID = regexp.MustCompile(`^[0-9]{5}$`)
	reRefl  = regexp.MustCompile(`^[0-9]{1,5}$`)
)

// Item is one selectable entry. Value is what gets written: the master
// address (dmr), the 5-digit reflector id (ysf) or the TG (p25, nxdn). Detail
// is the description or network id. Address and Port say where it connects.
type Item struct {
	Value   string `json:"value"`
	Name    string `json:"name"`
	Detail  string `json:"detail,omitempty"`
	Address string `json:"address"`
	Port    int    `json:"port"`
	// Password is the login password the DMR host list carries for the master
	// (a shared public value such as passw0rd); empty for other lists.
	Password string `json:"password,omitempty"`
}

// List is one list as served. ETag is a hash of the file content.
type List struct {
	Mode      string `json:"mode"`
	Items     []Item `json:"items"`
	Truncated bool   `json:"truncated,omitempty"`
	ETag      string `json:"etag"`
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

// eachLine calls f for every line that is neither blank nor a comment.
func eachLine(data []byte, f func(line string)) {
	for _, l := range strings.Split(string(data), "\n") {
		l = strings.TrimSpace(strings.TrimSuffix(l, "\r"))
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		f(l)
	}
}

// ParseDMR reads DMR_Hosts.txt. The format is five whitespace separated
// fields (the files pad with several tabs): name, network id, address,
// password, port. A four field line (name, address, password, port) is
// accepted too. The password is skipped, never stored. Loopback entries (the
// gateway internals) and malformed lines are skipped.
func ParseDMR(data []byte) []Item {
	var out []Item
	eachLine(data, func(l string) {
		f := strings.Fields(l)
		var name, id, host, port, pass string
		switch len(f) {
		case 5:
			name, id, host, pass, port = f[0], f[1], f[2], f[3], f[4]
		case 4:
			name, host, pass, port = f[0], f[1], f[2], f[3]
		default:
			return
		}
		p, ok := listPort(port)
		if !ok || !reHost.MatchString(host) || strings.HasPrefix(host, "127.") || host == "0.0.0.0" || name == "" {
			return
		}
		it := Item{Value: host, Name: cleanText(name, 64), Address: host, Port: p}
		if rePass.MatchString(pass) {
			it.Password = pass
		}
		if id != "" && strings.Trim(id, "0") != "" && len(id) <= 10 && strings.Trim(id, "0123456789") == "" {
			it.Detail = "ID " + id
		}
		out = append(out, it)
	})
	return out
}

// ParseYSF reads YSF_Hosts.txt: id;name;description;address;port;... with a
// 5-digit id (more fields follow and are ignored).
func ParseYSF(data []byte) []Item {
	var out []Item
	eachLine(data, func(l string) {
		f := strings.Split(l, ";")
		if len(f) < 5 {
			return
		}
		id, host := strings.TrimSpace(f[0]), strings.TrimSpace(f[3])
		p, ok := listPort(strings.TrimSpace(f[4]))
		if !ok || !reYSFID.MatchString(id) || !reHost.MatchString(host) {
			return
		}
		name := cleanText(f[1], 64)
		if name == "" {
			name = "Reflector " + id
		}
		out = append(out, Item{Value: id, Name: name, Detail: cleanText(f[2], 96), Address: host, Port: p})
	})
	return out
}

// ParseTG reads P25_Hosts.txt and NXDN_Hosts.txt: TG, address, port,
// whitespace separated. label is "P25" or "NXDN".
func ParseTG(data []byte, label string) []Item {
	var out []Item
	eachLine(data, func(l string) {
		f := strings.Fields(l)
		if len(f) < 3 {
			return
		}
		p, ok := listPort(f[2])
		if !ok || !reRefl.MatchString(f[0]) || !reHost.MatchString(f[1]) {
			return
		}
		out = append(out, Item{Value: f[0], Name: label + " TG " + f[0], Address: f[1], Port: p})
	})
	return out
}

func parse(mode string, data []byte) []Item {
	switch mode {
	case "dmr":
		return ParseDMR(data)
	case "ysf":
		return ParseYSF(data)
	case "p25":
		return ParseTG(data, "P25")
	default:
		return ParseTG(data, "NXDN")
	}
}

// BMMasters picks the BrandMeister masters out of a parsed DMR list (Pi-Star
// names them BM_<id>_<Country_or_Region>, for example BM_3104_United_States)
// and returns them sorted by master id. Value is the host name, Name the label
// "3104 – United States". The Homebrew port the file carries is not used by
// the native client, so Port is 0. Entries without a numeric id and repeated
// hosts are dropped.
func BMMasters(items []Item) []Item {
	type m struct {
		id int
		it Item
	}
	var ms []m
	seen := map[string]bool{}
	for _, it := range items {
		rest, ok := strings.CutPrefix(it.Name, "BM_")
		if !ok || seen[it.Value] {
			continue
		}
		tok := strings.Split(rest, "_")
		id, err := strconv.Atoi(tok[0])
		if err != nil || id < 0 || len(tok[0]) > 6 {
			continue
		}
		label := tok[0]
		if r := strings.TrimSpace(strings.Join(tok[1:], " ")); r != "" {
			label += " – " + r
		}
		seen[it.Value] = true
		ms = append(ms, m{id, Item{Value: it.Value, Name: label, Address: it.Address}})
	}
	sort.SliceStable(ms, func(i, j int) bool { return ms[i].id < ms[j].id })
	out := make([]Item, len(ms))
	for i, x := range ms {
		out[i] = x.it
	}
	return out
}

// OtherDMRMasters picks the masters for the "DMR - Other" card out of a parsed
// DMR list: everything except BrandMeister (names starting BM_) and TGIF (names
// starting TGIF, any case), which have their own cards. List order is kept;
// repeated hosts are dropped. Value and Address are the host, Port and Password
// come from the list.
func OtherDMRMasters(items []Item) []Item {
	out := []Item{}
	seen := map[string]bool{}
	for _, it := range items {
		if strings.HasPrefix(it.Name, "BM_") || strings.HasPrefix(strings.ToUpper(it.Name), "TGIF") || seen[it.Value] {
			continue
		}
		seen[it.Value] = true
		out = append(out, it)
	}
	return out
}

// TGIFMaster is the first TGIF entry (name starting TGIF, any case) of a parsed
// DMR list. Only its address and port are used: the password of a TGIF master is
// the operator's own, never the list's.
func TGIFMaster(items []Item) (Item, bool) {
	for _, it := range items {
		if strings.HasPrefix(strings.ToUpper(it.Name), "TGIF") {
			return it, true
		}
	}
	return Item{}, false
}

// TGIFFromFile reads the downloaded DMR list in dir and returns the address and
// port of its first TGIF entry; fallbackHost and fallbackPort when the file is
// missing, unreadable or has no TGIF entry.
func TGIFFromFile(dir, fallbackHost string, fallbackPort int) (string, int) {
	f, err := os.Open(filepath.Join(dir, files["dmr"]))
	if err != nil {
		return fallbackHost, fallbackPort
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxFile))
	if err != nil {
		return fallbackHost, fallbackPort
	}
	if it, ok := TGIFMaster(ParseDMR(data)); ok {
		return it.Address, it.Port
	}
	return fallbackHost, fallbackPort
}
