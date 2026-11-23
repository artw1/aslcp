package configsvc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/artw1/aslcp/internal/helper"
)

// DemoHelper is an in-memory /etc/asterisk with a few anonymized files and a
// fake history, for --demo. It enforces the same rules as the real helper
// where they matter to the UI: base conflicts, custom/-only new files.
type DemoHelper struct {
	mu      sync.Mutex
	files   map[string]string
	mtime   map[string]time.Time
	commits []demoCommit // oldest first
	seq     int
}

type demoCommit struct {
	id, author, message string
	date                time.Time
	snap                map[string]string
	changed             []string
}

const demoRpt = `; Demo node configuration (anonymized)
[general]
node_lookup_method = both

[node-main](!)
rxchannel = Local/pseudo
duplex = 1
linktolink = no
hangtime = 2000
althangtime = 4000
totime = 180000
telemdefault = 1
idrecording = |iNOTSET
context = radio
controlstates = controlstates

[2006](node-main)
rxchannel = SimpleUSB/usb
callsign = N0CALL
idrecording = |iN0CALL
`

const demoEchoLink = `[el0]
call = INVALID
pwd = INVALID
name = YOUR NAME
qth = INVALID
email = INVALID
astnode = 2006
maxstns = 20
`

const demoManager = `[general]
enabled = yes
port = 5038
bindaddr = 127.0.0.1

[admin]
secret = demo-not-a-real-secret
read = all
write = all
`

const demoRegs = `[general]
register => 2006:demo-not-a-real-password@register.example.invalid
`

// NewDemoHelper returns a demo helper with two commits of history.
func NewDemoHelper() *DemoHelper {
	d := &DemoHelper{files: map[string]string{
		"rpt.conf":                    demoRpt,
		"simpleusb.conf":              "[usb]\nrxmixerset = 650\ntxmixaset = 500\ntxmixbset = 0\nrxboost = no\npreemphasis = yes\ndeemphasis = yes\nplfilter = yes\ninvertptt = no\ncarrierfrom = usbinvert\nctcssfrom = no\nrxondelay = 0\ntxoffdelay = 0\n",
		"modules.conf":                "[modules]\nautoload = no\nload = chan_simpleusb.so\nnoload = chan_usrp.so\nnoload = chan_echolink.so\n",
		"echolink.conf":               demoEchoLink,
		"iax.conf":                    "[general]\nbindport = 4569\ndisallow = all\nallow = ulaw\n",
		"manager.conf":                demoManager,
		"extensions.conf":             "[radio]\nexten => _X.,1,Rpt(${EXTEN})\n",
		"rpt_http_registrations.conf": demoRegs,
		"custom/rpt/macros.conf":      "[macro-demo]\n1 = *81\n",
	}, mtime: map[string]time.Time{}}
	t0 := time.Now().Add(-72 * time.Hour)
	for p := range d.files {
		d.mtime[p] = t0
	}
	d.files["rpt.conf"] = strings.Replace(demoRpt, "duplex = 1", "duplex = 0", 1)
	d.commit("Before: initial snapshot", "aslcp", t0, nil)
	d.files["rpt.conf"] = demoRpt
	d.commit("Tune duplex", "admin", t0.Add(24*time.Hour), []string{"rpt.conf"})
	return d
}

func (d *DemoHelper) commit(msg, author string, at time.Time, changed []string) string {
	d.seq++
	id := fmt.Sprintf("%040x", sha256.Sum256([]byte(fmt.Sprint(d.seq, msg, at.UnixNano()))))[:40]
	snap := make(map[string]string, len(d.files))
	for k, v := range d.files {
		snap[k] = v
	}
	d.commits = append(d.commits, demoCommit{id, author, msg, at, snap, changed})
	return id
}

func sum(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }

// ConfigList implements Helper.
func (d *DemoHelper) ConfigList(context.Context) ([]helper.ConfigInfo, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := []helper.ConfigInfo{}
	for p, c := range d.files {
		out = append(out, helper.ConfigInfo{Path: p, Size: int64(len(c)), MTime: d.mtime[p].Unix(), SHA256: sum(c),
			Mode: "0644", Owner: "asterisk", Group: "asterisk"})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// ConfigRead implements Helper.
func (d *DemoHelper) ConfigRead(_ context.Context, path string) (helper.ConfigContent, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	c, ok := d.files[path]
	if !ok {
		return helper.ConfigContent{}, errors.New(helper.ErrNoSuchFile)
	}
	return helper.ConfigContent{Path: path, Content: c, SHA256: sum(c)}, nil
}

// ConfigApply implements Helper.
func (d *DemoHelper) ConfigApply(_ context.Context, files []helper.ConfigFile, message, author string) (helper.ConfigApplyResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var conflicts []string
	for _, f := range files {
		if err := helper.ValidatePath(f.Path); err != nil {
			return helper.ConfigApplyResult{}, errors.New("invalid path: " + f.Path)
		}
		cur, ok := d.files[f.Path]
		switch {
		case ok && sum(cur) != f.Base:
			conflicts = append(conflicts, f.Path)
		case !ok && !strings.HasPrefix(f.Path, "custom/"):
			return helper.ConfigApplyResult{}, errors.New("invalid path: new files are only allowed under custom/")
		case !ok && f.Base != "":
			conflicts = append(conflicts, f.Path)
		}
	}
	if len(conflicts) > 0 {
		return helper.ConfigApplyResult{}, &helper.ConflictError{Files: conflicts}
	}
	var written []string
	for _, f := range files {
		if cur, ok := d.files[f.Path]; !ok || cur != f.Content {
			written = append(written, f.Path)
		}
	}
	if len(written) == 0 {
		return helper.ConfigApplyResult{Files: []string{}}, nil
	}
	// Like the real helper: the state before the change is in the history
	// (a "Before" commit when it was not), and Before names it.
	before := ""
	if n := len(d.commits); n > 0 && !sameFiles(d.commits[n-1].snap, d.files) {
		before = d.commit("Before: "+message, author, time.Now(), nil)
	} else if n > 0 {
		before = d.commits[n-1].id
	}
	for _, f := range files {
		if cur, ok := d.files[f.Path]; ok && cur == f.Content {
			continue
		}
		d.files[f.Path] = f.Content
		d.mtime[f.Path] = time.Now()
	}
	id := d.commit(message, author, time.Now(), written)
	return helper.ConfigApplyResult{Commit: id, Before: before, Files: written}, nil
}

func (c demoCommit) info() helper.ConfigCommit {
	return helper.ConfigCommit{Commit: c.id, Date: c.date.UTC().Format(time.RFC3339), Author: c.author,
		Message: c.message, Files: append([]string{}, c.changed...)}
}

// ConfigLog implements Helper (newest first).
func (d *DemoHelper) ConfigLog(_ context.Context, limit, offset int) ([]helper.ConfigCommit, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := []helper.ConfigCommit{}
	for i := len(d.commits) - 1 - offset; i >= 0 && len(out) < limit; i-- {
		out = append(out, d.commits[i].info())
	}
	return out, nil
}

func (d *DemoHelper) find(id string) int {
	if len(id) < 7 {
		return -1
	}
	for i, c := range d.commits {
		if strings.HasPrefix(c.id, id) {
			return i
		}
	}
	return -1
}

// ConfigShow implements Helper.
func (d *DemoHelper) ConfigShow(_ context.Context, commit string) (helper.ConfigShowResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	i := d.find(commit)
	if i < 0 {
		return helper.ConfigShowResult{}, errors.New("unknown commit")
	}
	c := d.commits[i]
	var prev map[string]string
	if i > 0 {
		prev = d.commits[i-1].snap
	}
	res := helper.ConfigShowResult{Commit: c.id, Date: c.date.UTC().Format(time.RFC3339), Author: c.author,
		Message: c.message, Files: []helper.ConfigFileDiff{}}
	paths := []string{}
	for p := range c.snap {
		paths = append(paths, p)
	}
	for p := range prev {
		if _, ok := c.snap[p]; !ok {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	for _, p := range paths {
		o, hadO := prev[p]
		n, hasN := c.snap[p]
		if hadO && hasN && o == n || i == 0 && !hasN {
			continue
		}
		st := "modified"
		switch {
		case !hadO:
			st = "added"
		case !hasN:
			st = "deleted"
		}
		res.Files = append(res.Files, helper.ConfigFileDiff{Path: p, Status: st, Diff: UnifiedDiff(p, o, n, !hadO)})
	}
	return res, nil
}

// ConfigRestore implements Helper.
func (d *DemoHelper) ConfigRestore(_ context.Context, commit, author string) (helper.ConfigApplyResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	i := d.find(commit)
	if i < 0 {
		return helper.ConfigApplyResult{}, errors.New("unknown commit")
	}
	snap := d.commits[i].snap
	var written, skipped []string
	for p, c := range snap {
		if d.files[p] != c {
			written = append(written, p)
		}
	}
	for p := range d.files {
		if _, ok := snap[p]; !ok {
			skipped = append(skipped, p)
		}
	}
	if len(written) == 0 {
		return helper.ConfigApplyResult{}, errors.New("nothing to restore")
	}
	sort.Strings(written)
	sort.Strings(skipped)
	before := d.commit("Before: restore "+d.commits[i].id[:7], author, time.Now(), nil)
	for _, p := range written {
		d.files[p] = snap[p]
		d.mtime[p] = time.Now()
	}
	id := d.commit("Restore "+d.commits[i].id[:7], author, time.Now(), written)
	return helper.ConfigApplyResult{Commit: id, Before: before, Files: written, Skipped: skipped}, nil
}

// AsteriskRestart implements Helper (nothing to restart in demo mode).
func (d *DemoHelper) AsteriskRestart(context.Context) error { return nil }

// SetFile replaces a file's content without a history entry (the simulated
// radio driver rewriting simpleusb.conf).
func (d *DemoHelper) SetFile(path, content string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.files[path] = content
	d.mtime[path] = time.Now()
}

// ConfigRecord commits the current state of a radio interface file to the
// history (see helper.Ops.ConfigRecord).
func (d *DemoHelper) ConfigRecord(_ context.Context, path, message, author string) (helper.ConfigApplyResult, error) {
	if path != "simpleusb.conf" && path != "usbradio.conf" {
		return helper.ConfigApplyResult{}, errors.New("only simpleusb.conf and usbradio.conf can be recorded")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.commits) > 0 && d.commits[len(d.commits)-1].snap[path] == d.files[path] {
		return helper.ConfigApplyResult{Commit: d.commits[len(d.commits)-1].id, Files: []string{}}, nil
	}
	id := d.commit(message, author, time.Now(), []string{path})
	return helper.ConfigApplyResult{Commit: id, Files: []string{path}}, nil
}

func sameFiles(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}
