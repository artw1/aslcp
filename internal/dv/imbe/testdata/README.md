# IMBE golden vectors

Bit-exact reference output for the Go port in `internal/dv/imbe`. Synthetic
signals only.

Reference: OP25, https://github.com/boatbod/op25, commit
`38a37491df4532d932f9f1d5634617af410cb3c9`, directory
`op25/gr-op25_repeater/lib/imbe_vocoder` (Pavel Yazev, GPL-3.0-or-later), and
`op25/gr-op25_repeater/lib/op25_imbe_frame.h` for the FEC. The reference
source is not included here.

| file            | content                                                              |
|-----------------|----------------------------------------------------------------------|
| `harness.cc`    | generator / driver linked against the unmodified reference           |
| `enc.frames`    | `harness enc` of the 300-frame test signal (11 bytes per frame)      |
| `dec.pcm.gz`    | `harness dec enc.frames` (s16le, 160 samples per frame)              |
| `rnd.frames`    | `harness rnd 200`: 200 pseudo-random 88-bit frames                   |
| `rnddec.pcm.gz` | `harness dec rnd.frames`                                             |
| `fec.bin.gz`    | first 200 records of `harness fec` (60 bytes each, see harness.cc)   |

The test signal (`harness gen`, 48000 samples, s16le) is regenerated in Go by
the tests; its SHA-256 is
`6f21ca48734297884247aace07b5b123a15ca2353bb42a42945d78e3d729acc5`.

## Regenerating

```sh
git clone https://github.com/boatbod/op25 && cd op25
git checkout 38a37491df4532d932f9f1d5634617af410cb3c9
L=$PWD/op25/gr-op25_repeater/lib
mkdir -p /tmp/imbe && cd /tmp/imbe
for f in $L/imbe_vocoder/*.cc; do g++ -O2 -c -I$L/imbe_vocoder "$f"; done
g++ -O2 -I$L/imbe_vocoder -I$L path/to/testdata/harness.cc *.o -o harness
./harness gen in.pcm
./harness enc in.pcm enc.frames
./harness dec enc.frames dec.pcm
./harness rnd 200 rnd.frames
./harness dec rnd.frames rnddec.pcm
./harness fec 200 fec.bin
gzip -9n dec.pcm rnddec.pcm fec.bin
```

Each `enc`/`dec` run must be a separate process: the reference's `rand_gen()`
seed is shared by all `imbe_vocoder` instances in a process. The outputs were
identical with `-O0`, `-O2`, `-O3`, `-O2 -fwrapv` and an ASan/UBSan build
(UBSan reports only the reference's intentional signed-overflow wraps).
