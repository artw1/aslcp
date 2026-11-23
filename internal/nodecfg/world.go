// Package nodecfg is the node model of the configuration forms and wizards
// (docs/CONFIG.md, "Forms"): it reads the Asterisk files back every time,
// resolves each node's settings with their provenance, runs the cross-field
// checks, and plans a form change as edits of the lossless astconf trees.
// It does no I/O of its own beyond the astconf.FS it is given, so everything
// here runs on the asl3-dist corpus in tests.
package nodecfg

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/artw1/aslcp/internal/astconf"
)

// Files the model reads. rpt.conf is required; the others may be missing.
const (
	FileRpt      = "rpt.conf"
	FileModules  = "modules.conf"
	FileSimple   = "simpleusb.conf"
	FileUSBRadio = "usbradio.conf"
	FileVoter    = "voter.conf"
	FileEcho     = "echolink.conf"
	FileHTTPReg  = "rpt_http_registrations.conf"
	FileIAX      = "iax.conf"
	FileManager  = "manager.conf"
	FileExt      = "extensions.conf"
)

var modelFiles = []string{FileRpt, FileModules, FileSimple, FileUSBRadio, FileVoter, FileEcho, FileHTTPReg, FileIAX, FileManager, FileExt}

// World is the parsed state of the files the model uses.
type World struct {
	FS astconf.FS
	// Trees holds the include-expanded tree of each model file that exists.
	Trees map[string]*astconf.Tree
	// Errors holds the load error of a file that exists but cannot be used
	// (a syntax-level problem in an include, an include cycle, ...).
	Errors map[string]error
}

// cacheFS reads each file once per load.
type cacheFS struct {
	base astconf.FS
	mu   sync.Mutex
	data map[string][]byte
	errs map[string]error
}

func (c *cacheFS) ReadFile(p string) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if b, ok := c.data[p]; ok {
		return b, nil
	}
	if e, ok := c.errs[p]; ok {
		return nil, e
	}
	b, err := c.base.ReadFile(p)
	if err != nil {
		c.errs[p] = err
		return nil, err
	}
	c.data[p] = b
	return b, nil
}

func (c *cacheFS) Glob(pattern string) ([]string, error) { return c.base.Glob(pattern) }

// Load reads and parses the model files from fsys.
func Load(fsys astconf.FS) *World {
	w := &World{FS: fsys, Trees: map[string]*astconf.Tree{}, Errors: map[string]error{}}
	c := &cacheFS{base: fsys, data: map[string][]byte{}, errs: map[string]error{}}
	for _, name := range modelFiles {
		t, err := astconf.LoadTree(c, "", name)
		switch {
		case err == nil:
			w.Trees[name] = t
		case errors.Is(err, fs.ErrNotExist) && !strings.Contains(err.Error(), "#"):
			// the file itself is missing
		default:
			w.Errors[name] = err
		}
	}
	return w
}

// overlayFS serves edited contents over a base file system.
type overlayFS struct {
	base astconf.FS
	over map[string]string
}

func (o overlayFS) ReadFile(p string) ([]byte, error) {
	if s, ok := o.over[path.Clean(p)]; ok {
		return []byte(s), nil
	}
	return o.base.ReadFile(p)
}

func (o overlayFS) Glob(pattern string) ([]string, error) {
	out, err := o.base.Glob(pattern)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, p := range out {
		seen[p] = true
	}
	for p := range o.over {
		if ok, _ := path.Match(pattern, p); ok && !seen[p] {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out, nil
}

// With returns the world as it would be after the given file contents are
// written.
func (w *World) With(changed map[string]string) *World {
	return Load(overlayFS{base: w.FS, over: changed})
}

// File returns the parsed file with the logical name, from any tree.
func (w *World) File(name string) *astconf.File {
	for _, f := range modelFiles {
		if t := w.Trees[f]; t != nil {
			if pf := t.Files[name]; pf != nil {
				return pf
			}
		}
	}
	return nil
}

// Content returns the current text of a file the model has read.
func (w *World) Content(name string) (string, bool) {
	if f := w.File(name); f != nil {
		return string(f.Bytes()), true
	}
	return "", false
}

// SHA returns the sha256 of a file's current text, the base of a write.
func (w *World) SHA(name string) string {
	c, ok := w.Content(name)
	if !ok {
		return ""
	}
	h := sha256.Sum256([]byte(c))
	return hex.EncodeToString(h[:])
}

// Has reports whether the model file exists and loaded.
func (w *World) Has(name string) bool { return w.Trees[name] != nil }
