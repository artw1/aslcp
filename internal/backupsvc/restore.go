package backupsvc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/artw1/aslcp/internal/backup"
	"github.com/artw1/aslcp/internal/helper"
	"github.com/artw1/aslcp/internal/store"
)

// ---------------------------------------------------------------- types

// Warning is a notice about the backup that does not stop the restore.
type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// File is one /etc/asterisk file of the plan.
type File struct {
	Path   string `json:"path"`
	Action string `json:"action"`
	Note   string `json:"note,omitempty"`
}

// User is an account the restored database holds.
type User struct {
	Username string `json:"username"`
	Role     string `json:"role"`
}

// ChecklistItem is one thing the backup does not cover.
type ChecklistItem struct {
	Item  string `json:"item"`
	Value string `json:"value"`
}

// Radio is the SA818 configuration of the backup.
type Radio struct {
	Band        string `json:"band"`
	FreqRx      string `json:"freqRx"`
	FreqTx      string `json:"freqTx"`
	Tone        string `json:"tone"`
	CtcssRx     string `json:"ctcssRx"`
	CtcssTx     string `json:"ctcssTx"`
	DcsRx       string `json:"dcsRx"`
	DcsTx       string `json:"dcsTx"`
	Port        string `json:"port"`
	PortPresent bool   `json:"portPresent"`
}

// BackupInfo describes the backup itself.
type BackupInfo struct {
	Hostname        string   `json:"hostname"`
	Created         string   `json:"created"`
	CreatedBy       string   `json:"createdBy"`
	ASLCPVersion    string   `json:"aslcpVersion"`
	ASL3Version     string   `json:"asl3Version"`
	AsteriskVersion string   `json:"asteriskVersion"`
	Arch            string   `json:"arch"`
	OSID            string   `json:"osId,omitempty"`
	OSCodename      string   `json:"osCodename,omitempty"`
	OSName          string   `json:"osName,omitempty"`
	Nodes           []string `json:"nodes"`
	Channel         string   `json:"channel"`
}

// NodeInfo describes this node, for comparison with the backup.
type NodeInfo struct {
	ASL3Version     string `json:"asl3Version"`
	AsteriskVersion string `json:"asteriskVersion"`
	Arch            string `json:"arch"`
	OSID            string `json:"osId,omitempty"`
	OSCodename      string `json:"osCodename,omitempty"`
	OSName          string `json:"osName,omitempty"`
}

// PartsPresent says which optional parts the backup holds.
type PartsPresent struct {
	History bool `json:"history"`
	Allmon3 bool `json:"allmon3"`
	Sounds  bool `json:"sounds"`
	SA818   bool `json:"sa818"`
	// DV: the ini files and dvsm.macro of /opt (with secrets).
	DV bool `json:"dv"`
}

// Preview is what the restore page shows before anything changes.
type Preview struct {
	Backup          BackupInfo      `json:"backup"`
	Node            NodeInfo        `json:"node"`
	Warnings        []Warning       `json:"warnings"`
	Files           []File          `json:"files"`
	Users           []User          `json:"users"`
	Favorites       int             `json:"favorites"`
	Schedules       int             `json:"schedules"`
	MissingPackages []string        `json:"missingPackages"`
	InstallCommand  string          `json:"installCommand"`
	Checklist       []ChecklistItem `json:"checklist"`
	Radio           *Radio          `json:"radio"`
	Parts           PartsPresent    `json:"parts"`
}

// Upload is the answer to a validated upload.
type Upload struct {
	ID      string  `json:"id"`
	Preview Preview `json:"preview"`
}

// Step is one step of an apply.
type Step struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// ApplyResult is the answer to an apply.
type ApplyResult struct {
	OK         bool   `json:"ok"`
	Steps      []Step `json:"steps"`
	Restarting bool   `json:"restarting"`
	// Error is set when OK is false (additive to the contract).
	Error string `json:"error,omitempty"`
}

// Check is one health check.
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// Health is the health check after the restart. Pending is true while it
// still runs.
type Health struct {
	OK      bool    `json:"ok"`
	Pending bool    `json:"pending"`
	Checks  []Check `json:"checks"`
}

// Result is the last restore, kept so the page can show it after the
// restart.
type Result struct {
	At            time.Time       `json:"at"`
	BackupCreated time.Time       `json:"backupCreated"`
	Steps         []Step          `json:"steps"`
	Health        Health          `json:"health"`
	Checklist     []ChecklistItem `json:"checklist"`
}

type stored struct {
	Result
	Nodes []string `json:"nodes"`
}

const resultFile = "restore-result.json"

// ---------------------------------------------------------------- upload

func (s *Service) purgeLocked(now time.Time) {
	for id, p := range s.pending {
		if now.Sub(p.created) > pendingTTL {
			os.RemoveAll(p.dir)
			delete(s.pending, id)
		}
	}
	for len(s.pending) >= maxPending {
		var oldest *pending
		for _, p := range s.pending {
			if oldest == nil || p.created.Before(oldest.created) {
				oldest = p
			}
		}
		os.RemoveAll(oldest.dir)
		delete(s.pending, oldest.id)
	}
}

// Purge removes expired uploads.
func (s *Service) Purge() {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.o.Now()
	for id, p := range s.pending {
		if now.Sub(p.created) > pendingTTL {
			os.RemoveAll(p.dir)
			delete(s.pending, id)
		}
	}
}

// Upload stores, validates and previews a backup read from r.
func (s *Service) Upload(ctx context.Context, r io.Reader) (Upload, error) {
	s.mu.Lock()
	s.purgeLocked(s.o.Now())
	s.mu.Unlock()
	dir, err := s.newDir("restore-")
	if err != nil {
		return Upload{}, err
	}
	ok := false
	defer func() {
		if !ok {
			os.RemoveAll(dir)
		}
	}()
	arc := filepath.Join(dir, "archive.tar.gz")
	f, err := os.OpenFile(arc, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return Upload{}, err
	}
	n, err := io.Copy(f, io.LimitReader(r, backup.MaxUpload+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return Upload{}, err
	}
	if n > backup.MaxUpload {
		return Upload{}, ErrTooLarge
	}
	in, err := os.Open(arc)
	if err != nil {
		return Upload{}, err
	}
	var sa818 []byte
	dbPath := filepath.Join(dir, "aslcp.db")
	m, err := backup.Read(in, func(e backup.Entry, body io.Reader) error {
		switch e.Path {
		case backup.PathAslcpDB:
			w, err := os.OpenFile(dbPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err != nil {
				return err
			}
			_, err = io.Copy(w, body)
			if cerr := w.Close(); err == nil {
				err = cerr
			}
			return err
		case backup.PathSA818:
			if e.Size > 1<<20 {
				return nil
			}
			var b bytes.Buffer
			_, err := io.Copy(&b, body)
			sa818 = b.Bytes()
			return err
		}
		return nil
	})
	in.Close()
	if err != nil {
		var nf *backup.NewerFormatError
		if errors.As(err, &nf) {
			return Upload{}, &TooNewError{Msg: "this backup was made by a newer ASLCP (backup format " + fmt.Sprint(nf.Version) + "); update ASLCP first"}
		}
		return Upload{}, err
	}
	if backup.VersionNewer(m.ASLCPVersion, s.o.Version) {
		return Upload{}, &TooNewError{Msg: fmt.Sprintf("this backup was made by ASLCP %s, newer than the installed %s; update ASLCP first", m.ASLCPVersion, s.o.Version)}
	}
	if m.Find(backup.PathAslcpDB) == nil {
		return Upload{}, &backup.Error{Msg: "the backup has no ASLCP database"}
	}
	insp, err := store.Inspect(dbPath)
	if err != nil {
		if errors.Is(err, store.ErrSchemaNewer) {
			return Upload{}, &TooNewError{Msg: "the database in this backup is newer than this ASLCP understands; update ASLCP first"}
		}
		return Upload{}, &backup.Error{Msg: "the ASLCP database in the backup is not usable: " + err.Error()}
	}
	in, err = os.Open(arc)
	if err != nil {
		return Upload{}, err
	}
	plan, err := s.o.Helper.BackupRestore(ctx, helper.RestoreRequest{DryRun: true}, in)
	in.Close()
	if err != nil {
		var un *helper.UnavailableError
		if errors.As(err, &un) || errors.Is(err, helper.ErrBusy) {
			return Upload{}, err
		}
		// The helper re-validates the archive as root; what it rejects is
		// a bad backup.
		return Upload{}, &backup.Error{Msg: err.Error()}
	}
	prev, offered := s.preview(m, plan, insp, sa818)
	id := newID()
	final := filepath.Join(s.tmpDir(), "restore-"+id)
	if err := os.Rename(dir, final); err != nil {
		return Upload{}, err
	}
	ok = true
	s.mu.Lock()
	s.pending[id] = &pending{id: id, dir: final, created: s.o.Now(), m: m, prev: prev, offered: offered}
	s.mu.Unlock()
	return Upload{ID: id, Preview: prev}, nil
}

func (s *Service) preview(m *backup.Manifest, plan helper.RestoreResult, insp store.Inspection, sa818 []byte) (Preview, map[string]bool) {
	p := Preview{
		Backup: BackupInfo{Hostname: m.Hostname, Created: m.Created.UTC().Format(time.RFC3339), CreatedBy: m.CreatedBy,
			ASLCPVersion: m.ASLCPVersion, ASL3Version: m.ASL3Version, AsteriskVersion: m.AsteriskVersion, Arch: m.Arch,
			OSID: m.OSID, OSCodename: backup.ReleaseOf(m.OSVersionCodename, m.ASL3Version, m.AsteriskVersion), OSName: m.OSPrettyName,
			Nodes: nonNil(m.Nodes), Channel: m.Channel},
		Node: NodeInfo{ASL3Version: plan.ASL3Version, AsteriskVersion: plan.AsteriskVersion, Arch: plan.Arch,
			OSID: plan.OSID, OSCodename: backup.ReleaseOf(plan.OSCodename, plan.ASL3Version, plan.AsteriskVersion), OSName: plan.OSName},
		Warnings: []Warning{}, Files: []File{}, Users: []User{}, MissingPackages: []string{}, Checklist: []ChecklistItem{},
		Favorites: insp.Favorites,
		Schedules: insp.Schedules,
		Parts:     PartsPresent{History: m.Parts.History, Allmon3: m.Parts.Allmon3, Sounds: len(m.Parts.Sounds) > 0, SA818: m.Parts.SA818, DV: m.Parts.DV},
	}
	offered := map[string]bool{}
	dist := 0
	for _, f := range plan.Files {
		p.Files = append(p.Files, File{Path: f.Path, Action: f.Action, Note: f.Note})
		switch f.Action {
		case helper.ActDeleteOptional:
			offered[f.Path] = true
		case helper.ActRestoreDist:
			dist++
		}
	}
	for _, u := range insp.Users {
		p.Users = append(p.Users, User{Username: u.Username, Role: u.Role})
	}

	// warnings
	if bc, nc := p.Backup.OSCodename, p.Node.OSCodename; bc != "" && nc != "" && bc != nc {
		p.Warnings = append(p.Warnings, Warning{"os_release", crossReleaseMessage(bc, nc)})
	}
	if m.ASL3Version != "" && plan.ASL3Version != "" && m.ASL3Version != plan.ASL3Version {
		msg := fmt.Sprintf("The backup was made on ASL3 %s; this node runs %s. Stock files keep this node's version.", m.ASL3Version, plan.ASL3Version)
		if backup.VersionNewer(m.ASL3Version, plan.ASL3Version) {
			msg = fmt.Sprintf("The backup was made on ASL3 %s, newer than the %s on this node. Update ASL3 first if the configuration uses newer features.", m.ASL3Version, plan.ASL3Version)
		}
		p.Warnings = append(p.Warnings, Warning{"asl3_version", msg})
	}
	if m.AsteriskVersion != "" && plan.AsteriskVersion != "" && m.AsteriskVersion != plan.AsteriskVersion {
		p.Warnings = append(p.Warnings, Warning{"asterisk_version",
			fmt.Sprintf("The backup was made on Asterisk %s; this node runs %s.", m.AsteriskVersion, plan.AsteriskVersion)})
	}
	if m.Arch != "" && plan.Arch != "" && m.Arch != plan.Arch {
		p.Warnings = append(p.Warnings, Warning{"arch",
			fmt.Sprintf("The backup was made on a %s system; this node is %s. Configuration files are portable, but check audio device settings.", m.Arch, plan.Arch)})
	}
	if dist > 0 {
		p.Warnings = append(p.Warnings, Warning{"dist_files",
			fmt.Sprintf("%d file(s) were changed by you and ASL3 changed the default since: your version is restored and the new default is kept beside it as .dpkg-dist. Review them.", dist)})
	}
	if plan.DVLegacySettings {
		p.Warnings = append(p.Warnings, Warning{"dv_settings_not_restored", "The backup holds the settings of the previous digital voice stack. ASLCP 2.0 does not restore them: set up digital modes again on the Digital modes page after the restore."})
	}
	if len(m.Skipped) > 0 {
		p.Warnings = append(p.Warnings, Warning{"skipped_files",
			fmt.Sprintf("%d item(s) could not be included when the backup was made (for example unreadable files or symbolic links leaving the tree).", len(m.Skipped))})
	}

	// packages
	have := map[string]bool{}
	for _, pk := range plan.Packages {
		have[pk.Name] = true
	}
	for _, pk := range m.Packages {
		// Digital modes packages are installed by the enable job, never by a
		// plain apt command.
		if !have[pk.Name] && backup.ValidPackageName(pk.Name) && !helper.IsDVPackage(pk.Name) {
			p.MissingPackages = append(p.MissingPackages, pk.Name)
		}
	}
	sort.Strings(p.MissingPackages)
	if len(p.MissingPackages) > 0 {
		p.InstallCommand = "sudo apt install " + strings.Join(p.MissingPackages, " ")
	}

	// radio
	if len(sa818) > 0 {
		p.Radio = parseRadio(sa818)
		p.Radio.PortPresent = plan.SA818PortPresent
		if p.Radio.Port != "" && !p.Radio.PortPresent {
			p.Warnings = append(p.Warnings, Warning{"sa818_port",
				fmt.Sprintf("The SA818 settings use the serial port %s, which does not exist on this node. Check the module is plugged in before running sa818-menu --apply.", p.Radio.Port)})
		}
	}

	p.Checklist = checklist(m, p.Radio != nil)
	return p, offered
}

// crossReleaseMessage is the warning for a backup made on another Debian
// release than the one this node runs.
func crossReleaseMessage(backupRel, nodeRel string) string {
	return fmt.Sprintf("This backup was made on %s; this node runs %s. Files you edited will replace the new release's defaults; the new defaults are kept next to them as .dpkg-dist. ASL3 changes some configs between releases (for example DAHDI timing is no longer needed), so if Asterisk or app_rpt does not start, compare your files against the .dpkg-dist copies.",
		backup.ReleaseLabel(backupRel), backup.ReleaseLabel(nodeRel))
}

func checklist(m *backup.Manifest, radio bool) []ChecklistItem {
	other := "Anything installed outside the ASL3 packages (for example an M17 bridge and its service)"
	var names []string
	for _, pk := range m.Packages {
		if strings.Contains(pk.Name, "m17") || strings.HasPrefix(pk.Name, "supermon") {
			names = append(names, pk.Name)
		}
	}
	if len(names) > 0 {
		other = strings.Join(names, ", ") + " (install and configure them again)"
	}
	c := []ChecklistItem{
		{"Hostname", m.Hostname}, {"Timezone", m.Timezone},
		{"Network and Wi-Fi", "Not included in the backup"},
		{"SSH keys and OS users", "Not included in the backup"},
		{"Other software", other},
		{"Firewall rules", "Only ASLCP's own rule is set up by the package"},
		{"Cron jobs", "Not included in the backup"},
	}
	if radio {
		c = append(c, ChecklistItem{"SA818 radio module", "To program it from the restored file run: sudo sa818-menu --apply"})
	}
	return c
}

func parseRadio(data []byte) *Radio {
	kv := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] == '#' {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if len(v) > 200 {
			continue
		}
		kv[k] = v
	}
	r := &Radio{Band: kv["CURRENT_BAND"], FreqRx: kv["CURRENT_FREQ_RX"], FreqTx: kv["CURRENT_FREQ_TX"],
		CtcssRx: kv["CURRENT_CTCSS_RX"], CtcssTx: kv["CURRENT_CTCSS_TX"], DcsRx: kv["CURRENT_DCS_RX"], DcsTx: kv["CURRENT_DCS_TX"],
		Port: kv["CURRENT_PORT"]}
	none := func(v string) bool { return v == "" || strings.Trim(v, "0.") == "" || strings.EqualFold(v, "none") }
	switch {
	case !none(r.CtcssRx) || !none(r.CtcssTx):
		r.Tone = "CTCSS " + r.CtcssRx + " / " + r.CtcssTx
	case !none(r.DcsRx) || !none(r.DcsTx):
		r.Tone = "DCS " + r.DcsRx + " / " + r.DcsTx
	default:
		r.Tone = "none"
	}
	return r
}

// Cancel discards an upload.
func (s *Service) Cancel(id string) {
	s.mu.Lock()
	p := s.pending[id]
	delete(s.pending, id)
	s.mu.Unlock()
	if p != nil {
		os.RemoveAll(p.dir)
	}
}

// Has reports whether an upload exists.
func (s *Service) Has(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.pending[id]
	return ok
}

// ---------------------------------------------------------------- apply

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// StagedPath is the staged database ApplyStaged swaps in at startup.
func StagedPath(dataDir string) string { return filepath.Join(dataDir, "aslcp.db.restore") }

// Apply runs the restore of upload id for who: the helper applies the system
// part, then ASLCP's database is staged for the swap at the next start. On
// success the caller must restart the server (Restarting is true).
func (s *Service) Apply(ctx context.Context, id string, deleteMissing []string, who string) (ApplyResult, error) {
	if !s.apply.TryLock() {
		return ApplyResult{}, ErrBusy
	}
	defer s.apply.Unlock()
	s.mu.Lock()
	p := s.pending[id]
	s.mu.Unlock()
	if p == nil {
		return ApplyResult{}, ErrNotFound
	}
	if deleteMissing == nil {
		deleteMissing = []string{}
	}
	for _, d := range deleteMissing {
		if !p.offered[d] {
			return ApplyResult{}, &backup.Error{Msg: d + " is not a file this restore offers to delete"}
		}
	}
	staged := StagedPath(s.o.DataDir)
	part := staged + ".part"
	_ = os.Remove(part)
	if err := copyFile(filepath.Join(p.dir, "aslcp.db"), part); err != nil {
		return ApplyResult{}, err
	}
	detail, _ := json.Marshal(map[string]any{"backupCreated": p.m.Created.UTC().Format(time.RFC3339), "backupHostname": p.m.Hostname})
	au := store.Audit{Username: who, Action: "backup.restore", Detail: string(detail), Time: s.o.Now()}
	if err := store.MigrateFile(part, &au); err != nil {
		os.Remove(part)
		return ApplyResult{}, &backup.Error{Msg: "the ASLCP database in the backup cannot be upgraded: " + err.Error()}
	}
	for _, suf := range []string{"-wal", "-shm"} {
		_ = os.Remove(part + suf)
	}
	arc, err := os.Open(filepath.Join(p.dir, "archive.tar.gz"))
	if err != nil {
		os.Remove(part)
		return ApplyResult{}, err
	}
	res, err := s.o.Helper.BackupRestore(ctx, helper.RestoreRequest{DeleteMissing: deleteMissing}, arc)
	arc.Close()
	if err != nil {
		os.Remove(part)
		return ApplyResult{}, err
	}
	out := ApplyResult{Steps: []Step{}}
	for _, st := range res.Steps {
		out.Steps = append(out.Steps, Step{Name: st.Name, OK: st.OK, Detail: st.Detail})
	}
	if !res.OK {
		os.Remove(part)
		out.Error = res.Error
		return out, nil
	}
	health := Health{Pending: true, Checks: []Check{}}
	if s.o.Demo {
		os.Remove(part)
		out.Steps = append(out.Steps, Step{Name: "Stage ASLCP database", OK: true, Detail: "skipped in demo mode"})
		out.OK = true
		health = Health{OK: true, Checks: []Check{{Name: "Demo node", OK: true, Detail: "nothing to restart in demo mode"}}}
	} else if err := os.Rename(part, staged); err != nil {
		os.Remove(part)
		out.Steps = append(out.Steps, Step{Name: "Stage ASLCP database", OK: false, Detail: err.Error()})
		out.Error = "Stage ASLCP database: " + err.Error()
		return out, nil
	}
	if !s.o.Demo {
		out.Steps = append(out.Steps, Step{Name: "Stage ASLCP database", OK: true,
			Detail: "replaced when ASLCP restarts; the previous database is kept as aslcp.db.pre-restore"})
		out.OK, out.Restarting = true, true
	}
	r := stored{Result: Result{At: s.o.Now().UTC(), BackupCreated: p.m.Created.UTC(), Steps: out.Steps,
		Health: health, Checklist: p.prev.Checklist}, Nodes: nonNil(p.m.Nodes)}
	if err := s.saveResult(r); err != nil {
		s.o.Logger.Printf("restore: saving the result: %v", err)
	}
	s.Cancel(id)
	return out, nil
}

// ---------------------------------------------------------------- result

func (s *Service) resultPath() string { return filepath.Join(s.o.DataDir, resultFile) }

func (s *Service) saveResult(r stored) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	tmp := s.resultPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.resultPath())
}

func (s *Service) loadResult() (stored, bool) {
	b, err := os.ReadFile(s.resultPath())
	if err != nil {
		return stored{}, false
	}
	var r stored
	if json.Unmarshal(b, &r) != nil {
		return stored{}, false
	}
	if r.Steps == nil {
		r.Steps = []Step{}
	}
	if r.Health.Checks == nil {
		r.Health.Checks = []Check{}
	}
	if r.Checklist == nil {
		r.Checklist = []ChecklistItem{}
	}
	return r, true
}

// LastResult returns the last restore, if any.
func (s *Service) LastResult() (Result, bool) {
	r, ok := s.loadResult()
	return r.Result, ok
}

// RunHealth completes the health check of a restore that is waiting for it
// (after the restart): run is retried until every check passes or the time
// is up, and each state is saved.
func (s *Service) RunHealth(ctx context.Context, run func(ctx context.Context, nodes []string) []Check) {
	r, ok := s.loadResult()
	if !ok || !r.Health.Pending {
		return
	}
	deadline := s.o.Now().Add(3 * time.Minute)
	for {
		checks := run(ctx, r.Nodes)
		all := true
		for _, c := range checks {
			all = all && c.OK
		}
		done := all || s.o.Now().After(deadline)
		r.Health = Health{OK: all, Pending: !done, Checks: nonNilChecks(checks)}
		if err := s.saveResult(r); err != nil {
			s.o.Logger.Printf("restore: saving the health check: %v", err)
		}
		if done {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}

// AppRptCheck turns the output of "module show like app_rpt" into a health
// check: Asterisk can be up and answering AMI while app_rpt failed to load
// (for example after config files from another release were restored).
func AppRptCheck(lines []string, err error) Check {
	c := Check{Name: "app_rpt module"}
	if err != nil {
		c.Detail = err.Error()
		return c
	}
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "app_rpt.so") {
			c.OK, c.Detail = true, "loaded"
			return c
		}
	}
	c.Detail = "not loaded; see the Asterisk log, and compare restored files with their .dpkg-dist copies"
	return c
}

// NodeAnswers reports whether the output of "rpt show variables N" is a real
// answer (an unloaded app_rpt makes Asterisk answer "No such command").
func NodeAnswers(lines []string) bool {
	for _, l := range lines {
		if strings.Contains(l, "No such command") {
			return false
		}
	}
	return true
}

func nonNilChecks(c []Check) []Check {
	if c == nil {
		return []Check{}
	}
	return c
}

// ---------------------------------------------------------------- startup

// ApplyStaged swaps a staged restore database in before the store is opened
// (ASLCP restarts after a restore). The previous database is kept as
// aslcp.db.pre-restore. A staged file that does not check out is set aside
// as aslcp.db.restore-rejected and the current database stays.
func ApplyStaged(dataDir string) (swapped bool, err error) {
	staged := StagedPath(dataDir)
	if _, err := os.Stat(staged); err != nil {
		return false, nil
	}
	if _, err := store.Inspect(staged); err != nil {
		_ = os.Rename(staged, staged+"-rejected")
		return false, fmt.Errorf("staged restore database rejected: %w", err)
	}
	live := filepath.Join(dataDir, "aslcp.db")
	prev := live + ".pre-restore"
	for _, suf := range []string{"", "-wal", "-shm"} {
		_ = os.Remove(prev + suf)
	}
	for _, suf := range []string{"", "-wal", "-shm"} {
		if err := os.Rename(live+suf, prev+suf); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("moving the current database aside: %w", err)
		}
	}
	for _, suf := range []string{"-wal", "-shm"} {
		_ = os.Remove(staged + suf)
	}
	if err := os.Rename(staged, live); err != nil {
		return false, fmt.Errorf("installing the restored database: %w", err)
	}
	return true, nil
}

// SpoolTemp creates a private temporary file for an upload in transit.
func (s *Service) SpoolTemp() (*os.File, error) {
	if err := os.MkdirAll(s.tmpDir(), 0o700); err != nil {
		return nil, err
	}
	return os.CreateTemp(s.tmpDir(), "upload-")
}
