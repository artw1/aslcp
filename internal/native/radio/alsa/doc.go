// Package alsa captures and plays PCM audio through the Linux ALSA kernel
// interface directly (the /dev/snd/pcmC*D*[cp] and controlC* device nodes and
// their SNDRV_* ioctls), with no libasound and no cgo.
//
// The USB sound chips on the supported radio interfaces run at 48000 Hz mono
// S16_LE. [Capture] and [Playback] hide that: they convert to and from the
// node's 8000 Hz, 20 ms, 160-sample [Frame] with a polyphase resampler.
//
// The ioctl structures here mirror the 64-bit kernel layouts (amd64 and
// arm64); compile-time assertions in types.go fail the build on any other
// layout.
package alsa
