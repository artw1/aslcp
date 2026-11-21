package helper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// The steps below run inside the install and uninstall units (the unit
// scripts call `aslcp-helper dvswitch-step <phase>`), as root. They take no
// client input: the sim file, the marker and the purge list are fixed files
// of the state directory. Output goes to the unit's journal, which is the job
// log shown in the panel.

// Steps of the units.
const (
	StepInstallPlan   = "install-plan"
	StepInstallCheck  = "install-check"
	StepUninstallPlan = "uninstall-plan"
	// StepRestoreApply applies the DVSwitch settings staged by a backup
	// restore (backup_dv_pending.go). It never fails the install.
	StepRestoreApply = "restore-apply"
)

// RunDVSwitchStep runs one step. A non-nil error makes the unit fail; its
// text is printed to w by the caller.
func (o *Ops) RunDVSwitchStep(ctx context.Context, phase string, w io.Writer) error {
	switch phase {
	case StepInstallPlan:
		return o.dvInstallPlan(w)
	case StepInstallCheck:
		return o.dvInstallCheck(w)
	case StepUninstallPlan:
		return o.dvUninstallPlan(ctx, w)
	case StepInstallDefaults:
		return o.dvInstallDefaults(ctx, w)
	case StepRestoreApply:
		return o.dvRestoreApply(ctx, w)
	case StepPrivateRev:
		return o.dvPrivateRevert(ctx, w)
	case StepSTFUPlan:
		return o.dvSTFUPlan(w)
	}
	return fmt.Errorf("unknown step %q", phase)
}

// dvInstallPlan reads the simulation written by the script, refuses a plan
// that pulls in a web server, PHP, a broker or Node.js (unexpected_deps),
// and records the newly installed packages in the marker before anything is
// installed.
func (o *Ops) dvInstallPlan(w io.Writer) error {
	data, err := os.ReadFile(o.dvSimPath())
	if err != nil {
		return fmt.Errorf("cannot read the simulation: %w", err)
	}
	inst := ParseInstLines(string(data))
	prev, err := ReadDVMarker(o.dvMarkerPath())
	if err != nil {
		return err
	}
	if bad := UnexpectedDeps(inst); len(bad) > 0 {
		fmt.Fprintf(w, "unexpected_deps: the install would also install %s; nothing was installed\n", strings.Join(bad, ", "))
		if prev == nil { // nothing of DVSwitch is installed: do not leave the repository behind
			_ = os.Remove(o.dvSourceList())
			_ = os.Remove(o.dvKeyring())
			fmt.Fprintln(w, "the DVSwitch apt source and key were removed again")
		}
		return errors.New("unexpected_deps")
	}
	have, err := o.dpkgInstalled(context.Background(), []string{dvWget})
	if err != nil {
		return err
	}
	_, wgetPre := have[dvWget]
	var newPkgs []string
	for _, i := range inst {
		if !i.New {
			continue
		}
		if !dvNameRe.MatchString(i.Name) || dvProtectedRe.MatchString(i.Name) {
			return fmt.Errorf("unexpected package name %q in the simulation", i.Name)
		}
		newPkgs = append(newPkgs, i.Name)
	}
	m := MergeDVMarker(prev, newPkgs, wgetPre, o.now())
	if err := WriteDVMarker(o.dvMarkerPath(), m); err != nil {
		return fmt.Errorf("cannot write the install marker: %w", err)
	}
	fmt.Fprintf(w, "recorded %d newly installed packages: %s\n", len(m.Packages), strings.Join(m.Packages, " "))
	if m.WgetPreinstalled {
		fmt.Fprintln(w, "wget was already installed and is kept by an uninstall")
	}
	_ = os.Remove(o.dvSimPath())
	return nil
}

// dvInstallCheck warns about missing or empty host files (DVSM_Update.sh
// exits 0 when its downloads fail). It never fails the job.
func (o *Ops) dvInstallCheck(w io.Writer) error {
	bad := 0
	for _, n := range dvHostFiles {
		fi, err := os.Stat(filepath.Join(o.dvHostDir(), n))
		if err != nil || !fi.Mode().IsRegular() || fi.Size() == 0 {
			bad++
			fmt.Fprintf(w, "warning: host file %s is missing or empty (the daily DVSM_Update retries; check the internet access of this node)\n", filepath.Join(o.dvHostDir(), n))
		}
	}
	if bad == 0 {
		fmt.Fprintln(w, "host files ok")
	}
	return nil
}

// dvSimPurge simulates `apt-get purge names...` and returns the packages it
// would remove.
func (o *Ops) dvSimPurge(ctx context.Context, names []string) ([]string, error) {
	argv := append([]string{"env", "LC_ALL=C", "DEBIAN_FRONTEND=noninteractive", "apt-get", "-s", "-q", "-o", "Debug::NoLocking=1", "purge"}, names...)
	out, code, err := o.Run.Run(ctx, argv...)
	if err != nil {
		return nil, fmt.Errorf("apt-get -s purge: %w", err)
	}
	if code != 0 {
		return nil, fmt.Errorf("apt-get -s purge failed: %s", LastLines(out, 4))
	}
	return ParseRemovals(out), nil
}

// outside returns the elements of removed that are not in allowed or that
// are protected packages.
func dvOutside(removed, allowed []string) []string {
	ok := map[string]bool{}
	for _, a := range allowed {
		ok[a] = true
	}
	var res []string
	for _, r := range removed {
		if !ok[r] || dvProtectedRe.MatchString(r) {
			res = append(res, r)
		}
	}
	return res
}

func errUnexpectedRemovals(w io.Writer, names []string) error {
	fmt.Fprintf(w, "unexpected_removals: the purge would also remove %s; nothing was changed\n", strings.Join(names, ", "))
	return errors.New("unexpected_removals")
}

// dvUninstallPlan decides, from the marker and apt simulations, which
// packages the purge removes, and writes them to the purge list. It aborts
// (unexpected_removals) when a purge would remove anything outside the
// recorded list, and keeps a recorded dependency that something else still
// needs. Nothing is changed before this step succeeds.
func (o *Ops) dvUninstallPlan(ctx context.Context, w io.Writer) error {
	m, err := ReadDVMarker(o.dvMarkerPath())
	if err != nil {
		return err
	}
	if m == nil {
		return ErrDVOutsideASLCP
	}
	cands := dvPurgeCandidates(m)
	if len(cands) > 60 {
		return errors.New("too many packages in the install marker")
	}
	have, err := o.dpkgInstalled(ctx, cands)
	if err != nil {
		return err
	}
	inSet := map[string]bool{}
	for _, p := range DVSwitchPackages() {
		inSet[p] = true
	}
	inSet[dvSTFUPkg] = true // installed later through the STFU install, recorded in the marker
	var core, deps []string
	for _, p := range cands {
		if _, ok := have[p]; !ok {
			continue
		}
		if inSet[p] {
			core = append(core, p)
		} else {
			deps = append(deps, p)
		}
	}
	sort.Strings(deps)
	if m.WgetPreinstalled {
		fmt.Fprintln(w, "keeping wget: it was installed before DVSwitch")
	}
	if len(core)+len(deps) == 0 {
		fmt.Fprintln(w, "no DVSwitch package is installed; only the files are cleaned up")
		return os.WriteFile(o.dvPurgePath(), nil, 0o600)
	}
	kept := append([]string{}, core...)
	if len(kept) > 0 {
		removed, err := o.dvSimPurge(ctx, kept)
		if err != nil {
			return err
		}
		if bad := dvOutside(removed, kept); len(bad) > 0 {
			return errUnexpectedRemovals(w, bad)
		}
	}
	pending := deps
	for round := 0; round < 10 && len(pending) > 0; round++ {
		var still []string
		for _, d := range pending {
			try := append(append([]string{}, kept...), d)
			removed, err := o.dvSimPurge(ctx, try)
			if err != nil {
				return err
			}
			if len(dvOutside(removed, try)) == 0 {
				kept = try
			} else {
				still = append(still, d)
			}
		}
		if len(still) == len(pending) {
			break
		}
		pending = still
	}
	for _, d := range pending {
		fmt.Fprintf(w, "keeping %s: another package still needs it\n", d)
	}
	removed, err := o.dvSimPurge(ctx, kept)
	if err != nil {
		return err
	}
	if bad := dvOutside(removed, kept); len(bad) > 0 {
		return errUnexpectedRemovals(w, bad)
	}
	sort.Strings(kept)
	if err := os.MkdirAll(o.dvStateDir(), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(o.dvPurgePath(), []byte(strings.Join(kept, "\n")+"\n"), 0o600); err != nil {
		return err
	}
	fmt.Fprintf(w, "purge plan: %s\n", strings.Join(kept, " "))
	return nil
}
