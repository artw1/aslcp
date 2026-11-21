// Package radioid keeps the radioid.net user list (user.csv) for the "Now
// calling" strip: DMR ID to callsign, name and location, and callsign to the
// same for YSF.
//
// The node may be a 32-bit Raspberry Pi with little RAM, so the file stays on
// disk and only a compact index is held in memory: two sorted parallel slices
// per index (4 bytes ID + 4 bytes offset, 8 bytes callsign hash + 4 bytes
// offset), about 12 bytes per row on average and 3.5 MB for 300 000 rows. A
// lookup binary-searches the index and reads one line with ReadAt.
package radioid

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Header is the first line of a valid user.csv.
const Header = "RADIO_ID,CALLSIGN,FIRST_NAME,LAST_NAME,CITY,STATE,COUNTRY"

// maxLine bounds one row; a longer line is skipped as malformed.
const maxLine = 1024

// cacheMax is the size of the lookup cache.
const cacheMax = 64

// User is one row of the list.
type User struct {
	ID       uint32 `json:"id"`
	Callsign string `json:"callsign"`
	First    string `json:"first"`
	Last     string `json:"last"`
	City     string `json:"city"`
	State    string `json:"state"`
	Country  string `json:"country"`
}

// Name is "First Last" (either may be empty).
func (u User) Name() string { return strings.TrimSpace(u.First + " " + u.Last) }

// ValidHeader reports whether line is the user.csv header (a BOM, case and
// surrounding blanks are tolerated).
func ValidHeader(line string) bool {
	line = strings.TrimPrefix(strings.TrimSpace(line), "\ufeff")
	return strings.EqualFold(strings.ReplaceAll(line, " ", ""), Header)
}

// Index is the in-memory index of one user.csv. It is immutable after
// BuildIndex apart from its small lookup cache; safe for concurrent use.
type Index struct {
	f    *os.File
	size int64

	ids    []uint32 // sorted
	idOffs []uint32 // offset of the row of ids[i]
	hashes []uint64 // sorted by (hash, offset)
	hOffs  []uint32

	mu    sync.Mutex
	cache map[string]*User // key "i<id>" or "c<CALL>"; nil value = not found
	order []string
}

// Entries is the number of rows indexed.
func (x *Index) Entries() int {
	if x == nil {
		return 0
	}
	return len(x.ids)
}

// Close releases the file.
func (x *Index) Close() error {
	if x == nil || x.f == nil {
		return nil
	}
	return x.f.Close()
}

type byID struct{ x *Index }

func (b byID) Len() int { return len(b.x.ids) }
func (b byID) Less(i, j int) bool {
	if b.x.ids[i] != b.x.ids[j] {
		return b.x.ids[i] < b.x.ids[j]
	}
	return b.x.idOffs[i] < b.x.idOffs[j]
}
func (b byID) Swap(i, j int) {
	b.x.ids[i], b.x.ids[j] = b.x.ids[j], b.x.ids[i]
	b.x.idOffs[i], b.x.idOffs[j] = b.x.idOffs[j], b.x.idOffs[i]
}

type byHash struct{ x *Index }

func (b byHash) Len() int { return len(b.x.hashes) }
func (b byHash) Less(i, j int) bool {
	if b.x.hashes[i] != b.x.hashes[j] {
		return b.x.hashes[i] < b.x.hashes[j]
	}
	return b.x.hOffs[i] < b.x.hOffs[j]
}
func (b byHash) Swap(i, j int) {
	b.x.hashes[i], b.x.hashes[j] = b.x.hashes[j], b.x.hashes[i]
	b.x.hOffs[i], b.x.hOffs[j] = b.x.hOffs[j], b.x.hOffs[i]
}

// hashCall is FNV-1a 64 of the upper-cased callsign.
func hashCall(c string) uint64 {
	h := uint64(14695981039346656037)
	for i := 0; i < len(c); i++ {
		b := c[i]
		if b >= 'a' && b <= 'z' {
			b -= 'a' - 'A'
		}
		h ^= uint64(b)
		h *= 1099511628211
	}
	return h
}

// BuildIndex opens path, validates the header and indexes every well-formed
// row. Malformed rows are skipped. An empty list or a bad header is an error.
// The file stays open (the index reads rows from it); Close releases it.
func BuildIndex(path string) (*Index, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if fi.Size() > int64(^uint32(0)) {
		f.Close()
		return nil, errors.New("user list too large to index")
	}
	x := &Index{f: f, size: fi.Size(), cache: map[string]*User{}}
	br := bufio.NewReaderSize(f, 64<<10)
	var off int64
	first := true
	for {
		start := off
		line, err := br.ReadSlice('\n')
		off += int64(len(line))
		for err == bufio.ErrBufferFull { // an over-long line: skip it whole
			line = nil
			var more []byte
			more, err = br.ReadSlice('\n')
			off += int64(len(more))
		}
		if off == start {
			break
		}
		if first {
			first = false
			if !ValidHeader(string(line)) {
				f.Close()
				return nil, errors.New("unexpected header: not a radioid.net user.csv")
			}
		} else if id, call, ok := keyFields(line); ok {
			x.ids = append(x.ids, id)
			x.idOffs = append(x.idOffs, uint32(start))
			x.hashes = append(x.hashes, hashCall(call))
			x.hOffs = append(x.hOffs, uint32(start))
		}
		if err != nil {
			break
		}
	}
	if first {
		f.Close()
		return nil, errors.New("empty file")
	}
	if len(x.ids) == 0 {
		f.Close()
		return nil, errors.New("no usable rows")
	}
	sort.Sort(byID{x})
	sort.Sort(byHash{x})
	return x, nil
}

// keyFields extracts the ID and callsign of a row without allocating; a row
// needs 7 fields, a numeric ID and a plausible callsign.
func keyFields(line []byte) (id uint32, call string, ok bool) {
	line = bytes.TrimRight(line, "\r\n")
	if len(line) == 0 || len(line) > maxLine || bytes.Count(line, []byte{','}) < 6 {
		return 0, "", false
	}
	i := bytes.IndexByte(line, ',')
	n, err := strconv.ParseUint(string(line[:i]), 10, 32)
	if err != nil || n == 0 {
		return 0, "", false
	}
	rest := line[i+1:]
	j := bytes.IndexByte(rest, ',')
	c := rest[:j]
	if len(c) == 0 || len(c) > 16 || bytes.ContainsAny(c, "\" ") {
		return 0, "", false
	}
	return uint32(n), string(c), true
}

// readAt parses the row that starts at off.
func (x *Index) readAt(off uint32) (*User, error) {
	buf := make([]byte, maxLine+2)
	n, err := x.f.ReadAt(buf, int64(off))
	if err != nil && err != io.EOF {
		return nil, err
	}
	buf = buf[:n]
	if i := bytes.IndexByte(buf, '\n'); i >= 0 {
		buf = buf[:i]
	}
	r := csv.NewReader(bytes.NewReader(buf))
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	rec, err := r.Read()
	if err != nil || len(rec) < 7 {
		return nil, fmt.Errorf("bad row at %d", off)
	}
	id, _ := strconv.ParseUint(rec[0], 10, 32)
	return &User{ID: uint32(id), Callsign: rec[1], First: rec[2], Last: rec[3], City: rec[4], State: rec[5], Country: rec[6]}, nil
}

func (x *Index) cached(key string) (*User, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	u, ok := x.cache[key]
	return u, ok
}

func (x *Index) remember(key string, u *User) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if _, ok := x.cache[key]; !ok {
		if len(x.order) >= cacheMax {
			delete(x.cache, x.order[0])
			x.order = x.order[1:]
		}
		x.order = append(x.order, key)
	}
	x.cache[key] = u
}

// ByID looks a DMR ID up (the first row with that ID).
func (x *Index) ByID(id uint32) (User, bool) {
	if x == nil || id == 0 {
		return User{}, false
	}
	key := "i" + strconv.FormatUint(uint64(id), 10)
	if u, ok := x.cached(key); ok {
		if u == nil {
			return User{}, false
		}
		return *u, true
	}
	i := sort.Search(len(x.ids), func(i int) bool { return x.ids[i] >= id })
	if i < len(x.ids) && x.ids[i] == id {
		if u, err := x.readAt(x.idOffs[i]); err == nil {
			x.remember(key, u)
			return *u, true
		}
	}
	x.remember(key, nil)
	return User{}, false
}

// ByCallsign looks a callsign up (case-insensitive). One callsign can have
// several IDs; the first row of the file wins, they share name and location.
func (x *Index) ByCallsign(call string) (User, bool) {
	call = strings.ToUpper(strings.TrimSpace(call))
	if x == nil || call == "" {
		return User{}, false
	}
	key := "c" + call
	if u, ok := x.cached(key); ok {
		if u == nil {
			return User{}, false
		}
		return *u, true
	}
	h := hashCall(call)
	i := sort.Search(len(x.hashes), func(i int) bool { return x.hashes[i] >= h })
	for ; i < len(x.hashes) && x.hashes[i] == h; i++ {
		if u, err := x.readAt(x.hOffs[i]); err == nil && strings.EqualFold(u.Callsign, call) {
			x.remember(key, u)
			return *u, true
		}
	}
	x.remember(key, nil)
	return User{}, false
}
