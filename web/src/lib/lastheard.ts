// The rows of the Last heard table (LastHeardTable.svelte), shared by the overview Dashboard, the DV tab and the digital modes card.
// An AllStar call and a digital call end up in the same shape: callsign, mode, target, when, and the lookup links the callsign opens.
import { heardRow } from './dvpanel.ts';
import { qrzUrl, radioidUrl } from './nowcalling.ts';
import type { DVCaller, NowCall, PubSnapshot } from './types';

export interface HeardItem {
  key: string;
  /** What the callsign cell shows ("N0CALL", a DMR ID, "Echo test", "Private call"). */
  call: string;
  /** "ASL", "DMR", "YSF", "M17", ... */
  mode: string;
  /** "TG 91 · TS2", "YSF-TEST" (the Mode column says YSF), "Node 2001"; '' when not known. */
  target: string;
  /** When it was heard (ms); 0 when unknown. */
  at: number;
  /** The caller's name and place for the muted second line under the callsign ("Pat Example · Springfield, XX"); '' when not known. */
  person: string;
  /** Tooltip for the row. */
  title?: string;
  /** Lookup links; '' when there is nothing to look up (a pseudo-callsign, no DMR ID). */
  qrz: string;
  radioid: string;
}

/** The second line under a callsign: the name and the place, whichever are known. */
export const personLine = (name: string | undefined, place: string | undefined): string => [name, place].map((x) => (x ?? '').trim()).filter(Boolean).join(' · ');

/** A callsign worth a lookup: letters and a digit, so "Echo test", a bare DMR ID and "Private call" are not. */
export function lookupCall(call: string | undefined): string {
  const c = (call ?? '').trim();
  return /[A-Za-z]/.test(c) && /\d/.test(c) ? qrzUrl(c) : '';
}

const parseAt = (s: string | undefined): number => { const t = s ? Date.parse(s.replace(' ', 'T')) : NaN; return Number.isFinite(t) ? t : 0; };

/** Digital modes heard list (the snapshot's `heard`) as table rows. A private call has no callsign to look up. */
export function dvHeardItems(heard: DVCaller[] | undefined): HeardItem[] {
  return (heard ?? []).map((c, i) => {
    const r = heardRow(c);
    return {
      key: `dv:${i}:${c.at ?? ''}`,
      call: r.call,
      mode: r.mode,
      target: [r.target, r.slot].filter(Boolean).join(' · '),
      at: parseAt(c.at),
      person: c.private ? '' : personLine(c.name, c.place),
      title: c.dmrId ? `DMR ID ${c.dmrId}` : undefined,
      qrz: c.private ? '' : lookupCall(c.call),
      radioid: c.private ? '' : radioidUrl({ kind: 'dmr', dmrId: c.dmrId }),
    };
  });
}

/** AllStar calls (the Now Calling list; digital calls come from the snapshot instead) as table rows: mode "ASL", target the node. */
export function aslHeardItems(recent: NowCall[] | undefined): HeardItem[] {
  const out: HeardItem[] = [];
  for (const c of recent ?? []) {
    if (c.kind !== 'allstar') continue;
    const call = (c.call ?? '').trim();
    out.push({
      key: `asl:${c.id}`,
      call: call || (c.node ? `Node ${c.node}` : 'Unknown'),
      mode: 'ASL',
      target: c.node ? `Node ${c.node}` : '',
      at: parseAt(c.end || c.start),
      person: '',
      title: (c.desc ?? '').trim() || undefined,
      qrz: lookupCall(call),
      radioid: '',
    });
  }
  return out;
}

/** AllStar and digital rows together, newest first, at most `limit`. */
export function mergeHeard(recent: NowCall[] | undefined, heard: DVCaller[] | undefined, limit = 12): HeardItem[] {
  return [...aslHeardItems(recent), ...dvHeardItems(heard)].sort((a, b) => b.at - a.at).slice(0, limit);
}

/** The public page's Last heard rows: ASL transmissions (the public activity list) and, when the digital modes setting is on, DV calls, newest first.
 *  Only what the public API sends: no IDs, so a callsign opens QRZ and nothing opens radioid.net. A bridge node's ASL row is the DV call itself, so it is left out when DV is shown. */
export function pubHeardItems(snap: Pick<PubSnapshot, 'activity' | 'dv' | 'showDV' | 'showActivity'> | null | undefined, limit = 12): HeardItem[] {
  const out: HeardItem[] = [];
  (snap?.showActivity ? snap.activity ?? [] : []).forEach((a, i) => {
    if (a.link === 'local' || (snap?.showDV && a.label)) return;
    out.push({
      key: `asl:${i}:${a.at}`, call: a.callsign || a.label || `Node ${a.link}`, mode: 'ASL', target: `Node ${a.link}`, at: a.at * 1000,
      person: '', qrz: lookupCall(a.callsign), radioid: '',
    });
  });
  (snap?.showDV ? snap.dv?.heard ?? [] : []).forEach((c, i) => {
    out.push({
      key: `dv:${i}:${c.at}`, call: c.callsign || `${c.mode} caller`, mode: c.mode,
      target: [c.target, c.slot ? `TS${c.slot}` : ''].filter(Boolean).join(' \u00b7 '), at: c.at * 1000,
      person: personLine(c.name, c.location), qrz: lookupCall(c.callsign), radioid: '',
    });
  });
  return out.sort((a, b) => b.at - a.at).slice(0, limit);
}
