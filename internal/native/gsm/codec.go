package gsm

// Frame sizes.
const (
	// SampleRate is the codec's sample rate in Hz.
	SampleRate = 8000
	// FrameSamples is the number of PCM samples in one 20 ms frame.
	FrameSamples = 160
	// FrameBytes is the size of one packed frame.
	FrameBytes = 33
)

// Params holds the 76 coded parameters of one frame (GSM 06.10 table 1.1).
type Params struct {
	LARc  [8]int16     // log-area ratios, 6,6,5,5,4,4,3,3 bits
	Nc    [4]int16     // LTP lag per sub-frame, 7 bits
	Bc    [4]int16     // LTP gain per sub-frame, 2 bits
	Mc    [4]int16     // RPE grid position per sub-frame, 2 bits
	Xmaxc [4]int16     // RPE block maximum per sub-frame, 6 bits
	XMc   [4][13]int16 // RPE pulses per sub-frame, 3 bits each
}

// Encoder encodes 16-bit PCM into GSM frames. The zero value is ready to use.
type Encoder struct {
	// Pre-processing state.
	z1  int16
	lz2 int32
	mp  int16

	st shortTerm
	// dp holds 120 samples of reconstructed short-term residual history
	// followed by the 160 samples being built for the current frame.
	dp [280]int16
	// e is the RPE input/output buffer with five guard samples each side.
	e [50]int16
}

// NewEncoder returns an encoder in its initial state.
func NewEncoder() *Encoder { return new(Encoder) }

// Reset returns the encoder to its initial state.
func (e *Encoder) Reset() { *e = Encoder{} }

// EncodeParams encodes one frame of PCM into its coded parameters.
func (e *Encoder) EncodeParams(pcm *[FrameSamples]int16) Params {
	var p Params
	var so [FrameSamples]int16
	e.preprocess(pcm, &so)

	var lACF [9]int32
	var lar [8]int16
	autocorrelation(&so, &lACF)
	reflectionCoefficients(&lACF, &lar)
	toLogAreaRatios(&lar)
	quantizeLARs(&lar, &p.LARc)

	e.st.analysis(&p.LARc, &so)

	var x [40]int16
	var xM, xMp [13]int16
	for j := 0; j < 4; j++ {
		d := so[j*40 : j*40+40]
		// History window for this sub-frame: 120 samples before it.
		hist := e.dp[j*40 : j*40+120]
		cur := e.dp[j*40+120 : j*40+160]
		nc, bc := ltpParameters(d, hist)
		p.Nc[j], p.Bc[j] = nc, bc
		// cur receives the prediction dpp first, then the reconstruction.
		ltpFilter(bc, nc, hist, d, cur, e.e[5:45])

		weightingFilter(&e.e, &x)
		p.Mc[j] = gridSelection(&x, &xM)
		var exp, mant int16
		p.Xmaxc[j], exp, mant = apcmQuantize(&xM, &p.XMc[j])
		apcmDequantize(&p.XMc[j], exp, mant, &xMp)
		gridPosition(p.Mc[j], &xMp, e.e[5:45])

		for k := 0; k < 40; k++ {
			cur[k] = add(e.e[5+k], cur[k])
		}
	}
	copy(e.dp[0:120], e.dp[160:280])
	return p
}

// Encode encodes one frame of PCM into a 33-byte packed frame.
func (e *Encoder) Encode(pcm *[FrameSamples]int16) [FrameBytes]byte {
	p := e.EncodeParams(pcm)
	return p.Pack()
}

// Decoder decodes GSM frames into 16-bit PCM. The zero value is not ready;
// use NewDecoder.
type Decoder struct {
	st  shortTerm
	drp [160]int16 // 120 history samples and the current sub-frame
	nrp int16      // last valid LTP lag
	msr int16      // de-emphasis memory
}

// NewDecoder returns a decoder in its initial state.
func NewDecoder() *Decoder { return &Decoder{nrp: 40} }

// Reset returns the decoder to its initial state.
func (d *Decoder) Reset() { *d = Decoder{nrp: 40} }

// DecodeParams decodes one frame of coded parameters into PCM. Out-of-range
// parameter values are masked to their field width.
func (d *Decoder) DecodeParams(p *Params, pcm *[FrameSamples]int16) {
	var wt [FrameSamples]int16
	var erp [40]int16
	var xMc, xMp [13]int16
	for j := 0; j < 4; j++ {
		xmaxc := p.Xmaxc[j] & 63
		for i := range xMc {
			xMc[i] = p.XMc[j][i] & 7
		}
		exp, mant := expMant(xmaxc)
		apcmDequantize(&xMc, exp, mant, &xMp)
		gridPosition(p.Mc[j], &xMp, erp[:])
		d.ltpSynthesis(p.Nc[j]&127, p.Bc[j], &erp, wt[j*40:j*40+40])
	}

	var larc [8]int16
	for i, v := range p.LARc {
		larc[i] = v & int16(1<<larBits[i]-1)
	}
	d.st.synthesis(&larc, &wt, pcm)

	// De-emphasis, upscaling and output truncation (4.3.5-4.3.7).
	msr := d.msr
	for k := range pcm {
		msr = add(pcm[k], multR(msr, 28180))
		pcm[k] = add(msr, msr) &^ 7
	}
	d.msr = msr
}

// Decode decodes one 33-byte packed frame into PCM. It returns an error if
// the frame has the wrong length or signature, leaving the state unchanged.
func (d *Decoder) Decode(frame []byte, pcm *[FrameSamples]int16) error {
	p, err := Unpack(frame)
	if err != nil {
		return err
	}
	d.DecodeParams(&p, pcm)
	return nil
}
