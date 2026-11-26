package gsm

// Constant tables from GSM 06.10 section 5.

// Log-area-ratio coding (table 5.1/5.2): mic is the lower limit, b the offset
// and inva the inverse slope used when decoding.
var (
	larMIC  = [8]int16{-32, -32, -16, -16, -8, -8, -4, -4}
	larB    = [8]int16{0, 0, 2048, -2560, 94, -1792, -341, -1144}
	larINVA = [8]int16{13107, 13107, 13107, 13107, 19223, 17476, 31454, 29708}
)

// larStep holds the quantizer slope A, offset B, and range MAC/MIC for each
// log-area ratio (section 4.2.7).
var larStep = [8]struct{ a, b, mac, mic int16 }{
	{20480, 0, 31, -32},
	{20480, 0, 31, -32},
	{20480, 2048, 15, -16},
	{20480, -2560, 15, -16},
	{13964, 94, 7, -8},
	{15360, -1792, 7, -8},
	{8534, -341, 3, -4},
	{9036, -1144, 3, -4},
}

// ltpDLB are the decision levels and ltpQLB the quantized values of the
// long-term predictor gain (table 5.3).
var (
	ltpDLB = [4]int16{6554, 16384, 26214, 32767}
	ltpQLB = [4]int16{3277, 11469, 21299, 32767}
)

// weightH is the impulse response of the RPE weighting filter (table 5.4).
var weightH = [11]int16{-134, -374, 0, 2054, 5741, 8192, 5741, 2054, 0, -374, -134}

// rpeNRFAC holds the inverse mantissas used by APCM quantization and rpeFAC
// the normalized mantissas used by inverse quantization (table 5.5/5.6).
var (
	rpeNRFAC = [8]int16{29128, 26215, 23832, 21846, 20165, 18725, 17476, 16384}
	rpeFAC   = [8]int16{18431, 20479, 22527, 24575, 26623, 28671, 30719, 32767}
)
