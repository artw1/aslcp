package audio

import (
	"fmt"
	"math"
	"strings"
)

// CW identifier defaults.
const (
	DefaultCWWPM     = 20
	DefaultCWPitchHz = 800
	DefaultCWLevelDb = -18.0
)

var morse = map[rune]string{
	'A': ".-", 'B': "-...", 'C': "-.-.", 'D': "-..", 'E': ".", 'F': "..-.",
	'G': "--.", 'H': "....", 'I': "..", 'J': ".---", 'K': "-.-", 'L': ".-..",
	'M': "--", 'N': "-.", 'O': "---", 'P': ".--.", 'Q': "--.-", 'R': ".-.",
	'S': "...", 'T': "-", 'U': "..-", 'V': "...-", 'W': ".--", 'X': "-..-",
	'Y': "-.--", 'Z': "--..",
	'0': "-----", '1': ".----", '2': "..---", '3': "...--", '4': "....-",
	'5': ".....", '6': "-....", '7': "--...", '8': "---..", '9': "----.",
	'/': "-..-.", '?': "..--..", '.': ".-.-.-", ',': "--..--", '-': "-....-",
}

// CWOptions configures a CW message; zero values take the defaults.
type CWOptions struct {
	WPM     int     // words per minute (PARIS timing), default 20
	PitchHz float64 // default 800
	LevelDb float64 // peak level in dBFS; 0 means the default -18
}

// Element is one on or off run of a CW message, in samples.
type Element struct {
	On      bool
	Samples int
}

// CWElements returns the keying of text: a dot is 1 unit, a dash 3, the gap
// inside a character 1, between characters 3, between words 7, where a unit
// is 1.2 s / WPM (60 ms at 20 WPM). There is no trailing gap. Unknown
// characters are an error.
func CWElements(text string, wpm int) ([]Element, error) {
	if wpm <= 0 {
		wpm = DefaultCWWPM
	}
	unit := func(n int) int { return n * 1200 * SampleRate / 1000 / wpm }
	var els []Element
	gap := 0 // pending off units before the next element
	add := func(on bool, units int) {
		if !on {
			gap += units
			return
		}
		if gap > 0 && len(els) > 0 {
			els = append(els, Element{false, unit(gap)})
		}
		gap = 0
		els = append(els, Element{true, unit(units)})
	}
	for _, w := range strings.Fields(strings.ToUpper(text)) {
		if len(els) > 0 {
			gap = 7
		}
		for ci, c := range w {
			code, ok := morse[c]
			if !ok {
				return nil, fmt.Errorf("audio: no Morse code for %q", c)
			}
			if ci > 0 {
				gap = 3
			}
			for ei, e := range code {
				if ei > 0 {
					gap = 1
				}
				if e == '.' {
					add(true, 1)
				} else {
					add(true, 3)
				}
			}
		}
	}
	return els, nil
}

// CW plays a CW message as a Source.
type CW struct {
	els   []Element
	amp   float64
	pitch float64
	i     int
	pos   int
	o     osc
}

// NewCW returns a CW source for text (a callsign, for the identifier).
func NewCW(text string, opt CWOptions) (*CW, error) {
	els, err := CWElements(text, opt.WPM)
	if err != nil {
		return nil, err
	}
	if opt.PitchHz <= 0 {
		opt.PitchHz = DefaultCWPitchHz
	}
	if opt.LevelDb == 0 {
		opt.LevelDb = DefaultCWLevelDb
	}
	return &CW{els: els, amp: DBFS(opt.LevelDb), pitch: opt.PitchHz}, nil
}

// Samples is the total length of the message.
func (c *CW) Samples() int {
	n := 0
	for _, e := range c.els {
		n += e.Samples
	}
	return n
}

// Next implements Source.
func (c *CW) Next(f *Frame) bool {
	*f = Frame{}
	if c.i >= len(c.els) {
		return false
	}
	for n := 0; n < FrameSamples && c.i < len(c.els); {
		e := c.els[c.i]
		if c.pos >= e.Samples {
			c.i++
			c.pos = 0
			continue
		}
		if e.On {
			f[n] = int16(math.Round(c.o.next(c.pitch) * c.amp * envelope(c.pos, e.Samples)))
		}
		c.pos++
		n++
		if c.pos >= e.Samples {
			c.i++
			c.pos = 0
		}
	}
	return true
}
