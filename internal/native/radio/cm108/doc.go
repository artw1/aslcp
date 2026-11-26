// Package cm108 finds a CM108-family USB sound chip (the audio chip of the
// SHARI and similar radio interfaces), maps it to its hidraw node and ALSA
// card, keys PTT through a HID output report and reads COS from the HID
// input report.
//
// The report layouts follow what the chips do and what chan_simpleusb
// configures for its default interface type: PTT on GPIO3, COS on the
// volume-down input.
package cm108
