package helper

import (
	"archive/tar"
	"context"
	"crypto/md5" //nolint:gosec // dpkg conffile checksums, not security
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"syscall"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver (AstDB snapshot)

	"github.com/artw1/aslcp/internal/backup"
	"github.com/artw1/aslcp/internal/legacy"
)

// Default locations of the backup parts.
const (
	DefaultAstDB      = "/var/lib/asterisk/astdb.sqlite3"
	DefaultSoundsVar  = "/var/lib/asterisk/sounds/custom"
	DefaultSoundsUsr  = "/usr/share/asterisk/sounds/custom"
	DefaultSA818Conf  = "/etc/sa818.conf"
	DefaultAllmonDir  = "/etc/allmon3"
	DefaultAslcpConf  = "/etc/aslcp/aslcp.conf"
	DefaultOSRelease  = "/etc/os-release"
	defaultTempDir    = "/var/tmp"
	stockConfigPrefix = "/etc/asterisk/"
	maxSkipped        = 200
)

func (o *Ops) astDB() string       { return orDefault(o.AstDBPath, DefaultAstDB) }
func (o *Ops) sa818Conf() string   { return orDefault(o.SA818Conf, DefaultSA818Conf) }
func (o *Ops) allmonDir() string   { return orDefault(o.AllmonDir, DefaultAllmonDir) }
func (o *Ops) aslcpConf() string   { return orDefault(o.AslcpConf, DefaultAslcpConf) }
func (o *Ops) osRelease() string   { return orDefault(o.OSRelease, DefaultOSRelease) }
func (o *Ops) tempDirRoot() string { return orDefault(o.TempDir, defaultTempDir) }

func orDefault(v, d string) string {
	if v != "" {
		return v
	}
	return d
}

// soundDir is one custom sounds directory.
type soundDir struct{ id, path string }

// soundDirs returns the existing custom sounds directories, de-duplicated by
// real path (one may link to the other).
func (o *Ops) soundDirs() []soundDir {
	paths := [2]string{orDefault(o.SoundsDirs[0], DefaultSoundsVar), orDefault(o.SoundsDirs[1], DefaultSoundsUsr)}
	ids := [2]string{"var-lib", "usr-share"}
	var out []soundDir
	seen := map[string]bool{}
	for i, p := range paths {
		rp, err := filepath.EvalSymlinks(p)
		if err != nil {
			continue
		}
		if fi, err := os.Stat(rp); err != nil || !fi.IsDir() || seen[rp] {
			continue
		}
		seen[rp] = true
		out = append(out, soundDir{ids[i], rp})
	}
	return out
}

// ---------------------------------------------------------------- dpkg

type confInfo struct{ pkg, version, md5 string }

// dpkgState is what dpkg knows: installed packages and the conffiles under
// /etc/asterisk (keyed by path relative to it).
type dpkgState struct {
	installed map[string]string
	conf      map[string]confInfo
}

// dpkgState runs dpkg-query with fixed argument vectors through the Runner.
// A node without dpkg yields an empty state (everything is "local").
func (o *Ops) dpkgState(ctx context.Context) dpkgState {
	st := dpkgState{installed: map[string]string{}, conf: map[string]confInfo{}}
	out, code, err := o.Run.Run(ctx, "dpkg-query", "-W", `-f=${Package}\t${Version}\t${db:Status-Abbrev}\n`)
	if err == nil && code == 0 {
		for _, line := range strings.Split(out, "\n") {
			f := strings.Split(line, "\t")
			if len(f) == 3 && strings.HasPrefix(strings.TrimSpace(f[2]), "ii") {
				st.installed[f[0]] = f[1]
			}
		}
	}
	out, code, err = o.Run.Run(ctx, "dpkg-query", "-W", `-f=${Package} ${Version}\n${Conffiles}\n`)
	if err != nil || code != 0 {
		return st
	}
	var pkg, ver string
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if line[0] != ' ' && line[0] != '\t' {
			f := strings.Fields(line)
			if len(f) == 2 {
				pkg, ver = f[0], f[1]
			}
			continue
		}
		f := strings.Fields(line)
		if len(f) < 2 || len(f) > 3 || !strings.HasPrefix(f[0], stockConfigPrefix) || !reMD5.MatchString(f[1]) {
			continue
		}
		if len(f) == 3 { // "obsolete" or "remove-on-upgrade"
			continue
		}
		if _, dup := st.conf[strings.TrimPrefix(f[0], stockConfigPrefix)]; !dup {
			st.conf[strings.TrimPrefix(f[0], stockConfigPrefix)] = confInfo{pkg, ver, f[1]}
		}
	}
	return st
}

var reMD5 = regexp.MustCompile(`^[0-9a-f]{32}$`)

// wantedPackage selects the packages recorded in the manifest.
func wantedPackage(n string) bool {
	for _, p := range []string{"asl3", "asterisk", "allmon", "supermon", legacy.PackagePrefix} {
		if strings.HasPrefix(n, p) {
			return true
		}
	}
	return strings.Contains(n, "m17")
}

func (s dpkgState) packages() []backup.Package {
	out := []backup.Package{}
	for n, v := range s.installed {
		if wantedPackage(n) {
			out = append(out, backup.Package{Name: n, Version: v})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (s dpkgState) asl3() string { return s.installed["asl3"] }
func (s dpkgState) asterisk() string {
	if v := s.installed["asl3-asterisk"]; v != "" {
		return v
	}
	return s.installed["asterisk"]
}

func (o *Ops) arch(ctx context.Context) string {
	out, code, err := o.Run.Run(ctx, "dpkg", "--print-architecture")
	if a := strings.TrimSpace(out); err == nil && code == 0 && a != "" && len(a) < 32 && !strings.ContainsAny(a, " \n\t") {
		return a
	}
	return runtime.GOARCH
}

// osInfo reads os-release; the codename falls back to the ".debNN" suffix of
// the ASL3 or Asterisk version.
func (o *Ops) osInfo(st dpkgState) backup.OSInfo {
	var os_ backup.OSInfo
	if b, err := os.ReadFile(o.osRelease()); err == nil {
		os_ = backup.ParseOSRelease(b)
	}
	os_.Codename = backup.ReleaseOf(os_.Codename, st.asl3(), st.asterisk())
	return os_
}

var reNodeSection = regexp.MustCompile(`(?m)^\[([0-9]{1,10})\]`)

func (o *Ops) nodes() []string {
	out := []string{}
	b, err := os.ReadFile(filepath.Join(o.configRoot(), "rpt.conf"))
	if err != nil {
		return out
	}
	seen := map[string]bool{}
	for _, m := range reNodeSection.FindAllStringSubmatch(string(b), -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}

// ---------------------------------------------------------------- info

func dirSize(ctx context.Context, root string) int64 {
	var n int64
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || ctx.Err() != nil {
			return nil
		}
		if d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}

// BackupInfo reports which optional parts exist and their size.
func (o *Ops) BackupInfo(ctx context.Context) (BackupInfo, error) {
	info := BackupInfo{Parts: []BackupPartInfo{}}
	hist := BackupPartInfo{ID: "history"}
	if _, err := os.Stat(filepath.Join(o.historyDir(), "HEAD")); err == nil {
		hist.Present, hist.Size = true, dirSize(ctx, o.historyDir())
	}
	am := BackupPartInfo{ID: "allmon3"}
	if fi, err := os.Stat(o.allmonDir()); err == nil && fi.IsDir() {
		am.Present, am.Size = true, dirSize(ctx, o.allmonDir())
	}
	sd := BackupPartInfo{ID: "sounds"}
	for _, d := range o.soundDirs() {
		sd.Present = true
		sd.Size += dirSize(ctx, d.path)
	}
	info.Parts = append(info.Parts, hist, am, sd, o.dvPartInfo())
	if fi, err := os.Lstat(o.sa818Conf()); err == nil && fi.Mode().IsRegular() {
		info.SA818 = true
	}
	return info, nil
}

// ---------------------------------------------------------------- create

// srcEntry is a manifest entry and where its content comes from.
type srcEntry struct {
	e   backup.Entry
	src string // regular files only
}

func safeName(id int, group bool) string {
	n := lookupName(id, group)
	if !backup.ValidName(n) {
		return "root"
	}
	return n
}

func openRegular(path string) (*os.File, fs.FileInfo, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	if !fi.Mode().IsRegular() {
		f.Close()
		return nil, nil, errors.New("not a regular file")
	}
	return f, fi, nil
}

// entryFor fills the common fields of an entry from file info.
func entryFor(path, typ string, fi fs.FileInfo) backup.Entry {
	e := backup.Entry{Path: path, Type: typ, Mode: backup.FormatMode(uint32(fi.Mode().Perm()))}
	uid, gid, _ := ownerIDs(fi)
	e.Owner, e.Group = safeName(uid, false), safeName(gid, true)
	return e
}

// hashFile returns size, sha256 and md5 of a regular file.
func hashFile(path string) (int64, string, string, fs.FileInfo, error) {
	f, fi, err := openRegular(path)
	if err != nil {
		return 0, "", "", nil, err
	}
	defer f.Close()
	s, m := sha256.New(), md5.New() //nolint:gosec
	n, err := io.Copy(io.MultiWriter(s, m), f)
	if err != nil {
		return 0, "", "", nil, err
	}
	return n, hex.EncodeToString(s.Sum(nil)), hex.EncodeToString(m.Sum(nil)), fi, nil
}

type collector struct {
	ctx     context.Context
	entries []srcEntry
	skipped []string
	total   int64
}

func (c *collector) skip(p, why string) {
	if len(c.skipped) < maxSkipped {
		c.skipped = append(c.skipped, p+" ("+why+")")
	}
}

func (c *collector) add(se srcEntry) error {
	if len(c.entries) >= backup.MaxEntries {
		return errors.New("too many files to back up")
	}
	c.total += se.e.Size
	if c.total > backup.MaxExpanded {
		return errors.New("the selected parts are too large for a backup")
	}
	c.entries = append(c.entries, se)
	return nil
}

// walk collects the tree at srcRoot (a real directory) under the archive
// path archRoot. conf, when non-nil, classifies files as in /etc/asterisk.
func (c *collector) walk(srcRoot, archRoot string, conf map[string]confInfo) error {
	return filepath.WalkDir(srcRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			rel, _ := filepath.Rel(srcRoot, p)
			c.skip(filepath.ToSlash(rel), "unreadable")
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if c.ctx.Err() != nil {
			return c.ctx.Err()
		}
		rel, _ := filepath.Rel(srcRoot, p)
		rel = filepath.ToSlash(rel)
		arch := archRoot
		if rel != "." {
			arch = archRoot + "/" + rel
		}
		if backup.ValidPath(arch) != nil {
			c.skip(rel, "unsupported file name")
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			c.skip(rel, "unreadable")
			return nil
		}
		switch {
		case d.IsDir():
			return c.add(srcEntry{e: entryFor(arch, backup.TypeDir, fi)})
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil || backup.CheckSymlink(arch, target) != nil {
				c.skip(rel, "symlink leaves the tree")
				return nil
			}
			e := entryFor(arch, backup.TypeSymlink, fi)
			e.Target = target
			e.Class = classLocalIf(conf)
			return c.add(srcEntry{e: e})
		case d.Type().IsRegular():
			n, sum, md, fi, err := hashFile(p)
			if err != nil {
				c.skip(rel, "unreadable")
				return nil
			}
			e := entryFor(arch, backup.TypeFile, fi)
			e.Size, e.SHA256 = n, sum
			if conf != nil {
				e.Class = backup.ClassLocal
				if ci, ok := conf[rel]; ok {
					e.Package, e.PackageVersion, e.StockMD5 = ci.pkg, ci.version, ci.md5
					if md == ci.md5 {
						e.Class = backup.ClassStock
					} else {
						e.Class = backup.ClassChanged
					}
				}
			}
			return c.add(srcEntry{e: e, src: p})
		}
		c.skip(rel, "not a regular file")
		return nil
	})
}

func classLocalIf(conf map[string]confInfo) string {
	if conf != nil {
		return backup.ClassLocal
	}
	return ""
}

// sqliteCopy writes a consistent copy of the SQLite database src to dst
// (which must not exist) while other processes may be writing: VACUUM INTO
// reads the source inside a read transaction.
func sqliteCopy(ctx context.Context, src, dst string) error {
	u := url.URL{Scheme: "file", Path: src, RawQuery: url.Values{"mode": {"ro"}, "_pragma": {"busy_timeout(5000)"}}.Encode()}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	_, err = db.ExecContext(ctx, "VACUUM INTO ?", dst)
	return err
}

// BackupCreate writes the header line and the tar stream of the system part
// to w. sent reports whether anything was written (so a failure before the
// header can still be answered with an error line).
func (o *Ops) BackupCreate(ctx context.Context, req *BackupRequest, w io.Writer) (sent bool, err error) {
	if req == nil {
		req = &BackupRequest{}
	}
	// No config change may rewrite the history while it is being read.
	o.applyMu.Lock()
	defer o.applyMu.Unlock()

	st := o.dpkgState(ctx)
	root, err := filepath.EvalSymlinks(o.configRoot())
	if err != nil {
		return false, errors.New("the Asterisk configuration directory does not exist")
	}
	c := &collector{ctx: ctx}
	if err := c.walk(root, backup.PathAsteriskEtc, st.conf); err != nil {
		return false, err
	}
	osi := o.osInfo(st)
	hdr := BackupHeader{
		Arch: o.arch(ctx), OSID: osi.ID, OSCodename: osi.Codename, OSName: osi.Pretty, ASL3Version: st.asl3(), AsteriskVersion: st.asterisk(), Nodes: o.nodes(),
		Packages: st.packages(), MissingStock: []backup.MissingStock{}, Skipped: []string{},
		Parts: backup.Parts{Sounds: []string{}},
	}
	hdr.Channel, _ = readChannel(o.sourceList())
	var missing []string
	for rel := range st.conf {
		if _, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel))); errors.Is(err, fs.ErrNotExist) {
			missing = append(missing, rel)
		}
	}
	sort.Strings(missing)
	for _, rel := range missing {
		if backup.ValidPath(rel) == nil {
			ci := st.conf[rel]
			hdr.MissingStock = append(hdr.MissingStock, backup.MissingStock{Path: rel, Package: ci.pkg, Version: ci.version})
		}
	}

	tmp, err := os.MkdirTemp(o.tempDirRoot(), "aslcp-backup-")
	if err != nil {
		return false, fmt.Errorf("temporary directory: %w", err)
	}
	defer os.RemoveAll(tmp)

	// AstDB: a consistent copy, owner and mode of the original.
	if fi, err := os.Lstat(o.astDB()); err == nil && fi.Mode().IsRegular() {
		cp := filepath.Join(tmp, "astdb.sqlite3")
		if err := sqliteCopy(ctx, o.astDB(), cp); err != nil {
			return false, fmt.Errorf("copying AstDB: %w", err)
		}
		n, sum, _, _, err := hashFile(cp)
		if err != nil {
			return false, fmt.Errorf("copying AstDB: %w", err)
		}
		e := entryFor(backup.PathAstDB, backup.TypeFile, fi)
		e.Size, e.SHA256 = n, sum
		if err := c.add(srcEntry{e: e, src: cp}); err != nil {
			return false, err
		}
		hdr.Parts.AstDB = true
	}
	if fi, err := os.Lstat(o.sa818Conf()); err == nil && fi.Mode().IsRegular() {
		n, sum, _, fi2, err := hashFile(o.sa818Conf())
		if err == nil {
			e := entryFor(backup.PathSA818, backup.TypeFile, fi2)
			e.Size, e.SHA256 = n, sum
			if err := c.add(srcEntry{e: e, src: o.sa818Conf()}); err != nil {
				return false, err
			}
			hdr.Parts.SA818 = true
		} else {
			c.skip("sa818.conf", "unreadable")
		}
	}
	if req.History {
		if _, err := os.Stat(filepath.Join(o.historyDir(), "HEAD")); err == nil {
			rp, err := filepath.EvalSymlinks(o.historyDir())
			if err != nil {
				return false, err
			}
			if err := c.walk(rp, backup.PathHistory, nil); err != nil {
				return false, err
			}
			hdr.Parts.History = true
		}
	}
	if req.Allmon3 {
		if rp, err := filepath.EvalSymlinks(o.allmonDir()); err == nil {
			if fi, err := os.Stat(rp); err == nil && fi.IsDir() {
				if err := c.walk(rp, backup.PathAllmon3, nil); err != nil {
					return false, err
				}
				hdr.Parts.Allmon3 = true
			}
		}
	}
	if req.Sounds {
		for _, d := range o.soundDirs() {
			root := backup.PathSoundsVar
			if d.id == "usr-share" {
				root = backup.PathSoundsUsr
			}
			if err := c.walk(d.path, root, nil); err != nil {
				return false, err
			}
			hdr.Parts.Sounds = append(hdr.Parts.Sounds, d.id)
		}
	}
	if req.DV && o.dvInstalled() {
		added, err := o.collectDV(c)
		if err != nil {
			return false, err
		}
		hdr.Parts.DV = added
	}
	hdr.Skipped = append(hdr.Skipped, c.skipped...)
	hdr.Entries = make([]backup.Entry, len(c.entries))
	for i, se := range c.entries {
		hdr.Entries[i] = se.e
	}
	line, err := json.Marshal(hdr)
	if err != nil {
		return false, err
	}
	if len(line) > maxBackupHeader {
		return false, errors.New("too many files to back up")
	}
	if _, err := w.Write(append(line, '\n')); err != nil {
		return true, err
	}
	tw := tar.NewWriter(w)
	now := time.Now()
	for _, se := range c.entries {
		if ctx.Err() != nil {
			return true, ctx.Err()
		}
		var body io.Reader
		var f *os.File
		if se.e.Type == backup.TypeFile {
			f, _, err = openRegular(se.src)
			if err != nil {
				return true, fmt.Errorf("%s: %w", se.e.Path, err)
			}
			body = f
		}
		err := backup.WriteTarEntry(tw, se.e, body, now)
		if f != nil {
			f.Close()
		}
		if err != nil {
			return true, err
		}
	}
	return true, tw.Close()
}

// sqliteCheck verifies that path is an intact SQLite database.
func sqliteCheck(path string) error {
	u := url.URL{Scheme: "file", Path: path, RawQuery: url.Values{"mode": {"ro"}}.Encode()}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return err
	}
	defer db.Close()
	var res string
	if err := db.QueryRow("PRAGMA quick_check").Scan(&res); err != nil {
		return err
	}
	if res != "ok" {
		return errors.New(res)
	}
	return nil
}
