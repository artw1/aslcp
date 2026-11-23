// Search and labels for the digital modes host and TG pickers (docs/DV.md, "Host and TG lists").
// A list holds a few thousand entries: each gets one lowercase haystack once, a query then
// needs every word in it, so a keystroke costs one pass of string searches.
import type { DVListItem, DVListMode } from './types';

export interface DVIndexed { item: DVListItem; hay: string; value: string; name: string }

export function indexList(items: DVListItem[]): DVIndexed[] {
  return items.map((item) => ({
    item,
    value: item.value.toLowerCase(),
    name: item.name.toLowerCase(),
    hay: `${item.name} ${item.value} ${item.detail ?? ''} ${item.address} ${item.address}:${item.port}`.toLowerCase(),
  }));
}

/** Entries that match every word of `query` (name, ID/TG, description, address), best first, at most `limit`. An empty query returns the first entries. */
export function searchList(index: DVIndexed[], query: string, limit = 50): DVListItem[] {
  const words = query.toLowerCase().split(/\s+/).filter(Boolean);
  if (!words.length) return index.slice(0, limit).map((x) => x.item);
  const hits: { rank: number; item: DVListItem }[] = [];
  for (const x of index) {
    if (!words.every((w) => x.hay.includes(w))) continue;
    const q = words[0];
    // exact ID or address first, then names and IDs that start with the query, then the rest
    const rank = x.value === q ? 0 : x.value.startsWith(q) ? 1 : x.name.startsWith(q) ? 2 : 3;
    hits.push({ rank, item: x.item });
  }
  hits.sort((a, b) => a.rank - b.rank);
  return hits.slice(0, limit).map((h) => h.item);
}

/** The reflector lists of these modes are DVRef data and carry its credit; the DMR list is not. */
export const DVREF_URL = 'https://dvref.com/';
export const DVREF_TEXT = 'Reflector data provided by';
export const hasDVRefCredit = (mode: string): boolean => ['ysf', 'p25', 'nxdn', 'm17'].includes(mode.toLowerCase());

/** "name — id — address:port". The DMR and TG lists leave the id out: the address (or the TG in the name) is the value. */
export function optionLabel(mode: DVListMode, o: DVListItem): string {
  const parts = [o.name];
  if (mode === 'ysf') parts.push(o.value);
  parts.push(`${o.address}:${o.port}`);
  return parts.join(' — ');
}

/** The entry whose value is `value` (case-insensitive), or undefined for a value the user typed. */
export function findByValue(items: DVListItem[], value: string): DVListItem | undefined {
  const v = value.trim().toLowerCase();
  return v ? items.find((i) => i.value.toLowerCase() === v) : undefined;
}
