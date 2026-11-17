// Package allscan imports favorites from an existing AllScan (or Supermon)
// installation.
//
// Format (verified against github.com/davidgsd/allscan, favorites-Sample.ini
// and include/favsUtils.php): a PHP parse_ini_file() INI with a [general]
// section (and optionally per-node [NNNN] sections) holding parallel arrays:
//
//	label[] = "N0AAT Example Hub, Metropolis, ST 43763"
//	cmd[]   = "rpt cmd %node% ilink 3 43763"
//
// AllScan looks for the file at favorites.ini next to its web root,
// ../supermon/favorites.ini and /etc/allscan/favorites.ini, plus any
// favorites*.ini in those directories. It extracts the node number with the
// regex [0-9]{4,8} from the cmd and ignores duplicates and commands without
// a number.
package allscan

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Item is one importable favorite.
type Item struct {
	Node  string `json:"node"`
	Label string `json:"label"`
}

// Result is a preview of an AllScan favorites file.
type Result struct {
	Found bool   `json:"found"`
	Path  string `json:"path"`
	Items []Item `json:"items"`
}

// DefaultDirs are the directories searched for favorites*.ini.
var DefaultDirs = []string{
	"/etc/allscan",
	"/var/www/html/allscan",
	"/var/www/html/supermon",
}

var (
	reKey      = regexp.MustCompile(`(?i)^(label|cmd)\s*\[[^\]]*\]$`)
	reIlinkNum = regexp.MustCompile(`(?i)\bilink\s+[0-9]+\s+([0-9]{3,10})\b`)
	reAnyNum   = regexp.MustCompile(`[0-9]{4,8}`)
)

// ExtractNode finds the target node in an AllScan command string. The number
// after "ilink <code>" wins; otherwise the first 4-8 digit run (AllScan's
// own rule). The %node% placeholder is ignored.
func ExtractNode(cmd string) string {
	cmd = strings.ReplaceAll(cmd, "%node%", " ")
	if m := reIlinkNum.FindStringSubmatch(cmd); m != nil {
		return m[1]
	}
	return reAnyNum.FindString(cmd)
}

type section struct{ labels, cmds []string }

// Parse reads favorites in INI format. Sections are processed in file order;
// labels and commands are paired by index within a section; duplicate nodes
// and commands without a node number are skipped.
func Parse(r io.Reader) []Item {
	var secs []*section
	cur := &section{}
	secs = append(secs, cur)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\ufeff"))
		if line == "" || line[0] == ';' || line[0] == '#' {
			continue
		}
		if line[0] == '[' {
			if i := strings.IndexByte(line, ']'); i > 0 {
				cur = &section{}
				secs = append(secs, cur)
			}
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		m := reKey.FindStringSubmatch(strings.TrimSpace(k))
		if m == nil {
			continue
		}
		val := parseValue(v)
		if strings.EqualFold(m[1], "label") {
			cur.labels = append(cur.labels, val)
		} else {
			cur.cmds = append(cur.cmds, val)
		}
	}
	seen := map[string]bool{}
	items := []Item{}
	for _, s := range secs {
		for i, c := range s.cmds {
			node := ExtractNode(strings.TrimSpace(c))
			if node == "" || seen[node] {
				continue
			}
			seen[node] = true
			label := ""
			if i < len(s.labels) {
				label = strings.TrimSpace(s.labels[i])
			}
			items = append(items, Item{Node: node, Label: label})
		}
	}
	return items
}

// parseValue handles "double", 'single' and bare values with ; comments.
func parseValue(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if q := v[0]; q == '"' || q == '\'' {
		var sb strings.Builder
		for i := 1; i < len(v); i++ {
			c := v[i]
			if c == '\\' && q == '"' && i+1 < len(v) && (v[i+1] == '"' || v[i+1] == '\\') {
				sb.WriteByte(v[i+1])
				i++
				continue
			}
			if c == q {
				return sb.String()
			}
			sb.WriteByte(c)
		}
		return sb.String() // unterminated: take the rest
	}
	if i := strings.IndexByte(v, ';'); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

// Candidates lists favorites*.ini files in dirs: favorites.ini first, then
// the rest alphabetically, directory by directory.
func Candidates(dirs []string) []string {
	var out []string
	for _, d := range dirs {
		var found []string
		matches, _ := filepath.Glob(filepath.Join(d, "favorites*.ini"))
		sort.Strings(matches)
		for _, m := range matches {
			if filepath.Base(m) == "favorites.ini" {
				found = append([]string{m}, found...)
			} else {
				found = append(found, m)
			}
		}
		out = append(out, found...)
	}
	return out
}

// Find returns the first candidate that yields items, or else the first
// readable candidate (Found true, no items), or Found false.
func Find(dirs []string) (Result, error) {
	res := Result{Items: []Item{}}
	var firstErr error
	for _, p := range Candidates(dirs) {
		f, err := os.Open(p)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		items := Parse(f)
		f.Close()
		if len(items) > 0 {
			return Result{Found: true, Path: p, Items: items}, nil
		}
		if !res.Found {
			res.Found, res.Path = true, p
		}
	}
	if !res.Found && firstErr != nil {
		return res, firstErr
	}
	return res, nil
}
