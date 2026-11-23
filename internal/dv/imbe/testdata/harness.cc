// SPDX-License-Identifier: GPL-3.0-or-later
//
// Golden-vector harness for the Go port of Pavel Yazev's imbe_vocoder.
//
// Build (see README.md): link against the unmodified OP25 imbe_vocoder
// sources and include op25_imbe_frame.h from gr-op25_repeater/lib.
//
// Modes:
//   harness gen  out.pcm              write the deterministic test signal
//   harness enc  in.pcm  out.frames   encode (one imbe_vocoder per process)
//   harness dec  in.frames out.pcm    decode (one imbe_vocoder per process)
//   harness rnd  n out.frames         write n pseudo-random 88-bit frames
//   harness fec  n out.fec            FEC (Golay/Hamming/PN) vectors
//
// Frames are 11 bytes: u0..u3 (12 bits each), u4..u6 (11 bits each) and
// u7 (7 bits) concatenated MSB first. PCM is 16-bit little-endian.
//
// NOTE: imbe_vocoder's rand_gen() uses a file-static seed shared by every
// imbe_vocoder instance in the process (the constructor draws 56 values), so
// exactly one instance is created per process here.

#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <vector>

#include "imbe_vocoder.h"
#include "op25_imbe_frame.h"

static const int kFrames = 300;
static const int kSamples = kFrames * 160;

// ---- deterministic integer-only signal generator (mirrored in Go) ----

static uint32_t lcg_state;
static uint32_t lcg(void) {
	lcg_state = lcg_state * 1103515245u + 12345u;
	return lcg_state;
}

// Parabolic sine approximation, phase is a full turn in 2^32, result Q15.
static int32_t isin(uint32_t phase) {
	int32_t t = (int32_t)phase >> 16; // -32768..32767 == -pi..pi
	int32_t at = t < 0 ? -t : t;
	int32_t y = (int32_t)(((int64_t)4 * t * (32768 - at)) >> 15);
	if (y > 32767) y = 32767;
	if (y < -32767) y = -32767;
	return y;
}

static uint32_t inc_for(uint32_t milli_hz) { // frequency in mHz
	return (uint32_t)(((uint64_t)milli_hz << 32) / 8000000u);
}

static int16_t clip16(int32_t v) {
	if (v > 32767) return 32767;
	if (v < -32768) return -32768;
	return (int16_t)v;
}

static void gen_signal(int16_t *out) {
	lcg_state = 0x12345678u;
	uint32_t ph[12] = {0};
	for (int n = 0; n < kSamples; n++) {
		int32_t s = 0;
		int32_t noise = (int32_t)((lcg() >> 16) & 0xFFFF) - 32768; // Q15
		if (n < 12000) {
			// A: three sines + noise
			ph[0] += inc_for(300000);
			ph[1] += inc_for(1100000);
			ph[2] += inc_for(2500000);
			s = (isin(ph[0]) * 6000 + isin(ph[1]) * 3000 + isin(ph[2]) * 1500) >> 15;
			s += (noise * 800) >> 15;
		} else if (n < 16000) {
			// A': same mix driven into hard clipping (saturation paths)
			ph[0] += inc_for(300000);
			ph[1] += inc_for(1100000);
			ph[2] += inc_for(2500000);
			s = (isin(ph[0]) * 30000 + isin(ph[1]) * 20000 + isin(ph[2]) * 10000) >> 15;
			s += (noise * 4000) >> 15;
		} else if (n < 24000) {
			// B: digital silence
			s = 0;
		} else if (n < 40000) {
			// C: linear chirp 100 Hz -> 3500 Hz
			int32_t k = n - 24000;
			uint32_t mhz = 100000u + (uint32_t)(((uint64_t)3400000u * k) / 16000u);
			ph[3] += inc_for(mhz);
			s = (isin(ph[3]) * 9000) >> 15;
		} else {
			// D: harmonic "voiced" source with vibrato, alternating with
			// noise bursts every 1600 samples
			int32_t k = n - 40000;
			if ((k / 1600) % 2 == 0) {
				ph[4] += inc_for(5000); // 5 Hz vibrato LFO
				int32_t f0 = 160000 + ((isin(ph[4]) * 60000) >> 15); // mHz
				for (int h = 1; h <= 7; h++) {
					ph[4 + h] += inc_for((uint32_t)(f0 * h));
					s += (isin(ph[4 + h]) * (7000 / h)) >> 15;
				}
			} else {
				s = (noise * 5000) >> 15;
			}
		}
		out[n] = clip16(s);
	}
}

// ---- 11-byte frame packing ----

static void pack(const int16_t *fv, uint8_t *b) {
	static const int widths[8] = {12, 12, 12, 12, 11, 11, 11, 7};
	memset(b, 0, 11);
	int pos = 0;
	for (int i = 0; i < 8; i++) {
		uint32_t v = (uint16_t)fv[i];
		if (v >> widths[i]) {
			fprintf(stderr, "u%d=0x%x does not fit %d bits\n", i, v, widths[i]);
			exit(2);
		}
		for (int k = widths[i] - 1; k >= 0; k--, pos++)
			if ((v >> k) & 1) b[pos / 8] |= 0x80 >> (pos % 8);
	}
}

static void unpack(const uint8_t *b, int16_t *fv) {
	static const int widths[8] = {12, 12, 12, 12, 11, 11, 11, 7};
	int pos = 0;
	for (int i = 0; i < 8; i++) {
		uint32_t v = 0;
		for (int k = 0; k < widths[i]; k++, pos++)
			v = (v << 1) | ((b[pos / 8] >> (7 - pos % 8)) & 1);
		fv[i] = (int16_t)v;
	}
}

static std::vector<uint8_t> read_file(const char *p) {
	FILE *f = fopen(p, "rb");
	if (!f) { perror(p); exit(1); }
	std::vector<uint8_t> d;
	uint8_t buf[4096];
	size_t n;
	while ((n = fread(buf, 1, sizeof buf, f)) > 0) d.insert(d.end(), buf, buf + n);
	fclose(f);
	return d;
}

static void write_file(const char *p, const void *d, size_t n) {
	FILE *f = fopen(p, "wb");
	if (!f || fwrite(d, 1, n, f) != n) { perror(p); exit(1); }
	fclose(f);
}

static void put16(std::vector<uint8_t> &o, int16_t v) {
	o.push_back((uint8_t)(v & 0xFF));
	o.push_back((uint8_t)((uint16_t)v >> 8));
}

int main(int argc, char **argv) {
	if (argc < 3) { fprintf(stderr, "usage: see source\n"); return 1; }
	const char *mode = argv[1];
	if (!strcmp(mode, "gen")) {
		static int16_t s[kSamples];
		gen_signal(s);
		std::vector<uint8_t> o;
		for (int i = 0; i < kSamples; i++) put16(o, s[i]);
		write_file(argv[2], o.data(), o.size());
	} else if (!strcmp(mode, "enc")) {
		std::vector<uint8_t> in = read_file(argv[2]);
		imbe_vocoder v;
		std::vector<uint8_t> o;
		for (size_t off = 0; off + 320 <= in.size(); off += 320) {
			int16_t snd[160], fv[8];
			for (int i = 0; i < 160; i++)
				snd[i] = (int16_t)(in[off + 2 * i] | (in[off + 2 * i + 1] << 8));
			v.imbe_encode(fv, snd);
			uint8_t b[11];
			pack(fv, b);
			o.insert(o.end(), b, b + 11);
		}
		write_file(argv[3], o.data(), o.size());
	} else if (!strcmp(mode, "dec")) {
		std::vector<uint8_t> in = read_file(argv[2]);
		imbe_vocoder v;
		std::vector<uint8_t> o;
		for (size_t off = 0; off + 11 <= in.size(); off += 11) {
			int16_t snd[160], fv[8];
			unpack(&in[off], fv);
			v.imbe_decode(fv, snd);
			for (int i = 0; i < 160; i++) put16(o, snd[i]);
		}
		write_file(argv[3], o.data(), o.size());
	} else if (!strcmp(mode, "rnd")) {
		int n = atoi(argv[2]);
		lcg_state = 0xC0FFEE01u;
		std::vector<uint8_t> o;
		for (int i = 0; i < n; i++)
			for (int k = 0; k < 11; k++) o.push_back((uint8_t)(lcg() >> 24));
		write_file(argv[3], o.data(), o.size());
	} else if (!strcmp(mode, "fec")) {
		// Record: u[8] packed (11 bytes) | 144-bit codeword (18 bytes) |
		//         corrupted codeword (18 bytes) | decoded frame (11 bytes) |
		//         E0 (1 byte) | ET (1 byte)   == 60 bytes
		int n = atoi(argv[2]);
		lcg_state = 0xFEC0FEC0u;
		std::vector<uint8_t> o;
		for (int r = 0; r < n; r++) {
			uint8_t fb[11];
			for (int k = 0; k < 11; k++) fb[k] = (uint8_t)(lcg() >> 24);
			int16_t fv[8];
			unpack(fb, fv);
			voice_codeword cw(voice_codeword_sz);
			// op25 keeps u7 left-shifted by one (bit 0 unused)
			imbe_header_encode(cw, fv[0], fv[1], fv[2], fv[3], fv[4], fv[5], fv[6],
			                   (uint32_t)fv[7] << 1);
			uint8_t cb[18];
			extract(cw, 0, 144, cb);
			// corrupt 0..4 random bits
			int nerr = (int)((lcg() >> 24) % 5);
			for (int e = 0; e < nerr; e++) {
				int bit = (int)((lcg() >> 16) % 144);
				cw[bit] = !cw[bit];
			}
			uint8_t xb[18];
			extract(cw, 0, 144, xb);
			uint32_t u[8], E0, ET;
			imbe_header_decode(cw, u[0], u[1], u[2], u[3], u[4], u[5], u[6], u[7], E0, ET);
			int16_t dv[8];
			for (int i = 0; i < 8; i++) dv[i] = (int16_t)u[i];
			dv[7] >>= 1;
			uint8_t db[11];
			pack(dv, db);
			o.insert(o.end(), fb, fb + 11);
			o.insert(o.end(), cb, cb + 18);
			o.insert(o.end(), xb, xb + 18);
			o.insert(o.end(), db, db + 11);
			o.push_back((uint8_t)E0);
			o.push_back((uint8_t)ET);
		}
		write_file(argv[3], o.data(), o.size());
	} else {
		fprintf(stderr, "unknown mode\n");
		return 1;
	}
	return 0;
}
