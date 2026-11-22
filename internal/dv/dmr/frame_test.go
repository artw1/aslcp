package dmr

import (
	"bytes"
	"math/rand"
	"testing"
)

func TestSyncPatterns(t *testing.T) {
	// ETSI TS 102 361-1 table 9.2 (as MMDVMHost DMRDefines.h, shifted by a nibble).
	want := map[Sync][7]byte{
		SyncBSVoice:    {0x07, 0x55, 0xFD, 0x7D, 0xF7, 0x5F, 0x70},
		SyncBSData:     {0x0D, 0xFF, 0x57, 0xD7, 0x5D, 0xF5, 0xD0},
		SyncMSVoice:    {0x07, 0xF7, 0xD5, 0xDD, 0x57, 0xDF, 0xD0},
		SyncMSData:     {0x0D, 0x5D, 0x7F, 0x77, 0xFD, 0x75, 0x70},
		SyncTDMA1Voice: {0x05, 0xD5, 0x77, 0xF7, 0x75, 0x7F, 0xF0},
		SyncTDMA1Data:  {0x0F, 0x7F, 0xDD, 0x5D, 0xDF, 0xD5, 0x50},
		SyncTDMA2Voice: {0x07, 0xDF, 0xFD, 0x5F, 0x55, 0xD5, 0xF0},
		SyncTDMA2Data:  {0x0D, 0x75, 0x57, 0xF5, 0xFF, 0x7F, 0x50},
	}
	for s, w := range want {
		var b Burst
		b.PutSync(s)
		if !bytes.Equal(b[13:20], w[:]) {
			t.Errorf("sync %012x placed as %x, want %x", uint64(s), b[13:20], w)
		}
		if b.Sync() != s {
			t.Errorf("Sync() = %x", b.Sync())
		}
		b[15] ^= 0x41 // two bit errors
		if got, n := MatchSync(&b, SyncMaxBitError); got != s || n != 2 {
			t.Errorf("MatchSync = %x %d", got, n)
		}
	}
	var b Burst
	if s, _ := MatchSync(&b, SyncMaxBitError); s != SyncNone {
		t.Errorf("zero burst matched %x", s)
	}
	if !SyncBSVoice.IsVoice() || SyncBSVoice.IsData() || !SyncMSData.IsData() {
		t.Error("IsVoice/IsData")
	}
}

func TestLCBytes(t *testing.T) {
	lc := LC{FLCO: FLCOPrivate, FID: 0x10, ServiceOptions: 0x20, Dst: 0x123456, Src: 1234567}
	b := lc.Bytes()
	want := [9]byte{0x03, 0x10, 0x20, 0x12, 0x34, 0x56, 0x12, 0xD6, 0x87}
	if b != want {
		t.Fatalf("Bytes = %x", b)
	}
	got, err := ParseLC(b[:])
	if err != nil || got != lc || !got.Private() {
		t.Fatalf("ParseLC = %+v %v", got, err)
	}
	if _, err := ParseLC(b[:8]); err == nil {
		t.Fatal("short LC accepted")
	}
}

func TestLCBurstErrors(t *testing.T) {
	lc := LC{FLCO: FLCOGroup, Dst: 1998, Src: 1234567}
	b := LCBurst(lc, DataVoiceLCHeader, 7)
	if s, _ := MatchSync(&b, 0); s != SyncBSData {
		t.Fatalf("header sync %x", s)
	}
	// Wrong mask: a header is not a valid terminator.
	if _, err := DecodeLCBurst(&b, DataTerminatorWithLC); err == nil {
		t.Fatal("header decoded as terminator")
	}
	if _, err := DecodeLCBurst(&b, DataCSBK); err == nil {
		t.Fatal("CSBK accepted")
	}
	// Burst errors spread over the BPTC are corrected (FEC + RS).
	r := rand.New(rand.NewSource(7))
	for n := 0; n < 200; n++ {
		x := b
		for _, j := range r.Perm(196)[:3] {
			p := bptcPos(j)
			setBit(x[:], p, !getBit(x[:], p))
		}
		// and up to 3 slot type errors
		for _, j := range r.Perm(20)[:3] {
			p := 98 + j
			if j >= 10 {
				p = bitCentre2 + j - 10
			}
			setBit(x[:], p, !getBit(x[:], p))
		}
		got, st, err := ParseLCBurst(&x)
		if err != nil || got != lc || st != (SlotType{CC: 7, DataType: DataVoiceLCHeader}) {
			t.Fatalf("3+3 errors: %+v %+v %v", got, st, err)
		}
	}
}

func seqAMBE(i int) AMBE {
	return AMBE{byte(i), byte(i >> 8), 0x55, 0xAA, byte(i * 7), 0x0F, 0x80}
}

func TestTXRX(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cfg    TXConfig
		frames int
	}{
		{"group", TXConfig{Src: 1234567, Dst: 1997, CC: 1}, 54},
		{"private partial", TXConfig{Src: 1234567, Dst: 9990, Private: true, CC: 3, Headers: 2}, 20},
		{"pad superframe", TXConfig{Src: 1234567, Dst: 1999, CC: 15, PadSuperframe: true, MSSync: true}, 7},
		{"empty", TXConfig{Src: 1234567, Dst: 1997, CC: 1}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := NewTX(tc.cfg)
			out := tx.Start()
			hdrs := tc.cfg.Headers
			if hdrs == 0 {
				hdrs = 3
			}
			if len(out) != hdrs {
				t.Fatalf("%d headers", len(out))
			}
			for i := 0; i < tc.frames; i++ {
				out = append(out, tx.Write(seqAMBE(i))...)
			}
			out = append(out, tx.End()...)
			if tx.End() != nil || tx.Write(seqAMBE(0)) != nil {
				t.Fatal("frames after End")
			}
			bursts := (tc.frames + 2) / 3
			if tc.cfg.PadSuperframe {
				bursts = (bursts + 5) / 6 * 6
			}
			if len(out) != hdrs+bursts+1 {
				t.Fatalf("%d frames, want %d", len(out), hdrs+bursts+1)
			}
			voiceSync, dataSync := SyncBSVoice, SyncBSData
			if tc.cfg.MSSync {
				voiceSync, dataSync = SyncMSVoice, SyncMSData
			}
			want := tx.LC()
			rx := NewRX()
			var got []AMBE
			calls, ends := 0, 0
			for i, f := range out {
				switch {
				case i < hdrs:
					if f.FrameType != FrameDataSync || DataType(f.DTypeVSeq) != DataVoiceLCHeader {
						t.Fatalf("frame %d: %+v", i, f)
					}
				case i == len(out)-1:
					if f.FrameType != FrameDataSync || DataType(f.DTypeVSeq) != DataTerminatorWithLC {
						t.Fatalf("last frame: %+v", f)
					}
				default:
					seq := (i - hdrs) % 6
					if int(f.DTypeVSeq) != seq || (seq == 0) != (f.FrameType == FrameVoiceSync) {
						t.Fatalf("frame %d: type %d seq %d", i, f.FrameType, f.DTypeVSeq)
					}
					if seq == 0 {
						if s, _ := MatchSync(&f.Burst, 0); s != voiceSync {
							t.Fatalf("burst A sync %x", s)
						}
					} else {
						e, ok := f.Burst.EMB()
						if !ok || e.CC != tc.cfg.CC || e.PI {
							t.Fatalf("EMB %+v %v", e, ok)
						}
						if seq == 5 && (e.LCSS != LCSSSingle || f.Burst.Embedded() != [4]byte{}) {
							t.Fatal("burst F not null")
						}
					}
				}
				if f.FrameType == FrameDataSync {
					if s, _ := MatchSync(&f.Burst, 0); s != dataSync {
						t.Fatalf("data sync %x", s)
					}
					if st, ok := f.Burst.SlotType(); !ok || st.CC != tc.cfg.CC {
						t.Fatalf("slot type %+v", st)
					}
				}
				b := f.Burst
				res := rx.Push(f.FrameType, f.DTypeVSeq, &b)
				if res.NewCall {
					calls++
					if res.LC != want || !res.LCFromHeader {
						t.Fatalf("LC %+v", res.LC)
					}
				}
				if res.LCValid && res.LC != want {
					t.Fatalf("LC %+v", res.LC)
				}
				if res.HasVoice {
					if res.FECErrors != 0 {
						t.Fatalf("FEC errors %d", res.FECErrors)
					}
					got = append(got, res.Voice[:]...)
				}
				if res.End {
					ends++
				}
			}
			if calls != 1 || ends != 1 {
				t.Fatalf("calls %d ends %d", calls, ends)
			}
			if len(got) != 3*bursts {
				t.Fatalf("%d voice frames", len(got))
			}
			for i, f := range got {
				w := SilenceAMBE
				if i < tc.frames {
					w = seqAMBE(i)
				}
				if f != w {
					t.Fatalf("voice %d = %x, want %x", i, f, w)
				}
			}
			if want.Private() != tc.cfg.Private || want.Src != tc.cfg.Src || want.Dst != tc.cfg.Dst {
				t.Fatalf("LC %+v", want)
			}
		})
	}
}

func TestRXLateEntry(t *testing.T) {
	tx := NewTX(TXConfig{Src: 1234567, Dst: 9990, Private: true, CC: 1})
	tx.Start() // headers lost
	var bursts []TXFrame
	for i := 0; i < 3*14; i++ {
		bursts = append(bursts, tx.Write(seqAMBE(i))...)
	}
	rx := NewRX()
	// Join in the middle of a superframe (burst C): the first LC can only come
	// from the next complete B..E sequence.
	newAt := -1
	for i, f := range bursts[2:] {
		b := f.Burst
		res := rx.Push(f.FrameType, f.DTypeVSeq, &b)
		if res.NewCall {
			if newAt >= 0 {
				t.Fatal("second NewCall")
			}
			newAt = i + 2
			if res.LC != tx.LC() || res.LCFromHeader || !res.LC.Private() {
				t.Fatalf("late LC %+v", res.LC)
			}
		}
	}
	if newAt != 10 { // burst E of the second superframe
		t.Fatalf("late entry at burst %d", newAt)
	}
	if lc, ok := rx.LC(); !ok || lc != tx.LC() {
		t.Fatal("LC()")
	}
	// A corrupted EMB breaks the fragment chain for that superframe.
	rx.Reset()
	for i, f := range bursts[:6] {
		b := f.Burst
		if i == 2 {
			b[14] ^= 0xF0
			b[18] ^= 0x0F
		}
		if rx.Push(f.FrameType, f.DTypeVSeq, &b).LCValid {
			t.Fatal("LC from broken chain")
		}
	}
	// Other embedded LCs (e.g. talker alias, FLCO 4) are ignored.
	rx.Reset()
	ta := EmbeddedLC(LC{FLCO: 4, Dst: 0x4e3043, Src: 0x414c4c})
	for k := 0; k < 4; k++ {
		var b Burst
		b.PutVoice([3]AMBE{SilenceAMBE, SilenceAMBE, SilenceAMBE})
		b.PutEMB(EMB{CC: 1, LCSS: []LCSS{LCSSFirst, LCSSContinue, LCSSContinue, LCSSLast}[k]})
		b.PutEmbedded(ta[k])
		if rx.Push(FrameVoice, uint8(k+1), &b).LCValid {
			t.Fatal("talker alias reported as LC")
		}
	}
}

func TestRXErrors(t *testing.T) {
	tx := NewTX(TXConfig{Src: 1234567, Dst: 1997, CC: 1})
	frames := tx.Start()
	for i := 0; i < 6; i++ {
		frames = append(frames, tx.Write(seqAMBE(i))...)
	}
	frames = append(frames, tx.End()...)
	rx := NewRX()
	r := rand.New(rand.NewSource(8))
	for _, f := range frames {
		b := f.Burst
		if f.FrameType != FrameDataSync {
			// one correctable error in C0 of every AMBE frame
			v := b.Voice72()
			for k := range v {
				v[k] = ambeFlip(v[k], 0, r.Intn(24))
			}
			b.PutVoice72(v)
		}
		res := rx.Push(f.FrameType, f.DTypeVSeq, &b)
		if res.HasVoice && res.FECErrors != 3 {
			t.Fatalf("FEC errors %d", res.FECErrors)
		}
	}
	// Unknown data types are ignored.
	var b Burst
	if res := rx.Push(FrameDataSync, uint8(DataCSBK), &b); res.LCValid || res.End || res.HasVoice {
		t.Fatal("CSBK")
	}
}

func TestDMRD(t *testing.T) {
	d := DMRD{Seq: 0x2A, Src: 1234567, Dst: 9990, RepeaterID: 123456701, Slot: 2, Private: true,
		FrameType: FrameVoice, DTypeVSeq: 3, StreamID: 0xDEADBEEF, BER: 4, RSSI: 70}
	for i := range d.Burst {
		d.Burst[i] = byte(i)
	}
	p := d.Marshal()
	want := []byte{'D', 'M', 'R', 'D', 0x2A, 0x12, 0xD6, 0x87, 0x00, 0x27, 0x06,
		0x07, 0x5B, 0xCC, 0xBD, 0xC3, 0xDE, 0xAD, 0xBE, 0xEF}
	if len(p) != DMRDLen || !bytes.Equal(p[:20], want) || p[20] != 0 || p[52] != 32 || p[53] != 4 || p[54] != 70 {
		t.Fatalf("Marshal = %x", p)
	}
	got, err := ParseDMRD(p)
	d.HasBERRSSI = true
	if err != nil || got != d {
		t.Fatalf("ParseDMRD = %+v %v", got, err)
	}
	if lc := got.LC(); !lc.Private() || lc.Src != 1234567 || lc.Dst != 9990 {
		t.Fatalf("LC %+v", lc)
	}
	// Group, slot 1, data sync terminator, 53 bytes.
	d = DMRD{Src: 1234567, Dst: 1997, Slot: 1, FrameType: FrameDataSync, DTypeVSeq: uint8(DataTerminatorWithLC)}
	p = d.AppendTo([]byte("x"))[1:]
	if p[15] != 0x22 {
		t.Fatalf("flags %02x", p[15])
	}
	got, err = ParseDMRD(p[:DMRDMinLen])
	if err != nil || got.HasBERRSSI || got.Slot != 1 || got.Private || got.FrameType != FrameDataSync || got.DTypeVSeq != 2 {
		t.Fatalf("53-byte parse %+v %v", got, err)
	}
	if (&DMRD{FrameType: FrameVoiceSync}).Marshal()[15] != 0x10 {
		t.Fatal("voice sync flags")
	}
	for _, bad := range [][]byte{p[:52], append([]byte("DMRA"), p[4:]...)} {
		if _, err := ParseDMRD(bad); err == nil {
			t.Fatalf("accepted %x", bad)
		}
	}
}
