// Package radio is the SHARI radio hardware layer of the native node: the
// USB sound chip (audio both ways, PTT, COS) and the SA818 module's serial
// programming, behind one [Radio] interface.
//
// Audio is the node contract: signed 16-bit mono PCM at 8000 Hz in 20 ms
// frames. The sound chip runs at 48000 Hz; the alsa subpackage converts.
//
// Subpackages: usbsys (sysfs walking), alsa (PCM, mixer, resampler), cm108
// (HID PTT and COS) and sa818 (AT programming). [Fake] stands in for tests and
// for a node with no radio.
package radio
