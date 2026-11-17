package helper

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // git object ids, not security
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"
)

// Limits and defaults of the config-* operations.
const (
	DefaultConfigRoot = "/etc/asterisk"
	// DefaultHistoryDir is root:root 0700. It deliberately is NOT below
	// /var/lib/aslcp: that directory belongs to the unprivileged aslcp user,
	// who could swap a repository in it for one with a hostile git config
	// that the root helper would then execute.
	DefaultHistoryDir = "/var/lib/aslcp-config-history"

	maxConfigRequest = 4 << 20
	maxConfigFile    = 1 << 20
	maxConfigFiles   = 50
	maxListEntries   = 500
	maxPathParts     = 4
	maxMessageRunes  = 200
	maxShowDiff      = 256 << 10
	maxShowTotal     = 8 << 20
)

var (
	rePath   = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._-]*(/[A-Za-z0-9_][A-Za-z0-9._-]*)*$`)
	reAuthor = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	reCommit = regexp.MustCompile(`^[0-9a-f]{7,40}$`)
	reSHA256 = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// ConfigWriter atomically replaces the regular file abs (inside the verified
// real directory parent) with data, giving it uid/gid/mode. Ops.ConfigWrite
// lets tests inject failures.
type ConfigWriter func(abs, parent string, data []byte, uid, gid int, mode fs.FileMode) error

func (o *Ops) configRoot() string {
	if o.ConfigRoot != "" {
		return o.ConfigRoot
	}
	return DefaultConfigRoot
}

func (o *Ops) historyDir() string {
	if o.HistoryDir != "" {
		return o.HistoryDir
	}
	return DefaultHistoryDir
}

func isConfigOp(op string) bool {
	switch op {
	case OpConfigList, OpConfigRead, OpConfigApply, OpConfigLog, OpConfigShow, OpConfigRestore, OpConfigRecord:
		return true
	}
	return false
}

func sha256hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// ---------------------------------------------------------------- validation

// validateRel checks the syntactic path rules (no filesystem access).
func validateRel(rel string) error {
	if len(rel) > 200 || !rePath.MatchString(rel) {
		return errors.New("invalid path")
	}
	if !strings.HasSuffix(rel, ".conf") {
		return errors.New("only .conf files are allowed")
	}
	if strings.Count(rel, "/")+1 > maxPathParts {
		return errors.New("path too deep")
	}
	return nil
}

func validContent(s string) error {
	if len(s) > maxConfigFile {
		return errors.New("file too large (limit 1 MiB)")
	}
	if !utf8.ValidString(s) {
		return errors.New("content is not valid UTF-8")
	}
	if strings.IndexByte(s, 0) >= 0 {
		return errors.New("content contains a NUL byte")
	}
	return nil
}

func sanitizeMessage(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", errors.New("message required")
	}
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > maxMessageRunes {
		return "", errors.New("invalid message (max 200 characters)")
	}
	for _, r := range s {
		if unicode.IsControl(r) || r == ' ' || r == ' ' {
			return "", errors.New("message must be a single line without control characters")
		}
	}
	return s, nil
}

// cleanSubject makes text taken from an existing commit safe for reuse.
func cleanSubject(s string, max int) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if r == utf8.RuneError || unicode.IsControl(r) || r == ' ' || r == ' ' {
			r = ' '
		}
		if n >= max {
			break
		}
		b.WriteRune(r)
		n++
	}
	return strings.TrimSpace(b.String())
}

func strayFields(c ConfigRequest, keep string) error {
	var bad []string
	chk := func(letter byte, set bool, name string) {
		if set && strings.IndexByte(keep, letter) < 0 {
			bad = append(bad, name)
		}
	}
	chk('p', c.Path != "", "path")
	chk('f', len(c.Files) > 0, "files")
	chk('m', c.Message != "", "message")
	chk('a', c.Author != "", "author")
	chk('c', c.Commit != "", "commit")
	chk('l', c.Limit != 0, "limit")
	chk('o', c.Offset != 0, "offset")
	if len(bad) > 0 {
		return fmt.Errorf("invalid request: unexpected field %s", bad[0])
	}
	return nil
}

// target is a validated config file location.
type target struct {
	rel    string
	abs    string // inside the real parent directory
	parent string // real path of the parent directory
	info   fs.FileInfo
}

// resolve validates rel against the path rules and the filesystem. With
// write, a missing file is allowed under an existing custom/ directory.
func (o *Ops) resolve(rel string, write bool) (*target, error) {
	if err := validateRel(rel); err != nil {
		return nil, err
	}
	root, err := filepath.EvalSymlinks(o.configRoot())
	if err != nil {
		return nil, errors.New("config directory unavailable")
	}
	parent, err := filepath.EvalSymlinks(filepath.Join(root, filepath.Dir(rel)))
	if err != nil {
		return nil, errors.New("directory does not exist")
	}
	if !within(root, parent) {
		return nil, errors.New("path escapes the config directory")
	}
	if pi, err := os.Stat(parent); err != nil || !pi.IsDir() {
		return nil, errors.New("not a directory")
	}
	t := &target{rel: rel, parent: parent, abs: filepath.Join(parent, filepath.Base(rel))}
	fi, err := os.Lstat(t.abs)
	switch {
	case err == nil:
		if !fi.Mode().IsRegular() {
			return nil, errors.New("not a regular file")
		}
		t.info = fi
	case errors.Is(err, fs.ErrNotExist):
		if !write {
			return nil, errors.New(ErrNoSuchFile)
		}
		if !within(filepath.Join(root, "custom"), parent) {
			return nil, errors.New("new files are only allowed in an existing custom/ directory")
		}
	default:
		return nil, errors.New("cannot access file")
	}
	return t, nil
}

func within(root, p string) bool {
	return p == root || strings.HasPrefix(p, strings.TrimSuffix(root, "/")+"/")
}

// readFile reads a regular file without following a final symlink.
func readFile(abs string) ([]byte, fs.FileInfo, error) {
	f, err := os.OpenFile(abs, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, nil, errors.New("not a regular file")
	}
	b, err := io.ReadAll(io.LimitReader(f, maxConfigFile+1))
	if err != nil {
		return nil, nil, err
	}
	if len(b) > maxConfigFile {
		return nil, nil, errors.New("file too large (limit 1 MiB)")
	}
	return b, fi, nil
}

// ---------------------------------------------------------------- list/read

func lookupName(id int, group bool) string {
	s := strconv.Itoa(id)
	if group {
		if g, err := user.LookupGroupId(s); err == nil {
			return g.Name
		}
	} else if u, err := user.LookupId(s); err == nil {
		return u.Username
	}
	return s
}

// ConfigList lists every *.conf file (exact suffix) under the config root.
func (o *Ops) ConfigList(ctx context.Context) ([]ConfigInfo, error) {
	root, err := filepath.EvalSymlinks(o.configRoot())
	if err != nil {
		return nil, errors.New("config directory unavailable")
	}
	out := []ConfigInfo{}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		rel, _ := filepath.Rel(root, p)
		if rel == "." {
			return nil
		}
		parts := strings.Count(rel, "/") + 1
		if d.Type()&fs.ModeSymlink != 0 {
			return nil // never followed, never listed
		}
		if d.IsDir() {
			if parts >= maxPathParts {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || validateRel(rel) != nil {
			return nil
		}
		b, fi, err := readFile(p)
		if err != nil {
			return nil
		}
		ci := ConfigInfo{Path: rel, Size: fi.Size(), MTime: fi.ModTime().Unix(), SHA256: sha256hex(b),
			Mode: fmt.Sprintf("%04o", fi.Mode().Perm())}
		if uid, gid, ok := ownerIDs(fi); ok {
			ci.Owner, ci.Group = lookupName(uid, false), lookupName(gid, true)
		}
		out = append(out, ci)
		if len(out) >= maxListEntries {
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// ConfigRead returns one file.
func (o *Ops) ConfigRead(rel string) (ConfigContent, error) {
	t, err := o.resolve(rel, false)
	if err != nil {
		return ConfigContent{}, err
	}
	b, _, err := readFile(t.abs)
	if err != nil {
		return ConfigContent{}, err
	}
	if err := validContent(string(b)); err != nil {
		return ConfigContent{}, errors.New("file is not editable text: " + err.Error())
	}
	return ConfigContent{Path: rel, Content: string(b), SHA256: sha256hex(b)}, nil
}

// ---------------------------------------------------------------- writing

func (o *Ops) writer() ConfigWriter {
	if o.ConfigWrite != nil {
		return o.ConfigWrite
	}
	return atomicWrite
}

// atomicWrite is the default ConfigWriter: temp file in the same directory
// (name not ending in .conf, created exclusively, no symlink following),
// owner and mode set, fsync, rename.
func atomicWrite(abs, parent string, data []byte, uid, gid int, mode fs.FileMode) error {
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return err
	}
	tmp := filepath.Join(parent, ".aslcp-tmp-"+hex.EncodeToString(rnd[:]))
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		if !ok {
			f.Close()
			os.Remove(tmp)
		}
	}()
	if err := f.Chown(uid, gid); err != nil && os.Geteuid() == 0 {
		return err // the real helper is root; unprivileged test runs cannot chown
	}
	if err := f.Chmod(mode.Perm()); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	// Last checks before the rename: the directory must still be the one we
	// validated and the destination must not have become a non-regular file.
	if rp, err := filepath.EvalSymlinks(parent); err != nil || rp != parent {
		return errors.New("directory changed during write")
	}
	if fi, err := os.Lstat(abs); err == nil && !fi.Mode().IsRegular() {
		return errors.New("destination is not a regular file")
	}
	if err := os.Rename(tmp, abs); err != nil {
		return err
	}
	ok = true
	if d, err := os.Open(parent); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}

func (o *Ops) newFileOwner() (uid, gid int, mode fs.FileMode) {
	if o.NewFileOwner != nil {
		return o.NewFileOwner()
	}
	u, err1 := user.Lookup("asterisk")
	g, err2 := user.LookupGroup("asterisk")
	if err1 == nil && err2 == nil {
		uu, e1 := strconv.Atoi(u.Uid)
		gg, e2 := strconv.Atoi(g.Gid)
		if e1 == nil && e2 == nil {
			return uu, gg, 0o640
		}
	}
	return 0, 0, 0o644
}

// pending is one file to be written.
type pending struct {
	t        *target
	content  []byte
	orig     []byte
	existed  bool
	uid, gid int
	mode     fs.FileMode
}

// stage reads the current state of the target of a pending write.
func (o *Ops) stage(rel string, content []byte) (*pending, error) {
	t, err := o.resolve(rel, true)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	p := &pending{t: t, content: content}
	if t.info != nil {
		b, fi, err := readFile(t.abs)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", rel, err)
		}
		p.orig, p.existed = b, true
		p.mode = fi.Mode().Perm()
		p.uid, p.gid, _ = ownerIDs(fi)
	} else {
		p.uid, p.gid, p.mode = o.newFileOwner()
	}
	return p, nil
}

// write performs all writes; on any error the files already written are put
// back (originals kept in memory, new files removed) and the error returned.
func (o *Ops) writeAll(ps []*pending) error {
	w := o.writer()
	var done []*pending
	rollback := func(cause error) error {
		var failed []string
		for i := len(done) - 1; i >= 0; i-- {
			p := done[i]
			var err error
			if p.existed {
				err = w(p.t.abs, p.t.parent, p.orig, p.uid, p.gid, p.mode)
			} else {
				err = os.Remove(p.t.abs)
			}
			if err != nil {
				failed = append(failed, p.t.rel)
			}
		}
		if len(failed) > 0 {
			return fmt.Errorf("%w (rollback failed for: %s)", cause, strings.Join(failed, ", "))
		}
		return cause
	}
	for _, p := range ps {
		// Re-check right before writing: an outside edit after the snapshot
		// must not be overwritten silently.
		if p.existed {
			cur, _, err := readFile(p.t.abs)
			if err != nil || !bytes.Equal(cur, p.orig) {
				return rollback(&ConflictError{Files: []string{p.t.rel}})
			}
		} else if _, err := os.Lstat(p.t.abs); err == nil {
			return rollback(&ConflictError{Files: []string{p.t.rel}})
		}
		if err := w(p.t.abs, p.t.parent, p.content, p.uid, p.gid, p.mode); err != nil {
			return rollback(fmt.Errorf("writing %s: %w", p.t.rel, err))
		}
		done = append(done, p)
	}
	return nil
}

// ---------------------------------------------------------------- git

// gitEnv is the fixed environment of every git call (env -i clears whatever
// the helper inherited, GIT_* variables included).
func (o *Ops) gitArgv(hist, root string, args ...string) []string {
	argv := []string{"env", "-i",
		"PATH=/usr/local/bin:/usr/bin:/bin",
		"HOME=/nonexistent",
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_ASKPASS=/bin/false",
		"LC_ALL=C",
		"GIT_DIR=" + hist,
		"GIT_WORK_TREE=" + root,
		"git", "--no-pager",
		"-c", "core.hooksPath=/dev/null",
		"-c", "commit.gpgSign=false",
		"-c", "core.fileMode=true",
		"-c", "core.fsmonitor=false",
		"-c", "core.quotePath=false",
		"-c", "core.autocrlf=false",
		"-c", "core.safecrlf=false",
		"-c", "core.symlinks=true",
		"-c", "gc.autoDetach=false",
		"-c", "protocol.allow=never",
		"-c", "user.name=aslcp",
		"-c", "user.email=aslcp@localhost",
	}
	return append(argv, args...)
}

// repo is a prepared history repository.
type repo struct {
	o          *Ops
	hist, root string
}

func (r *repo) run(ctx context.Context, args ...string) (string, int, error) {
	return r.o.Run.Run(ctx, r.o.gitArgv(r.hist, r.root, args...)...)
}

func (r *repo) git(ctx context.Context, args ...string) (string, error) {
	out, code, err := r.run(ctx, args...)
	if err != nil {
		return "", fmt.Errorf("git: %w", err)
	}
	if code != 0 {
		return "", fmt.Errorf("git %s failed: %s", args[0], LastLines(out, 3))
	}
	return out, nil
}

func (r *repo) head(ctx context.Context) (string, error) {
	out, code, err := r.run(ctx, "rev-parse", "--verify", "--quiet", "HEAD")
	if err != nil {
		return "", fmt.Errorf("git: %w", err)
	}
	if code != 0 {
		return "", nil
	}
	return strings.TrimSpace(out), nil
}

func authorArg(author string) string { return "--author=" + author + " <" + author + "@localhost>" }

func (r *repo) commit(ctx context.Context, msg, author string, allowEmpty bool) error {
	args := []string{"commit", "--quiet", "--no-verify", "--no-gpg-sign", "--cleanup=whitespace", authorArg(author)}
	if allowEmpty {
		args = append(args, "--allow-empty")
	}
	_, err := r.git(ctx, append(args, "-m", msg)...)
	return err
}

// commitIfChanged stages the whole tree and commits when it differs from HEAD.
func (r *repo) commitIfChanged(ctx context.Context, msg, author string) error {
	if _, err := r.git(ctx, "add", "-A"); err != nil {
		return err
	}
	_, code, err := r.run(ctx, "diff", "--cached", "--quiet")
	if err != nil {
		return fmt.Errorf("git: %w", err)
	}
	if code == 0 {
		return nil
	}
	return r.commit(ctx, msg, author, false)
}

// checkAncestors requires every directory from dir up to / to be owned by
// root or the helper and not writable by group or others.
func checkAncestors(dir string) error {
	for p := dir; ; p = filepath.Dir(p) {
		fi, err := os.Stat(p)
		if err != nil {
			return err
		}
		if uid, _, ok := ownerIDs(fi); ok && uid != 0 && uid != os.Geteuid() {
			return fmt.Errorf("%s is not owned by root", p)
		}
		if fi.Mode().Perm()&0o022 != 0 {
			return fmt.Errorf("%s is writable by others", p)
		}
		if p == filepath.Dir(p) {
			return nil
		}
	}
}

// prepareRepo makes sure the history repository exists with a safe directory
// and an initial snapshot, and returns it.
func (o *Ops) prepareRepo(ctx context.Context) (*repo, error) {
	dir := o.historyDir()
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return nil, fmt.Errorf("history directory: %w", err)
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(dir))
	if err != nil {
		return nil, fmt.Errorf("history directory: %w", err)
	}
	if o.StrictHistoryParents {
		if err := checkAncestors(parent); err != nil {
			return nil, fmt.Errorf("history directory refused: %w", err)
		}
	}
	hist := filepath.Join(parent, filepath.Base(dir))
	if _, err := os.Lstat(hist); errors.Is(err, fs.ErrNotExist) {
		if err := os.Mkdir(hist, 0o700); err != nil {
			return nil, fmt.Errorf("history directory: %w", err)
		}
	}
	fi, err := os.Lstat(hist)
	if err != nil || !fi.IsDir() {
		return nil, errors.New("history directory refused: not a directory")
	}
	if uid, _, ok := ownerIDs(fi); ok && uid != os.Geteuid() {
		return nil, errors.New("history directory refused: not owned by the helper")
	}
	if fi.Mode().Perm() != 0o700 {
		if err := os.Chmod(hist, 0o700); err != nil {
			return nil, fmt.Errorf("history directory: %w", err)
		}
	}
	root, err := filepath.EvalSymlinks(o.configRoot())
	if err != nil {
		return nil, errors.New("config directory unavailable")
	}
	r := &repo{o: o, hist: hist, root: root}
	if _, err := os.Stat(filepath.Join(hist, "HEAD")); err != nil {
		if _, err := r.git(ctx, "init", "--quiet", "--initial-branch=main"); err != nil {
			return nil, err
		}
	}
	h, err := r.head(ctx)
	if err != nil {
		return nil, err
	}
	if h == "" {
		if _, err := r.git(ctx, "add", "-A"); err != nil {
			return nil, err
		}
		if err := r.commit(ctx, "Initial snapshot", "aslcp", true); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// ---------------------------------------------------------------- apply

func checkAuthor(a string) error {
	if !reAuthor.MatchString(a) {
		return errors.New("invalid author")
	}
	return nil
}

// ConfigApply runs the apply pipeline.
func (o *Ops) ConfigApply(ctx context.Context, req *ConfigRequest) (ConfigApplyResult, error) {
	if req == nil || len(req.Files) == 0 {
		return ConfigApplyResult{}, errors.New("no files")
	}
	if len(req.Files) > maxConfigFiles {
		return ConfigApplyResult{}, errors.New("too many files (limit 50)")
	}
	msg, err := sanitizeMessage(req.Message)
	if err != nil {
		return ConfigApplyResult{}, err
	}
	if err := checkAuthor(req.Author); err != nil {
		return ConfigApplyResult{}, err
	}
	o.applyMu.Lock()
	defer o.applyMu.Unlock()

	// 1. validate everything, then compare bases.
	seen := map[string]bool{}
	var ps []*pending
	var conflicts []string
	for _, f := range req.Files {
		if seen[f.Path] {
			return ConfigApplyResult{}, fmt.Errorf("%s: duplicate path", f.Path)
		}
		seen[f.Path] = true
		if f.Base != "" && !reSHA256.MatchString(f.Base) {
			return ConfigApplyResult{}, fmt.Errorf("%s: invalid base", f.Path)
		}
		if err := validContent(f.Content); err != nil {
			return ConfigApplyResult{}, fmt.Errorf("%s: %w", f.Path, err)
		}
		p, err := o.stage(f.Path, []byte(f.Content))
		if err != nil {
			return ConfigApplyResult{}, err
		}
		cur := ""
		if p.existed {
			cur = sha256hex(p.orig)
		}
		if cur != f.Base {
			conflicts = append(conflicts, f.Path)
		}
		ps = append(ps, p)
	}
	if len(conflicts) > 0 {
		return ConfigApplyResult{}, &ConflictError{Files: conflicts}
	}
	r, err := o.prepareRepo(ctx)
	if err != nil {
		return ConfigApplyResult{}, err
	}
	return o.applyPending(ctx, r, ps, msg, req.Author, nil)
}

// applyPending is steps 2-5 of the pipeline (snapshot, write, commit).
func (o *Ops) applyPending(ctx context.Context, r *repo, ps []*pending, msg, author string, skipped []string) (ConfigApplyResult, error) {
	if err := r.commitIfChanged(ctx, "Before: "+msg, author); err != nil {
		return ConfigApplyResult{}, err
	}
	before, err := r.head(ctx)
	if err != nil {
		return ConfigApplyResult{}, err
	}
	var todo []*pending
	for _, p := range ps {
		// A file edited outside since step 1 shows up here (the snapshot
		// captured it): compare with what we staged, not with the base.
		if p.existed && bytes.Equal(p.orig, p.content) {
			continue
		}
		todo = append(todo, p)
	}
	res := ConfigApplyResult{Commit: before, Before: before, Files: []string{}, Skipped: skipped}
	if len(todo) == 0 {
		return res, nil
	}
	if err := o.writeAll(todo); err != nil {
		return ConfigApplyResult{}, err
	}
	if err := r.commitIfChanged(ctx, msg, author); err != nil {
		// Keep config and history consistent: put the files back.
		for _, p := range todo {
			p.content, p.orig = p.orig, p.content
		}
		var back []*pending
		for _, p := range todo {
			if p.existed {
				back = append(back, p)
			} else {
				_ = os.Remove(p.t.abs)
			}
		}
		if rerr := o.writeAllNoCheck(back); rerr != nil {
			return ConfigApplyResult{}, fmt.Errorf("%w (and restoring the files failed: %v)", err, rerr)
		}
		return ConfigApplyResult{}, err
	}
	after, err := r.head(ctx)
	if err != nil {
		return ConfigApplyResult{}, err
	}
	res.Commit = after
	for _, p := range todo {
		res.Files = append(res.Files, p.t.rel)
	}
	return res, nil
}

// writeAllNoCheck writes without the concurrent-edit re-check (used to undo).
func (o *Ops) writeAllNoCheck(ps []*pending) error {
	w := o.writer()
	for _, p := range ps {
		if err := w(p.t.abs, p.t.parent, p.content, p.uid, p.gid, p.mode); err != nil {
			return err
		}
	}
	return nil
}

// recordable are the only files config-record accepts: the radio interface
// configurations that Asterisk rewrites itself when tuning is saved.
var recordable = map[string]bool{"simpleusb.conf": true, "usbradio.conf": true}

// ConfigRecord commits the current on-disk state of one recordable file to
// the history. The helper reads the file itself; nothing but the path, a
// message and an author comes from the caller. When nothing differs from
// the last commit no commit is made and Commit is the current HEAD.
func (o *Ops) ConfigRecord(ctx context.Context, req *ConfigRequest) (ConfigApplyResult, error) {
	if req == nil || !recordable[req.Path] {
		return ConfigApplyResult{}, errors.New("only simpleusb.conf and usbradio.conf can be recorded")
	}
	msg, err := sanitizeMessage(req.Message)
	if err != nil {
		return ConfigApplyResult{}, err
	}
	if err := checkAuthor(req.Author); err != nil {
		return ConfigApplyResult{}, err
	}
	o.applyMu.Lock()
	defer o.applyMu.Unlock()
	if _, err := o.resolve(req.Path, false); err != nil {
		return ConfigApplyResult{}, fmt.Errorf("%s: %w", req.Path, err)
	}
	r, err := o.prepareRepo(ctx)
	if err != nil {
		return ConfigApplyResult{}, err
	}
	before, err := r.head(ctx)
	if err != nil {
		return ConfigApplyResult{}, err
	}
	if err := r.commitIfChanged(ctx, msg, req.Author); err != nil {
		return ConfigApplyResult{}, err
	}
	after, err := r.head(ctx)
	if err != nil {
		return ConfigApplyResult{}, err
	}
	res := ConfigApplyResult{Commit: after, Before: before, Files: []string{}}
	if after != before {
		res.Files = append(res.Files, req.Path)
	}
	return res, nil
}

// ---------------------------------------------------------------- log / show

func (o *Ops) resolveCommit(ctx context.Context, r *repo, c string) (string, error) {
	if !reCommit.MatchString(c) {
		return "", errors.New("invalid commit")
	}
	out, code, err := r.run(ctx, "rev-parse", "--verify", "--quiet", c+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("git: %w", err)
	}
	full := strings.TrimSpace(out)
	if code != 0 || len(full) != 40 {
		return "", errors.New("unknown commit")
	}
	return full, nil
}

// ConfigLog lists commits, newest first.
func (o *Ops) ConfigLog(ctx context.Context, req *ConfigRequest) ([]ConfigCommit, error) {
	limit, offset := 50, 0
	if req != nil {
		if req.Limit != 0 {
			limit = req.Limit
		}
		offset = req.Offset
	}
	if limit < 1 || limit > 200 || offset < 0 || offset > 1_000_000 {
		return nil, errors.New("invalid limit or offset")
	}
	o.applyMu.Lock()
	defer o.applyMu.Unlock()
	r, err := o.prepareRepo(ctx)
	if err != nil {
		return nil, err
	}
	out, err := r.git(ctx, "log", "--no-color", "--format=%x1e%H%x1f%aI%x1f%an%x1f%s", "--name-only",
		"-n", strconv.Itoa(limit), "--skip="+strconv.Itoa(offset))
	if err != nil {
		return nil, err
	}
	commits := []ConfigCommit{}
	for _, rec := range strings.Split(out, "\x1e") {
		if strings.TrimSpace(rec) == "" {
			continue
		}
		lines := strings.Split(rec, "\n")
		h := strings.SplitN(lines[0], "\x1f", 4)
		if len(h) != 4 {
			continue
		}
		c := ConfigCommit{Commit: h[0], Date: h[1], Author: h[2], Message: h[3], Files: []string{}}
		for _, l := range lines[1:] {
			if l != "" && len(c.Files) < maxListEntries {
				c.Files = append(c.Files, l)
			}
		}
		commits = append(commits, c)
	}
	return commits, nil
}

// ConfigShow returns one commit with raw unified diffs against its parent.
func (o *Ops) ConfigShow(ctx context.Context, req *ConfigRequest) (ConfigShowResult, error) {
	if req == nil {
		return ConfigShowResult{}, errors.New("invalid request")
	}
	o.applyMu.Lock()
	defer o.applyMu.Unlock()
	r, err := o.prepareRepo(ctx)
	if err != nil {
		return ConfigShowResult{}, err
	}
	full, err := o.resolveCommit(ctx, r, req.Commit)
	if err != nil {
		return ConfigShowResult{}, err
	}
	meta, err := r.git(ctx, "show", "-s", "--no-color", "--format=%H%x1f%aI%x1f%an%x1f%s", full)
	if err != nil {
		return ConfigShowResult{}, err
	}
	h := strings.SplitN(strings.TrimRight(meta, "\n"), "\x1f", 4)
	if len(h) != 4 {
		return ConfigShowResult{}, errors.New("unexpected git output")
	}
	res := ConfigShowResult{Commit: h[0], Date: h[1], Author: h[2], Message: h[3], Files: []ConfigFileDiff{}}
	names, err := r.git(ctx, "diff-tree", "--root", "--no-commit-id", "--no-renames", "-r", "-z", "--name-status", full)
	if err != nil {
		return ConfigShowResult{}, err
	}
	tok := strings.Split(strings.TrimRight(names, "\x00"), "\x00")
	for i := 0; i+1 < len(tok) && len(res.Files) < maxListEntries; i += 2 {
		st := "modified"
		switch tok[i] {
		case "A":
			st = "added"
		case "D":
			st = "deleted"
		}
		res.Files = append(res.Files, ConfigFileDiff{Path: tok[i+1], Status: st})
	}
	if len(res.Files) == 0 {
		return res, nil
	}
	patch, err := r.git(ctx, "diff-tree", "--root", "--no-commit-id", "--no-renames", "-r", "-p", "-U3",
		"--no-ext-diff", "--no-textconv", "--no-color", full)
	if err != nil {
		return ConfigShowResult{}, err
	}
	chunks := splitPatch(patch)
	if len(chunks) == len(res.Files) {
		total := 0
		for i := range res.Files {
			d := chunks[i]
			if len(d) > maxShowDiff {
				d = d[:maxShowDiff] + "\n(diff truncated)\n"
			}
			if total+len(d) > maxShowTotal {
				d = "(diff omitted: output too large)\n"
			}
			total += len(d)
			res.Files[i].Diff = d
		}
	}
	return res, nil
}

// splitPatch splits git's patch output per file and drops the extended
// headers (everything before the "---" line).
func splitPatch(patch string) []string {
	var out []string
	for _, part := range strings.Split("\n"+patch, "\ndiff --git ")[1:] {
		lines := strings.SplitAfter(part, "\n")
		start := len(lines)
		for i, l := range lines[1:] {
			if strings.HasPrefix(l, "--- ") || strings.HasPrefix(l, "Binary files ") {
				start = i + 1
				break
			}
		}
		out = append(out, strings.Join(lines[start:], ""))
	}
	return out
}

// ---------------------------------------------------------------- restore

func gitBlobSum(b []byte) string {
	h := sha1.New() //nolint:gosec
	fmt.Fprintf(h, "blob %d\x00", len(b))
	h.Write(b)
	return hex.EncodeToString(h.Sum(nil))
}

// ConfigRestore writes the files of a commit back (only those that differ).
func (o *Ops) ConfigRestore(ctx context.Context, req *ConfigRequest) (ConfigApplyResult, error) {
	if req == nil {
		return ConfigApplyResult{}, errors.New("invalid request")
	}
	if err := checkAuthor(req.Author); err != nil {
		return ConfigApplyResult{}, err
	}
	o.applyMu.Lock()
	defer o.applyMu.Unlock()
	r, err := o.prepareRepo(ctx)
	if err != nil {
		return ConfigApplyResult{}, err
	}
	full, err := o.resolveCommit(ctx, r, req.Commit)
	if err != nil {
		return ConfigApplyResult{}, err
	}
	subj, err := r.git(ctx, "show", "-s", "--no-color", "--format=%s", full)
	if err != nil {
		return ConfigApplyResult{}, err
	}
	head := "Restore " + full[:7] + ": "
	orig := cleanSubject(subj, maxMessageRunes-len(head))
	msg := strings.TrimSpace(head + orig)

	// Snapshot first (captures outside edits), then compare with the disk.
	if err := r.commitIfChanged(ctx, "Before: "+msg, req.Author); err != nil {
		return ConfigApplyResult{}, err
	}
	tree, err := r.git(ctx, "ls-tree", "-r", "-z", "--full-tree", full)
	if err != nil {
		return ConfigApplyResult{}, err
	}
	inCommit := map[string]bool{}
	var ps []*pending
	skipped := []string{}
	for _, ent := range strings.Split(tree, "\x00") {
		meta, rel, ok := strings.Cut(ent, "\t")
		if !ok {
			continue
		}
		f := strings.Fields(meta)
		if len(f) != 3 || f[1] != "blob" || (f[0] != "100644" && f[0] != "100755") {
			continue // symlinks, submodules: never restored
		}
		if !strings.HasSuffix(rel, ".conf") {
			continue // outside the editable set
		}
		inCommit[rel] = true
		if validateRel(rel) != nil {
			skipped = append(skipped, rel)
			continue
		}
		t, err := o.resolve(rel, true)
		if err != nil {
			skipped = append(skipped, rel)
			continue
		}
		if t.info != nil {
			cur, _, err := readFile(t.abs)
			if err == nil && gitBlobSum(cur) == f[2] {
				continue // already identical
			}
		}
		blob, err := r.git(ctx, "cat-file", "blob", f[2])
		if err != nil || validContent(blob) != nil {
			skipped = append(skipped, rel)
			continue
		}
		p, err := o.stage(rel, []byte(blob))
		if err != nil {
			skipped = append(skipped, rel)
			continue
		}
		ps = append(ps, p)
		if len(ps) > maxConfigFiles {
			return ConfigApplyResult{}, errors.New("too many files differ (limit 50)")
		}
	}
	if cur, err := o.ConfigList(ctx); err == nil {
		for _, ci := range cur {
			if !inCommit[ci.Path] {
				skipped = append(skipped, ci.Path)
			}
		}
	}
	sort.Strings(skipped)
	if len(ps) == 0 {
		return ConfigApplyResult{}, errors.New("nothing to restore: the files already match that version")
	}
	return o.applyPending(ctx, r, ps, msg, req.Author, skipped)
}

// ---------------------------------------------------------------- restart

// AsteriskRestart restarts the asterisk service.
func (o *Ops) AsteriskRestart(ctx context.Context) (OKResult, error) {
	o.applyMu.Lock()
	defer o.applyMu.Unlock()
	out, code, err := o.Run.Run(ctx, "systemctl", "restart", "asterisk")
	if err != nil {
		return OKResult{}, fmt.Errorf("systemctl: %w", err)
	}
	if code != 0 {
		return OKResult{}, fmt.Errorf("systemctl restart asterisk failed: %s", LastLines(out, 3))
	}
	return OKResult{OK: true}, nil
}

// handleConfig dispatches the config-* operations.
func (o *Ops) handleConfig(ctx context.Context, req Request) (any, error) {
	c := ConfigRequest{}
	if req.Config != nil {
		c = *req.Config
	}
	switch req.Op {
	case OpConfigList:
		if err := strayFields(c, ""); err != nil {
			return nil, err
		}
		return o.ConfigList(ctx)
	case OpConfigRead:
		if err := strayFields(c, "p"); err != nil {
			return nil, err
		}
		return o.ConfigRead(c.Path)
	case OpConfigApply:
		if err := strayFields(c, "fma"); err != nil {
			return nil, err
		}
		return o.ConfigApply(ctx, &c)
	case OpConfigLog:
		if err := strayFields(c, "lo"); err != nil {
			return nil, err
		}
		return o.ConfigLog(ctx, &c)
	case OpConfigShow:
		if err := strayFields(c, "c"); err != nil {
			return nil, err
		}
		return o.ConfigShow(ctx, &c)
	case OpConfigRestore:
		if err := strayFields(c, "ca"); err != nil {
			return nil, err
		}
		return o.ConfigRestore(ctx, &c)
	case OpConfigRecord:
		if err := strayFields(c, "pma"); err != nil {
			return nil, err
		}
		return o.ConfigRecord(ctx, &c)
	}
	return nil, errors.New("unknown op")
}

// ValidatePath applies the syntactic path rules (for the server's early
// checks; the helper itself always re-validates).
func ValidatePath(rel string) error { return validateRel(rel) }

// ValidateContent applies the content rules (size, UTF-8, no NUL).
func ValidateContent(s string) error { return validContent(s) }

// SanitizeMessage applies the commit message rules.
func SanitizeMessage(s string) (string, error) { return sanitizeMessage(s) }

// Limits shared with the server.
const (
	MaxConfigFiles = maxConfigFiles
	MaxConfigFile  = maxConfigFile
	// ErrNoSuchFile is the helper's message for a missing file.
	ErrNoSuchFile = "no such file"
)
