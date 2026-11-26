// The public page's Now Calling strip: the call in progress (red) or the most recent public activity (muted "Last: ..." line).
// Built only from the public snapshot, so it never holds an ID; the sheet shows plain rows and a QRZ link. Tested in pubnow.test.ts.
import { agoText, mmss, qrzUrl } from './nowcalling.ts';
import type { SheetRow } from './nowcalling.ts';
import { modeTitle } from './modes.ts';
import { targetCell } from './lastheard.ts';
import type { PubCall, PubSnapshot } from './types';

export interface PubNow {
  mode: 'talking' | 'idle';
  /** "AllStar", "BrandMeister", "YSF". */
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

/** Talking: the call in progress. Idle: the newest heard call with a callsign. null when there is nothing. */
export function pubNow(snap: Pick<PubSnapshot, 'calling' | 'heard' | 'showHeard'> | null | undefined, skewMs: number): PubNow | null {
  if (!snap) return null;
  const mk = (c: PubCall, mode: 'talking' | 'idle'): PubNow => ({
    mode, source: modeTitle(c.mode), main: [c.callsign, c.name].filter(Boolean).join(' · '), call: c.callsign ?? '', name: c.name ?? '', place: c.location ?? '',
    target: targetCell(c.mode, c.target), startMs: c.at * 1000 - skewMs, durSec: mode === 'idle' ? c.duration : null, qrz: qrzUrl(c.callsign),
  });
  if (snap.calling) return mk(snap.calling, 'talking');
  if (!snap.showHeard) return null;
  const last = [...(snap.heard ?? [])].filter((c) => c.callsign).sort((a, b) => b.at - a.at)[0];
  return last ? mk(last, 'idle') : null;
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
