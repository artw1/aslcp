package backup

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// OSInfo describes the Debian release of a node (from /etc/os-release).
type OSInfo struct {
	ID       string // ID, e.g. "debian" or "raspbian"
	Codename string // VERSION_CODENAME, e.g. "trixie"
	Pretty   string // PRETTY_NAME
}

// debSuffix matches the ".debNN" release suffix of ASL3 and Asterisk
// version strings (22.10.1+asl3-3.10.8-1.deb12).
var debSuffix = regexp.MustCompile(`\.deb([0-9]{1,2})(?:$|[^0-9])`)

// debCodenames maps the Debian major version to its codename.
var debCodenames = map[string]string{"12": "bookworm", "13": "trixie", "14": "forky"}

// CodenameFromVersion derives the Debian codename from a ".debNN" suffix of
// a package version; "" when there is none or the release is unknown.
func CodenameFromVersion(v string) string {
	if m := debSuffix.FindStringSubmatch(v); m != nil {
		return debCodenames[m[1]]
	}
	return ""
}

// ParseOSRelease reads the keys it needs from the content of an os-release
// file (KEY=value or KEY="value" lines).
func ParseOSRelease(data []byte) OSInfo {
	var o OSInfo
	for _, line := range strings.Split(string(data), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		if len(v) > 200 || !utf8.ValidString(v) {
			continue
		}
		switch k {
		case "ID":
			o.ID = v
		case "VERSION_CODENAME":
			o.Codename = v
		case "PRETTY_NAME":
			o.Pretty = v
		}
	}
	return o
}

// ReleaseOf returns the Debian codename of a backup or node: the explicit
// codename if known, else derived from the ASL3 and then the Asterisk
// version.
func ReleaseOf(codename, asl3Version, asteriskVersion string) string {
	if codename != "" {
		return codename
	}
	if c := CodenameFromVersion(asl3Version); c != "" {
		return c
	}
	return CodenameFromVersion(asteriskVersion)
}

// DebianNumber returns the Debian major version of a known codename
// ("bookworm" -> "12"), or "".
func DebianNumber(codename string) string {
	for n, c := range debCodenames {
		if c == codename {
			return n
		}
	}
	return ""
}

// ReleaseLabel is the human form: "Debian 13 (trixie)" or just the codename.
func ReleaseLabel(codename string) string {
	if n := DebianNumber(codename); n != "" {
		return "Debian " + n + " (" + codename + ")"
	}
	return codename
}
