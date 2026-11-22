# NXDN test vectors

`mmdvmhost_vectors.txt` holds byte-exact vectors produced by the unmodified
MMDVMHost NXDN classes (NXDNLICH, NXDNSACCH, NXDNFACCH1, NXDNConvolution,
NXDNCRC, NXDNLayer3; g4klx/MMDVMHost, GPL-2.0-or-later) driven by a small C++
program written for this purpose. All inputs are synthetic (unit IDs 1234,
TG 1997, RAN values chosen to cover edge cases); no captured traffic.

Format: one `name value` per line, hex without separators. `*.frame` values are
48-byte RF frames before scrambling, starting from all zero, so only the bits
the layer under test writes are set. `*.decode` lines are what the MMDVMHost
decoder returned (`ok` = CRC passed). The `rnd.*` frames carry 1-4 random bit
errors (fixed LCG seed) and include cases the decoder cannot repair; the Go
port must reproduce the C++ output exactly, failures included.

Regenerate (not committed, the harness links GPL code):

    g++ -std=c++11 -w -I MMDVMHost -o gen gen.cpp \
        MMDVMHost/NXDN{LICH,SACCH,FACCH1,Convolution,CRC,Layer3}.cpp
    ./gen > mmdvmhost_vectors.txt

The harness calls, per vector: `CNXDNCRC::encodeCRC6/12/15`,
`CNXDNConvolution::encode`, `CNXDNLICH::{setRFCT,setFCT,setOption,setDirection,
getRaw,encode,decode}`, `CNXDNSACCH::{setRAN,setStructure,setData,getRaw,encode,
decode}`, `CNXDNFACCH1::{setData,getRaw,encode,decode}` and builds the
`net.header.*` / `net.trailer.*` 33-byte payloads exactly like
`CNXDNControl::processVoice` does for the gateway (LICH, idle SACCH with RAN 5,
FACCH1 raw twice).

Not covered by C++ vectors: the AMBE 49-bit packing (MMDVMHost's
NXDNAudio::decode needs its 4096-entry PRNG table and Golay code, which this
repository does not carry); it is tested against the documented bit layout.
