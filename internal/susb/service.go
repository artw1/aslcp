package susb

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/artw1/aslcp/internal/ami"
)

// Commander runs one Asterisk CLI command and returns its output lines.
type Commander interface {
	Command(ctx context.Context, cmd string) ([]string, error)
}

// Device is one SimpleUSB device (a stanza of simpleusb.conf).
type Device struct {
	Name string `json:"name"`
	// Node is the node number whose rxchannel uses the device ("" if no
	// node does).
	Node string `json:"node,omitempty"`
	// USBPath and Card are the device's assignment, when known.
	USBPath string `json:"usbPath,omitempty"`
	Card    int    `json:"card"`
}

// State is the full state of one device as the page shows it.
type State struct {
	Device   string   `json:"device"`
	Node     string   `json:"node,omitempty"`
	Params   Params   `json:"params"`
	Saved    Params   `json:"saved"`
	Dirty    []string `json:"dirty"`
	Identity Identity `json:"identity"`
	// Echo is the echo mode state with its automatic switch-off time.
	Echo EchoState `json:"echoState"`
}

// EchoState describes echo mode.
type EchoState struct {
	On bool `json:"on"`
	// ExpiresAt is when echo mode is switched off automatically (zero when off).
	ExpiresAt time.Time `json:"expiresAt,omitempty"`
}

// Applied is one field that was set.
type Applied struct {
	Field string `json:"field"`
	Reply string `json:"reply"`
}

// Failed is one field that could not be set.
type Failed struct {
	Field   string `json:"field"`
	Message string `json:"message"`
	Reply   string `json:"reply,omitempty"`
}

// ApplyResult is the outcome of a parameter update.
type ApplyResult struct {
	Applied []Applied `json:"applied"`
	Errors  []Failed  `json:"errors"`
	Params  Params    `json:"params"`
	Dirty   []string  `json:"dirty"`
}

// MeterReading is a meter value with its age.
type MeterReading struct {
	Meter
	At time.Time `json:"at"`
	// Stale is true when the reading was served from the cache because the
	// radio interface was busy.
	Stale bool `json:"stale,omitempty"`
}

// SaveResult is the outcome of a save.
type SaveResult struct {
	Saved   bool     `json:"saved"`
	Params  Params   `json:"params"`
	Changed []string `json:"changed"`
	// Commit is the config history commit, when the file was recorded.
	Commit string `json:"commit,omitempty"`
	// Warning explains a partial success (the history record failed, or the
	// file does not show every running value).
	Warning string `json:"warning,omitempty"`
}

// Service tunes SimpleUSB devices.
type Service struct {
	AMI      Commander
	Config   ConfigSource // may be nil: no node mapping, no saved values
	Recorder Recorder     // may be nil: saves are not recorded in the history
	Logger   *log.Logger
	Now      func() time.Time

	// CmdTimeout bounds a command.
	CmdTimeout time.Duration
	// EchoTimeout switches echo mode off after this long; EchoIdle switches
	// it off when no page has asked for the device for this long.
	EchoTimeout time.Duration
	EchoIdle    time.Duration
	// MeterMinGap is the shortest time between two reads of the meter; faster
	// requests (other tabs) are served from the last reading.
	MeterMinGap time.Duration
	// DeviceTTL is how long the device list is cached.
	DeviceTTL time.Duration

	mu sync.Mutex // serialises every tune operation (the driver has one active device)

	stMu     sync.Mutex
	devs     []Device
	devsAt   time.Time
	baseline map[string]Params
	meter    map[string]MeterReading
	echo     map[string]*echoRun
	touched  map[string]time.Time
	closed   bool
}

type echoRun struct {
	until time.Time
	stop  chan struct{}
}

// New returns a Service with the default limits.
func New(c Commander) *Service {
	return &Service{
		AMI: c, Now: time.Now,
		CmdTimeout:  10 * time.Second,
		EchoTimeout: 10 * time.Minute, EchoIdle: 30 * time.Second,
		MeterMinGap: 700 * time.Millisecond,
		DeviceTTL:   5 * time.Second,
		baseline:    map[string]Params{}, meter: map[string]MeterReading{}, echo: map[string]*echoRun{},
		touched: map[string]time.Time{},
	}
}

func (s *Service) logf(format string, a ...any) {
	if s.Logger != nil {
		s.Logger.Printf(format, a...)
	}
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// run sends one command from the closed set and returns the cleaned lines.
func (s *Service) run(ctx context.Context, cmd string, timeout time.Duration) ([]string, error) {
	if !Allowed(cmd) {
		return nil, errf(CodeInvalid, "command not allowed: %q", cmd)
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	lines, err := s.AMI.Command(ctx, cmd)
	if err != nil {
		switch {
		case errors.Is(err, ami.ErrNotConnected), errors.Is(err, ami.ErrClosed):
			return nil, errf(CodeUnavailable, "Asterisk is not connected")
		case errors.Is(err, context.DeadlineExceeded):
			return nil, errf(CodeUnavailable, "Asterisk did not answer in time")
		case errors.Is(err, context.Canceled):
			return nil, err
		}
		return nil, errf(CodeCommandFailed, "%v", err)
	}
	lines = clean(lines)
	if len(lines) > 0 && reNoCmd.MatchString(lines[0]) {
		return nil, errf(CodeNoDriver, "chan_simpleusb is not available (%s)", first(lines))
	}
	return lines, nil
}

// ---------------------------------------------------------------- devices

// Devices lists the SimpleUSB devices. The list is cached briefly.
func (s *Service) Devices(ctx context.Context) ([]Device, error) {
	s.stMu.Lock()
	if s.devs != nil && s.now().Sub(s.devsAt) < s.DeviceTTL {
		d := append([]Device(nil), s.devs...)
		s.stMu.Unlock()
		return d, nil
	}
	s.stMu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.devicesLocked(ctx)
}

func (s *Service) devicesLocked(ctx context.Context) ([]Device, error) {
	lines, err := s.run(ctx, cmdDevices, s.CmdTimeout)
	if err != nil {
		return nil, err
	}
	names, err := ParseDevices(lines)
	if err != nil {
		return nil, err
	}
	devs := make([]Device, 0, len(names))
	for _, n := range names {
		devs = append(devs, Device{Name: n, Card: -1})
	}
	if len(devs) > 0 {
		// Labels only: failures here never fail the listing.
		if al, err := s.run(ctx, cmdActiveShow, s.CmdTimeout); err == nil {
			for _, a := range ParseAssignments(al) {
				for i := range devs {
					if devs[i].Name == a.Device {
						devs[i].USBPath, devs[i].Card = a.Path, a.Card
					}
				}
			}
		}
		if s.Config != nil {
			if m, err := deviceNodes(ctx, s.Config); err == nil {
				for i := range devs {
					devs[i].Node = m[devs[i].Name]
				}
			} else {
				s.logf("susb: reading rpt.conf: %v", err)
			}
		}
	}
	s.stMu.Lock()
	s.devs, s.devsAt = append([]Device(nil), devs...), s.now()
	s.stMu.Unlock()
	return devs, nil
}

// device validates dev against the current list (refreshing it when stale).
// The caller holds s.mu.
func (s *Service) deviceLocked(ctx context.Context, dev string) (Device, error) {
	if !ValidDevice(dev) {
		return Device{}, errf(CodeUnknownDevice, "unknown device")
	}
	s.stMu.Lock()
	fresh := s.devs != nil && s.now().Sub(s.devsAt) < s.DeviceTTL
	list := append([]Device(nil), s.devs...)
	s.stMu.Unlock()
	if !fresh {
		var err error
		if list, err = s.devicesLocked(ctx); err != nil {
			return Device{}, err
		}
	}
	for _, d := range list {
		if d.Name == dev {
			return d, nil
		}
	}
	return Device{}, errf(CodeUnknownDevice, "unknown device")
}

// withDevice makes dev the active device (and checks the reply), runs fn and,
// when restore is set, puts the previously active device back. The caller
// holds s.mu.
func (s *Service) withDevice(ctx context.Context, dev string, restore bool, fn func() error) error {
	prev := ""
	if restore {
		if lines, err := s.run(ctx, cmdActive, s.CmdTimeout); err == nil {
			prev, _ = ParseActive(lines)
		}
	}
	lines, err := s.run(ctx, cmdActive+" "+dev, s.CmdTimeout)
	if err != nil {
		return err
	}
	if err := VerifySetActive(dev, lines); err != nil {
		return err
	}
	ferr := fn()
	if restore && prev != "" && prev != dev && ValidDevice(prev) {
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.CmdTimeout)
		if _, err := s.run(rctx, cmdActive+" "+prev, 0); err != nil {
			s.logf("susb: restoring the active device %q: %v", prev, err)
		}
		cancel()
	}
	return ferr
}

// readParams reads and parses the 16-field block of the active device.
func (s *Service) readParams(ctx context.Context) (Params, error) {
	lines, err := s.run(ctx, cmdParams, s.CmdTimeout)
	if err != nil {
		return Params{}, err
	}
	return ParseParams(lines)
}

// ---------------------------------------------------------------- read

// Read returns the running parameters of dev with the saved values and the
// list of unsaved differences. The first read of a device after ASLCP
// started fixes the baseline that Revert returns to: the running values,
// overlaid with what simpleusb.conf defines, so tuning that was never saved
// before ASLCP started still shows as unsaved.
func (s *Service) Read(ctx context.Context, dev string) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.deviceLocked(ctx, dev)
	if err != nil {
		return State{}, err
	}
	var st State
	st.Device, st.Node = dev, d.Node
	err = s.withDevice(ctx, dev, true, func() error {
		lines, err := s.run(ctx, cmdIdentity, s.CmdTimeout)
		if err != nil {
			return err
		}
		if st.Identity, err = ParseIdentity(dev, lines); err != nil {
			return err
		}
		st.Params, err = s.readParams(ctx)
		return err
	})
	if err != nil {
		return State{}, err
	}
	s.stMu.Lock()
	base, ok := s.baseline[dev]
	s.stMu.Unlock()
	if !ok {
		base = st.Params
		if s.Config != nil {
			if p, _, err := saved(ctx, s.Config, dev, st.Params); err == nil {
				base = p
			} else {
				s.logf("susb: reading %s: %v", SimpleUSBConf, err)
			}
		}
		s.stMu.Lock()
		s.baseline[dev] = base
		s.stMu.Unlock()
	}
	st.Saved, st.Dirty = base, Diff(st.Params, base)
	if st.Dirty == nil {
		st.Dirty = []string{}
	}
	s.adoptEcho(dev, st.Params.Echo)
	st.Echo = s.echoState(dev)
	return st, nil
}

// ---------------------------------------------------------------- apply

// Apply validates the patch and sets each field with its own command. Fields
// that fail are reported; the others are still applied.
func (s *Service) Apply(ctx context.Context, dev string, p Patch) (ApplyResult, error) {
	specs, err := p.Specs(dev)
	if err != nil {
		return ApplyResult{}, err
	}
	if len(specs) == 0 {
		return ApplyResult{}, errf(CodeInvalid, "nothing to change")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.deviceLocked(ctx, dev); err != nil {
		return ApplyResult{}, err
	}
	return s.applyLocked(ctx, dev, specs)
}

func (s *Service) applyLocked(ctx context.Context, dev string, specs []setSpec) (ApplyResult, error) {
	res := ApplyResult{Applied: []Applied{}, Errors: []Failed{}}
	err := s.withDevice(ctx, dev, true, func() error {
		for _, sp := range specs {
			lines, err := s.run(ctx, sp.cmd, s.CmdTimeout)
			if err != nil {
				var e *Error
				if errors.As(err, &e) && (e.Code == CodeUnavailable || e.Code == CodeNoDriver) {
					return err // the connection is gone: stop
				}
				if errors.Is(err, context.Canceled) {
					return err
				}
				res.Errors = append(res.Errors, Failed{Field: sp.field, Message: err.Error()})
				continue
			}
			reply, err := sp.VerifyReply(lines)
			if err != nil {
				res.Errors = append(res.Errors, Failed{Field: sp.field, Message: err.Error(), Reply: reply})
				continue
			}
			res.Applied = append(res.Applied, Applied{Field: sp.field, Reply: reply})
		}
		p, err := s.readParams(ctx)
		if err != nil {
			return err
		}
		res.Params = p
		return nil
	})
	if err != nil {
		return ApplyResult{}, err
	}
	s.stMu.Lock()
	base, ok := s.baseline[dev]
	s.stMu.Unlock()
	res.Dirty = []string{}
	if ok {
		res.Dirty = append(res.Dirty, Diff(res.Params, base)...)
	}
	return res, nil
}

// Revert sets every tuning value back to the baseline (the values at the
// last save, or the first read after ASLCP started). It does not use
// "susb tune load", which only reads the interface EEPROM.
func (s *Service) Revert(ctx context.Context, dev string) (ApplyResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.deviceLocked(ctx, dev); err != nil {
		return ApplyResult{}, err
	}
	s.stMu.Lock()
	base, ok := s.baseline[dev]
	s.stMu.Unlock()
	if !ok {
		return ApplyResult{}, errf(CodeNoBaseline, "no saved values are known for this device yet; open the device first")
	}
	specs, err := PatchFrom(base).Specs(dev)
	if err != nil {
		return ApplyResult{}, err
	}
	return s.applyLocked(ctx, dev, specs)
}

// ---------------------------------------------------------------- meter

// Touch records that a page is looking at dev (it keeps echo mode on).
func (s *Service) Touch(dev string) {
	s.stMu.Lock()
	s.touched[dev] = s.now()
	s.stMu.Unlock()
}

// Meter reads the receive audio statistics of dev. Requests closer together
// than MeterMinGap, and requests that arrive while another operation holds the radio interface, are answered from the
// last reading.
func (s *Service) Meter(ctx context.Context, dev string) (MeterReading, error) {
	s.Touch(dev)
	s.stMu.Lock()
	last, have := s.meter[dev]
	s.stMu.Unlock()
	if have && s.now().Sub(last.At) < s.MeterMinGap {
		return last, nil
	}
	if !s.mu.TryLock() {
		if have && s.now().Sub(last.At) < 30*time.Second {
			last.Stale = true
			return last, nil
		}
		return MeterReading{}, errf(CodeUnavailable, "the radio interface is busy")
	}
	defer s.mu.Unlock()
	if _, err := s.deviceLocked(ctx, dev); err != nil {
		return MeterReading{}, err
	}
	var m Meter
	// No restore of the previously active device: the meter polls every
	// second and the restore would triple the commands.
	err := s.withDevice(ctx, dev, false, func() error {
		lines, err := s.run(ctx, cmdMeter, s.CmdTimeout)
		if err != nil {
			return err
		}
		m, err = ParseMeter(lines)
		return err
	})
	if err != nil {
		return MeterReading{}, err
	}
	r := MeterReading{Meter: m, At: s.now()}
	s.stMu.Lock()
	s.meter[dev] = r
	s.stMu.Unlock()
	return r, nil
}

// ---------------------------------------------------------------- echo

// EchoState returns the echo state of dev.
func (s *Service) echoState(dev string) EchoState {
	s.stMu.Lock()
	defer s.stMu.Unlock()
	if r, ok := s.echo[dev]; ok {
		return EchoState{On: true, ExpiresAt: r.until}
	}
	return EchoState{}
}

// EchoStatus returns the echo state of dev as tracked by the service.
func (s *Service) EchoStatus(dev string) EchoState { return s.echoState(dev) }

// SetEcho switches echo mode on or off. Echo mode is runtime only and is
// never saved on: it is switched off automatically after EchoTimeout, when no
// page asks for the device for EchoIdle, before a save and when the service
// closes.
func (s *Service) SetEcho(ctx context.Context, dev string, on bool) (EchoState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.deviceLocked(ctx, dev); err != nil {
		return EchoState{}, err
	}
	if err := s.setEchoLocked(ctx, dev, on); err != nil {
		return EchoState{}, err
	}
	return s.echoState(dev), nil
}

func (s *Service) setEchoLocked(ctx context.Context, dev string, on bool) error {
	sp := echoSpec(on)
	err := s.withDevice(ctx, dev, true, func() error {
		lines, err := s.run(ctx, sp.cmd, s.CmdTimeout)
		if err != nil {
			return err
		}
		_, err = sp.VerifyReply(lines)
		return err
	})
	if err != nil {
		return err
	}
	if on {
		s.trackEcho(dev)
	} else {
		s.untrackEcho(dev)
	}
	return nil
}

func (s *Service) trackEcho(dev string) {
	s.stMu.Lock()
	defer s.stMu.Unlock()
	if s.closed {
		return
	}
	until := s.now().Add(s.EchoTimeout)
	if r, ok := s.echo[dev]; ok {
		r.until = until
		return
	}
	r := &echoRun{until: until, stop: make(chan struct{})}
	s.echo[dev] = r
	s.touched[dev] = s.now()
	go s.watchEcho(dev, r)
}

func (s *Service) untrackEcho(dev string) {
	s.stMu.Lock()
	defer s.stMu.Unlock()
	if r, ok := s.echo[dev]; ok {
		close(r.stop)
		delete(s.echo, dev)
	}
}

// adoptEcho puts echo mode that is on in the driver, but unknown to the
// service (ASLCP restarted, or someone used the terminal), under the same
// automatic switch-off.
func (s *Service) adoptEcho(dev string, on bool) {
	if on {
		s.stMu.Lock()
		_, known := s.echo[dev]
		s.stMu.Unlock()
		if !known {
			s.trackEcho(dev)
		}
	} else {
		s.untrackEcho(dev)
	}
}

func (s *Service) watchEcho(dev string, r *echoRun) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-tick.C:
		}
		s.stMu.Lock()
		due := !s.now().Before(r.until)
		idle := s.EchoIdle > 0 && s.now().Sub(s.touched[dev]) > s.EchoIdle
		s.stMu.Unlock()
		if !due && !idle {
			continue
		}
		why := "timeout"
		if !due {
			why = "page closed"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*s.CmdTimeout)
		s.mu.Lock()
		select {
		case <-r.stop: // switched off while waiting for the lock
			s.mu.Unlock()
			cancel()
			return
		default:
		}
		err := s.setEchoLocked(ctx, dev, false)
		s.mu.Unlock()
		cancel()
		if err == nil {
			s.logf("susb: echo mode on %s switched off (%s)", dev, why)
			return
		}
		s.logf("susb: switching echo mode off on %s failed (%s), retrying: %v", dev, why, err)
	}
}

// Close switches off every echo mode that is on. It is meant for shutdown.
func (s *Service) Close(ctx context.Context) {
	s.stMu.Lock()
	s.closed = true
	var devs []string
	for d := range s.echo {
		devs = append(devs, d)
	}
	s.stMu.Unlock()
	for _, d := range devs {
		s.mu.Lock()
		if err := s.setEchoLocked(ctx, d, false); err != nil {
			s.logf("susb: switching echo mode off on %s at shutdown: %v", d, err)
		}
		s.mu.Unlock()
	}
}

func hasLine(lines []string, sub string) bool {
	for _, l := range lines {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- save

var reAuthorChars = regexp.MustCompile(`[^A-Za-z0-9._-]`)

func authorName(user string) string {
	a := reAuthorChars.ReplaceAllString(user, "_")
	if len(a) > 64 {
		a = a[:64]
	}
	if a == "" {
		return "aslcp"
	}
	return a
}

// Save writes the running values to simpleusb.conf with "susb tune save"
// (Asterisk rewrites the file and, for interfaces with an EEPROM, the
// EEPROM), checks the file against the running values and records the file
// in the config history. Echo mode is switched off first and is never saved.
func (s *Service) Save(ctx context.Context, dev, user string) (SaveResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.deviceLocked(ctx, dev); err != nil {
		return SaveResult{}, err
	}
	if s.echoState(dev).On {
		if err := s.setEchoLocked(ctx, dev, false); err != nil {
			return SaveResult{}, fmt.Errorf("switching echo mode off before saving: %w", err)
		}
	}
	var res SaveResult
	err := s.withDevice(ctx, dev, true, func() error {
		before, err := s.readParams(ctx)
		if err != nil {
			return err
		}
		s.stMu.Lock()
		base, ok := s.baseline[dev]
		s.stMu.Unlock()
		if ok {
			res.Changed = Diff(before, base)
		}
		lines, err := s.run(ctx, cmdSave, s.CmdTimeout)
		if err != nil {
			return err
		}
		if !hasLine(lines, "Saved radio tuning settings") {
			return errf(CodeCommandFailed, "the radio did not confirm the save: %s", first(lines))
		}
		res.Saved = true
		res.Params, err = s.readParams(ctx)
		return err
	})
	if err != nil {
		return res, err
	}
	if res.Changed == nil {
		res.Changed = []string{}
	}
	s.stMu.Lock()
	s.baseline[dev] = res.Params
	s.stMu.Unlock()
	var warn []string
	if s.Config != nil {
		if p, have, err := saved(ctx, s.Config, dev, res.Params); err != nil {
			warn = append(warn, "could not read simpleusb.conf to check the save: "+err.Error())
		} else if d := Diff(res.Params, p); len(d) > 0 {
			warn = append(warn, "simpleusb.conf does not show the running value of "+strings.Join(d, ", ")+"; check the Asterisk log")
		} else if len(have) == 0 {
			warn = append(warn, "simpleusb.conf has no section for "+dev)
		}
	}
	if s.Recorder != nil {
		msg := "Radio tuning saved from web UI (device " + dev + ")"
		if r, err := s.Recorder.ConfigRecord(ctx, SimpleUSBConf, msg, authorName(user)); err != nil {
			warn = append(warn, "saved, but not recorded in the config history: "+err.Error())
		} else {
			res.Commit = r.Commit
		}
	}
	res.Warning = strings.Join(warn, "; ")
	return res, nil
}
