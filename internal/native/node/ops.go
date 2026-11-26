package node

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/artw1/aslcp/internal/native/mode"
	"github.com/artw1/aslcp/internal/native/radio"
	"github.com/artw1/aslcp/internal/native/radio/sa818"
)

// Status is a snapshot of the node for the UI. It never carries a password.
type Status struct {
	Callsign string      `json:"callsign,omitempty"`
	Started  time.Time   `json:"started"`
	Radio    RadioStatus `json:"radio"`
	// Active is the active mode ("" when idle) and Mode its status.
	Active string       `json:"active,omitempty"`
	Since  time.Time    `json:"since,omitempty"`
	Mode   *mode.Status `json:"mode,omitempty"`
	// Registration is the AllStar registration, kept up whenever AllStar
	// is configured, active or not.
	Registration *mode.Registration `json:"registration,omitempty"`
	ID           IDStatus           `json:"id"`
	// Configured is false until the station has a callsign.
	Configured bool `json:"configured"`
	// RestartRequired: the saved radio wiring (PTT GPIO, PTT or COS
	// polarity, SA818 port) differs from what the radio was opened with.
	// The node opens the radio once, so it restarts itself to apply it
	// (Options.RadioChanged); until then the old wiring is in use.
	RestartRequired bool `json:"restartRequired,omitempty"`
}

// RadioStatus is the radio side.
type RadioStatus struct {
	Present  bool `json:"present"`
	PTT      bool `json:"ptt"`
	COS      bool `json:"cos"`
	RxLocked bool `json:"rxLocked,omitempty"` // a local transmission timed out
	TxLocked bool `json:"txLocked,omitempty"` // the network timed out the transmitter
	// PTTStuck: unkeying the transmitter failed and is being retried, so
	// it may still be on the air.
	PTTStuck bool `json:"pttStuck,omitempty"`
}

// IDStatus is the station ID.
type IDStatus struct {
	Enabled bool      `json:"enabled"`
	Owed    bool      `json:"owed"`
	Due     time.Time `json:"due,omitempty"`
	Last    time.Time `json:"last,omitempty"`
	Why     string    `json:"why,omitempty"`
}

// Status returns a snapshot of the node.
func (e *Engine) Status() Status {
	e.mu.Lock()
	st := Status{
		Callsign: e.cfg.Station.Callsign,
		Started:  e.started,
		Radio: RadioStatus{
			Present: !e.o.RadioAbsent, PTT: e.sx.ptt, COS: e.sx.cos,
			RxLocked: e.sx.rfLocked, TxLocked: e.sx.txLocked, PTTStuck: e.sx.pttStuck,
		},
		ID: IDStatus{
			Enabled: e.params.id.on, Owed: e.sx.idT.owed,
			Last: e.sx.idT.lastID, Why: string(e.sx.idT.lastWhy),
		},
		Configured:      e.cfg.Station.Callsign != "",
		RestartRequired: !sameWiring(e.cfg.Station.Radio.Wiring(), e.o.RadioConfig),
	}
	if e.sx.idT.owed {
		st.ID.Due = e.sx.idT.deadline
	}
	in := e.active
	as := e.modes[mode.AllStar]
	if in != nil {
		st.Active, st.Since = in.name, e.since
	}
	e.mu.Unlock()
	if in != nil {
		ms := in.m.Status()
		ms.Active = true
		st.Mode = &ms
	}
	if as != nil {
		st.Registration = as.m.Status().Registration
	}
	return st
}

// ModeInfo describes one mode for the settings UI.
type ModeInfo struct {
	Name        string       `json:"name"`
	Title       string       `json:"title"`
	TargetLabel string       `json:"targetLabel,omitempty"`
	TargetHelp  string       `json:"targetHelp,omitempty"`
	NeedsHost   bool         `json:"needsHost,omitempty"`
	Fields      []mode.Field `json:"fields"`
	// Settings are the saved settings with every secret blank; SecretsSet
	// says which secrets have a value.
	Settings   mode.Settings   `json:"settings"`
	SecretsSet map[string]bool `json:"secretsSet,omitempty"`
	Configured bool            `json:"configured"`
	Active     bool            `json:"active"`
	Status     *mode.Status    `json:"status,omitempty"`
	Error      string          `json:"error,omitempty"` // why it could not be built
}

// Modes lists every mode with its settings. Passwords are never returned.
func (e *Engine) Modes() []ModeInfo {
	e.mu.Lock()
	cfg := e.cfg
	built := make(map[string]*instance, len(e.modes))
	for k, v := range e.modes {
		built[k] = v
	}
	active := e.active
	errs := make(map[string]string, len(e.buildErr))
	for k, v := range e.buildErr {
		errs[k] = v
	}
	e.mu.Unlock()
	var out []ModeInfo
	for _, sp := range mode.Specs() {
		s, ok := cfg.Modes[sp.Name]
		red, set := sp.Redact(s)
		mi := ModeInfo{
			Name: sp.Name, Title: sp.Title, TargetLabel: sp.TargetLabel,
			TargetHelp: sp.TargetHelp, NeedsHost: sp.NeedsHost, Fields: sp.Fields,
			Settings: red, SecretsSet: set, Configured: ok, Error: errs[sp.Name],
		}
		if in := built[sp.Name]; in != nil {
			ms := in.m.Status()
			mi.Active = in == active
			ms.Active = mi.Active
			mi.Status = &ms
		}
		out = append(out, mi)
	}
	return out
}

// SaveStation replaces the station settings (but not the active mode,
// which Select manages), saves them and applies them. When the radio's
// channel changed and a radio is present, the SA818 is programmed.
func (e *Engine) SaveStation(st Station) error {
	e.opMu.Lock()
	defer e.opMu.Unlock()
	e.mu.Lock()
	c := e.cfg.Clone()
	e.mu.Unlock()
	old := c.Station
	st.Active = old.Active
	c.Station = st
	c.Normalize()
	if err := c.Validate(); err != nil {
		return err
	}
	if err := e.commit(c, true); err != nil {
		return err
	}
	var errs []error
	if !sameWiring(old.Radio.Wiring(), c.Station.Radio.Wiring()) && e.o.RadioChanged != nil {
		go e.o.RadioChanged()
	}
	if !reflect.DeepEqual(old.Radio, c.Station.Radio) {
		if err := e.applyVolumes(c.Station.Radio); err != nil {
			errs = append(errs, err)
		}
		if channelChanged(old.Radio, c.Station.Radio) && c.Station.Radio.FrequencyMHz != 0 && !e.o.RadioAbsent {
			if err := e.program(c.Station.Radio); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// sameWiring compares the parts of two radio configs that Wiring sets.
func sameWiring(a, b radio.Config) bool {
	return a.PTTGPIO == b.PTTGPIO && a.PTTActiveLow == b.PTTActiveLow &&
		a.COSActiveLow == b.COSActiveLow && a.SA818Port == b.SA818Port
}

func channelChanged(a, b RadioConfig) bool {
	return a.FrequencyMHz != b.FrequencyMHz || a.Tone != b.Tone ||
		a.SquelchLevel() != b.SquelchLevel() || a.Narrow != b.Narrow || a.SA818Port != b.SA818Port
}

// SaveMode merges upd into mode n's saved settings (a blank secret keeps
// the saved one), checks, saves and applies them. The active mode is
// restarted on its target when its settings changed.
func (e *Engine) SaveMode(n string, upd mode.Settings) error {
	n = strings.ToLower(strings.TrimSpace(n))
	sp, ok := mode.Lookup(n)
	if !ok {
		return fmt.Errorf("mode %q is not a known mode", n)
	}
	e.opMu.Lock()
	defer e.opMu.Unlock()
	e.mu.Lock()
	c := e.cfg.Clone()
	e.mu.Unlock()
	s := sp.Merge(c.Modes[n], upd)
	if err := sp.Check(s); err != nil {
		return err
	}
	c.Modes[n] = s
	return e.commit(c, true)
}

// DeleteMode forgets mode n's settings. An active mode is dropped first.
func (e *Engine) DeleteMode(ctx context.Context, n string) error {
	n = strings.ToLower(strings.TrimSpace(n))
	e.opMu.Lock()
	defer e.opMu.Unlock()
	e.mu.Lock()
	act := e.active != nil && e.active.name == n
	_, had := e.cfg.Modes[n]
	e.mu.Unlock()
	if !had {
		return fmt.Errorf("mode %q has no saved settings", n)
	}
	if act {
		if err := e.selectLocked(ctx, "", nil, true); err != nil {
			return err
		}
	}
	e.mu.Lock()
	c := e.cfg.Clone()
	e.mu.Unlock()
	delete(c.Modes, n)
	return e.commit(c, true)
}

// Reload reads the config file again and applies it. A bad file is
// reported and the running config is kept.
func (e *Engine) Reload(ctx context.Context) error {
	if e.o.ConfigPath == "" {
		return errors.New("no config file")
	}
	c, err := LoadConfig(e.o.ConfigPath)
	if err != nil {
		return err
	}
	e.opMu.Lock()
	defer e.opMu.Unlock()
	e.mu.Lock()
	same := reflect.DeepEqual(c, e.cfg)
	cur := ""
	if e.active != nil {
		cur = e.active.name
	}
	oldRadio := e.cfg.Station.Radio
	e.cfg = c.Clone()
	e.params = paramsFor(c)
	e.mu.Unlock()
	if same {
		return nil
	}
	e.applyModes(c)
	var errs []error
	if !reflect.DeepEqual(oldRadio, c.Station.Radio) {
		errs = append(errs, e.applyVolumes(c.Station.Radio))
	}
	want := ""
	var t *mode.Target
	if a := c.Station.Active; a != nil {
		want, t = a.Mode, a.Target
	}
	if want != cur {
		errs = append(errs, e.selectLocked(ctx, want, t, false))
	}
	e.hub.publish(Event{Kind: EventConfig, At: e.now(), Text: "reloaded"})
	return errors.Join(errs...)
}

// Tune sets the sound chip's mixer (0 to 1 each; nil keeps a level) and
// saves the levels.
func (e *Engine) Tune(rx, tx *float64) error {
	for _, v := range []*float64{rx, tx} {
		if v != nil && (*v < 0 || *v > 1) {
			return errors.New("levels must be 0 to 1")
		}
	}
	e.opMu.Lock()
	defer e.opMu.Unlock()
	e.mu.Lock()
	c := e.cfg.Clone()
	e.mu.Unlock()
	if rx != nil {
		v := *rx
		c.Station.Radio.RxVolume = &v
	}
	if tx != nil {
		v := *tx
		c.Station.Radio.TxVolume = &v
	}
	if err := e.applyVolumes(c.Station.Radio); err != nil {
		return err
	}
	return e.commit(c, false)
}

func (e *Engine) applyVolumes(r RadioConfig) error {
	if r.RxVolume == nil && r.TxVolume == nil {
		return nil
	}
	cur := e.o.Radio.Levels()
	rx, tx := cur.CaptureVolume, cur.PlaybackVolume
	if r.RxVolume != nil {
		rx = *r.RxVolume
	}
	if r.TxVolume != nil {
		tx = *r.TxVolume
	}
	if err := e.o.Radio.SetVolumes(rx, tx); err != nil {
		return fmt.Errorf("radio levels: %w", err)
	}
	return nil
}

// Levels returns the radio's levels.
func (e *Engine) Levels() radio.Levels { return e.o.Radio.Levels() }

// ProgramRadio programs the SA818 with the station's channel.
func (e *Engine) ProgramRadio() error {
	if e.o.RadioAbsent {
		return errors.New("no radio is connected")
	}
	e.mu.Lock()
	r := e.cfg.Station.Radio
	e.mu.Unlock()
	return e.program(r)
}

// ProgramIfChanged programs the SA818 at boot when the station's channel
// differs from what the module was last programmed with.
func (e *Engine) ProgramIfChanged() error {
	e.mu.Lock()
	r := e.cfg.Station.Radio
	e.mu.Unlock()
	if e.o.RadioAbsent || r.FrequencyMHz == 0 {
		return nil
	}
	want, err := r.SA818()
	if err != nil {
		return err
	}
	if st, err := radio.SavedSA818(e.o.RadioConfig); err == nil && sameChannel(st.Settings, want) {
		return nil
	}
	return e.program(r)
}

func sameChannel(a, b sa818.Settings) bool {
	return a.RxMHz == b.RxMHz && a.TxMHz == b.TxMHz && a.RxTone == b.RxTone &&
		a.TxTone == b.TxTone && a.Squelch == b.Squelch && a.Bandwidth == b.Bandwidth
}

func (e *Engine) program(r RadioConfig) error {
	s, err := r.SA818()
	if err != nil {
		return fmt.Errorf("radio: %w", err)
	}
	rc := e.o.RadioConfig
	if r.SA818Port != "" {
		rc.SA818Port = r.SA818Port
	}
	if err := e.o.Program(rc, s); err != nil {
		return fmt.Errorf("programming the SA818: %w", err)
	}
	e.log.Info("programmed the SA818", "mhz", s.RxMHz, "squelch", s.Squelch)
	return nil
}

// WatchConfig reloads the config whenever the file's modification time
// changes, checking every interval, until ctx ends.
func (e *Engine) WatchConfig(ctx context.Context, interval time.Duration) {
	if e.o.ConfigPath == "" {
		return
	}
	mtime := func() time.Time {
		fi, err := os.Stat(e.o.ConfigPath)
		if err != nil {
			return time.Time{}
		}
		return fi.ModTime()
	}
	last := mtime()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m := mtime()
			if m.Equal(last) {
				continue
			}
			last = m
			if err := e.Reload(ctx); err != nil {
				e.logf(e.now(), "config reload: %v", err)
			}
		}
	}
}
