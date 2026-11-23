// Vector generator for internal/dv/dmr (see README.md), linked against unmodified MMDVMHost
// sources (G4KLX, GPL-2.0-or-later). Synthetic inputs only.
#define private public
#include "AMBEFEC.h"
#include "DMREmbeddedData.h"
#include "DMRFullLC.h"
#include "DMRLC.h"
#include "DMREMB.h"
#include "DMRSlotType.h"
#include "DMRDefines.h"
#include "Golay24128.h"
#undef private
#include <cstdio>
#include <cstring>

// The AMBEFEC tables are file-static; pull them in.
#include "AMBEFEC.cpp"

static unsigned int rng = 12345U;
static unsigned int rnd() { rng = rng * 1103515245U + 12345U; return (rng >> 8) & 0xFFFFFFU; }

static void hex(const unsigned char* p, unsigned int n) { for (unsigned int i = 0; i < n; i++) printf("%02x", p[i]); }

int main()
{
	// AMBE: clean frames built from (a12, b12, c25) with MMDVMHost's tables.
	for (int n = 0; n < 32; n++) {
		unsigned int ad = rnd() & 0xFFFU, bd = rnd() & 0xFFFU, c = rnd() & 0x1FFFFFFU;
		if (n == 0) { ad = 0; bd = 0; c = 0; }
		unsigned int a = CGolay24128::encode24128(ad);
		unsigned int p = PRNG_TABLE[ad] >> 1;
		unsigned int b = (CGolay24128::encode23127(bd) >> 1) ^ p;
		unsigned char f[9]; memset(f, 0, 9);
		unsigned int M = 0x800000U;
		for (int i = 0; i < 24; i++, M >>= 1) WRITE_BIT(f, DMR_A_TABLE[i], a & M);
		M = 0x400000U;
		for (int i = 0; i < 23; i++, M >>= 1) WRITE_BIT(f, DMR_B_TABLE[i], b & M);
		M = 0x1000000U;
		for (int i = 0; i < 25; i++, M >>= 1) WRITE_BIT(f, DMR_C_TABLE[i], c & M);
		// 49-bit form: a12 b12 c25, MSB first.
		unsigned char u[7]; memset(u, 0, 7);
		unsigned long long v = ((unsigned long long)ad << 37) | ((unsigned long long)bd << 25) | c;
		for (int i = 0; i < 49; i++) if (v & (1ULL << (48 - i))) u[i >> 3] |= 0x80 >> (i & 7);
		printf("ambe "); hex(u, 7); printf(" "); hex(f, 9); printf(" %06x\n", p);
	}
	// AMBE regenerate: bursts of three clean frames (as above) with n % 6
	// random bit errors per frame, plus four fully random bursts.
	CAMBEFEC fec;
	for (int n = 0; n < 40; n++) {
		unsigned char b[33];
		for (int i = 0; i < 33; i++) b[i] = rnd() & 0xFF;
		if (n < 36) {
			for (unsigned int k = 0; k < 3; k++) {
				unsigned int ad = rnd() & 0xFFFU, bd = rnd() & 0xFFFU, c = rnd() & 0x1FFFFFFU;
				unsigned int a = CGolay24128::encode24128(ad);
				unsigned int b2 = (CGolay24128::encode23127(bd) >> 1) ^ (PRNG_TABLE[ad] >> 1);
				for (unsigned int e = 0; e < (unsigned int)(n % 6); e++) {
					unsigned int bit = rnd() % 72U;
					if (bit < 24U) a ^= 1U << bit; else if (bit < 47U) b2 ^= 1U << (bit - 24U); else c ^= 1U << (bit - 47U);
				}
				unsigned int M = 0x800000U;
				for (int i = 0; i < 24; i++, M >>= 1) {
					unsigned int pos = DMR_A_TABLE[i] + 72U * k; if (k == 1U && pos >= 108U) pos += 48U; if (k == 2U) pos += 48U;
					WRITE_BIT(b, pos, a & M);
				}
				M = 0x400000U;
				for (int i = 0; i < 23; i++, M >>= 1) {
					unsigned int pos = DMR_B_TABLE[i] + 72U * k; if (k == 1U && pos >= 108U) pos += 48U; if (k == 2U) pos += 48U;
					WRITE_BIT(b, pos, b2 & M);
				}
				M = 0x1000000U;
				for (int i = 0; i < 25; i++, M >>= 1) {
					unsigned int pos = DMR_C_TABLE[i] + 72U * k; if (k == 1U && pos >= 108U) pos += 48U; if (k == 2U) pos += 48U;
					WRITE_BIT(b, pos, c & M);
				}
			}
		}
		unsigned char out[33]; memcpy(out, b, 33);
		unsigned int errs = fec.regenerateDMR(out);
		printf("regen "); hex(b, 33); printf(" "); hex(out, 33); printf(" %u\n", errs);
	}
	// FNV-1a 32 of the whole PRNG table as used (entry >> 1, 3 bytes, big endian).
	unsigned int h = 2166136261U;
	for (int i = 0; i < 4096; i++)
		for (int s = 16; s >= 0; s -= 8) { h ^= ((PRNG_TABLE[i] >> 1) >> s) & 0xFFU; h *= 16777619U; }
	printf("prngfnv %08x\n", h);
	// EMB for every (cc, pi, lcss).
	for (unsigned int i = 0; i < 128; i++) {
		CDMREMB emb; emb.setColorCode(i >> 3); emb.setPI((i & 4) != 0); emb.setLCSS(i & 3);
		unsigned char b[33]; memset(b, 0, 33); emb.getData(b);
		printf("emb %u %u %u ", i >> 3, (i >> 2) & 1, i & 3); hex(b + 13, 7); printf("\n");
	}
	// Slot type for a few (cc, dt).
	for (unsigned int i = 0; i < 256; i += 7) {
		CDMRSlotType st; st.setColorCode(i >> 4); st.setDataType(i & 0xF);
		unsigned char b[33]; memset(b, 0, 33); st.getData(b);
		printf("slottype %u %u ", i >> 4, i & 15); hex(b + 12, 9); printf("\n");
	}
	// LCs with placeholder IDs.
	struct { FLCO flco; unsigned int src, dst; unsigned char opt; } lcs[] = {
		{FLCO::GROUP, 1234567U, 9990U, 0U}, {FLCO::GROUP, 1234567U, 1997U, 0U},
		{FLCO::USER_USER, 1234567U, 123456701U & 0xFFFFFFU, 0x20U}, {FLCO::GROUP, 1234501U, 1999U, 0x00U},
	};
	for (auto& l : lcs) {
		CDMRLC lc(l.flco, l.src, l.dst); lc.m_options = l.opt;
		unsigned char raw[9]; lc.getData(raw);
		CDMRFullLC full;
		unsigned char h[33]; memset(h, 0, 33); full.encode(lc, h, DT_VOICE_LC_HEADER);
		unsigned char t[33]; memset(t, 0, 33); full.encode(lc, t, DT_TERMINATOR_WITH_LC);
		printf("lc "); hex(raw, 9); printf(" "); hex(h, 33); printf(" "); hex(t, 33); printf("\n");
		CDMREmbeddedData ed; ed.setLC(lc);
		printf("emblc "); hex(raw, 9);
		for (unsigned char k = 1; k <= 4; k++) {
			unsigned char b[33]; memset(b, 0, 33);
			unsigned char lcss = ed.getData(b, k);
			printf(" %u:", lcss); hex(b + 14, 5);
		}
		printf("\n");
	}
	return 0;
}
