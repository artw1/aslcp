// Package radio is the radio hardware layer of the native node: the
// USB sound chip (audio both ways, PTT, COS) and the SA818 module's serial
// programming, behind one [Radio] interface.
//
// Audio is the node contract: signed 16-bit mono PCM at 8000 Hz in 20 ms
// frames. The sound chip runs at 48000 Hz; the alsa subpackage converts.
//
// The interface is one USB driver with presets: the SHARI (a CM108B and an
// SA818), a generic CM108/CM119 interface wired to an external radio
// (URI-style) and none (radioless). A preset fills in the PTT pin, the COS and
// CTCSS inputs and their polarity, and whether the SA818 is programmed;
// [Overrides] change single settings. See [Resolve].
//
// Subpackages: usbsys (sysfs walking), alsa (PCM, mixer, resampler), cm108
// (HID PTT and COS) and sa818 (AT programming). [Fake] stands in for tests and
// for a node with no radio.
package radio
