// Package backup defines the backup archive format of docs/BACKUP.md
// (format 1) and the code that reads and validates it. Both the server and
// the root helper use this package: the helper never trusts the server's
// check and validates the archive again with Read.
//
// An archive is a gzip-compressed tar whose first entry is manifest.json.
// Every other entry must be listed in the manifest, with the same type, size
// and (for files) sha256. Owner, group and mode come from the manifest; the
// tar headers are ignored apart from name, type, size and link target.
package backup

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/artw1/aslcp/internal/debver"
)

// Format limits.
const (
	FormatVersion = 1
	ManifestName  = "manifest.json"

	// MaxUpload is the size limit of an uploaded or created archive.
	MaxUpload = 256 << 20
	// MaxExpanded bounds the total size of all files after decompression.
	MaxExpanded = 4 << 30
	// MaxEntries bounds the number of manifest entries.
	MaxEntries = 300000

	maxManifest = 32 << 20
	maxPathLen  = 1024
)

// Entry types.
const (
	TypeFile    = "file"
	TypeDir     = "dir"
	TypeSymlink = "symlink"
)

// Classification of a file of /etc/asterisk.
const (
	ClassStock   = "stock"   // a conffile that still matches what its package shipped
	ClassChanged = "changed" // a conffile that was edited
	ClassLocal   = "local"   // not a conffile of any package
)

// Archive locations of the single-file parts and the roots of the tree parts.
const (
	PathAsteriskEtc = "asterisk/etc"
	PathAstDB       = "asterisk/astdb.sqlite3"
	PathAslcpDB     = "aslcp/aslcp.db"
	PathAslcpConf   = "aslcp/aslcp.conf"
	PathSA818       = "sa818/sa818.conf"
	PathHistory     = "history"
	PathAllmon3     = "allmon3"
	PathSoundsVar   = "sounds/var-lib"
	PathSoundsUsr   = "sounds/usr-share"
)

// PathDVSwitchOpt is the archive prefix of the DVSwitch part: the file
// /opt/<rel> is stored as dvswitch/opt/<rel>.
const PathDVSwitchOpt = "dvswitch/opt"

// DVSwitchFiles lists the only files of the DVSwitch part, relative to /opt:
// the allowlisted ini files and the macro file. Each is a single-file entry
// of the layout, so no other path below dvswitch/ is valid.
var DVSwitchFiles = []string{
	"Analog_Bridge/Analog_Bridge.ini",
	"Analog_Bridge/dvsm.macro",
	"MMDVM_Bridge/MMDVM_Bridge.ini",
	"MMDVM_Bridge/DVSwitch.ini",
	"YSFGateway/YSFGateway.ini",
	"P25Gateway/P25Gateway.ini",
	"NXDNGateway/NXDNGateway.ini",
}

// DVSwitchPath returns the archive path of a DVSwitch part file.
func DVSwitchPath(rel string) string { return PathDVSwitchOpt + "/" + rel }

// PathDVSwitchPrivate is the archive path of the record of the private bridge
// node (privatenode.json of the helper's DVSwitch state directory, not a file
// of /opt). It is the DVSwitch part's one file outside dvswitch/opt, optional
// (older backups lack it), and on restore it is kept only when the restored
// rpt.conf has the node's stanza.
const PathDVSwitchPrivate = "dvswitch/state/privatenode.json"

// PathDVSwitchNetworks is the archive path of the DMR network settings
// (networks.json of the helper's DVSwitch state directory): each DMR network
// mode's settings, password included, and enabled state, and the BM stay
// setting. Optional (older backups lack it).
const PathDVSwitchNetworks = "dvswitch/state/networks.json"

var treeRoots = []string{PathAsteriskEtc, PathHistory, PathAllmon3, PathSoundsVar, PathSoundsUsr}
var singleFiles = func() []string {
	f := []string{PathAstDB, PathAslcpDB, PathAslcpConf, PathSA818, PathDVSwitchPrivate, PathDVSwitchNetworks}
	for _, r := range DVSwitchFiles {
		f = append(f, DVSwitchPath(r))
	}
	return f
}()

// Entry is one file, directory or symlink of the archive.
type Entry struct {
	Path   string `json:"path"`
	Type   string `json:"type"`
	Size   int64  `json:"size,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
	Owner  string `json:"owner"`
	Group  string `json:"group"`
	Mode   string `json:"mode"` // octal permission bits, e.g. "0640"
	Target string `json:"target,omitempty"`
	// Class, Package, PackageVersion and StockMD5 describe files of
	// asterisk/etc: the stock/changed/local classification, the owning
	// package and its version, and the md5 dpkg recorded for the shipped
	// version.
	Class          string `json:"class,omitempty"`
	Package        string `json:"package,omitempty"`
	PackageVersion string `json:"packageVersion,omitempty"`
	StockMD5       string `json:"stockMd5,omitempty"`
}

// Package is an installed package.
type Package struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// MissingStock is a conffile of an installed package that was deleted on
// the backed-up node (Path relative to /etc/asterisk).
type MissingStock struct {
	Path    string `json:"path"`
	Package string `json:"package"`
	Version string `json:"version"`
}

// Parts says which optional parts are in the archive.
type Parts struct {
	History bool `json:"history"`
	Allmon3 bool `json:"allmon3"`
	SA818   bool `json:"sa818"`
	AstDB   bool `json:"astdb"`
	// DVSwitch is true when the archive holds files under dvswitch/ (the ini
	// files of dvswitch/opt, and the bridge node record).
	// Added after format 1 shipped: older archives lack it (false) and stay
	// valid; the format number did not change.
	DVSwitch bool `json:"dvswitch,omitempty"`
	// Sounds lists the archived custom sound directories ("var-lib",
	// "usr-share"); one only when both are the same directory.
	Sounds []string `json:"sounds"`
}

// Manifest is manifest.json.
type Manifest struct {
	Format          int       `json:"format"`
	Created         time.Time `json:"created"`
	CreatedBy       string    `json:"createdBy"`
	ASLCPVersion    string    `json:"aslcpVersion"`
	ASL3Version     string    `json:"asl3Version"`
	AsteriskVersion string    `json:"asteriskVersion"`
	Hostname        string    `json:"hostname"`
	Timezone        string    `json:"timezone"`
	Arch            string    `json:"arch"`
	// The Debian release of the backed-up node (os-release ID,
	// VERSION_CODENAME and PRETTY_NAME). Optional: older backups lack them.
	OSID              string         `json:"osId,omitempty"`
	OSVersionCodename string         `json:"osCodename,omitempty"`
	OSPrettyName      string         `json:"osName,omitempty"`
	Nodes             []string       `json:"nodes"`
	Channel           string         `json:"channel"`
	Packages          []Package      `json:"packages"`
	Parts             Parts          `json:"parts"`
	MissingStock      []MissingStock `json:"missingStock"`
	Skipped           []string       `json:"skipped,omitempty"`
	Files             []Entry        `json:"files"`
}

// Error is a problem with the archive; its message is safe to show.
type Error struct{ Msg string }

func (e *Error) Error() string { return e.Msg }

func bad(format string, a ...any) error { return &Error{Msg: fmt.Sprintf(format, a...)} }

// NewerFormatError means the archive has a format version this build does
// not know.
type NewerFormatError struct{ Version int }

func (e *NewerFormatError) Error() string {
	return fmt.Sprintf("the backup uses format %d, which this version of ASLCP does not know; update ASLCP first", e.Version)
}

var (
	reName    = regexp.MustCompile(`^[a-z_][a-z0-9_.-]{0,31}\$?$`)
	reMode    = regexp.MustCompile(`^0[0-7]{3}$`)
	reSHA     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	reNode    = regexp.MustCompile(`^[0-9]{1,10}$`)
	rePkgName = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]{0,99}$`)
	reMD5     = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

// ValidName reports whether s is a plausible user or group name.
func ValidName(s string) bool { return reName.MatchString(s) }

// ValidPackageName reports whether s is a Debian package name.
func ValidPackageName(s string) bool { return rePkgName.MatchString(s) }

// ParseMode parses an octal permission string ("0640").
func ParseMode(s string) (uint32, error) {
	if !reMode.MatchString(s) {
		return 0, bad("invalid mode %q", s)
	}
	v, _ := strconv.ParseUint(s, 8, 32)
	return uint32(v), nil
}

// FormatMode formats permission bits as "0640".
func FormatMode(m uint32) string { return fmt.Sprintf("%04o", m&0o777) }

// ValidPath checks an archive path: relative, clean, no "..", no control
// characters or backslashes.
func ValidPath(p string) error {
	if p == "" || len(p) > maxPathLen {
		return bad("invalid path length")
	}
	if !utf8.ValidString(p) {
		return bad("path is not valid UTF-8")
	}
	if p[0] == '/' {
		return bad("absolute path %q", p)
	}
	for _, r := range p {
		if !unicode.IsPrint(r) || r == '\\' {
			return bad("path %q contains an invalid character", p)
		}
	}
	for _, c := range strings.Split(p, "/") {
		if c == "" || c == "." || c == ".." {
			return bad("path %q is not clean", p)
		}
	}
	return nil
}

// RootOf returns the part root a path belongs to (a tree root, or the path
// itself for a single-file part). ok is false for paths outside the layout.
func RootOf(p string) (root string, ok bool) {
	for _, r := range treeRoots {
		if p == r || strings.HasPrefix(p, r+"/") {
			return r, true
		}
	}
	for _, f := range singleFiles {
		if p == f {
			return f, true
		}
	}
	return "", false
}

// IsTreeRoot reports whether p is exactly the root of a tree part.
func IsTreeRoot(p string) bool {
	for _, r := range treeRoots {
		if p == r {
			return true
		}
	}
	return false
}

func validTarget(path, root, target string) error {
	if target == "" || len(target) > maxPathLen || !utf8.ValidString(target) || strings.ContainsAny(target, "\\\x00") {
		return bad("invalid symlink target for %q", path)
	}
	if target[0] == '/' {
		return bad("symlink %q points to an absolute path", path)
	}
	parts := strings.Split(target, "/")
	i := 0
	for i < len(parts) && parts[i] == ".." {
		i++
	}
	for _, c := range parts[i:] {
		if c == "" || c == "." || c == ".." {
			return bad("symlink %q has an unsupported target", path)
		}
	}
	depth := strings.Count(path, "/") - strings.Count(root, "/") - 1 // directories between the root and the link
	if root == path {
		return bad("symlink %q cannot be a part root", path)
	}
	if i > depth {
		return bad("symlink %q points outside the backup tree", path)
	}
	return nil
}

func validEntry(e *Entry) error {
	if err := ValidPath(e.Path); err != nil {
		return err
	}
	root, ok := RootOf(e.Path)
	if !ok {
		return bad("path %q is not part of the backup layout", e.Path)
	}
	if !reName.MatchString(e.Owner) || !reName.MatchString(e.Group) {
		return bad("invalid owner or group for %q", e.Path)
	}
	if _, err := ParseMode(e.Mode); err != nil {
		return bad("invalid mode for %q", e.Path)
	}
	single := root == e.Path && !IsTreeRoot(e.Path)
	switch e.Type {
	case TypeFile:
		if e.Size < 0 || e.Size > MaxExpanded || !reSHA.MatchString(e.SHA256) || e.Target != "" {
			return bad("invalid file entry %q", e.Path)
		}
	case TypeDir:
		if single || e.Size != 0 || e.SHA256 != "" || e.Target != "" {
			return bad("invalid directory entry %q", e.Path)
		}
	case TypeSymlink:
		if single || e.Size != 0 || e.SHA256 != "" {
			return bad("invalid symlink entry %q", e.Path)
		}
		if err := validTarget(e.Path, root, e.Target); err != nil {
			return err
		}
	default:
		return bad("entry %q has unsupported type %q (devices and special files are not allowed)", e.Path, e.Type)
	}
	if single && e.Type != TypeFile {
		return bad("%q must be a regular file", e.Path)
	}
	switch e.Class {
	case "", ClassStock, ClassChanged, ClassLocal:
	default:
		return bad("invalid classification for %q", e.Path)
	}
	if e.StockMD5 != "" && !reMD5.MatchString(e.StockMD5) {
		return bad("invalid checksum for %q", e.Path)
	}
	if e.Package != "" && !rePkgName.MatchString(e.Package) || len(e.PackageVersion) > 100 {
		return bad("invalid package for %q", e.Path)
	}
	return nil
}

// Validate checks the manifest (format, every path, type, size, owner and
// mode, symlinks, duplicates, totals). It does not look at archive content.
func (m *Manifest) Validate() error {
	if m.Format > FormatVersion {
		return &NewerFormatError{Version: m.Format}
	}
	if m.Format != FormatVersion {
		return bad("unsupported backup format %d", m.Format)
	}
	if len(m.Files) > MaxEntries {
		return bad("too many entries")
	}
	for _, s := range []string{m.Hostname, m.Timezone, m.Arch, m.ASL3Version, m.AsteriskVersion, m.ASLCPVersion, m.CreatedBy, m.OSID, m.OSVersionCodename, m.OSPrettyName} {
		if len(s) > 256 || !utf8.ValidString(s) {
			return bad("invalid text field in the manifest")
		}
	}
	if m.Channel != "" && m.Channel != "stable" && m.Channel != "beta" {
		return bad("invalid update channel %q", m.Channel)
	}
	for _, n := range m.Nodes {
		if !reNode.MatchString(n) {
			return bad("invalid node number")
		}
	}
	if len(m.Packages) > 5000 || len(m.MissingStock) > 5000 {
		return bad("too many packages")
	}
	for _, p := range m.Packages {
		if !rePkgName.MatchString(p.Name) || len(p.Version) > 100 {
			return bad("invalid package entry")
		}
	}
	for _, s := range m.MissingStock {
		if ValidPath(s.Path) != nil {
			return bad("invalid missing-stock entry")
		}
	}
	for _, s := range m.Parts.Sounds {
		if s != "var-lib" && s != "usr-share" {
			return bad("invalid sounds part")
		}
	}
	seen := make(map[string]*Entry, len(m.Files))
	var total int64
	for i := range m.Files {
		e := &m.Files[i]
		if err := validEntry(e); err != nil {
			return err
		}
		if seen[e.Path] != nil {
			return bad("duplicate entry %q", e.Path)
		}
		seen[e.Path] = e
		total += e.Size
		if total > MaxExpanded {
			return bad("the backup is larger than %d GiB when unpacked", MaxExpanded>>30)
		}
	}
	for _, e := range seen {
		// No entry may live below a symlink.
		for p := parent(e.Path); p != ""; p = parent(p) {
			if a := seen[p]; a != nil && a.Type != TypeDir {
				return bad("entry %q is below %q, which is not a directory", e.Path, p)
			}
		}
	}
	return nil
}

func parent(p string) string {
	i := strings.LastIndexByte(p, '/')
	if i < 0 {
		return ""
	}
	return p[:i]
}

// Total returns the sum of all file sizes.
func (m *Manifest) Total() int64 {
	var t int64
	for _, e := range m.Files {
		t += e.Size
	}
	return t
}

// Sort orders the entries by path.
func (m *Manifest) Sort() {
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Path < m.Files[j].Path })
}

// Find returns the entry for path, or nil.
func (m *Manifest) Find(path string) *Entry {
	for i := range m.Files {
		if m.Files[i].Path == path {
			return &m.Files[i]
		}
	}
	return nil
}

// ---------------------------------------------------------------- reading

// countReader fails once more than n bytes were read.
type countReader struct {
	r io.Reader
	n int64
}

var errExpanded = bad("the backup expands to more than its manifest declares (possible archive bomb)")

func (c *countReader) Read(p []byte) (int, error) {
	if c.n < 0 {
		return 0, errExpanded
	}
	if int64(len(p)) > c.n+1 {
		p = p[:c.n+1]
	}
	n, err := c.r.Read(p)
	c.n -= int64(n)
	if c.n < 0 {
		return n, errExpanded
	}
	return n, err
}

// Handler receives each entry of an archive. body is non-nil for regular
// files and yields exactly e.Size bytes; the content is verified against the
// manifest after the handler returns (an error from Read means the archive is
// bad even if the handler already used the bytes, so handlers must write to
// scratch space only).
type Handler func(e Entry, body io.Reader) error

// Read validates a gzip-compressed archive completely and calls handle (may
// be nil) for every entry. The manifest must be the first entry; every other
// entry must be in the manifest with matching type, size and sha256; every
// manifest entry must be present; nothing may expand beyond the manifest's
// total (decompression limit).
func Read(r io.Reader, handle Handler) (*Manifest, error) {
	cr := &countReader{r: r, n: int64(MaxUpload) * 2}
	gz, err := gzip.NewReader(cr)
	if err != nil {
		return nil, bad("not a gzip-compressed backup file")
	}
	defer gz.Close()
	// Budget for the decompressed stream, until the manifest is known.
	lim := &countReader{r: gz, n: maxManifest + 4096}
	tr := tar.NewReader(lim)
	hdr, err := tr.Next()
	if err != nil {
		return nil, wrapTar(err, "the archive is empty or damaged")
	}
	if hdr.Name != ManifestName || (hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA) {
		return nil, bad("the first entry of the archive is not manifest.json")
	}
	if hdr.Size > maxManifest {
		return nil, bad("manifest.json is too large")
	}
	raw, err := io.ReadAll(io.LimitReader(tr, hdr.Size))
	if err != nil {
		return nil, wrapTar(err, "manifest.json is damaged")
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		var fm struct {
			Format int `json:"format"`
		}
		if json.Unmarshal(raw, &fm) == nil && fm.Format > FormatVersion {
			return nil, &NewerFormatError{Version: fm.Format}
		}
		return nil, bad("manifest.json is not valid")
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	byPath := make(map[string]*Entry, len(m.Files))
	for i := range m.Files {
		byPath[m.Files[i].Path] = &m.Files[i]
	}
	// Tar headers (512 bytes, plus padding and PAX extensions) of every
	// entry are budgeted on top of the file contents.
	lim.n = m.Total() + int64(len(m.Files)+8)*(4096+maxPathLen*2)
	seen := make(map[string]bool, len(m.Files))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, wrapTar(err, "the archive is damaged or truncated")
		}
		name := strings.TrimSuffix(hdr.Name, "/")
		e := byPath[name]
		if e == nil {
			return nil, bad("the archive contains %q, which is not in the manifest", hdr.Name)
		}
		if seen[name] {
			return nil, bad("duplicate archive entry %q", name)
		}
		seen[name] = true
		switch hdr.Typeflag {
		case tar.TypeReg, tar.TypeRegA:
			if e.Type != TypeFile || hdr.Size != e.Size {
				return nil, bad("entry %q does not match the manifest", name)
			}
			h := sha256.New()
			er := &errReader{r: io.LimitReader(tr, e.Size)}
			body := io.TeeReader(er, h)
			if handle != nil {
				if err := handle(*e, body); err != nil {
					if er.err != nil && er.err != io.EOF {
						// The handler failed because the archive itself
						// could not be read (truncated or corrupt gzip).
						return nil, wrapTar(er.err, "the archive is damaged or truncated")
					}
					return nil, err
				}
			}
			if _, err := io.Copy(io.Discard, body); err != nil {
				return nil, wrapTar(err, "the archive is damaged or truncated")
			}
			// (the tar reader returns ErrUnexpectedEOF on a short entry)
			if hex.EncodeToString(h.Sum(nil)) != e.SHA256 {
				return nil, bad("checksum mismatch for %q", name)
			}
		case tar.TypeDir:
			if e.Type != TypeDir {
				return nil, bad("entry %q does not match the manifest", name)
			}
			if handle != nil {
				if err := handle(*e, nil); err != nil {
					return nil, err
				}
			}
		case tar.TypeSymlink:
			if e.Type != TypeSymlink || hdr.Linkname != e.Target {
				return nil, bad("entry %q does not match the manifest", name)
			}
			if handle != nil {
				if err := handle(*e, nil); err != nil {
					return nil, err
				}
			}
		default:
			return nil, bad("entry %q is a device, pipe, hard link or other special file", name)
		}
	}
	for i := range m.Files {
		if !seen[m.Files[i].Path] {
			return nil, bad("the archive is truncated: %q is missing", m.Files[i].Path)
		}
	}
	// Consume the rest (gzip trailer, padding) inside the budget.
	if _, err := io.Copy(io.Discard, lim); err != nil {
		return nil, wrapTar(err, "the archive is damaged")
	}
	return &m, nil
}

// errReader remembers the first read error of the archive stream.
type errReader struct {
	r   io.Reader
	err error
}

func (e *errReader) Read(p []byte) (int, error) {
	n, err := e.r.Read(p)
	if err != nil && e.err == nil {
		e.err = err
	}
	return n, err
}

func wrapTar(err error, msg string) error {
	var be *Error
	if errors.As(err, &be) {
		return be
	}
	return bad("%s", msg)
}

// ---------------------------------------------------------------- writing

// Writer writes an archive: gzip, tar, manifest first.
type Writer struct {
	gz   *gzip.Writer
	tw   *tar.Writer
	mod  time.Time
	seen map[string]bool
	m    *Manifest
}

// NewWriter starts an archive on w and writes the manifest (which must be
// valid and complete, entries in the order they will be written).
func NewWriter(w io.Writer, m *Manifest) (*Writer, error) {
	m.Sort()
	if err := m.Validate(); err != nil {
		return nil, err
	}
	raw, err := json.MarshalIndent(m, "", " ")
	if err != nil {
		return nil, err
	}
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	bw := &Writer{gz: gz, tw: tw, mod: m.Created, seen: map[string]bool{}, m: m}
	if err := tw.WriteHeader(&tar.Header{Name: ManifestName, Mode: 0o600, Size: int64(len(raw)), ModTime: m.Created, Format: tar.FormatPAX}); err != nil {
		return nil, err
	}
	if _, err := tw.Write(raw); err != nil {
		return nil, err
	}
	return bw, nil
}

// Add writes the entry named path from the manifest; body is required for
// files and is checked against the manifest's size and sha256.
func (w *Writer) Add(path string, body io.Reader) error {
	e := w.m.Find(path)
	if e == nil {
		return fmt.Errorf("backup: %q is not in the manifest", path)
	}
	if w.seen[path] {
		return fmt.Errorf("backup: %q written twice", path)
	}
	w.seen[path] = true
	return WriteTarEntry(w.tw, *e, body, w.mod)
}

// Close checks that every manifest entry was written and finishes the
// archive.
func (w *Writer) Close() error {
	for _, e := range w.m.Files {
		if !w.seen[e.Path] {
			return fmt.Errorf("backup: %q was not written", e.Path)
		}
	}
	if err := w.tw.Close(); err != nil {
		return err
	}
	return w.gz.Close()
}

// WriteTarEntry writes one entry (header, and for files the content, which
// is verified against e.Size and e.SHA256).
func WriteTarEntry(tw *tar.Writer, e Entry, body io.Reader, mod time.Time) error {
	mode, _ := ParseMode(e.Mode)
	h := &tar.Header{Name: e.Path, Mode: int64(mode), ModTime: mod}
	switch e.Type {
	case TypeDir:
		h.Typeflag, h.Name = tar.TypeDir, e.Path+"/"
		return tw.WriteHeader(h)
	case TypeSymlink:
		h.Typeflag, h.Linkname = tar.TypeSymlink, e.Target
		return tw.WriteHeader(h)
	}
	h.Typeflag, h.Size = tar.TypeReg, e.Size
	if err := tw.WriteHeader(h); err != nil {
		return err
	}
	sum := sha256.New()
	n, err := io.Copy(io.MultiWriter(tw, sum), io.LimitReader(body, e.Size+1))
	if err != nil {
		return err
	}
	if n != e.Size || hex.EncodeToString(sum.Sum(nil)) != e.SHA256 {
		return fmt.Errorf("backup: %q changed while it was being read", e.Path)
	}
	return nil
}

// CheckSymlink reports whether a symlink at archive path p may point to
// target (relative, inside the same part, no "." or inner ".." components).
func CheckSymlink(p, target string) error {
	root, ok := RootOf(p)
	if !ok {
		return bad("path %q is not part of the backup layout", p)
	}
	return validTarget(p, root, target)
}

// VersionNewer reports whether version a is strictly newer than b by the
// dpkg algorithm. Either being empty or "dev" yields false.
func VersionNewer(a, b string) bool {
	if a == "" || b == "" || a == "dev" || b == "dev" || !debver.Valid(a) || !debver.Valid(b) {
		return false
	}
	return debver.Compare(a, b) > 0
}
