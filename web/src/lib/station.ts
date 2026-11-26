// Station settings helpers. Pure functions, tested in station.test.ts.
import type { Station } from './types';

/** The standard CTCSS tones (Hz), for the tone picker's suggestions. */
export const CTCSS = ['67.0', '71.9', '74.4', '77.0', '79.7', '82.5', '85.4', '88.5', '91.5', '94.8', '97.4', '100.0', '103.5', '107.2', '110.9', '114.8', '118.8', '123.0', '127.3', '131.8', '136.5', '141.3', '146.2', '151.4', '156.7', '162.2', '167.9', '173.8', '179.9', '186.2', '192.8', '203.5', '210.7', '218.1', '225.7', '233.6', '241.8', '250.3'];

/** A tone as the node accepts it: "", a CTCSS tone with one decimal ("100.0") or a DCS code ("D023N"). Returns an error text or ''. */
export function toneError(t: string | undefined): string {
  const s = (t ?? '').trim();
  if (s === '') return '';
  if (/^D\d{3}[NI]$/i.test(s)) return '';
  if (CTCSS.includes(s)) return '';
  return 'Use a standard CTCSS tone such as 100.0 or a DCS code such as D023N';
}

/** Largest digital IDs: DMR is 24 bits, NXDN 16 bits. */
export const MAX_DMR_ID = 16777215, MAX_NXDN_ID = 65535;

/** A digital ID as the node accepts it: empty or 0 for none, otherwise a whole number 1 to `max`. Returns an error text or ''. */
export function digitalIdError(label: string, v: unknown, max: number): string {
  if (v == null || (typeof v === 'string' && v.trim() === '')) return '';
  const n = typeof v === 'number' ? v : /^\d+$/.test(String(v).trim()) ? Number(String(v).trim()) : NaN;
  if (n === 0) return '';
  if (!Number.isInteger(n) || n < 1 || n > max) return `${label} must be a whole number from 1 to ${max}`;
  return '';
}

/** A digital ID to save: a positive whole number, or undefined (left out) when empty or 0. */
export function digitalIdValue(v: unknown): number | undefined {
  const n = typeof v === 'number' ? v : Number(String(v ?? '').trim());
  return Number.isInteger(n) && n > 0 ? n : undefined;
}

/** Validation of the station form; returns an error text or ''. `radio` false skips the radio fields. */
export function validateStation(st: Station, radio: boolean): string {
  const call = (st.callsign ?? '').trim();
  if (!/^[A-Za-z0-9/]{3,12}$/.test(call) || !/\d/.test(call) || !/[A-Za-z]/.test(call)) return 'Enter the station callsign, for example N0CALL';
  const idErr = digitalIdError('DMR ID', st.dmrId, MAX_DMR_ID) || digitalIdError('NXDN ID', st.nxdnId, MAX_NXDN_ID);
  if (idErr) return idErr;
  if (!radio) return '';
  const f = st.radio.frequencyMHz;
  if (f != null && f !== 0 && (!Number.isFinite(f) || f < 100 || f > 500)) return 'Enter the frequency in MHz, for example 146.520';
  const sq = st.radio.squelch;
  if (sq != null && (!Number.isInteger(sq) || sq < 0 || sq > 8)) return 'Squelch is 0 to 8';
  return toneError(st.radio.tone);
}

/** The body to save: the callsign upper-cased and trimmed, empty numbers and unset IDs left out. */
export function stationBody(st: Station, radio: boolean): Station {
  const r = { ...st.radio };
  const { dmrId: _d, nxdnId: _n, ...rest } = st;
  const ids: Pick<Station, 'dmrId' | 'nxdnId'> = {};
  const d = digitalIdValue(st.dmrId), n = digitalIdValue(st.nxdnId);
  if (d) ids.dmrId = d;
  if (n) ids.nxdnId = n;
  st = { ...rest, ...ids };
  if (!radio) return { ...st, callsign: (st.callsign ?? '').trim().toUpperCase() };
  r.tone = (r.tone ?? '').trim();
  if (r.frequencyMHz == null || (r.frequencyMHz as unknown) === '') delete r.frequencyMHz;
  if (r.squelch == null || (r.squelch as unknown) === '') delete r.squelch;
  return { ...st, callsign: (st.callsign ?? '').trim().toUpperCase(), radio: r };
}
