// The modes of the node: names, order and small helpers for the UI. Pure functions, tested in modes.test.ts.
import type { Favorite, Heard, ModeField, ModeInfo, NowCall, Target } from './types';

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

/** The first required field that is empty (and not a saved secret), or null. */
export function missingField(m: ModeInfo, draft: Record<string, string>): ModeField | null {
  for (const f of m.fields ?? []) {
    if (!f.required) continue;
    if ((draft[f.key] ?? '').trim() !== '') continue;
    if (f.secret && m.secretsSet?.[f.key]) continue;
    return f;
  }
  return null;
}

/** True when the draft differs from the saved values (a typed secret counts). */
export function dirty(m: ModeInfo, draft: Record<string, string>): boolean {
  const base = draftOf(m);
  return (m.fields ?? []).some((f) => (draft[f.key] ?? '') !== (base[f.key] ?? ''));
}

/** A short line for a mode's link state, for the mode cards. */
export function stateText(state: string | undefined, detail?: string, err?: string): string {
  if (err) return err;
  const s = ({ idle: 'Idle', starting: 'Starting', ready: 'Ready', connecting: 'Connecting', connected: 'Connected', error: 'Error' } as Record<string, string>)[state ?? ''] ?? '';
  return detail && state !== 'idle' ? `${s} · ${detail}` : s;
}
