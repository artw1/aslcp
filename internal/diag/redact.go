package diag

import (
	"net"
	"regexp"
	"strings"
)

// Redaction rules (applied to the whole report, so a value that slips into a
// log line is still removed):
//
//   - values of secret-like keys (secret, password, token, key, authorization,
//     cookie, ...) in key=value, key: value and JSON forms; Bearer tokens;
//     user:password@ in URLs; setup links (?code=...); long random-looking
//     tokens; and every literal in Options.Secrets (the AMI secret)
//   - email addresses
//   - IPv4 and IPv6 addresses except loopback (127.x.x.x, ::1) and the
//     unspecified address, and MAC addresses
//   - callsign-like tokens ([A-Z]{1,2}[0-9][A-Z]{1,4}) except those in
//     Options.KeepCalls (the callsigns of this node's own entries in the node list)
//
// Node numbers are plain digits and are kept.

// Placeholders.
const (
	Redacted = "<redacted>"
	phEmail  = "<email>"
	phIPv4   = "<ipv4>"
	phIPv6   = "<ipv6>"
	phMAC    = "<mac>"
	phCall   = "<call>"
)

// Options tunes Redact.
type Options struct {
	// Secrets are literal strings removed wherever they occur (case-sensitive).
	Secrets []string
	// KeepCalls are callsigns (upper case) that are not replaced.
	KeepCalls []string
}

const secretName = `[\w.\-]*(?:secret|passw(?:or)?d|passphrase|token|apikey|api[_\-]key|private[_\-]key|credential)[\w.\-]*|(?:[\w.\-]*[_\-])?key|session|sid`

var (
	// "Authorization: ..." / "Cookie: ..." : the rest of the line goes.
	reHeaderLine = regexp.MustCompile(`(?i)\b((?:proxy-)?authorization|set-cookie|cookie)(["']?\s*[=:]\s*)[^\n]*`)
	// name = value, name: value, "name": "value" (value: quoted or up to white space).
	reKeyValue = regexp.MustCompile(`(?i)\b(` + secretName + `)(["']?\s*[=:]\s*)("[^"\n]*"|'[^'\n]*'|[^\s"',;]+)`)
	reBearer   = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=\-]{6,}`)
	reURLCreds = regexp.MustCompile(`(?i)(://)[^/\s:@]*:[^/\s@]*@`)
	reSetup    = regexp.MustCompile(`(?i)((?:setup\?|[?&])code=)[^\s&"']+`)
	reLongTok  = regexp.MustCompile(`[A-Za-z0-9_+=\-]{32,}`)
	reEmail    = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9\-]+(?:\.[A-Za-z0-9\-]+)*\.[A-Za-z]{2,}`)
	reMAC      = regexp.MustCompile(`(?i)\b[0-9a-f]{2}(?::[0-9a-f]{2}){5}\b`)
	reV6Run    = regexp.MustCompile(`[0-9A-Fa-f:.]*:[0-9A-Fa-f:.]*(?:%[A-Za-z0-9_]+)?`)
	reV4       = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	reCall     = regexp.MustCompile(`\b[A-Z]{1,2}[0-9][A-Z]{1,4}\b`)
)

// Redact returns s with private data replaced; see the rules above.
func Redact(s string, o Options) string {
	// Literal secrets first (longest first is not needed: replaced wholesale).
	for _, lit := range o.Secrets {
		if len(lit) >= 3 { // "auto" and tiny values would shred the report
			s = strings.ReplaceAll(s, lit, Redacted)
		}
	}
	s = reHeaderLine.ReplaceAllString(s, "${1}${2}"+Redacted)
	s = reKeyValue.ReplaceAllStringFunc(s, func(m string) string {
		sub := reKeyValue.FindStringSubmatch(m)
		if sub[3] == "auto" || strings.Contains(sub[3], Redacted) { // "auto" = read from manager.conf
			return m
		}
		return sub[1] + sub[2] + Redacted
	})
	s = reBearer.ReplaceAllString(s, "${1} "+Redacted)
	s = reURLCreds.ReplaceAllString(s, "${1}"+Redacted+"@")
	s = reSetup.ReplaceAllString(s, "${1}"+Redacted)
	s = reLongTok.ReplaceAllStringFunc(s, func(m string) string {
		// Random-looking: has a digit and a letter (plain long words and
		// dashed names are left alone).
		var dig, let bool
		for _, c := range m {
			switch {
			case c >= '0' && c <= '9':
				dig = true
			case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z':
				let = true
			}
		}
		if dig && let {
			return Redacted
		}
		return m
	})
	s = reEmail.ReplaceAllString(s, phEmail)
	s = reMAC.ReplaceAllString(s, phMAC)
	s = reV6Run.ReplaceAllStringFunc(s, redactV6)
	s = reV4.ReplaceAllStringFunc(s, func(m string) string {
		ip := net.ParseIP(m)
		if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
			return m
		}
		return phIPv4
	})
	keep := map[string]bool{}
	for _, c := range o.KeepCalls {
		keep[strings.ToUpper(strings.TrimSpace(c))] = true
	}
	s = reCall.ReplaceAllStringFunc(s, func(m string) string {
		if keep[m] {
			return m
		}
		return phCall
	})
	return s
}

// redactV6 replaces a run of hex digits, colons and dots when it is a valid
// non-loopback IPv6 address (trailing punctuation stays).
func redactV6(run string) string {
	core, zone := run, ""
	if i := strings.IndexByte(core, '%'); i >= 0 {
		core, zone = core[:i], core[i:]
	}
	trimmed := strings.TrimRight(core, ":.")
	tail := core[len(trimmed):]
	ip := net.ParseIP(trimmed)
	if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
		return run
	}
	if zone != "" && tail != "" {
		return run // not an address
	}
	return phIPv6 + tail
}

// RedactConfigLine masks the value of a "key = value" line whose key looks
// secret; other lines are returned unchanged (apply Redact afterwards).
func RedactConfigLine(line string) string {
	t := strings.TrimSpace(line)
	if t == "" || strings.HasPrefix(t, ";") || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "[") {
		return line
	}
	k, _, ok := strings.Cut(line, "=")
	if !ok {
		return line
	}
	if reSecretKey.MatchString(strings.TrimSpace(k)) {
		return strings.TrimRight(k, " \t") + " = " + Redacted
	}
	return line
}

var reSecretKey = regexp.MustCompile(`(?i)^(?:` + secretName + `)$`)
