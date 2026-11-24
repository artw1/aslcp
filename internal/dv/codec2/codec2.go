// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) David Rowe and the other Codec2 contributors (Codec2,
// LGPL-2.1); Codec2-mod modifications by the M17 Project contributors.
//
// Derived from Codec2-mod (https://github.com/M17-Project/Codec2-mod) file(s)
// src/codec2_mod.c, inc/codec2_mod.h, inc/codec2_internal.h at commit
// 7c334283794fa6304dc014f12df70afc8393143a and relicensed under LGPL-2.1
// section 3 to GPL-3.0-or-later. See doc.go for the full licence statement.

package codec2

// Encoder is a Codec2 3200 bit/s encoder. It is stateful: use one Encoder
// per audio stream. It is not safe for concurrent use. The zero value is not
// usable; create one with NewEncoder.
type Encoder struct {
	w      [mPitch]float32 // analysis window
	bigW   [fftEnc]float32 // frequency response of the window
	sn     [mPitch]float32 // input speech memory
	nlp    nlpState
	prevF0 float32
	fft    rfft
	sw     [fftEnc]cpx // scratch: DFT of the speech
}

// NewEncoder returns an encoder in its initial state (codec2_encoder_init).
func NewEncoder() *Encoder {
	e := new(Encoder)
	for i := range e.sn {
		e.sn[i] = 1.0
	}
	e.fft.init(false)
	e.makeAnalysisWindow()
	e.prevF0 = float32(1.0) / pMaxS
	e.nlp.init()
	return e
}

// Encode encodes 160 samples (20 ms of 8 kHz, 16-bit linear PCM) into one
// 64-bit frame in the layout of libcodec2's CODEC2_MODE_3200
// (codec2_encode).
func (e *Encoder) Encode(dst *[BytesPerFrame]byte, pcm *[SamplesPerFrame]int16) {
	*dst = [BytesPerFrame]byte{}
	var mo model
	var ak [lspOrd + 1]float32
	var lsps [lspOrd]float32
	var lspdIdx [lspOrd]int
	var nbit uint

	// first 10 ms analysis frame - we just want voicing
	e.analyseOneFrame(&mo, pcm[:nSamp])
	pack(dst, &nbit, b2u(mo.voiced), 1)

	// second 10 ms analysis frame
	e.analyseOneFrame(&mo, pcm[nSamp:])
	pack(dst, &nbit, b2u(mo.voiced), 1)
	pack(dst, &nbit, uint(encodeWo(mo.wo, woBits)), woBits)

	energy := e.speechToUqLSPs(&lsps, &ak)
	pack(dst, &nbit, uint(encodeEnergy(energy, eBits)), eBits)

	encodeLSPDScalar(&lspdIdx, &lsps)
	for i := 0; i < lspOrd; i++ {
		pack(dst, &nbit, uint(lspdIdx[i]), 5) // codebook indices are 5 bits
	}
}

func b2u(b bool) uint {
	if b {
		return 1
	}
	return 0
}

// Decoder is a Codec2 3200 bit/s decoder. It is stateful: use one Decoder
// per audio stream. It is not safe for concurrent use. The zero value is not
// usable; create one with NewDecoder.
type Decoder struct {
	nextRn  uint32
	sn      [2 * nSamp]float32 // synthesised speech memory
	pn      [2 * nSamp]float32 // synthesis (Parzen) window
	exPhase float32
	bgEst   float32

	prevModel model
	prevLSPs  [lspOrd]float32
	prevE     float32

	fwd, inv rfft
	sw       [fftEnc/2 + 1]cpx // scratch: harmonics spectrum
	swTime   [fftEnc]float32   // scratch: its inverse DFT
}

// NewDecoder returns a decoder in its initial state (codec2_decoder_init).
func NewDecoder() *Decoder {
	d := new(Decoder)
	d.nextRn = 1
	d.fwd.init(false)
	d.inv.init(true)
	makeSynthesisWindow(&d.pn)

	d.prevModel.wo = woMinF
	d.prevModel.l = int(cPi / float64(d.prevModel.wo))
	for i := range d.prevLSPs {
		d.prevLSPs[i] = float32(float64(i) * cPi / (lspOrd + 1))
	}
	d.prevE = 1
	return d
}

// Decode decodes one 64-bit frame into 160 samples (codec2_decode). Any
// 8-byte value is a valid frame.
func (d *Decoder) Decode(pcm *[SamplesPerFrame]int16, src *[BytesPerFrame]byte) {
	var mo [2]model
	var lspdIdx [lspOrd]int
	var lsps [2][lspOrd]float32
	var en [2]float32
	var ak [2][lspOrd + 1]float32
	var aw [fftEnc/2 + 1]cpx

	// unpack bits from channel
	var nbit uint
	mo[0].voiced = unpack(src, &nbit, 1) != 0
	mo[1].voiced = unpack(src, &nbit, 1) != 0

	woIndex := unpack(src, &nbit, woBits)
	mo[1].wo = decodeWo(woIndex, woBits)
	mo[1].l = int(cPi / float64(mo[1].wo))

	eIndex := unpack(src, &nbit, eBits)
	en[1] = decodeEnergy(eIndex, eBits)

	for i := 0; i < lspOrd; i++ {
		lspdIdx[i] = unpack(src, &nbit, 5)
	}
	decodeLSPDScalar(&lsps[1], &lspdIdx)

	// Wo and energy are sampled every 20 ms, so interpolate the 10 ms frame
	// in between; likewise the LSPs.
	interpWo(&mo[0], &d.prevModel, &mo[1])
	en[0] = interpEnergy(d.prevE, en[1])
	interpolateLSP(&lsps[0], &d.prevLSPs, &lsps[1])

	for i := 0; i < 2; i++ {
		lspToLPC(&lsps[i], &ak[i])
		d.aksToMag2(&ak[i], &mo[i], en[i], &aw)
		applyLPCCorrection(&mo[i])
		d.synthesiseOneFrame(pcm[nSamp*i:nSamp*(i+1)], &mo[i], &aw)
	}

	// update memories for next frame
	d.prevModel = mo[1]
	d.prevE = en[1]
	d.prevLSPs = lsps[1]
}
