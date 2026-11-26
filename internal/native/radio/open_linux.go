package radio

import (
	"errors"
	"fmt"

	"github.com/artw1/aslcp/internal/native/radio/alsa"
	"github.com/artw1/aslcp/internal/native/radio/cm108"
	"github.com/artw1/aslcp/internal/native/radio/sa818"
)

// Open detects the radio interface (USB sound chip, hidraw node, ALSA card)
// and opens it. It returns an error wrapping [ErrNoRadio] when none is found.
// It does not program the SA818; see [ProgramSA818].
func Open(cfg Config) (Radio, error) {
	info, err := cm108.Discover(cfg.SysRoot, cfg.DevRoot, cfg.BusPath)
	if err != nil {
		if errors.Is(err, cm108.ErrNotFound) {
			return nil, fmt.Errorf("%w: %v", ErrNoRadio, err)
		}
		return nil, fmt.Errorf("radio: detect: %w", err)
	}
	keyer, err := cm108.Open(cm108.Config{
		SysRoot: cfg.SysRoot, DevRoot: cfg.DevRoot, BusPath: info.BusPath,
		PTTGPIO: cfg.PTTGPIO, PTTActiveLow: cfg.PTTActiveLow, COSActiveLow: cfg.COSActiveLow,
	})
	if err != nil {
		return nil, fmt.Errorf("radio: open HID: %w", err)
	}
	mix, err := alsa.OpenMixer(info.Card)
	if err != nil {
		keyer.Close()
		return nil, fmt.Errorf("radio: %w", err)
	}
	// A missing switch is fine; a failure to unmute is not worth failing for.
	_ = mix.Unmute()
	if v := cfg.InitialVolumes; v != nil {
		if err := mix.SetCaptureVolume(v.Capture); err != nil {
			keyer.Close()
			mix.Close()
			return nil, fmt.Errorf("radio: capture volume: %w", err)
		}
		if err := mix.SetPlaybackVolume(v.Playback); err != nil {
			keyer.Close()
			mix.Close()
			return nil, fmt.Errorf("radio: playback volume: %w", err)
		}
	}
	capt, err := alsa.OpenCapture(info.Card, 0, alsa.Params{})
	if err != nil {
		keyer.Close()
		mix.Close()
		return nil, fmt.Errorf("radio: %w", err)
	}
	play, err := alsa.OpenPlayback(info.Card, 0, alsa.Params{})
	if err != nil {
		capt.Close()
		keyer.Close()
		mix.Close()
		return nil, fmt.Errorf("radio: %w", err)
	}
	return New(Parts{Capture: capt, Playback: play, PTT: keyer, Mixer: mix}), nil
}

// ProgramSA818 finds the module's serial port, programs it with s and saves
// the applied settings to cfg.SA818StatePath (default /etc/aslcp/sa818.json).
// It opens and closes the port itself, so it can run while the radio is open.
func ProgramSA818(cfg Config, s sa818.Settings) error {
	path := cfg.SA818Port
	if path == "" {
		var err error
		if path, err = sa818.FindPort(cfg.SysRoot, cfg.DevRoot); err != nil {
			return err
		}
	}
	port, err := sa818.OpenSerial(path)
	if err != nil {
		return err
	}
	defer port.Close()
	return sa818.NewProgrammer(port, cfg.statePath()).Apply(s)
}

// SavedSA818 returns the settings last programmed, from the state file.
func SavedSA818(cfg Config) (sa818.State, error) {
	return sa818.LoadState(cfg.statePath())
}
