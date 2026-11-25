// The public page's Now Calling strip: the call in progress (red) or the most recent public activity (muted "Last: ..." line).
// Built only from the public snapshot, so it never holds an ID; the sheet shows plain rows and a QRZ link. Tested in pubnow.test.ts.
import { agoText, mmss, qrzUrl } from './nowcalling.ts';
import type { SheetRow } from './nowcalling.ts';
import { pubBanner } from './tiles.ts';
import type { PubSnapshot } from './types';

export interface PubNow {
  mode: 'talking' | 'idle';
  /** "ASL", "DMR", "YSF". */
  source: string;
  /** "N0CALL · Pat Example" (the strip's main line); '' when nothing is known. */
  main: string;
  call: string; name: string; place: string; target: string;
  /** Start in the page's clock (ms); 0 when unknown. */
  startMs: number;
  /** Length of a finished call in seconds; null when unknown. */
  durSec: number | null;
  qrz: string;
}

/** Talking: the banner call. Idle: the newest public ASL activity with a callsign or DV heard call. null when there is nothing. */
export function pubNow(snap: Pick<PubSnapshot, 'nodes' | 'dv' | 'showDV' | 'showActivity' | 'activity'> | null | undefined, skewMs: number, nodeAt: Record<string, number>): PubNow | null {
  if (!snap) return null;
  const b = pubBanner(snap, skewMs, nodeAt);
  if (b) {
    return { mode: 'talking', source: b.source, main: [b.main, b.sub].filter(Boolean).join(' · '), call: b.call, name: b.name, place: b.place, target: b.target, startMs: b.startMs, durSec: null, qrz: b.qrz };
  }
  const cands: PubNow[] = [];
  if (snap.showActivity) {
    for (const a of snap.activity ?? []) {
      if (!a.callsign || a.link === 'local' || (snap.showDV && a.label)) continue;
      cands.push({ mode: 'idle', source: 'ASL', main: a.callsign, call: a.callsign, name: '', place: '', target: `Node ${a.link}`, startMs: a.at * 1000 - skewMs, durSec: a.duration, qrz: qrzUrl(a.callsign) });
    }
  }
  if (snap.showDV) {
    for (const c of snap.dv?.heard ?? []) {
      cands.push({
        mode: 'idle', source: c.mode, main: c.callsign ?? '', call: c.callsign ?? '', name: c.name ?? '', place: c.location ?? '',
        target: [c.target, c.slot ? `TS${c.slot}` : ''].filter(Boolean).join(' · '), startMs: c.at * 1000 - skewMs, durSec: null, qrz: qrzUrl(c.callsign),
      });
    }
  }
  cands.sort((a, b2) => b2.startMs - a.startMs);
  return cands[0] ?? null;
}

/** "Last: N0CALL · DMR TG 91 · 5 min ago", with the empty parts left out. */
export function pubIdleLine(n: PubNow, nowMs: number): string {
  const what = n.call || [n.source, n.target].filter(Boolean).join(' ');
  const src = n.call ? [n.source, n.target].filter(Boolean).join(' ') : '';
  return ['Last: ' + what, src, n.startMs ? agoText((nowMs - n.startMs) / 1000) : ''].filter(Boolean).join(' · ');
}

/** The strip's spoken label. */
export function pubAria(n: PubNow, nowMs: number): string {
  if (n.mode === 'talking') return `Now calling: ${n.main || n.source}, ${n.source}, ${mmss(n.startMs ? (nowMs - n.startMs) / 1000 : 0)}. Open details.`;
  return `${pubIdleLine(n, nowMs)}. Open details.`;
}

/** The public-safe rows of the details sheet: source, callsign, name, place, started, duration. */
export function pubSheetRows(n: PubNow, nowMs: number, fmtTime: (ms: number) => string = (ms) => new Date(ms).toLocaleTimeString()): SheetRow[] {
  const rows: SheetRow[] = [{ label: 'Source', value: [n.source, n.target].filter(Boolean).join(' · ') }];
  if (n.call) rows.push({ label: 'Callsign', value: n.call, cls: 'font-semibold' });
  if (n.name) rows.push({ label: 'Name', value: n.name });
  if (n.place) rows.push({ label: 'Place', value: n.place });
  if (n.startMs) rows.push({ label: 'Started', value: fmtTime(n.startMs) });
  const dur = n.mode === 'talking' ? (n.startMs ? Math.max(0, (nowMs - n.startMs) / 1000) : null) : n.durSec;
  rows.push({ label: 'Duration', value: dur == null ? 'unknown' : mmss(dur), cls: 'tabular-nums' });
  return rows;
}
