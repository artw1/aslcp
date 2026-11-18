// DMR login badge and the "did the saved password work?" check (docs/DVSWITCH.md, "DMR login status").
import type { DVDMRState, DVDMRStatus } from './types';

export const POLL_MS = 2000;
export const POLL_MAX_MS = 40000;
export const IDLE_POLL_MS = 10000;

export type Tone = 'ok' | 'warn' | 'danger' | 'muted';
export interface Outcome { kind: 'ok' | 'error' | 'warn' | 'info'; message: string; needStart?: boolean }

export const MSG_REFUSED = 'BrandMeister refused the login: check the hotspot security password (set in BrandMeister SelfCare, not your account password) and the ESSID';
export const MSG_NO_ANSWER = 'No answer from the master: check the address, port and internet access';
export const MSG_PENDING = 'No answer yet';

/** The live "DMR login" badge. */
export function badge(s: DVDMRStatus | null): { text: string; tone: Tone } {
  if (!s) return { text: 'Checking…', tone: 'muted' };
  switch (s.state) {
    case 'logged_in': return { text: `Logged in to ${s.master || 'the master'}`, tone: 'ok' };
    case 'login_failed': return { text: 'Login failed', tone: 'danger' };
    case 'timed_out': return { text: 'Timed out', tone: 'danger' };
    case 'socket_failed': return { text: 'Socket failed', tone: 'danger' };
    case 'master_closing': return { text: 'Master closing down', tone: 'warn' };
    case 'connecting': return { text: 'Connecting…', tone: 'muted' };
    case 'not_running': return { text: 'Not running', tone: 'warn' };
    case 'disabled': return { text: 'DMR disabled', tone: 'muted' };
    default: return { text: 'Not installed', tone: 'muted' };
  }
}

const FAILED: DVDMRState[] = ['login_failed', 'timed_out', 'socket_failed'];

/** What to tell the person for a state; `last` is true once the time is up. null: keep waiting. */
export function outcomeFor(s: DVDMRStatus, last: boolean): Outcome | null {
  switch (s.state) {
    case 'logged_in': return { kind: 'ok', message: `Logged in to ${s.master || 'the master'}` };
    case 'login_failed': return { kind: 'error', message: MSG_REFUSED };
    case 'timed_out': case 'socket_failed': return { kind: 'error', message: MSG_NO_ANSWER };
    case 'not_running': return { kind: 'warn', message: `${s.via === 'stfu' ? 'stfu' : 'mmdvm_bridge'} is not running, so there is no login to check.`, needStart: true };
    case 'disabled': return { kind: 'info', message: 'DMR is disabled under Enabled modes, so the bridge does not log in.' };
    case 'not_installed': return { kind: 'info', message: s.via === 'stfu' ? 'STFU is not installed.' : 'MMDVM_Bridge is not installed.' };
    case 'master_closing': return last ? { kind: 'warn', message: 'The master is closing down. Try again in a moment.' } : null;
    default: return last ? { kind: 'warn', message: MSG_PENDING } : null;
  }
}

/**
 * Polls `get` every POLL_MS for up to POLL_MAX_MS. A success, "not running", "disabled" and "not installed"
 * end it at once. A failure ends it when two polls in a row agree (a single retry may still succeed), or at
 * the end. Returns null when `cancelled` turns true.
 */
export async function waitForLogin(
  get: () => Promise<DVDMRStatus>,
  o: { sleep: (ms: number) => Promise<void>; cancelled: () => boolean; onStatus?: (s: DVDMRStatus) => void; interval?: number; max?: number },
): Promise<{ outcome: Outcome; status: DVDMRStatus | null } | null> {
  const interval = o.interval ?? POLL_MS, polls = Math.max(1, Math.floor((o.max ?? POLL_MAX_MS) / interval));
  let last: DVDMRStatus | null = null, prevFailed: DVDMRState | '' = '';
  for (let i = 0; i < polls; i++) {
    if (i > 0) await o.sleep(interval);
    if (o.cancelled()) return null;
    let s: DVDMRStatus | null = null;
    try { s = await get(); } catch { /* try again */ }
    if (o.cancelled()) return null;
    if (!s) { prevFailed = ''; continue; }
    last = s;
    o.onStatus?.(s);
    const end = i === polls - 1;
    if (FAILED.includes(s.state)) {
      if (prevFailed === s.state || end) return { outcome: outcomeFor(s, true)!, status: s };
      prevFailed = s.state;
      continue;
    }
    prevFailed = '';
    const out = outcomeFor(s, end);
    if (out) return { outcome: out, status: s };
  }
  return { outcome: last ? outcomeFor(last, true)! : { kind: 'warn', message: 'Could not read the DMR login state.' }, status: last };
}

/** The unit holding the DMR login (mmdvm_bridge, or stfu with BrandMeister through STFU) counts as in use when it is enabled or running. */
export function bridgeInUse(units: { name: string; loaded: boolean; active: string; enabled: string }[], unit = 'mmdvm_bridge'): { inUse: boolean; running: boolean } {
  const u = units.find((x) => x.name === unit);
  if (!u || !u.loaded) return { inUse: false, running: false };
  const running = u.active === 'active' || u.active === 'activating';
  return { inUse: running || u.enabled === 'enabled', running };
}
