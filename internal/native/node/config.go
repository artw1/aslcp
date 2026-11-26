package node

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/artw1/aslcp/internal/native/audio"
	"github.com/artw1/aslcp/internal/native/mode"
	"github.com/artw1/aslcp/internal/native/radio/sa818"
)

// DefaultConfigPath is where the daemon keeps its config.
const DefaultConfigPath = "/etc/aslcp/node.json"

// ConfigVersion is the version written to new files.
const ConfigVersion = 1

// Config is the node config file: the station and each mode's settings.
type Config struct {
	Version int                      `json:"version"`
	Station Station                  `json:"station"`
	Modes   map[string]mode.Settings `json:"modes,omitempty"`
}

// Station is everything about the station that is not a mode.
type Station struct {
	// Callsign is the station callsign, sent in the ID and to the modes.
	Callsign string      `json:"callsign,omitempty"`
	Radio    RadioConfig `json:"radio"`
	ID       IDConfig    `json:"id"`
	// Active is the mode and target restored at boot.
	Active   *Selection `json:"active,omitempty"`
	Advanced Advanced   `json:"advanced"`
}

// Selection is a mode and, optionally, its target.
type Selection struct {
	Mode   string       `json:"mode"`
	Target *mode.Target `json:"target,omitempty"`
}

// RadioConfig is the SHARI radio: the SA818 channel and the sound chip.
type RadioConfig struct {
	// FrequencyMHz is the simplex channel. 0 leaves the module as it is.
	FrequencyMHz float64 `json:"frequencyMHz,omitempty"`
	// Tone is the CTCSS or DCS tone both ways: "" (none), "100.0", "D023N"
	// or "D023I".
	Tone string `json:"tone,omitempty"`
	// Squelch is the SA818 squelch level, 0 to 8 (default 4).
	Squelch *int `json:"squelch,omitempty"`
	// Narrow selects 12.5 kHz channels.
	Narrow bool `json:"narrow,omitempty"`
	// RxVolume and TxVolume are the sound chip mixer positions, 0 to 1.
	RxVolume *float64 `json:"rxVolume,omitempty"`
	TxVolume *float64 `json:"txVolume,omitempty"`
	// PTTGPIO, PTTActiveLow and COSActiveLow override the SHARI wiring.
	PTTGPIO      int    `json:"pttGpio,omitempty"`
	PTTActiveLow bool   `json:"pttActiveLow,omitempty"`
	COSActiveLow bool   `json:"cosActiveLow,omitempty"`
	SA818Port    string `json:"sa818Port,omitempty"`
}

// IDConfig is the station ID sent on the radio. It is never sent to a mode.
type IDConfig struct {
	// Enabled turns the ID on (the default) or off.
	Enabled *bool `json:"enabled,omitempty"`
	// IntervalS is the longest time from the first transmission to the ID,
	// at most 600 (the default).
	IntervalS int `json:"intervalS,omitempty"`
	// TailWindowS: within this long before the deadline, the ID rides the
	// tail of a transmission (default 60).
	TailWindowS int `json:"tailWindowS,omitempty"`
	// EndQuietS: the final ID plays after this long without transmitting
	// (default 60).
	EndQuietS int     `json:"endQuietS,omitempty"`
	WPM       int     `json:"wpm,omitempty"`     // CW speed (default 25)
	PitchHz   float64 `json:"pitchHz,omitempty"` // CW pitch (default 800)
	LevelDb   float64 `json:"levelDb,omitempty"` // CW level, dBFS (default -24)
	// VoiceFile is an optional 8 kHz mono WAV played instead of CW.
	VoiceFile string `json:"voiceFile,omitempty"`
}

// Advanced holds tuning most stations leave alone.
type Advanced struct {
	HangMS       int   `json:"hangMs,omitempty"`       // transmitter hang (default 500)
	TxTimeoutS   int   `json:"txTimeoutS,omitempty"`   // longest transmission either way (default 180)
	CourtesyTone *bool `json:"courtesyTone,omitempty"` // tone after a local transmission (default on)
	// AGC on the mode's audio for the radio (default on) and on the
	// radio's audio for the mode (default off).
	ModeAGC   *bool   `json:"modeAgc,omitempty"`
	RadioAGC  *bool   `json:"radioAgc,omitempty"`
	AGCTarget float64 `json:"agcTargetDb,omitempty"` // dBFS (default -22)
	AGCMax    float64 `json:"agcMaxDb,omitempty"`    // dB (default 18)
	// MD380 is the md380-emu address for AMBE (default 127.0.0.1:2470).
	MD380          string `json:"md380,omitempty"`
	MD380TimeoutMS int    `json:"md380TimeoutMs,omitempty"`
}

// Defaults.
const (
	DefaultHang        = 500 * time.Millisecond
	DefaultTxTimeout   = 180 * time.Second
	DefaultIDInterval  = 600 * time.Second
	DefaultIDTail      = 60 * time.Second
	DefaultIDEndQuiet  = 60 * time.Second
	DefaultIDWPM       = 25
	DefaultIDPitchHz   = 800
	DefaultIDLevelDb   = -24
	DefaultAGCTargetDb = -22
	DefaultAGCMaxDb    = 18
	DefaultSquelch     = 4
)

func on(b *bool, def bool) bool {
	if b == nil {
		return def
	}
	return *b
}

func secs(v int, def time.Duration) time.Duration {
	if v <= 0 {
		return def
	}
	return time.Duration(v) * time.Second
}

// IDOn reports whether the station ID is on.
func (c IDConfig) IDOn() bool { return on(c.Enabled, true) }

// Interval is the ID deadline after the first transmission.
func (c IDConfig) Interval() time.Duration { return secs(c.IntervalS, DefaultIDInterval) }

// TailWindow is how early the ID may ride a tail.
func (c IDConfig) TailWindow() time.Duration { return secs(c.TailWindowS, DefaultIDTail) }

// EndQuiet is the quiet time before the final ID.
func (c IDConfig) EndQuiet() time.Duration { return secs(c.EndQuietS, DefaultIDEndQuiet) }

// CW returns the CW options.
func (c IDConfig) CW() audio.CWOptions {
	o := audio.CWOptions{WPM: c.WPM, PitchHz: c.PitchHz, LevelDb: c.LevelDb}
	if o.WPM == 0 {
		o.WPM = DefaultIDWPM
	}
	if o.PitchHz == 0 {
		o.PitchHz = DefaultIDPitchHz
	}
	if o.LevelDb == 0 {
		o.LevelDb = DefaultIDLevelDb
	}
	return o
}

// Hang is the transmitter hang time.
func (a Advanced) Hang() time.Duration {
	if a.HangMS <= 0 {
		return DefaultHang
	}
	return time.Duration(a.HangMS) * time.Millisecond
}

// TxTimeout is the longest transmission.
func (a Advanced) TxTimeout() time.Duration { return secs(a.TxTimeoutS, DefaultTxTimeout) }

// Courtesy reports whether the courtesy tone is on.
func (a Advanced) Courtesy() bool { return on(a.CourtesyTone, true) }

// AGC returns the AGC parameters and which AGCs are on.
func (a Advanced) AGC() (p audio.AGCParams, modeOn, radioOn bool) {
	p = audio.AGCParams{TargetDb: a.AGCTarget, MaxGainDb: a.AGCMax}
	if p.TargetDb == 0 {
		p.TargetDb = DefaultAGCTargetDb
	}
	if p.MaxGainDb == 0 {
		p.MaxGainDb = DefaultAGCMaxDb
	}
	return p.Clamp(), on(a.ModeAGC, true), on(a.RadioAGC, false)
}

// SquelchLevel is the SA818 squelch.
func (r RadioConfig) SquelchLevel() int {
	if r.Squelch == nil {
		return DefaultSquelch
	}
	return *r.Squelch
}

var (
	callRE = regexp.MustCompile(`^[A-Z0-9]{1,3}[0-9][A-Z0-9]{0,4}[A-Z](/[A-Z0-9]{1,4})?$`)
	dcsRE  = regexp.MustCompile(`^D([0-7]{3})([NI])$`)
)

// ValidCallsign reports whether s looks like an amateur callsign.
func ValidCallsign(s string) bool { return callRE.MatchString(strings.ToUpper(s)) }

// ParseTone parses the tone setting.
func ParseTone(s string) (sa818.Tone, error) {
	s = strings.ToUpper(strings.TrimSpace(s))
	if s == "" || s == "NONE" {
		return sa818.Tone{}, nil
	}
	if m := dcsRE.FindStringSubmatch(s); m != nil {
		code, _ := strconv.Atoi(m[1])
		return sa818.Tone{Kind: sa818.ToneDCS, DCSCode: code, DCSInverted: m[2] == "I"}, nil
	}
	hz, err := strconv.ParseFloat(s, 64)
	if err == nil {
		for _, t := range sa818.CTCSSTones() {
			if t == hz {
				return sa818.Tone{Kind: sa818.ToneCTCSS, CTCSSHz: hz}, nil
			}
		}
	}
	return sa818.Tone{}, fmt.Errorf("tone %q is not a standard CTCSS tone (such as 100.0) or DCS code (such as D023N)", s)
}

// SA818 returns the module settings for the radio config.
func (r RadioConfig) SA818() (sa818.Settings, error) {
	s := sa818.DefaultSettings()
	if r.FrequencyMHz == 0 {
		return s, errors.New("no frequency set")
	}
	t, err := ParseTone(r.Tone)
	if err != nil {
		return s, err
	}
	s.RxMHz, s.TxMHz = r.FrequencyMHz, r.FrequencyMHz
	s.RxTone, s.TxTone = t, t
	s.Squelch = r.SquelchLevel()
	if r.Narrow {
		s.Bandwidth = sa818.Narrow
	}
	return s, s.Validate()
}

// Normalize cleans up what the operator typed.
func (c *Config) Normalize() {
	if c.Version == 0 {
		c.Version = ConfigVersion
	}
	c.Station.Callsign = strings.ToUpper(strings.TrimSpace(c.Station.Callsign))
	if a := c.Station.Active; a != nil {
		a.Mode = strings.ToLower(strings.TrimSpace(a.Mode))
		if a.Mode == "" {
			c.Station.Active = nil
		}
	}
	for k, s := range c.Modes {
		if nk := strings.ToLower(strings.TrimSpace(k)); nk != k {
			delete(c.Modes, k)
			c.Modes[nk] = s
		}
	}
}

// Validate checks the config and reports every problem in plain words.
func (c *Config) Validate() error {
	var errs []error
	st := c.Station
	if st.Callsign != "" && !ValidCallsign(st.Callsign) {
		errs = append(errs, fmt.Errorf("station callsign %q does not look like a callsign", st.Callsign))
	}
	r := st.Radio
	if r.FrequencyMHz != 0 {
		if _, err := r.SA818(); err != nil {
			errs = append(errs, fmt.Errorf("radio: %w", err))
		}
	} else if _, err := ParseTone(r.Tone); err != nil {
		errs = append(errs, fmt.Errorf("radio: %w", err))
	}
	if r.Squelch != nil && (*r.Squelch < 0 || *r.Squelch > 8) {
		errs = append(errs, errors.New("radio: squelch must be 0 to 8"))
	}
	for name, v := range map[string]*float64{"receive volume": r.RxVolume, "transmit volume": r.TxVolume} {
		if v != nil && (*v < 0 || *v > 1) {
			errs = append(errs, fmt.Errorf("radio: %s must be 0 to 1", name))
		}
	}
	if r.PTTGPIO < 0 || r.PTTGPIO > 4 {
		errs = append(errs, errors.New("radio: PTT GPIO must be 1 to 4"))
	}
	id := st.ID
	if id.IntervalS < 0 || id.IntervalS > 600 {
		errs = append(errs, errors.New("ID: interval must be at most 600 seconds (10 minutes)"))
	}
	if id.TailWindowS < 0 || id.EndQuietS < 0 {
		errs = append(errs, errors.New("ID: times must not be negative"))
	}
	if id.WPM != 0 && (id.WPM < 5 || id.WPM > 60) {
		errs = append(errs, errors.New("ID: CW speed must be 5 to 60 WPM"))
	}
	if id.LevelDb > 0 {
		errs = append(errs, errors.New("ID: level must be at most 0 dBFS"))
	}
	if id.VoiceFile != "" && !filepath.IsAbs(id.VoiceFile) {
		errs = append(errs, errors.New("ID: voice file must be an absolute path"))
	}
	a := st.Advanced
	if a.HangMS < 0 || a.HangMS > 10000 {
		errs = append(errs, errors.New("hang time must be 0 to 10000 ms"))
	}
	if a.TxTimeoutS < 0 || a.TxTimeoutS > 1800 {
		errs = append(errs, errors.New("transmit timeout must be at most 1800 seconds"))
	}
	if a.AGCMax < 0 || a.AGCTarget > 0 {
		errs = append(errs, errors.New("AGC: target must be below 0 dBFS and maximum gain positive"))
	}
	if st.Active != nil {
		if _, ok := mode.Lookup(st.Active.Mode); !ok {
			errs = append(errs, fmt.Errorf("active mode %q is not a known mode", st.Active.Mode))
		}
	}
	names := make([]string, 0, len(c.Modes))
	for n := range c.Modes {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		sp, ok := mode.Lookup(n)
		if !ok {
			errs = append(errs, fmt.Errorf("mode %q is not a known mode", n))
			continue
		}
		if err := sp.Check(c.Modes[n]); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// Clone returns a deep copy (through JSON, which is the file format).
func (c *Config) Clone() *Config {
	b, err := json.Marshal(c)
	if err != nil {
		panic(err) // the types always marshal
	}
	var out Config
	if err := json.Unmarshal(b, &out); err != nil {
		panic(err)
	}
	if out.Modes == nil {
		out.Modes = map[string]mode.Settings{}
	}
	return &out
}

// LoadConfig reads path. A missing or empty file is a first boot: it
// returns an empty config and no error.
func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) || (err == nil && len(strings.TrimSpace(string(b))) == 0) {
		return &Config{Version: ConfigVersion, Modes: map[string]mode.Settings{}}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	if c.Version > ConfigVersion {
		return nil, fmt.Errorf("config %s: version %d is newer than this node (%d)", path, c.Version, ConfigVersion)
	}
	if c.Modes == nil {
		c.Modes = map[string]mode.Settings{}
	}
	c.Normalize()
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	return &c, nil
}

// SaveConfig writes c to path atomically with mode 0600.
func SaveConfig(path string, c *Config) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	b = append(b, '\n')
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".node-*.json")
	if err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return fmt.Errorf("save config: %w", err)
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return fmt.Errorf("save config: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("save config: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	ok = true
	return nil
}
