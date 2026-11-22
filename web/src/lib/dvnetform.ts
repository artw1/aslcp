// The generic settings form of a digital modes network (docs/DV.md "BrandMeister"): the form is built from the
// option specs the server sends, so a new network needs no web change. Pure helpers so node --test covers them.
import type { DVIdentity, DVListItem, DVLogin, DVNetPut, DVNetView, DVOptSpec, DVSnapshot, DVTuneMode } from './types';

/** The modes the build implements natively. Only the fallback when the snapshot does not list a mode; the engine's `available` wins. */
export const NATIVE_MODES: readonly string[] = ['LOOP', 'BM', 'TGIF', 'DMR', 'YSF', 'P25', 'NXDN'];

/** Whether a mode is available: the snapshot's (engine's) answer, else the static native list. */
export function modeAvailable(modes: DVSnapshot['modes'] | undefined, id: DVTuneMode): boolean {
  const m = modes?.find((x) => x.id === id);
  return m ? m.available : NATIVE_MODES.includes(id);
}

/** The editable state of one network form. `password` is empty unless the operator typed a new one. */
export interface NetForm { enabled: boolean; address: string; port: string; password: string; clearPassword: boolean; options: Record<string, string> }

export function formOf(v: DVNetView): NetForm {
  return { enabled: v.enabled, address: v.address, port: v.port ? String(v.port) : '', password: '', clearPassword: false, options: { ...v.values } };
}

const HOST = /^(?:[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?)(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*\.?$/;
const IPV4 = /^\d{1,3}(\.\d{1,3}){3}$/;
export function validAddress(s: string): boolean {
  if (!s || s.length > 253) return false;
  if (IPV4.test(s)) return s.split('.').every((n) => Number(n) <= 255);
  return HOST.test(s);
}

/** The error of one option value, or ''. An empty value is fine: the engine default applies. */
export function optionError(o: DVOptSpec, raw: string): string {
  const v = raw.trim();
  if (v === '') return '';
  if (o.kind === 'bool') return v === 'true' || v === 'false' ? '' : `${o.label}: on or off`;
  if (o.kind === 'int') {
    if (!/^-?\d+$/.test(v)) return `${o.label}: a whole number`;
    const n = Number(v);
    if ((o.min !== undefined && n < o.min) || (o.max !== undefined && o.max > 0 && n > o.max)) return `${o.label}: ${o.min ?? 0} to ${o.max}`;
  }
  if (o.kind === 'text' && v.length > (o.maxLen ?? 32)) return `${o.label}: at most ${o.maxLen ?? 32} characters`;
  return '';
}

/** The first problem of a form, or '' when it can be saved. */
export function formError(v: DVNetView, f: NetForm): string {
  if (v.fields.address && f.address.trim() !== '' && !validAddress(f.address.trim())) return 'Master address: a host name or an IP address';
  if (v.fields.port && f.port.trim() !== '') {
    const n = Number(f.port);
    if (!Number.isInteger(n) || n < 1 || n > 65535) return 'Port: 1 to 65535';
  }
  if (v.fields.password && f.password !== '' && (f.password.length > 64 || /\s/.test(f.password))) return 'Password: up to 64 characters, no spaces';
  for (const o of v.options) { const e = optionError(o, f.options[o.key] ?? ''); if (e) return e; }
  return '';
}

/** The body of PUT /api/dv/networks/{mode}: only what differs from the stored view. The password is sent only when typed. */
export function buildPut(v: DVNetView, f: NetForm): DVNetPut {
  const b: DVNetPut = {};
  if (f.enabled !== v.enabled) b.enabled = f.enabled;
  if (v.fields.address && f.address.trim() !== v.address) b.address = f.address.trim();
  if (v.fields.port) {
    const n = f.port.trim() === '' ? 0 : Number(f.port);
    if (n !== (v.port || 0)) b.port = n;
  }
  if (v.fields.password) {
    if (f.clearPassword) b.clearPassword = true;
    else if (f.password !== '') b.password = f.password;
  }
  const opts: Record<string, string> = {};
  for (const o of v.options) { const x = (f.options[o.key] ?? '').trim(); if (x !== '') opts[o.key] = x; }
  const was: Record<string, string> = {};
  for (const o of v.options) if ((v.values[o.key] ?? '') !== '') was[o.key] = v.values[o.key];
  if (JSON.stringify(Object.entries(opts).sort()) !== JSON.stringify(Object.entries(was).sort())) b.options = opts;
  return b;
}

export const hasChanges = (b: DVNetPut): boolean => Object.keys(b).length > 0;

/** The password field: "Set" for a first password, "Change" when one is stored. The stored one is never shown. */
export function passwordUx(passwordSet: boolean, editing: boolean): { status: string; action: string; show: boolean } {
  if (!passwordSet) return { status: 'No password set', action: 'Set', show: true };
  return editing ? { status: 'Password set', action: 'Cancel', show: true } : { status: 'Password set', action: 'Change', show: false };
}

/** What blocks the Enable switch (the same checks the server makes), or ''. */
export function enableBlock(v: DVNetView, f: NetForm, identity: DVIdentity | null): string {
  if (v.fields.address && !v.defaultAddress && f.address.trim() === '') return 'Set the master address first';
  if (v.fields.password && !v.defaultPassword && !v.passwordSet && f.password === '') return 'Set the password first';
  if (v.needsIdentity && (!identity || !identity.callsign || !identity.dmrId)) return 'Set the callsign and DMR ID in the identity first';
  return '';
}

/** The login line of a network card: "Logged in", "Logging in...", "Login refused: detail"; '' when it is off. */
export function loginText(login: DVLogin | undefined, detail: string | undefined): string {
  switch (login) {
    case 'ok': return 'Logged in';
    case 'pending': return detail ? `Logging in... (${detail})` : 'Logging in...';
    case 'failed': return detail ? `Login failed: ${detail}` : 'Login failed';
    default: return '';
  }
}
export function loginTone(login: DVLogin | undefined): 'ok' | 'warn' | 'err' | 'off' {
  return login === 'ok' ? 'ok' : login === 'pending' ? 'warn' : login === 'failed' ? 'err' : 'off';
}

/** Identity form checks, the server's rules. The ESSID is optional: 2 digits, 01 to 99. */
export function identityError(callsign: string, dmrId: string, essid: string): string {
  if (!/^[A-Za-z0-9]{3,8}$/.test(callsign.trim())) return 'Callsign: 3 to 8 letters and digits';
  const n = Number(dmrId);
  if (!/^\d{7}$/.test(dmrId.trim()) || n < 1000000 || n > 9999999) return 'DMR ID: 7 digits';
  const e = essid.trim();
  if (e !== '' && (!/^\d{2}$/.test(e) || e === '00')) return 'ESSID: 2 digits (01–99), or empty';
  return '';
}

/** The repeater ID the API takes for an ESSID: the DMR ID × 100 + the ESSID, or undefined when there is no ESSID. */
export function essidToRepeaterId(dmrId: string, essid: string): number | undefined {
  if (essid.trim() === '') return undefined;
  return Number(dmrId) * 100 + Number(essid);
}

/** The ESSID shown for a stored repeater ID: '' when there is none or it is not this DMR ID's (DMR ID × 100 + ESSID). */
export function repeaterIdToEssid(dmrId: number, repeaterId?: number): string {
  if (!repeaterId || Math.floor(repeaterId / 100) !== dmrId) return '';
  return String(repeaterId % 100).padStart(2, '0');
}

/** The BM help line (also in the network's `help`). */
export const BM_HELP = 'Logs in as an Open DMR Terminal with your DMR ID and the password set in your BrandMeister SelfCare (hotspot security). Pick the master closest to you.';

/** The value of the master dropdown's "Other…" entry: the address is typed instead of picked. */
export const MASTER_OTHER = '__other__';

/** The dropdown choice for a stored address: the list entry it matches, '' when none is set yet, else "Other…". */
export function masterChoice(address: string, items: readonly DVListItem[]): string {
  const a = address.trim().toLowerCase();
  if (a === '') return '';
  const hit = items.find((i) => i.value.toLowerCase() === a);
  return hit ? hit.value : MASTER_OTHER;
}

/** The list a network's master dropdown is filled from (GET /api/dv/lists?mode=): BrandMeister and DMR - Other have one. */
export function masterListMode(mode: string): 'bm' | 'dmr-other' | null {
  return mode === 'BM' ? 'bm' : mode === 'DMR' ? 'dmr-other' : null;
}

/**
 * Picking a list entry: fills the address and the port (the BrandMeister list carries none: the port is left alone).
 * The list's password is filled only when the password field is empty, nothing is stored and it is not the default anyway.
 */
export function applyMaster(v: DVNetView, f: NetForm, item: DVListItem): NetForm {
  const g: NetForm = { ...f, address: item.value };
  if (item.port) g.port = String(item.port);
  if (v.fields.password && f.password === '' && !v.passwordSet && !f.clearPassword && item.password && item.password !== v.defaultPassword) g.password = item.password;
  return g;
}

/**
 * The login state a network card shows: the live one from the dashboard snapshot (SSE `dv`) when it lists the mode,
 * else the one stored in the settings view. The view is read once, right after a save, while the engine is still
 * logging in, so it alone would stay on "Logging in..." for good.
 */
export function liveLogin(v: Pick<DVNetView, 'mode' | 'login' | 'detail'>, modes: DVSnapshot['modes'] | undefined): { login: DVLogin | undefined; detail: string | undefined } {
  const m = modes?.find((x) => x.id === v.mode);
  return m && m.login ? { login: m.login, detail: m.detail } : { login: v.login, detail: v.detail };
}

/** The toast after a save: names the password when one was stored or removed, so the operator sees it was taken. */
export function savedMessage(label: string, put: DVNetPut, applyError?: string): string {
  const pw = put.clearPassword ? 'password removed' : put.password ? 'password saved' : '';
  const base = pw ? `${label} settings saved, ${pw}` : `${label} settings saved`;
  return applyError ? `${base}. ${applyError}` : base;
}
