package configsvc

import (
	"regexp"
	"strings"
)

// Masked replaces secret values in history diffs.
const Masked = "********"

var (
	reSecretKey = regexp.MustCompile(`(?i)^(\s*)(secret|password|passwd|pass|md5secret|dbsecret)(\s*=>?\s*)(.*)$`)
	reRegister  = regexp.MustCompile(`(?i)^(\s*register)(\s*=>?\s*)(.*)$`)
)

// maskRest masks a value (the text after the operator). A trailing ';'
// comment is kept. For register values only the "user:secret" part before
// the last '@' is replaced.
func maskRest(rest string, register bool) string {
	val, cmt := rest, ""
	if i := strings.IndexByte(rest, ';'); i >= 0 {
		val, cmt = rest[:i], rest[i:]
	}
	trimmed := strings.TrimRight(val, " \t\r")
	gap := val[len(trimmed):]
	if trimmed == "" {
		return rest
	}
	if register {
		if i := strings.LastIndexByte(trimmed, '@'); i >= 0 {
			return Masked + trimmed[i:] + gap + cmt
		}
	}
	return Masked + gap + cmt
}

// MaskLine masks the secret value of one config line (no line ending).
func MaskLine(line string) string {
	if m := reSecretKey.FindStringSubmatch(line); m != nil {
		return m[1] + m[2] + m[3] + maskRest(m[4], false)
	}
	if m := reRegister.FindStringSubmatch(line); m != nil {
		return m[1] + m[2] + maskRest(m[3], true)
	}
	return line
}

// MaskText masks every line of a config text.
func MaskText(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = MaskLine(l)
	}
	return strings.Join(lines, "\n")
}

// MaskDiff masks the content lines of a unified diff (the headers and hunk
// markers before and between hunks are left alone).
func MaskDiff(diff string) string {
	lines := strings.Split(diff, "\n")
	inHunk := false
	for i, l := range lines {
		if strings.HasPrefix(l, "@@") {
			inHunk = true
			continue
		}
		if !inHunk || l == "" {
			continue
		}
		switch l[0] {
		case ' ', '+', '-':
			lines[i] = l[:1] + MaskLine(l[1:])
		}
	}
	return strings.Join(lines, "\n")
}
