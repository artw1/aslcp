package alsa

import (
	"fmt"
	"sync"
)

// duplicate writes each mono sample of src into ch interleaved channels of
// dst (appended) and returns it.
func duplicate(dst, src []int16, ch int) []int16 {
	for _, v := range src {
		for i := 0; i < ch; i++ {
			dst = append(dst, v)
		}
	}
	return dst
}

// leftOf appends the first channel of each ch-channel interleaved frame in src
// to dst and returns it.
func leftOf(dst, src []int16, ch int) []int16 {
	for i := 0; i+ch <= len(src); i += ch {
		dst = append(dst, src[i])
	}
	return dst
}

// Capture reads 8 kHz Frames from a capture PCM running at 48 kHz.
type Capture struct {
	pcm  *PCM
	down *Down
	raw  []int16 // interleaved device samples
	mono []int16
	out  []int16
}

// OpenCapture opens the capture stream of the given card.
func OpenCapture(card, device int, par Params) (*Capture, error) {
	pcm, err := OpenPCM(PCMPath(card, device, DirCapture), DirCapture, par)
	if err != nil {
		return nil, err
	}
	return NewCapture(pcm)
}

// NewCapture wraps an opened capture PCM. The PCM must be mono or stereo at 48 kHz;
// stereo keeps the left channel.
func NewCapture(pcm *PCM) (*Capture, error) {
	if pcm.par.Channels < 1 || pcm.par.Channels > 2 || pcm.par.Rate != hwRate {
		pcm.Close()
		return nil, fmt.Errorf("alsa: capture needs 1 or 2 channels at %d Hz, have %d channels at %d Hz", hwRate, pcm.par.Channels, pcm.par.Rate)
	}
	return &Capture{pcm: pcm, down: NewDown(), raw: make([]int16, FrameSamples*resFactor*pcm.par.Channels)}, nil
}

// ReadFrame blocks for the next 20 ms frame.
func (c *Capture) ReadFrame() (Frame, error) {
	var f Frame
	if err := c.pcm.Read(c.raw); err != nil {
		return f, err
	}
	src := c.raw
	if ch := c.pcm.par.Channels; ch > 1 {
		c.mono = leftOf(c.mono[:0], c.raw, ch)
		src = c.mono
	}
	c.out = c.down.Process(src, c.out[:0])
	copy(f[:], c.out)
	return f, nil
}

// PCM returns the underlying stream.
func (c *Capture) PCM() *PCM { return c.pcm }

// Close releases the device.
func (c *Capture) Close() error { return c.pcm.Close() }

// Playback writes 8 kHz Frames to a playback PCM running at 48 kHz.
type Playback struct {
	pcm *PCM
	up  *Up
	out []int16 // upsampled mono
	buf []int16 // interleaved device samples

	mu    sync.Mutex
	armed bool // a silence prefill has been queued since the last underrun
}

// prefillFrames of silence are queued before audio starts, so a producer that
// is a little late does not underrun the device.
const prefillFrames = 2

// prefillSamples is the length of the silence prefill for ch channels.
func prefillSamples(ch int) int { return FrameSamples * resFactor * prefillFrames * ch }

// OpenPlayback opens the playback stream of the given card.
func OpenPlayback(card, device int, par Params) (*Playback, error) {
	pcm, err := OpenPCM(PCMPath(card, device, DirPlayback), DirPlayback, par)
	if err != nil {
		return nil, err
	}
	return NewPlayback(pcm)
}

// NewPlayback wraps an opened playback PCM. The PCM must be mono or stereo at
// 48 kHz; stereo plays the signal on both channels.
func NewPlayback(pcm *PCM) (*Playback, error) {
	if pcm.par.Channels < 1 || pcm.par.Channels > 2 || pcm.par.Rate != hwRate {
		pcm.Close()
		return nil, fmt.Errorf("alsa: playback needs 1 or 2 channels at %d Hz, have %d channels at %d Hz", hwRate, pcm.par.Channels, pcm.par.Rate)
	}
	return &Playback{pcm: pcm, up: NewUp()}, nil
}

// WriteFrame converts and queues one frame, blocking while the device buffer
// is full.
func (p *Playback) WriteFrame(f Frame) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.armed {
		silence := make([]int16, prefillSamples(p.pcm.par.Channels))
		if err := p.pcm.Write(silence); err != nil {
			return err
		}
		p.armed = true
	}
	p.out = p.up.Process(f[:], p.out[:0])
	data := p.out
	if ch := p.pcm.par.Channels; ch > 1 {
		p.buf = duplicate(p.buf[:0], p.out, ch)
		data = p.buf
	}
	before := p.pcm.Xruns()
	err := p.pcm.Write(data)
	if p.pcm.Xruns() != before {
		p.armed = false // underrun: prefill again on the next frame
	}
	return err
}

// Stop drops queued audio so the next frame starts with a fresh prefill. Call
// it when transmit ends.
func (p *Playback) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.armed = false
	return p.pcm.Drop()
}

// PCM returns the underlying stream.
func (p *Playback) PCM() *PCM { return p.pcm }

// Close releases the device.
func (p *Playback) Close() error { return p.pcm.Close() }

// Xruns counts overruns recovered on the capture stream.
func (c *Capture) Xruns() uint64 { return c.pcm.Xruns() }

// Xruns counts underruns recovered on the playback stream.
func (p *Playback) Xruns() uint64 { return p.pcm.Xruns() }
