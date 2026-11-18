// DMR connection method card (docs/DVSWITCH.md "DMR connection method"): MMDVM_Bridge's own DMR
// network, or BrandMeister through STFU. Pure helpers so node --test can cover them.
import type { DVDMRConn, DVDMRConnReq, DVDMRRx, DVDMRStatus, DVDMRVia, DVSTFUStatus } from './types';

export const DEFAULT_STFU_SERVER = '3102.repeater.net';
export const TALKER_ALIAS_MAX = 27;

/** The service that holds the DMR login for a method. */
export const unitFor = (via: DVDMRVia | undefined): string => (via === 'stfu' ? 'stfu' : 'mmdvm_bridge');

/** Same rule as the helper: dot-separated labels of letters, digits and inner hyphens, 253 characters at most. */
export function validHost(h: string): boolean {
  if (!h || h.length > 253) return false;
  return h.split('.').every((l) => /^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$/.test(l));
}

/** Same rule as the helper: printable ASCII without quotes, #, ; or =, up to 27 characters, no outer spaces. Empty keeps the saved one (or the callsign). */
export function validAlias(a: string): boolean {
  return a === '' || (a.length <= TALKER_ALIAS_MAX && a === a.trim() && /^[\x20-\x7e]+$/.test(a) && !/["';#=]/.test(a));
}

export interface ConnForm { via: DVDMRVia; server: string; talkerAlias: string; startTg: string }

export function formOf(c: DVDMRConn | undefined): ConnForm {
  const s = c?.stfu;
  return { via: c?.via ?? 'mmdvm_bridge', server: s?.server || DEFAULT_STFU_SERVER, talkerAlias: s?.talkerAlias ?? '', startTg: s?.startTg || '4000' };
}

/** The first problem with the form, or ''. */
export function formError(f: ConnForm): string {
  if (f.via !== 'stfu') return '';
  if (!validHost(f.server.trim())) return 'The server must be a host name such as 3102.repeater.net.';
  if (!validAlias(f.talkerAlias)) return `The talker alias is up to ${TALKER_ALIAS_MAX} characters, without quotes, #, ; or = and without leading or trailing spaces.`;
  if (!/^[0-9]{1,8}$/.test(f.startTg.trim())) return 'The start talkgroup is a number of up to 8 digits.';
  return '';
}

/** The request for a save; null when nothing differs from what is saved. */
export function connRequest(saved: DVDMRConn | undefined, f: ConnForm): DVDMRConnReq | null {
  const was = formOf(saved);
  if (f.via === 'mmdvm_bridge') return was.via === 'mmdvm_bridge' && !saved?.error ? null : { via: 'mmdvm_bridge' };
  const server = f.server.trim(), startTg = f.startTg.trim(), alias = f.talkerAlias;
  if (was.via === 'stfu' && was.server === server && (alias === '' || was.talkerAlias === alias) && was.startTg === startTg) return null;
  return { via: 'stfu', network: 'brandmeister', server, startTg, ...(alias ? { talkerAlias: alias } : {}) };
}

/** Why STFU cannot be chosen yet, or '' when it can. */
export function stfuBlocker(stfu: DVSTFUStatus | undefined, passwordSet: boolean): string {
  if (!stfu?.installed) return stfu?.job.job === 'running' ? 'STFU is being installed…' : 'STFU is not installed yet.';
  if (!passwordSet) return 'Save the BrandMeister hotspot password under DMR login first: STFU uses the same one.';
  return '';
}

/** One line about the last transmission from the network, or ''. */
export function rxLine(rx: DVDMRRx | undefined): string {
  if (!rx || !rx.src) return '';
  const to = `${rx.group ? 'TG ' : ''}${rx.dst ?? ''}`;
  return rx.active ? `Receiving: ${rx.src} → ${to}` : `Last heard: ${rx.src} → ${to}${rx.time ? ` at ${rx.time}` : ''}`;
}

/** The method shown next to the login badge. */
export function viaLabel(s: DVDMRStatus | null): string {
  return s?.via === 'stfu' ? 'BrandMeister through STFU' : s?.via === 'mmdvm_bridge' ? 'MMDVM_Bridge' : '';
}
