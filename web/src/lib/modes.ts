// The modes of the node: names, order and small helpers for the UI. Pure functions, tested in modes.test.ts.
import type { Favorite, Heard, LinkStats, ModeField, ModeInfo, NowCall, Station, Target, Vocoder } from './types';

/** The modes in the order the UI lists them. */
export const MODE_ORDER = ['allstar', 'echolink', 'bm', 'dmr', 'tgif', 'ysf', 'p25', 'nxdn', 'm17'] as const;

const TITLES: Record<string, string> = {
  allstar: 'AllStar', echolink: 'EchoLink', bm: 'BrandMeister', dmr: 'DMR', tgif: 'TGIF', ysf: 'YSF', p25: 'P25', nxdn: 'NXDN', m17: 'M17',
};

/** The display name of a mode; the server's title wins when the list is loaded. */
export function modeTitle(name: string, list?: ModeInfo[]): string {
  return list?.find((m) => m.name === name)?.title || TITLES[name] || name.toUpperCase();
}

/** Sort modes into the fixed order; unknown names go last, alphabetically. */
export function sortModes<T extends { name: string }>(l: T[]): T[] {
  const rank = (n: string) => { const i = (MODE_ORDER as readonly string[]).indexOf(n); return i < 0 ? 99 : i; };
  return [...l].sort((a, b) => rank(a.name) - rank(b.name) || a.name.localeCompare(b.name));
}

/** "Target" label of a mode: the server's targetLabel, else a generic one. */
export const targetLabel = (m: ModeInfo | undefined): string => m?.targetLabel || 'Target';

/** What a target looks like in a line: the name when known, else its ID. */
export function targetText(t: Target | undefined): string {
  if (!t) return '';
  return t.name && t.name !== t.id ? `${t.id} ${t.name}` : t.id;
}

/** The body of POST /api/connect for a typed or picked target. */
export function targetBody(id: string, extra: Partial<Target> = {}) {
  const b: { target: string; name?: string; host?: string; port?: number; slot?: number; private?: boolean } = { target: id.trim() };
  if (extra.name) b.name = extra.name;
  if (extra.host) { b.host = extra.host; if (extra.port) b.port = extra.port; }
  if (extra.slot) b.slot = extra.slot;
  if (extra.private) b.private = true;
  return b;
}

/** Favorites of one mode, pinned first, in their saved order. */
export function favoritesOf(all: Favorite[], mode: string): Favorite[] {
  return all.filter((f) => f.mode === mode).sort((a, b) => Number(b.pinned) - Number(a.pinned) || a.position - b.position);
}

/** Recently heard targets of one mode (distinct, newest first), for the "recent" picker. */
export function recentTargets(heard: Heard[], calls: NowCall[], mode: string, limit = 8): string[] {
  const seen = new Set<string>(), out: string[] = [];
  const add = (t: string | undefined) => { const v = (t ?? '').trim(); if (v && !seen.has(v)) { seen.add(v); out.push(v); } };
  for (const c of calls) if (c.kind === mode) add(c.target);
  for (const h of heard) if (h.mode === mode) add(h.target);
  return out.slice(0, limit);
}

/** A form value draft of a mode: every field's current value; secrets start empty (blank keeps the saved one). */
export function draftOf(m: ModeInfo): Record<string, string> {
  const d: Record<string, string> = {};
  for (const f of m.fields ?? []) d[f.key] = f.secret ? '' : m.settings?.values?.[f.key] ?? f.default ?? '';
  return d;
}

/** The values to save: a blank secret is left out so the server keeps it. */
export function savePayload(fields: ModeField[], draft: Record<string, string>): Record<string, string> {
  const v: Record<string, string> = {};
  for (const f of fields) {
    const x = (draft[f.key] ?? '').trim();
    if (f.secret && x === '') continue;
    v[f.key] = f.kind === 'password' ? draft[f.key] : x;
  }
  return v;
}

/** The station values the modes fall back to. */
export type StationValues = Pick<Station, 'callsign' | 'dmrId' | 'nxdnId'>;

/** The station value a field falls back to, as the form shows it ("" when the station has none). */
export function stationValue(f: ModeField, st?: StationValues): string {
  if (!f.stationDefault || !st) return '';
  const v = f.stationDefault === 'callsign' ? (st.callsign ?? '').trim().toUpperCase() : st[f.stationDefault] ? String(st[f.stationDefault]) : '';
  return v ? v + (f.stationSuffix ?? '') : '';
}

/** The hint under a field that falls back to the station: "from station: N0CALL", or what to set when the station has none. */
export function stationHint(f: ModeField, st?: StationValues): string {
  const v = stationValue(f, st);
  if (v) return `from station: ${v}`;
  const what = f.stationDefault === 'callsign' ? 'callsign' : f.stationDefault === 'nxdnId' ? 'NXDN ID' : 'DMR ID';
  return st ? `the station has no ${what} yet` : 'empty uses the station value';
}

/** The first required field that is empty (and not a saved secret), or null. A field with a stationDefault may be empty
 * when the station has that value; `st` is the station, and leaving it out assumes the node will check. */
export function missingField(m: ModeInfo, draft: Record<string, string>, st?: StationValues): ModeField | null {
  for (const f of m.fields ?? []) {
    if (!f.required) continue;
    if (f.stationDefault && (!st || stationValue(f, st))) continue;
    if ((draft[f.key] ?? '').trim() !== '') continue;
    if (f.secret && m.secretsSet?.[f.key]) continue;
    return f;
  }
  return null;
}

/** Why a mode is not ready, in plain words, from the server's `missing` keys ("" when ready). The word "login" is only for a password. */
export function notReadyText(m: ModeInfo): string {
  if (m.configured) return '';
  const title = m.title || modeTitle(m.name);
  const out: string[] = [];
  for (const key of m.missing ?? []) {
    const f = m.fields?.find((x) => x.key === key);
    if (f?.stationDefault) {
      const what = f.stationDefault === 'callsign' ? 'callsign' : f.stationDefault === 'nxdnId' ? 'NXDN ID' : 'DMR ID';
      out.push(`Set your ${what} in Settings → Station or on this mode's page.`);
    } else if (f && (f.secret || f.kind === 'password')) out.push(`${title} needs its password.`);
    else if (f) out.push(`${title} needs its ${f.label.toLowerCase()}.`);
    else out.push(`${title} needs ${key}.`);
  }
  if (!out.length) return m.error ? `${title} cannot start: ${m.error}` : `${title} needs setting up.`;
  return out.join(' ');
}

/** A short card line for a mode that is not ready. */
export function notReadyShort(m: ModeInfo): string {
  const n = m.missing?.length ?? 0;
  return n ? `Needs ${m.missing!.map((k) => m.fields?.find((f) => f.key === k)?.label.toLowerCase() ?? k).join(', ')}` : 'Not set up';
}

/** True when the draft differs from the saved values (a typed secret counts). */
export function dirty(m: ModeInfo, draft: Record<string, string>): boolean {
  const base = draftOf(m);
  return (m.fields ?? []).some((f) => (draft[f.key] ?? '') !== (base[f.key] ?? ''));
}

/** True when the mode needs the AMBE vocoder and it is not installed and running. v is null until GET /api/vocoder answered. */
export function needsVocoder(m: ModeInfo | undefined, v: Vocoder | null): boolean {
  return !!m?.needsAmbe && !!v && !(v.installed && v.active);
}

/** Whether to keep polling GET /api/vocoder: an install is running. */
export const vocoderBusy = (v: Vocoder | null): boolean => v?.job === 'running';

/** One compact line of the AllStar link counters ("rx 120+3400 tx 0 · VNAK 0/0 · retx 0 · gaps 0"), or "" without them. */
export function linkStatsText(l: LinkStats | undefined): string {
  if (!l) return '';
  const parts = [`rx ${l.rxVoiceFull}+${l.rxVoiceMini}`, `tx ${l.txVoice}`, `VNAK ${l.vnakSent}/${l.vnakRecv}`, `retx ${l.retransmits}`];
  if (l.gaps) parts.push(`gaps ${l.gaps}`);
  if (l.dropped) parts.push(`dropped ${l.dropped}`);
  if (l.parseErrors) parts.push(`bad ${l.parseErrors}`);
  return parts.join(' · ');
}

/** A short line for a mode's link state, for the mode cards. */
export function stateText(state: string | undefined, detail?: string, err?: string): string {
  if (err) return err;
  const s = ({ idle: 'Idle', starting: 'Starting', ready: 'Ready', connecting: 'Connecting', connected: 'Connected', error: 'Error' } as Record<string, string>)[state ?? ''] ?? '';
  return detail && state !== 'idle' ? `${s} · ${detail}` : s;
}
