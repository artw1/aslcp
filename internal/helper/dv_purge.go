package helper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/artw1/aslcp/internal/legacy"
)

// The purge of the previous digital voice stack on the upgrade to 2.0
// (docs/DV.md, "Upgrading from 1.x"). It removes only what ASLCP itself
// installed (the packages recorded in the install marker) and never touches
// md380-emu or qemu-user-static. The apt source and keyring stay: md380-emu
// is installed from that repository and keeps getting its updates. The
// settings of the old stack are not imported.

// StepPurgeLegacy is the dv-step that runs the purge (postinst starts it in a
// transient unit; the enable job runs it too, so a failed purge is retried).
const StepPurgeLegacy = "purge-legacy"

// DefaultDVHistoryDir is the ini history of the setup page of 1.4.0.
const DefaultDVHistoryDir = "/var/lib/aslcp-dvstate-history"

const (
	dvPurgeUnit     = "aslcp-dv-purge"
	dvPurgeIdleWait = 10 * time.Minute
)

// dvKeepPackages are never removed by the purge: the vocoder and its emulator.
var dvKeepPackages = map[string]bool{dvMD380Pkg: true, dvQemuPkg: true}

// dvOldStackPackages are the old stack's packages whose names carry no vendor
// prefix.
var dvOldStackPackages = []string{"analog-bridge", "mmdvm-bridge", "ysfgateway", "p25gateway", "nxdngateway", "stfu"}

// dvPurgeCandidate reports whether a package of the install marker belongs to
// the old stack and may be purged.
func dvPurgeCandidate(name string) bool {
	if dvKeepPackages[name] || dvProtectedRe.MatchString(name) || !dvNameRe.MatchString(name) {
		return false
	}
	if legacy.IsLegacyPackage(name) {
		return true
	}
	for _, p := range dvOldStackPackages {
		if p == name {
			return true
		}
	}
	return false
}

var dvRemvRe = regexp.MustCompile(`^(?:Remv|Purg) (\S+)`)

// ParseRemvLines returns the package names of the Remv and Purg lines of
// `apt-get -s purge`, sorted.
func ParseRemvLines(out string) []string {
	var res []string
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		if m := dvRemvRe.FindStringSubmatch(line); m != nil {
			res = append(res, m[1])
		}
	}
	sort.Strings(res)
	return res
}

func (o *Ops) dvHistoryDir() string { return orDefault(o.DVHistoryDir, DefaultDVHistoryDir) }

// removeOwnDir deletes dir when it is a real directory (never a symlink).
func removeOwnDir(dir string, w io.Writer) {
	fi, err := os.Lstat(dir)
	if err != nil || !fi.IsDir() {
		return
	}
	if err := os.RemoveAll(dir); err != nil {
		fmt.Fprintf(w, "warning: cannot remove %s: %v\n", dir, err)
		return
	}
	fmt.Fprintf(w, "removed %s\n", dir)
}

// waitDpkgIdle waits until the aslcp package is no longer being configured:
// postinst starts the purge while dpkg still holds its lock.
func (o *Ops) waitDpkgIdle(ctx context.Context) error {
	wait := o.DVPurgeWait
	if wait <= 0 {
		wait = dvPurgeIdleWait
	}
	sleep := o.DVPurgeSleep
	if sleep == nil {
		sleep = func(d time.Duration) { time.Sleep(d) }
	}
	deadline := time.Now().Add(wait)
	for {
		out, _, err := o.Run.Run(ctx, "dpkg-query", "-W", "-f=${db:Status-Status}", "aslcp")
		if err != nil || strings.TrimSpace(out) == "installed" || strings.TrimSpace(out) == "" {
			return nil
		}
		if ctx.Err() != nil || !time.Now().Before(deadline) {
			return errors.New("the package system stayed busy; run `aslcp dv enable` to retry")
		}
		sleep(2 * time.Second)
	}
}

// PurgeLegacy removes the previous digital voice stack that ASLCP installed:
//
//  1. the ini history copies of the old stack (ASLCP's own data) are deleted;
//  2. nothing else happens unless the install marker records old-stack
//     packages (ASLCP did not install anything else, so it removes nothing
//     else);
//  3. the old stack's units are stopped and disabled;
//  4. apt-get purge of the recorded old-stack packages, after a simulation
//     that must remove exactly those and nothing more (md380-emu included:
//     it is marked manually installed first);
//  5. the purged names leave the marker.
//
// The apt source and keyring are kept, because md380-emu comes from them.
//
// It is idempotent and a second run finds nothing to do.
func (o *Ops) PurgeLegacy(ctx context.Context, w io.Writer) error {
	removeOwnDir(o.dvHistoryDir(), w)
	removeOwnDir(orDefault(o.DVOldHistoryDir, legacy.OldHistoryDir), w)

	m, err := ReadDVMarker(o.dvMarkerPath())
	if err != nil {
		return err
	}
	if m == nil {
		return nil
	}
	var names []string
	for _, p := range m.Packages {
		if dvPurgeCandidate(p) {
			names = append(names, p)
		}
	}
	if len(names) == 0 {
		return nil
	}
	if err := o.waitDpkgIdle(ctx); err != nil {
		return err
	}
	fmt.Fprintln(w, "removing the previous digital voice stack")

	// The units come first: they hold the ports and the packages' scripts
	// would stop them anyway.
	for _, u := range o.dvUnitStates(ctx, dvLegacyUnits) {
		if !u.Loaded {
			continue
		}
		fmt.Fprintf(w, "stopping %s\n", u.Name)
		if out, code, err := o.Run.Run(ctx, "systemctl", "disable", "--now", u.Name+".service"); err != nil || code != 0 {
			fmt.Fprintf(w, "warning: could not stop %s: %s\n", u.Name, LastLines(out+errText(err), 2))
		}
	}

	have, err := o.dpkgInstalled(ctx, append(append([]string{}, names...), dvMD380Pkg, dvQemuPkg))
	if err != nil {
		return err
	}
	var installed []string
	for _, n := range names {
		if _, ok := have[n]; ok {
			installed = append(installed, n)
		}
	}
	if len(installed) > 0 {
		var keep []string
		for _, k := range []string{dvMD380Pkg, dvQemuPkg} {
			if _, ok := have[k]; ok {
				keep = append(keep, k)
			}
		}
		if len(keep) > 0 { // never an autoremove candidate
			if out, code, err := o.Run.Run(ctx, append([]string{"apt-mark", "manual"}, keep...)...); err != nil || code != 0 {
				return fmt.Errorf("apt-mark manual %s failed: %s", strings.Join(keep, " "), LastLines(out+errText(err), 2))
			}
		}
		if err := o.dvPurgePackages(ctx, w, installed); err != nil {
			return err
		}
		if _, ok := have[dvMD380Pkg]; ok {
			after, err := o.dpkgInstalled(ctx, []string{dvMD380Pkg})
			if err != nil || after[dvMD380Pkg] == "" {
				return errors.New("md380-emu is no longer installed after the purge; run `aslcp dv enable` to install it again")
			}
		}
	}

	// The marker keeps what is left (md380-emu, qemu-user-static and the
	// dependencies ASLCP installed with them).
	gone := map[string]bool{}
	for _, n := range names {
		gone[n] = true
	}
	var rest []string
	for _, p := range m.Packages {
		if !gone[p] {
			rest = append(rest, p)
		}
	}
	nm := *m
	nm.Packages = rest
	if nm.Packages == nil {
		nm.Packages = []string{}
	}
	if err := WriteDVMarker(o.dvMarkerPath(), nm); err != nil {
		return fmt.Errorf("cannot update the install marker: %w", err)
	}
	fmt.Fprintf(w, "the previous digital voice stack is removed (%d package(s)); md380-emu is kept\n", len(installed))
	return nil
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// dvPurgePackages purges pkgs after a simulation that removes exactly them.
func (o *Ops) dvPurgePackages(ctx context.Context, w io.Writer, pkgs []string) error {
	out, code, err := o.Run.Run(ctx, append([]string{"apt-get", "-s", "-q", "purge"}, pkgs...)...)
	if err != nil || code != 0 {
		return fmt.Errorf("apt-get purge simulation failed: %s", LastLines(out+errText(err), 3))
	}
	allowed := map[string]bool{}
	for _, p := range pkgs {
		allowed[p] = true
	}
	var extra []string
	for _, r := range ParseRemvLines(out) {
		if !allowed[r] {
			extra = append(extra, r)
		}
	}
	if len(extra) > 0 {
		fmt.Fprintf(w, "refused: the purge would also remove %s; nothing was removed\n", strings.Join(extra, ", "))
		return errors.New("unexpected_removals")
	}
	args := append([]string{"apt-get", "purge", "-y", "-q", "-o", "DPkg::Lock::Timeout=600",
		"-o", "Dpkg::Options::=--force-confold", "-o", "Dpkg::Use-Pty=0"}, pkgs...)
	if out, code, err := o.Run.Run(ctx, args...); err != nil || code != 0 {
		return fmt.Errorf("apt-get purge failed: %s", LastLines(out+errText(err), 3))
	}
	fmt.Fprintf(w, "purged %s\n", strings.Join(pkgs, " "))
	return nil
}
