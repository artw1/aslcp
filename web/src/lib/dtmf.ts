// Types and pure helpers for the read-only DTMF commands and macros view (docs/CONFIG.md "DTMF commands and macros").
import { advancedLink } from './nodecfg.ts';

export interface DtmfItem { code: string; action: string; desc?: string; file: string; line: number; section: string; inherited?: boolean }
export interface DtmfGroup {
  kind: 'functions' | 'link_functions' | 'phone_functions' | 'telemetry' | 'macro';
  title: string; stanza?: string; assumed?: boolean; sameAs?: string; file?: string; line?: number; note?: string; items: DtmfItem[];
}
export interface Dtmf { node: string; groups: DtmfGroup[] }

/** The prefix a person types before the code, for the group (*-codes of the stock configuration). */
export function codeLabel(kind: DtmfGroup['kind'], code: string): string {
  if (kind === 'telemetry') return code;
  if (kind === 'macro') return `*5 ${code}`;
  return `*${code}`;
}

/** The text for an item: the plain-language meaning, or the raw line when the code is not known. */
export function itemText(it: DtmfItem): { text: string; known: boolean } {
  return it.desc ? { text: it.desc, known: true } : { text: `${it.code} = ${it.action}`, known: false };
}

/** Link to the item's line in Advanced. */
export function itemHref(it: { file: string; line: number }): string {
  return '#' + advancedLink(it.file, it.line);
}

/** Link to the stanza of a group in Advanced ('' when its place is not known). */
export function groupHref(g: DtmfGroup): string {
  return g.file ? '#' + advancedLink(g.file, g.line) : '';
}

/** One line for the overview card. */
export function dtmfSummary(d: Dtmf | null): string {
  if (!d) return 'Loading…';
  const fn = d.groups.find((g) => g.kind === 'functions');
  const mac = d.groups.find((g) => g.kind === 'macro');
  const parts: string[] = [];
  if (fn?.items.length) parts.push(`${fn.items.length} command${fn.items.length === 1 ? '' : 's'}`);
  else if (fn?.note) parts.push('no commands found');
  if (mac) parts.push(mac.items.length ? `${mac.items.length} macro${mac.items.length === 1 ? '' : 's'}` : 'no macros');
  const unknown = d.groups.reduce((n, g) => n + (g.kind === 'telemetry' || g.kind === 'macro' ? 0 : g.items.filter((i) => !i.desc).length), 0);
  if (unknown) parts.push(`${unknown} not recognised`);
  return parts.join(' · ') || 'No DTMF settings found';
}
