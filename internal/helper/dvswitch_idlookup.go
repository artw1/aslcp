package helper

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/artw1/aslcp/internal/dvswitch/ini"
)

// DMR ID to callsign lookup for the caller (docs/DVSWITCH.md, "Caller
// callsign"). Through STFU the Analog_Bridge log line has an empty call=, so
// the callsign comes from the DMR ID list of MMDVM_Bridge ([DMR Id Lookup]
// File=, default /var/lib/mmdvm/DMRIds.dat).
//
// Design: a streaming scan with a small cache, not a map of the whole file.
// The file is about 7 MB (hundreds of thousands of IDs); a map of it costs
// tens of MB of resident memory in a process that exits after 60 s idle, for a
// handful of IDs per request. A scan of 7 MB takes a few milliseconds, runs
// once for all IDs not yet known (hits and misses are both cached, at most
// dvIDCacheMax entries) and again only when the file's size or mtime changes
// (it is refreshed daily) or the cache is full.

const (
	dvIDCacheMax = 64
	// dvIDFileMax bounds the scan (the list is about 7 MB).
	dvIDFileMax = 64 << 20
	dvIDDefault = "DMRIds.dat"
)

var reDVLookupCall = regexp.MustCompile(`^[A-Za-z0-9/-]{1,12}$`)

type dvIDCache struct {
	mu    sync.Mutex
	path  string
	mtime time.Time
	size  int64
	m     map[string]string // id -> callsign ("" = not in the file)
}

// dvIDFile is the DMR ID list: the File= of [DMR Id Lookup] in
// MMDVM_Bridge.ini when it is an absolute path inside the host directory
// (/var/lib/mmdvm), else DMRIds.dat there. Any other path in the ini is
// ignored: the helper reads as root and must not be pointed elsewhere by a
// file the web user can influence.
func (o *Ops) dvIDFile() string {
	dir := o.dvHostDir()
	def := filepath.Join(dir, dvIDDefault)
	def2, _ := dvFileByID(DVFileMMDVMBridge)
	st, err := o.dvRead(def2)
	if err != nil || !st.regular {
		return def
	}
	v, _ := ini.Parse(st.data).Get("DMR Id Lookup", "File")
	v = strings.TrimSpace(v)
	if v == "" || !filepath.IsAbs(v) || filepath.Clean(v) != v || filepath.Dir(v) != filepath.Clean(dir) {
		return def
	}
	return v
}

// dvLookupCalls returns callsign by DMR ID for the ids found in the list.
func (o *Ops) dvLookupCalls(ids []string) map[string]string {
	out := map[string]string{}
	if len(ids) == 0 {
		return out
	}
	path := o.dvIDFile()
	c := &o.dvIDs
	c.mu.Lock()
	defer c.mu.Unlock()
	fi, err := os.Lstat(path)
	if err != nil || !fi.Mode().IsRegular() {
		c.m = nil
		return out
	}
	if c.m == nil || c.path != path || !c.mtime.Equal(fi.ModTime()) || c.size != fi.Size() {
		c.m, c.path, c.mtime, c.size = map[string]string{}, path, fi.ModTime(), fi.Size()
	}
	want := map[string]bool{}
	for _, id := range ids {
		if _, ok := c.m[id]; !ok && reDVDigits.MatchString(id) {
			want[id] = true
		}
	}
	if len(want) > 0 {
		if len(c.m)+len(want) > dvIDCacheMax {
			c.m = map[string]string{}
		}
		found := scanDMRIDs(path, want)
		for id := range want {
			c.m[id] = found[id]
		}
	}
	for _, id := range ids {
		if v := c.m[id]; v != "" {
			out[id] = v
		}
	}
	return out
}

var reDVDigits = regexp.MustCompile(`^[0-9]{1,9}$`)

// scanDMRIDs reads the list once and returns the callsigns of the wanted IDs.
// A line is "ID CALLSIGN [NAME...]", separated by blanks, tabs or commas (the
// MMDVMHost format); blank lines, comments and malformed lines are skipped.
// IDs in the file may have leading zeros removed or not: they are compared as
// written, without leading zeros.
func scanDMRIDs(path string, want map[string]bool) map[string]string {
	found := map[string]string{}
	fd, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return found
	}
	defer fd.Close()
	if fi, err := fd.Stat(); err != nil || !fi.Mode().IsRegular() {
		return found
	}
	sc := bufio.NewScanner(io.LimitReader(fd, dvIDFileMax))
	sc.Buffer(make([]byte, 0, 4096), 4096)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		// cheap reject: the ID is the first field, digits
		i := 0
		for i < len(line) && line[i] >= '0' && line[i] <= '9' {
			i++
		}
		if i == 0 || i == len(line) || i > 9 {
			continue
		}
		id := strings.TrimLeft(string(line[:i]), "0")
		if !want[id] {
			continue
		}
		f := strings.FieldsFunc(string(line[i:]), func(r rune) bool { return r == ' ' || r == '\t' || r == ',' || r == ';' })
		if len(f) == 0 || !reDVLookupCall.MatchString(f[0]) {
			continue
		}
		found[id] = f[0]
		if len(found) == len(want) {
			break
		}
	}
	return found
}
