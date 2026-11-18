// BrandMeister (STFU) and DMR - Other (MMDVM_Bridge) sections of the DVSwitch setup page
// (docs/DVSWITCH.md "BrandMeister and DMR - Other"). Pure helpers so node --test can cover them.
import type { DVBMReq, DVBMView, DVDMRRx, DVListItem, DVSTFUStatus } from './types';

export const DEFAULT_STFU_SERVER = '3102.repeater.net';
export const STFU_PORT = 54006;
export const TALKER_ALIAS_MAX = 27;
export const DEFAULT_START_TG = '4000';

const BM_DOMAINS = ['brandmeister.network', 'repeater.net'];

/** Same rule as the helper: dot-separated labels of letters, digits and inner hyphens, 253 characters at most. */
export function validHost(h: string): boolean {
  if (!h || h.length > 253) return false;
  return h.split('.').every((l) => /^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$/.test(l));
}

/** Same rule as the helper: printable ASCII without quotes, #, ; or =, up to 27 characters, no outer spaces. Empty keeps the saved one (or the callsign). */
export function validAlias(a: string): boolean {
  return a === '' || (a.length <= TALKER_ALIAS_MAX && a === a.trim() && /^[\x20-\x7e]+$/.test(a) && !/["';#=]/.test(a));
}

/** Same rule as the helper: 1 to 64 printable ASCII characters without spaces, quotes, # or ;. */
export function validPassword(p: string): boolean {
  return p.length >= 1 && p.length <= 64 && /^[\x21-\x7e]+$/.test(p) && !/["#;]/.test(p);
}

/** A BrandMeister server by name (*.brandmeister.network, *.repeater.net). */
export function isBMHostName(host: string): boolean {
  const h = host.trim().toLowerCase().replace(/\.$/, '');
  return BM_DOMAINS.some((d) => h === d || h.endsWith(`.${d}`));
}

/** A BrandMeister master of DMR_Hosts.txt: a BM_ entry, or one on a BrandMeister domain. */
export const isBMItem = (it: DVListItem): boolean => it.name.startsWith('BM_') || isBMHostName(it.address);

/** True when host is a BrandMeister server: by name, or the address of a BM_ entry of the list. */
export function isBMHost(host: string, items: DVListItem[]): boolean {
  const h = host.trim().toLowerCase();
  if (!h) return false;
  return isBMHostName(h) || items.some((it) => it.name.startsWith('BM_') && it.address.toLowerCase() === h);
}

/** The BrandMeister master picker: the BM entries of the DMR list, hostname only, always port 54006. */
export function bmOptions(items: DVListItem[]): DVListItem[] {
  return items.filter(isBMItem).map((it) => ({ ...it, value: it.address, port: STFU_PORT }));
}

/** The DMR - Other master picker: every entry but the BrandMeister ones. */
export const otherOptions = (items: DVListItem[]): DVListItem[] => items.filter((it) => !isBMItem(it));

export interface BMForm { server: string; password: string; talkerAlias: string; startTg: string; stayLoggedIn: boolean }

export function formOf(b: DVBMView | undefined): BMForm {
  return { server: b?.server || DEFAULT_STFU_SERVER, password: '', talkerAlias: b?.talkerAlias ?? '', startTg: b?.startTg || DEFAULT_START_TG, stayLoggedIn: !!b?.stayLoggedIn };
}

/** The first problem with the form, or ''. passwordSet: a password is saved already, so the field may stay empty. */
export function formError(f: BMForm, passwordSet: boolean): string {
  if (!validHost(f.server.trim())) return `The master must be a host name such as ${DEFAULT_STFU_SERVER}.`;
  if (f.password ? !validPassword(f.password) : !passwordSet) return f.password ? 'The password is 1 to 64 printable characters, without spaces, quotes, # or ;.' : 'Enter the hotspot security password from BrandMeister SelfCare.';
  if (!validAlias(f.talkerAlias)) return `The talker alias is up to ${TALKER_ALIAS_MAX} characters, without quotes, #, ; or = and without leading or trailing spaces.`;
  if (!/^[0-9]{1,8}$/.test(f.startTg.trim())) return 'The start talkgroup is a number of up to 8 digits.';
  return '';
}

/** The PUT /api/dvswitch/brandmeister body; null when BrandMeister is set up and nothing differs. The password only when typed. */
export function bmRequest(saved: DVBMView | undefined, f: BMForm): Omit<DVBMReq, 'base'> | null {
  const was = formOf(saved);
  const server = f.server.trim(), startTg = f.startTg.trim(), alias = f.talkerAlias;
  const same = was.server === server && (alias === '' || was.talkerAlias === alias) && was.startTg === startTg && was.stayLoggedIn === f.stayLoggedIn && !f.password;
  if (same && saved?.passwordSet) return null;
  return { server, startTg, stayLoggedIn: f.stayLoggedIn, ...(alias ? { talkerAlias: alias } : {}), ...(f.password ? { password: f.password } : {}) };
}

/** Why BrandMeister cannot be saved yet, or ''. */
export function bmBlocker(stfu: DVSTFUStatus | undefined, view: DVBMView | undefined): string {
  if (stfu ? !stfu.installed : !view?.installed) return stfu?.job.job === 'running' ? 'STFU is being installed…' : 'STFU is not installed yet.';
  return '';
}

/** One line about the last transmission from the network, or ''. */
export function rxLine(rx: DVDMRRx | undefined): string {
  if (!rx || !rx.src) return '';
  const to = `${rx.group ? 'TG ' : ''}${rx.dst ?? ''}`;
  return rx.active ? `Receiving: ${rx.src} → ${to}` : `Last heard: ${rx.src} → ${to}${rx.time ? ` at ${rx.time}` : ''}`;
}
