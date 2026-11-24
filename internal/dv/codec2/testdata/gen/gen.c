/* SPDX-License-Identifier: GPL-3.0-or-later
 *
 * Golden-vector generator for the pure-Go Codec2 3200 port in
 * internal/dv/codec2. Links against the unmodified Codec2-mod sources (see
 * README.md in this directory). It is test tooling only: it is not built by
 * "go build" or "go test".
 *
 *   gen sig OUT.pcm         synthetic test signal (s16le, 512 frames)
 *   gen enc IN.pcm OUT.bits encode: 8 bytes per 160-sample frame
 *   gen dec IN.bits OUT.pcm decode: 160 samples per 8-byte frame
 *   gen rnd N OUT.bits      N pseudo-random 64-bit frames
 *   gen libm OUT.txt        the libm-derived tables embedded in libmtables.go:
 *                           KISS twiddles, analysis and NLP windows, as hex
 *
 * The signal is integer-only (no libm) so the Go test can regenerate it
 * bit-exactly; keep sigGen in codec2_test.go in step with this file.
 */
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <math.h>
#include "codec2_mod.h"

#define NFRAMES 512

static uint32_t lcg_s = 1;
static uint32_t lcg_next(void)
{
	lcg_s = lcg_s * 1103515245u + 12345u;
	return lcg_s;
}

static int32_t isin(uint32_t phase)
{
	int32_t t = (int32_t)phase >> 16;
	int32_t at = t < 0 ? -t : t;
	int32_t y = (int32_t)(((int64_t)4 * t * (32768 - at)) >> 15);
	if (y > 32767)
		y = 32767;
	if (y < -32767)
		y = -32767;
	return y;
}

static uint32_t inc_for(uint32_t milli_hz)
{
	return (uint32_t)(((uint64_t)milli_hz << 32) / 8000000u);
}

static int16_t clip16(int64_t v)
{
	if (v > 32767)
		return 32767;
	if (v < -32768)
		return -32768;
	return (int16_t)v;
}

/* Three cascaded resonators, Q14 coefficients: a1, a2, gain (DC gain 1). */
static const int32_t formants[2][3][3] = {
	{{27075, -15386, 4695}, {18187, -15266, 13463}, {-14192, -14910, 45486}},
	{{31121, -15630, 893}, {-7355, -15146, 38885}, {-22017, -14794, 53195}},
};

enum { SIL, VOICED, HPNOISE, TONE, WHITE };

typedef struct {
	int f_end;     /* segment ends before this frame index */
	int kind;
	int f0a, f0b;  /* milli-Hz, linear glide over the segment */
	int vib;       /* vibrato depth, milli-Hz */
	int set;       /* formant set */
	int pulse;     /* pulse amplitude */
	int noise;     /* additive noise amplitude (Q15 scale) */
	int gain;      /* output gain, 256 = 1.0 */
} seg_t;

static const seg_t segs[] = {
	{20, SIL, 0, 0, 0, 0, 0, 0, 0},
	{140, VOICED, 100000, 160000, 0, 0, 4000, 0, 256},
	{160, HPNOISE, 0, 0, 0, 0, 0, 4000, 256},
	{260, VOICED, 200000, 120000, 8000, 1, 10000, 0, 256},
	{280, HPNOISE, 0, 0, 0, 0, 0, 30, 256},
	{340, VOICED, 130000, 90000, 0, 0, 4000, 0, 2560},
	{380, VOICED, 140000, 140000, 0, 1, 8000, 2500, 256},
	{400, TONE, 440000, 0, 0, 0, 0, 12000, 256},
	{450, VOICED, 65000, 90000, 3000, 0, 4000, 0, 256},
	{480, WHITE, 0, 0, 0, 0, 0, 0, 256},
	{512, VOICED, 220000, 220000, 0, 1, 8000, 400, 256},
};

static void gen_signal(int16_t *out)
{
	uint32_t phase = 0, vph = 0, tph = 0;
	int64_t y1[3] = {0, 0, 0}, y2[3] = {0, 0, 0};
	int32_t prev_x = 0;
	int si = 0, seg_start = 0;
	lcg_s = 1;
	for (int n = 0; n < NFRAMES * 160; n++) {
		int f = n / 160;
		while (f >= segs[si].f_end) {
			si++;
			seg_start = n;
		}
		const seg_t *sg = &segs[si];
		int seg_len = (sg->f_end * 160) - (si ? segs[si - 1].f_end * 160 : 0);
		int32_t noise = (int32_t)(lcg_next() >> 16) - 32768; /* -32768..32767 */
		(void)seg_start;
		switch (sg->kind) {
		case SIL:
			out[n] = 0;
			break;
		case VOICED: {
			int32_t pos = n - (si ? segs[si - 1].f_end * 160 : 0);
			int64_t f0 = sg->f0a + ((int64_t)(sg->f0b - sg->f0a) * pos) / seg_len;
			f0 += ((int64_t)isin(vph) * sg->vib) >> 15;
			vph += inc_for(5500);
			uint32_t old = phase;
			phase += inc_for((uint32_t)f0);
			int64_t x = (phase < old) ? sg->pulse : 0;
			x += ((int64_t)noise * sg->noise) >> 15;
			for (int k = 0; k < 3; k++) {
				const int32_t *c = formants[sg->set][k];
				int64_t y = (x * c[2] + (int64_t)c[0] * y1[k] + (int64_t)c[1] * y2[k]) >> 14;
				if (y > (1 << 26))
					y = 1 << 26;
				if (y < -(1 << 26))
					y = -(1 << 26);
				y2[k] = y1[k];
				y1[k] = y;
				x = y;
			}
			out[n] = clip16((x * sg->gain) >> 8);
			break;
		}
		case HPNOISE: {
			int32_t x = (int32_t)(((int64_t)noise * sg->noise) >> 15);
			out[n] = clip16((int64_t)(x - prev_x));
			prev_x = x;
			break;
		}
		case TONE:
			out[n] = clip16(((int64_t)isin(tph) * sg->noise) >> 15);
			tph += inc_for((uint32_t)sg->f0a);
			break;
		case WHITE:
			out[n] = (int16_t)noise;
			break;
		}
	}
}

static void die(const char *m)
{
	fprintf(stderr, "gen: %s\n", m);
	exit(1);
}

static void *slurp(const char *path, size_t *n)
{
	FILE *f = fopen(path, "rb");
	if (!f)
		die("open input");
	fseek(f, 0, SEEK_END);
	long sz = ftell(f);
	fseek(f, 0, SEEK_SET);
	void *p = malloc(sz ? sz : 1);
	if (fread(p, 1, sz, f) != (size_t)sz)
		die("read input");
	fclose(f);
	*n = sz;
	return p;
}

static void spit(const char *path, const void *p, size_t n)
{
	FILE *f = fopen(path, "wb");
	if (!f)
		die("open output");
	if (fwrite(p, 1, n, f) != n)
		die("write output");
	fclose(f);
}

static void hexline(FILE *f, const char *name, int i, float a, float b)
{
	uint32_t x, y;
	memcpy(&x, &a, 4);
	memcpy(&y, &b, 4);
	fprintf(f, "%s %d %08x %08x\n", name, i, x, y);
}

/* Reproduces, with the host libm, the cosf()/sinf() values that the Codec2-mod
 * sources compute at start-up (kiss_fft.c, kiss_fftr.c, analysis.c, nlp.c). */
static void dump_libm(const char *path)
{
	FILE *f = fopen(path, "w");
	const double pi = 3.141592653589793238462643383279502884197169399375105820974944;
	if (!f)
		die("open");
	for (int i = 0; i < 256; i++) {
		double ph = -2 * pi * i / 256;
		hexline(f, "tw", i, cosf(ph), sinf(ph));
	}
	for (int i = 0; i < 128; i++) {
		float ph = -3.14159265358979323846264338327 * ((float)(i + 1) / 256 + .5);
		hexline(f, "super", i, cosf(ph), sinf(ph));
	}
	for (int j = 0; j < 279; j++) {
		float phase = (2.0f * 3.14159265358979323846) * j / (279 - 1);
		hexline(f, "analysis", j, 0.5f - 0.5f * cosf(phase), 0);
	}
	for (int i = 0; i < 64; i++)
		hexline(f, "nlp", i, 0.5 - 0.5 * cosf((2.0f * 3.14159265358979323846) * i / (64 - 1)), 0);
	fclose(f);
}

int main(int argc, char **argv)
{
	if (argc < 3)
		die("usage");
	if (!strcmp(argv[1], "sig")) {
		static int16_t pcm[NFRAMES * 160];
		gen_signal(pcm);
		spit(argv[2], pcm, sizeof pcm); /* little-endian host assumed */
	} else if (!strcmp(argv[1], "enc") && argc == 4) {
		size_t n;
		int16_t *pcm = slurp(argv[2], &n);
		int nf = (int)(n / 320);
		uint8_t *bits = calloc(nf, 8);
		static codec2_encoder_t enc;
		codec2_encoder_init(&enc);
		for (int i = 0; i < nf; i++)
			codec2_encode(&enc, bits + 8 * i, pcm + 160 * i);
		spit(argv[3], bits, (size_t)nf * 8);
	} else if (!strcmp(argv[1], "dec") && argc == 4) {
		size_t n;
		uint8_t *bits = slurp(argv[2], &n);
		int nf = (int)(n / 8);
		int16_t *pcm = calloc(nf * 160, 2);
		static codec2_decoder_t dec;
		codec2_decoder_init(&dec);
		for (int i = 0; i < nf; i++)
			codec2_decode(&dec, pcm + 160 * i, bits + 8 * i);
		spit(argv[3], pcm, (size_t)nf * 320);
	} else if (!strcmp(argv[1], "libm") && argc == 3) {
		dump_libm(argv[2]);
	} else if (!strcmp(argv[1], "rnd") && argc == 4) {
		int nf = atoi(argv[2]);
		uint8_t *bits = calloc(nf, 8);
		lcg_s = 7;
		for (int i = 0; i < nf * 8; i++)
			bits[i] = (uint8_t)(lcg_next() >> 16);
		spit(argv[3], bits, (size_t)nf * 8);
	} else
		die("usage");
	return 0;
}
