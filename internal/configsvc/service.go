// Package configsvc implements the server side of configuration editing
// (docs/CONFIG.md, beta.1): file listing, syntax check, diff preview, the
// reload plan, apply/restore through the root helper, targeted reloads, the
// health check and secret masking. The server never touches /etc/asterisk
// itself; everything goes through the Helper.
package configsvc

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/artw1/aslcp/internal/ami"
	"github.com/artw1/aslcp/internal/astconf"
	"github.com/artw1/aslcp/internal/helper"
	"github.com/artw1/aslcp/internal/rpt"
)

// Helper is the part of the helper client the service needs.
type Helper interface {
	ConfigList(ctx context.Context) ([]helper.ConfigInfo, error)
	ConfigRead(ctx context.Context, path string) (helper.ConfigContent, error)
	ConfigApply(ctx context.Context, files []helper.ConfigFile, message, author string) (helper.ConfigApplyResult, error)
	ConfigLog(ctx context.Context, limit, offset int) ([]helper.ConfigCommit, error)
	ConfigShow(ctx context.Context, commit string) (helper.ConfigShowResult, error)
	ConfigRestore(ctx context.Context, commit, author string) (helper.ConfigApplyResult, error)
	AsteriskRestart(ctx context.Context) error
}

// AMI is what reloads and the health check need.
type AMI interface {
	State() ami.State
	Command(ctx context.Context, cmd string) ([]string, error)
}

// Nodes is the app_rpt side of the health check.
type Nodes interface {
	LocalNodes(ctx context.Context) ([]string, error)
	XStat(ctx context.Context, node string) (*rpt.XStat, error)
}

// Service is the configuration editing service.
type Service struct {
	H     Helper
	AMI   AMI
	Nodes Nodes
	// HealthTimeout is how long the health check waits for Asterisk to come
	// back (spec: 15 s); HealthPoll the retry interval.
	HealthTimeout time.Duration
	HealthPoll    time.Duration
	// ReloadTimeout bounds each reload command.
	ReloadTimeout time.Duration
}

// New returns a Service.
func New(h Helper, a AMI, n Nodes) *Service {
	return &Service{H: h, AMI: a, Nodes: n, HealthTimeout: 15 * time.Second, HealthPoll: 500 * time.Millisecond, ReloadTimeout: 30 * time.Second}
}

// InvalidError is a rejected request (HTTP 400).
type InvalidError struct{ Msg string }

func (e *InvalidError) Error() string { return e.Msg }

// SyntaxIssue is a syntax error of one file.
type SyntaxIssue struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Message string `json:"message"`
}

// SyntaxErrors blocks an apply (HTTP 422).
type SyntaxErrors struct{ Issues []SyntaxIssue }

func (e *SyntaxErrors) Error() string {
	i := e.Issues[0]
	return fmt.Sprintf("syntax error in %s line %d: %s", i.Path, i.Line, i.Message)
}

// NotFoundError is a missing file or commit (HTTP 404).
type NotFoundError struct{ Msg string }

func (e *NotFoundError) Error() string { return e.Msg }

func classify(err error) error {
	if err == nil {
		return nil
	}
	m := err.Error()
	switch {
	case m == helper.ErrNoSuchFile:
		return &NotFoundError{"no such file"}
	case m == "unknown commit":
		return &NotFoundError{"unknown commit"}
	case strings.HasPrefix(m, "invalid "), strings.HasPrefix(m, "nothing to restore"):
		return &InvalidError{m}
	}
	return err
}

// File is an edited file of a preview or apply request.
type File struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Base    string `json:"base"`
}

// SyntaxError is the per-file error of a preview.
type SyntaxError struct {
	Line    int    `json:"line"`
	Message string `json:"message"`
}

// CheckSyntax parses content with astconf. The parser keeps anything it does
// not understand as an "unknown" line; the first such line is the error.
func CheckSyntax(path, content string) *SyntaxError {
	f, err := astconf.Parse(path, []byte(content))
	if err != nil {
		return &SyntaxError{Message: err.Error()}
	}
	for _, l := range f.Lines() {
		if l.Kind == astconf.KindUnknown {
			return &SyntaxError{Line: l.Num, Message: "not a section header, key = value, #include or comment"}
		}
	}
	return nil
}

// ---------------------------------------------------------------- files

// FileEntry is one entry of the file list.
type FileEntry struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	MTime  string `json:"mtime"`
	SHA256 string `json:"sha256"`
	Known  bool   `json:"known"`
	Group  string `json:"group"`
}

var knownFiles = map[string]string{
	"rpt.conf":                    "Core",
	"simpleusb.conf":              "Radio",
	"usbradio.conf":               "Radio",
	"res_usbradio.conf":           "Radio",
	"voter.conf":                  "Radio",
	"iax.conf":                    "Linking",
	"echolink.conf":               "Linking",
	"rpt_http_registrations.conf": "Linking",
	"rpt_auth.conf":               "Linking",
	"manager.conf":                "Platform",
	"modules.conf":                "Platform",
	"extensions.conf":             "Platform",
	"logger.conf":                 "Platform",
	"http.conf":                   "Platform",
	"gps.conf":                    "Platform",
	"asterisk.conf":               "Platform",
}

// GroupOf returns the UI group of a path and whether it is a known file.
func GroupOf(path string) (string, bool) {
	if strings.HasPrefix(path, "custom/") {
		return "Custom", true
	}
	if g, ok := knownFiles[path]; ok {
		return g, true
	}
	return "Other", false
}

// Files lists the editable files.
func (s *Service) Files(ctx context.Context) ([]FileEntry, error) {
	list, err := s.H.ConfigList(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]FileEntry, 0, len(list))
	for _, f := range list {
		g, k := GroupOf(f.Path)
		out = append(out, FileEntry{Path: f.Path, Size: f.Size, MTime: time.Unix(f.MTime, 0).UTC().Format(time.RFC3339),
			SHA256: f.SHA256, Known: k, Group: g})
	}
	return out, nil
}

// Read returns one file.
func (s *Service) Read(ctx context.Context, path string) (helper.ConfigContent, error) {
	if err := helper.ValidatePath(path); err != nil {
		return helper.ConfigContent{}, &InvalidError{"invalid path"}
	}
	c, err := s.H.ConfigRead(ctx, path)
	return c, classify(err)
}

// ---------------------------------------------------------------- preview

// PreviewFile is the per-file result of a preview.
type PreviewFile struct {
	Path    string       `json:"path"`
	OK      bool         `json:"ok"`
	Error   *SyntaxError `json:"error,omitempty"`
	Diff    string       `json:"diff"`
	Changed bool         `json:"changed"`
	// Conflict: the file changed on disk since the version it is based on.
	Conflict bool `json:"conflict,omitempty"`
}

// Preview is the result of a preview.
type Preview struct {
	Files   []PreviewFile `json:"files"`
	Reload  []ReloadStep  `json:"reload"`
	Restart bool          `json:"restart"`
}

func checkFiles(files []File) error {
	if len(files) == 0 {
		return &InvalidError{"no files"}
	}
	if len(files) > helper.MaxConfigFiles {
		return &InvalidError{"too many files (limit 50)"}
	}
	seen := map[string]bool{}
	for _, f := range files {
		if seen[f.Path] {
			return &InvalidError{f.Path + ": duplicate path"}
		}
		seen[f.Path] = true
	}
	return nil
}

// Preview parses and diffs the files against their current content and
// derives the reload plan. Per-file problems are reported in the result.
func (s *Service) Preview(ctx context.Context, files []File) (Preview, error) {
	if err := checkFiles(files); err != nil {
		return Preview{}, err
	}
	list, err := s.H.ConfigList(ctx)
	if err != nil {
		return Preview{}, err
	}
	have := map[string]helper.ConfigInfo{}
	for _, f := range list {
		have[f.Path] = f
	}
	res := Preview{Files: []PreviewFile{}, Reload: []ReloadStep{}}
	var changed []string
	for _, f := range files {
		pf := PreviewFile{Path: f.Path}
		if err := helper.ValidatePath(f.Path); err != nil {
			pf.Error = &SyntaxError{Message: err.Error()}
		} else if err := helper.ValidateContent(f.Content); err != nil {
			pf.Error = &SyntaxError{Message: err.Error()}
		} else if se := CheckSyntax(f.Path, f.Content); se != nil {
			pf.Error = se
		}
		cur := ""
		_, exists := have[f.Path]
		if pf.Error == nil {
			if exists {
				c, err := s.H.ConfigRead(ctx, f.Path)
				if err != nil {
					return Preview{}, classify(err)
				}
				cur = c.Content
				pf.Conflict = f.Base != c.SHA256
			} else if !strings.HasPrefix(f.Path, "custom/") {
				pf.Error = &SyntaxError{Message: "new files are only allowed under custom/"}
			} else {
				pf.Conflict = f.Base != ""
			}
		}
		pf.OK = pf.Error == nil
		if pf.OK {
			pf.Changed = !exists || cur != f.Content
			if pf.Changed {
				pf.Diff = UnifiedDiff(f.Path, cur, f.Content, !exists)
				changed = append(changed, f.Path)
			}
		}
		res.Files = append(res.Files, pf)
	}
	res.Reload, res.Restart = PlanReload(changed)
	if res.Reload == nil {
		res.Reload = []ReloadStep{}
	}
	return res, nil
}

// ---------------------------------------------------------------- apply

// ReloadResult is the outcome of one reload command.
type ReloadResult struct {
	Command string `json:"command"`
	OK      bool   `json:"ok"`
	Output  string `json:"output"`
}

// Check is one health check.
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// Health is the result of the health check.
type Health struct {
	OK     bool    `json:"ok"`
	Checks []Check `json:"checks"`
}

// ApplyResult is the result of Apply and Restore.
type ApplyResult struct {
	Commit  string         `json:"commit"`
	Before  string         `json:"before"`
	Reload  []ReloadResult `json:"reload"`
	Health  *Health        `json:"health"`
	Restart bool           `json:"restart"`
	// Files are the files written.
	Files   []string `json:"files"`
	Skipped []string `json:"skipped,omitempty"`
}

func cleanMessage(m string, files []File) string {
	var b strings.Builder
	for _, r := range m {
		if unicode.IsControl(r) || r == ' ' || r == ' ' {
			r = ' '
		}
		b.WriteRune(r)
	}
	m = strings.TrimSpace(b.String())
	if m == "" {
		switch len(files) {
		case 0:
		case 1:
			m = "Edit " + files[0].Path
		default:
			m = fmt.Sprintf("Edit %s and %d more", files[0].Path, len(files)-1)
		}
	}
	if r := []rune(m); len(r) > 200 {
		m = string(r[:200])
	}
	return m
}

// Apply writes the files through the helper and, when reload is set, runs
// the reload plan and the health check. author is the ASLCP username.
func (s *Service) Apply(ctx context.Context, author string, files []File, message string, reload bool) (ApplyResult, error) {
	if err := checkFiles(files); err != nil {
		return ApplyResult{}, err
	}
	var issues []SyntaxIssue
	hf := make([]helper.ConfigFile, 0, len(files))
	for _, f := range files {
		if err := helper.ValidatePath(f.Path); err != nil {
			return ApplyResult{}, &InvalidError{f.Path + ": " + err.Error()}
		}
		if err := helper.ValidateContent(f.Content); err != nil {
			return ApplyResult{}, &InvalidError{f.Path + ": " + err.Error()}
		}
		if se := CheckSyntax(f.Path, f.Content); se != nil {
			issues = append(issues, SyntaxIssue{Path: f.Path, Line: se.Line, Message: se.Message})
		}
		hf = append(hf, helper.ConfigFile{Path: f.Path, Content: f.Content, Base: f.Base})
	}
	if len(issues) > 0 {
		return ApplyResult{}, &SyntaxErrors{Issues: issues}
	}
	nodes := s.localNodes(ctx)
	r, err := s.H.ConfigApply(ctx, hf, cleanMessage(message, files), author)
	if err != nil {
		return ApplyResult{}, classify(err)
	}
	return s.finish(ctx, r, nodes, reload), nil
}

// Restore restores the files of a commit (the same pipeline as Apply).
func (s *Service) Restore(ctx context.Context, author, commit string, reload bool) (ApplyResult, error) {
	nodes := s.localNodes(ctx)
	r, err := s.H.ConfigRestore(ctx, commit, author)
	if err != nil {
		return ApplyResult{}, classify(err)
	}
	return s.finish(ctx, r, nodes, reload), nil
}

func (s *Service) finish(ctx context.Context, r helper.ConfigApplyResult, nodes []string, reload bool) ApplyResult {
	res := ApplyResult{Commit: r.Commit, Before: r.Before, Files: r.Files, Skipped: r.Skipped}
	if res.Files == nil {
		res.Files = []string{}
	}
	steps, restart := PlanReload(r.Files)
	res.Restart = restart
	if !reload || len(r.Files) == 0 {
		return res
	}
	res.Reload = s.runReload(ctx, steps)
	h := s.HealthCheck(ctx, nodes)
	res.Health = &h
	return res
}

func (s *Service) localNodes(ctx context.Context) []string {
	if s.Nodes == nil || s.AMI == nil || !s.AMI.State().Connected {
		return nil
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	n, _ := s.Nodes.LocalNodes(cctx)
	return n
}

// reloadFailure matches Asterisk CLI replies that mean a command did not run.
func reloadFailed(lines []string) bool {
	for _, l := range lines {
		t := strings.ToLower(strings.TrimSpace(l))
		if strings.HasPrefix(t, "no such command") || strings.HasPrefix(t, "unable to") ||
			strings.HasPrefix(t, "failed") || strings.HasPrefix(t, "error") || strings.Contains(t, "not loaded") {
			return true
		}
	}
	return false
}

func (s *Service) runReload(ctx context.Context, steps []ReloadStep) []ReloadResult {
	out := []ReloadResult{}
	for _, st := range steps {
		rr := ReloadResult{Command: st.Command}
		if s.AMI == nil {
			rr.Output = "not connected to Asterisk"
			out = append(out, rr)
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, s.ReloadTimeout)
		lines, err := s.AMI.Command(cctx, st.Command) // fixed strings from ReloadMap only
		cancel()
		rr.Output = strings.TrimSpace(strings.Join(lines, "\n"))
		switch {
		case err != nil:
			rr.Output = err.Error()
		default:
			rr.OK = !reloadFailed(lines)
		}
		out = append(out, rr)
	}
	return out
}

// HealthCheck waits up to HealthTimeout for AMI to answer and every node in
// nodes (those that existed before the apply) to answer an rpt status query.
func (s *Service) HealthCheck(ctx context.Context, nodes []string) Health {
	deadline := time.Now().Add(s.HealthTimeout)
	for {
		h := s.healthOnce(ctx, nodes)
		if h.OK || time.Now().After(deadline) || ctx.Err() != nil {
			return h
		}
		select {
		case <-ctx.Done():
			return h
		case <-time.After(s.HealthPoll):
		}
	}
}

func (s *Service) healthOnce(ctx context.Context, nodes []string) Health {
	h := Health{OK: true, Checks: []Check{}}
	add := func(name string, ok bool, detail string) {
		h.Checks = append(h.Checks, Check{Name: name, OK: ok, Detail: detail})
		if !ok {
			h.OK = false
		}
	}
	if s.AMI == nil || !s.AMI.State().Connected {
		add("ami", false, "not connected to Asterisk")
		return h
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := s.AMI.Command(cctx, "core show uptime seconds"); err != nil {
		add("ami", false, err.Error())
		return h
	}
	add("ami", true, "connected and answering")
	sort.Strings(nodes)
	for _, n := range nodes {
		if s.Nodes == nil {
			break
		}
		if _, err := s.Nodes.XStat(cctx, n); err != nil {
			add("node "+n, false, err.Error())
		} else {
			add("node "+n, true, "answers rpt status")
		}
	}
	return h
}

// ---------------------------------------------------------------- history

// History lists commits.
func (s *Service) History(ctx context.Context, limit, offset int) ([]helper.ConfigCommit, error) {
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 200 || offset < 0 {
		return nil, &InvalidError{"invalid limit or offset"}
	}
	l, err := s.H.ConfigLog(ctx, limit, offset)
	if err != nil {
		return nil, classify(err)
	}
	if l == nil {
		l = []helper.ConfigCommit{}
	}
	return l, nil
}

// Show returns one commit; secret values in the diffs are masked unless
// reveal is set.
func (s *Service) Show(ctx context.Context, commit string, reveal bool) (helper.ConfigShowResult, error) {
	r, err := s.H.ConfigShow(ctx, commit)
	if err != nil {
		return r, classify(err)
	}
	if !reveal {
		for i := range r.Files {
			r.Files[i].Diff = MaskDiff(r.Files[i].Diff)
		}
	}
	if r.Files == nil {
		r.Files = []helper.ConfigFileDiff{}
	}
	return r, nil
}

// RestartViaHelper restarts the asterisk service through the helper.
func (s *Service) RestartViaHelper(ctx context.Context) error { return s.H.AsteriskRestart(ctx) }
