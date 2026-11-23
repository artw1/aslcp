package helper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/artw1/aslcp/internal/astconf"
)

// Node setup wizard ops. Two fixed operations for the two files of an
// AllStarLink node install that are not Asterisk .conf files served by
// config-apply:
//
//	savenode-set      /etc/asterisk/savenode.conf (NODE, PASSWORD, ENABLE only)
//	allmon3-node-set  /etc/allmon3/allmon3.ini    (node section name, pass)
//
// Both read and write the file themselves; the request carries typed,
// strictly validated values (never a path or a command). The AMI secret of
// manager.conf is not an op here: it is an ordinary config-apply edit.

const (
	OpSavenodeSet   = "savenode-set"
	OpAllmonNodeSet = "allmon3-node-set"

	savenodeFile = "savenode.conf"
	allmonINI    = "allmon3.ini"
)

// Skip reasons of the results: the file is simply not part of this install.
const SetupSkipNoFile = "no-file"

var (
	reSetupNode = regexp.MustCompile(`^[1-9][0-9]{0,6}$`)
	// reSetupSecret is the set of characters that are safe unquoted in a
	// shell-sourced file (savenode.conf) and in an ini value. A leading "~"
	// is not allowed (the shell would expand it).
	reSetupSecret = regexp.MustCompile(`^[A-Za-z0-9._+=,:@%-]{1,64}$`)
)

// NodeSetupRequest is the argument of the node setup ops. Each op reads only
// the fields it documents.
type NodeSetupRequest struct {
	Node     string `json:"node,omitempty"`     // both: the (new) node number
	OldNode  string `json:"oldNode,omitempty"`  // allmon3-node-set: section to rename
	Password string `json:"password,omitempty"` // savenode-set
	Enable   *bool  `json:"enable,omitempty"`   // savenode-set
	AMIPass  string `json:"amiPass,omitempty"`  // allmon3-node-set (optional)
	Author   string `json:"author,omitempty"`   // savenode-set: history author
}

// NodeSetupResult reports what an op did.
type NodeSetupResult struct {
	Changed   bool   `json:"changed"`
	Skipped   string `json:"skipped,omitempty"` // SetupSkipNoFile
	Recorded  bool   `json:"recorded,omitempty"`
	Commit    string `json:"commit,omitempty"`
	Restarted bool   `json:"restarted,omitempty"`
}

func init() {
	registerSysOp(OpSavenodeSet, func(o *Ops, ctx context.Context, r *SysRequest) (any, error) {
		return o.SavenodeSet(ctx, r.NodeSetup)
	})
	registerSysOp(OpAllmonNodeSet, func(o *Ops, ctx context.Context, r *SysRequest) (any, error) {
		return o.AllmonNodeSet(ctx, r.NodeSetup)
	})
}

// ValidSetupNode reports whether s is a node number the ops accept (1 to 7
// digits, no leading zero).
func ValidSetupNode(s string) bool { return reSetupNode.MatchString(s) }

// ValidSetupSecret reports whether s is a password the ops accept.
func ValidSetupSecret(s string) bool { return reSetupSecret.MatchString(s) }

// setupTarget resolves a fixed regular file inside dir (symlinks in the
// directory path are resolved, a symlink as the file itself is refused).
// A missing file gives (nil, "", nil).
func setupTarget(dir, name string) (data []byte, parent string, err error) {
	parent, err = filepath.EvalSymlinks(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, "", nil
		}
		return nil, "", errors.New("directory unavailable")
	}
	abs := filepath.Join(parent, name)
	fi, err := os.Lstat(abs)
	if errors.Is(err, os.ErrNotExist) {
		return nil, "", nil
	}
	if err != nil || !fi.Mode().IsRegular() {
		return nil, "", fmt.Errorf("%s is not a regular file", name)
	}
	b, _, err := readFile(abs)
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", name, err)
	}
	if err := validContent(string(b)); err != nil {
		return nil, "", fmt.Errorf("%s: %w", name, err)
	}
	return b, parent, nil
}

// replaceSetupFile atomically writes data over an existing file, keeping its
// owner and mode.
func (o *Ops) replaceSetupFile(parent, name string, data []byte) error {
	abs := filepath.Join(parent, name)
	fi, err := os.Lstat(abs)
	if err != nil {
		return err
	}
	uid, gid, _ := ownerIDs(fi)
	return o.writer()(abs, parent, data, uid, gid, fi.Mode().Perm())
}

// setKeyLines sets KEY=value on every line that assigns KEY (the last wins in
// a shell), appends missing keys and keeps every other line byte for byte.
func setKeyLines(data []byte, vals [][2]string) []byte {
	text := string(data)
	lines := strings.SplitAfter(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for _, kv := range vals {
		found := false
		for i, l := range lines {
			body := strings.TrimRight(l, "\r\n")
			t := strings.TrimLeft(body, " \t")
			if !strings.HasPrefix(t, kv[0]+"=") {
				continue
			}
			found = true
			eol := l[len(body):]
			lines[i] = kv[0] + "=" + kv[1] + eol
		}
		if !found {
			if n := len(lines); n > 0 && !strings.HasSuffix(lines[n-1], "\n") {
				lines[n-1] += "\n"
			}
			lines = append(lines, kv[0]+"="+kv[1]+"\n")
		}
	}
	return []byte(strings.Join(lines, ""))
}

// SavenodeSet writes NODE, PASSWORD and ENABLE into savenode.conf and keeps
// every other line. A missing file is not created (Skipped "no-file"). When
// the history repository is available the change is recorded in it.
func (o *Ops) SavenodeSet(ctx context.Context, req *NodeSetupRequest) (NodeSetupResult, error) {
	if req == nil || !ValidSetupNode(req.Node) || !ValidSetupSecret(req.Password) || req.Enable == nil {
		return NodeSetupResult{}, errors.New("invalid node, password or enable value")
	}
	if err := checkAuthor(req.Author); err != nil {
		return NodeSetupResult{}, err
	}
	o.applyMu.Lock()
	defer o.applyMu.Unlock()
	cur, parent, err := setupTarget(o.configRoot(), savenodeFile)
	if err != nil {
		return NodeSetupResult{}, err
	}
	if cur == nil {
		return NodeSetupResult{Skipped: SetupSkipNoFile}, nil
	}
	enable := "0"
	if *req.Enable {
		enable = "1"
	}
	next := setKeyLines(cur, [][2]string{{"NODE", req.Node}, {"PASSWORD", req.Password}, {"ENABLE", enable}})
	if bytes.Equal(next, cur) {
		return NodeSetupResult{}, nil
	}
	if err := o.replaceSetupFile(parent, savenodeFile, next); err != nil {
		return NodeSetupResult{}, fmt.Errorf("writing %s: %w", savenodeFile, err)
	}
	res := NodeSetupResult{Changed: true}
	// Recording is best effort: the file is already in place, and a missing
	// git or history directory must not turn that into a failure.
	if r, err := o.prepareRepo(ctx); err == nil {
		if err := r.commitIfChanged(ctx, "Node setup: "+savenodeFile, req.Author); err == nil {
			if h, err := r.head(ctx); err == nil {
				res.Recorded, res.Commit = true, h
			}
		}
	}
	return res, nil
}

// AllmonNodeSet renames the node section of allmon3.ini from OldNode to Node
// (when they differ) and, if AMIPass is given, sets its pass key. The edit
// is lossless (astconf). A missing file is skipped. After a change a running
// allmon3 is restarted (systemctl try-restart: nothing happens when it is
// stopped).
func (o *Ops) AllmonNodeSet(ctx context.Context, req *NodeSetupRequest) (NodeSetupResult, error) {
	if req == nil || !ValidSetupNode(req.Node) || !ValidSetupNode(req.OldNode) ||
		(req.AMIPass != "" && !ValidSetupSecret(req.AMIPass)) {
		return NodeSetupResult{}, errors.New("invalid node or password")
	}
	o.applyMu.Lock()
	defer o.applyMu.Unlock()
	cur, parent, err := setupTarget(o.allmonDir(), allmonINI)
	if err != nil {
		return NodeSetupResult{}, err
	}
	if cur == nil {
		return NodeSetupResult{Skipped: SetupSkipNoFile}, nil
	}
	f, _ := astconf.Parse(allmonINI, cur)
	olds, news := f.Section(req.OldNode), f.Section(req.Node)
	var sec *astconf.Section
	switch {
	case req.OldNode == req.Node && len(news) == 1:
		sec = news[0]
	case len(news) == 1 && len(olds) == 0:
		sec = news[0] // already renamed (a repeated apply)
	case len(olds) == 1 && len(news) == 0:
		sec = olds[0]
		sec.Rename(req.Node)
	default:
		return NodeSetupResult{}, fmt.Errorf("%s has no single [%s] section to update", allmonINI, req.OldNode)
	}
	if req.AMIPass != "" {
		sec.Set("pass", req.AMIPass)
	}
	next := f.Bytes()
	if bytes.Equal(next, cur) {
		return NodeSetupResult{}, nil
	}
	if err := o.replaceSetupFile(parent, allmonINI, next); err != nil {
		return NodeSetupResult{}, fmt.Errorf("writing %s: %w", allmonINI, err)
	}
	res := NodeSetupResult{Changed: true}
	rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, code, err := o.Run.Run(rctx, "systemctl", "try-restart", "allmon3.service"); err == nil && code == 0 {
		res.Restarted = true
	}
	return res, nil
}

// SavenodeSet calls savenode-set.
func (c *Client) SavenodeSet(ctx context.Context, req NodeSetupRequest) (NodeSetupResult, error) {
	var v NodeSetupResult
	return v, c.sys(ctx, OpSavenodeSet, &SysRequest{NodeSetup: &req}, &v, 1<<20)
}

// AllmonNodeSet calls allmon3-node-set.
func (c *Client) AllmonNodeSet(ctx context.Context, req NodeSetupRequest) (NodeSetupResult, error) {
	var v NodeSetupResult
	return v, c.sys(ctx, OpAllmonNodeSet, &SysRequest{NodeSetup: &req}, &v, 1<<20)
}

// SavenodeSet validates like the real op and changes nothing.
func (d *DemoSystem) SavenodeSet(_ context.Context, req NodeSetupRequest) (NodeSetupResult, error) {
	if !ValidSetupNode(req.Node) || !ValidSetupSecret(req.Password) || req.Enable == nil {
		return NodeSetupResult{}, errors.New("invalid node, password or enable value")
	}
	return NodeSetupResult{Changed: true}, nil
}

// AllmonNodeSet validates like the real op and changes nothing.
func (d *DemoSystem) AllmonNodeSet(_ context.Context, req NodeSetupRequest) (NodeSetupResult, error) {
	if !ValidSetupNode(req.Node) || !ValidSetupNode(req.OldNode) || (req.AMIPass != "" && !ValidSetupSecret(req.AMIPass)) {
		return NodeSetupResult{}, errors.New("invalid node or password")
	}
	return NodeSetupResult{Changed: true}, nil
}
