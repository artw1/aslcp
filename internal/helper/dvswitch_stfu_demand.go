package helper

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// STFU on demand. STFU and other BrandMeister clients (a phone app) log in
// with the same DMR ID, and BrandMeister keeps one login per ID: STFU is
// started when the panel enters BM mode and stopped when it leaves, unless
// "stay logged in" is set (then it is enabled and always running).

const (
	dvSTFUStayFile     = "stfu-stay"  // helper state file: present = stay logged in
	dvBMEnabledFile    = "bm-enabled" // helper state file: present = BrandMeister enabled in ASLCP
	dvSTFULoginWaitDef = 10 * time.Second
	dvSTFULoginPollDef = 250 * time.Millisecond
)

func (o *Ops) dvSTFUStayPath() string { return filepath.Join(o.dvStateDir(), dvSTFUStayFile) }

// dvSTFUStay reports the "stay logged in outside BM mode" setting.
func (o *Ops) dvSTFUStay() bool {
	_, err := os.Stat(o.dvSTFUStayPath())
	return err == nil
}

// dvBMEnabled reports whether BrandMeister is enabled in ASLCP (the card's
// Enable). Off by default: without it the panel has no BM mode and ASLCP
// never starts stfu; the [STFU] settings stay as they are.
func (o *Ops) dvBMEnabled() bool {
	_, err := os.Stat(filepath.Join(o.dvStateDir(), dvBMEnabledFile))
	return err == nil
}

// dvBMEnabledSet stores the flag (a state file, root only).
func (o *Ops) dvBMEnabledSet(on bool) error {
	p := filepath.Join(o.dvStateDir(), dvBMEnabledFile)
	if on {
		if err := os.MkdirAll(o.dvStateDir(), 0o700); err != nil {
			return err
		}
		return writeStateFile(p, []byte("1\n"), 0o600)
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// dvSTFUStayOn: stay logged in is set and BrandMeister is enabled (the
// saved setting outlives a Disable, but a disabled BM never runs stfu).
func (o *Ops) dvSTFUStayOn() bool { return o.dvBMEnabled() && o.dvSTFUStay() }

// dvSTFUStaySet stores the setting (a state file, root only).
func (o *Ops) dvSTFUStaySet(on bool) error {
	p := o.dvSTFUStayPath()
	if on {
		if err := os.MkdirAll(o.dvStateDir(), 0o700); err != nil {
			return err
		}
		return writeStateFile(p, []byte("1\n"), 0o600)
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// dvUnitRunning: an ActiveState that means the unit is up or coming up.
func dvUnitRunning(active string) bool {
	return active == "active" || active == "activating" || active == "reloading"
}

// dvPanelIsBM reports whether Analog_Bridge currently points at STFU.
func (o *Ops) dvPanelIsBM(ctx context.Context) bool {
	info, err := o.DVSwitchInfo(ctx)
	return err == nil && info.Found && DVPanelMode(info) == DVModeBM
}

// dvSTFUWanted: stfu is supposed to be running. Never while BrandMeister
// is disabled.
func (o *Ops) dvSTFUWanted(ctx context.Context) bool {
	return o.dvBMEnabled() && (o.dvSTFUStay() || o.dvPanelIsBM(ctx))
}

func (o *Ops) dvSleep(ctx context.Context, d time.Duration) {
	if o.DVSleep != nil {
		o.DVSleep(ctx, d)
		return
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// dvSTFUWaitLogin polls the STFU log for a login after the unit's start, up
// to DVSTFULoginWait. It counts polls rather than reading a clock.
func (o *Ops) dvSTFUWaitLogin(ctx context.Context) bool {
	wait, poll := orDuration(o.DVSTFULoginWait, dvSTFULoginWaitDef), orDuration(o.DVSTFULoginPoll, dvSTFULoginPollDef)
	n := int((wait + poll - 1) / poll)
	for i := 0; ; i++ {
		if o.dvSTFUStatus(ctx).State == DMRLoggedIn {
			return true
		}
		if i >= n || ctx.Err() != nil {
			return false
		}
		o.dvSleep(ctx, poll)
	}
}

func orDuration(v, def time.Duration) time.Duration {
	if v > 0 {
		return v
	}
	return def
}

// dvSTFUEnsure starts stfu when it is not running and waits for its login.
// onlyIfStopped: with the unit already running, do nothing. It reports
// pending: the login was not seen in time (or stfu could not be started).
func (o *Ops) dvSTFUEnsure(ctx context.Context, onlyIfStopped bool) (pending bool) {
	loaded, active, _ := o.dvUnitStart(ctx, dvSTFUUnit+".service")
	if !loaded {
		return true
	}
	if dvUnitRunning(active) {
		if onlyIfStopped {
			return false
		}
	} else if !o.dvSystemctl(ctx, "start", dvSTFUUnit+".service") {
		return true
	}
	return !o.dvSTFUWaitLogin(ctx)
}

// dvSTFUStopIdle stops stfu after the panel left BM, unless it stays logged
// in. It reports failed: it should have been stopped and was not.
func (o *Ops) dvSTFUStopIdle(ctx context.Context) (failed bool) {
	if o.dvSTFUStayOn() {
		return false
	}
	loaded, active, _ := o.dvUnitStart(ctx, dvSTFUUnit+".service")
	if !loaded || !dvUnitRunning(active) {
		return false
	}
	return !o.dvSystemctl(ctx, "stop", dvSTFUUnit+".service")
}

// dvBMVerify runs after a BrandMeister save restarted stfu: it waits for the
// login, then stops stfu again when it is not wanted (not stay, panel not in
// BM). Under the tune lock so no mode switch interleaves.
func (o *Ops) dvBMVerify(ctx context.Context, stay bool) (login string, stopFailed bool) {
	unlock, err := o.lockTune(ctx)
	if err != nil {
		return "pending", false
	}
	defer unlock()
	login = "pending"
	if o.dvSTFUWaitLogin(ctx) {
		login = "logged_in"
	}
	if !stay && !o.dvPanelIsBM(ctx) {
		stopFailed = !o.dvSystemctl(ctx, "stop", dvSTFUUnit+".service")
	}
	return login, stopFailed
}
