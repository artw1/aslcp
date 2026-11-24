// The mode cards, the resume notice and the remembered tunes of the digital modes setup page
// (docs/DV.md). Pure helpers so node --test can cover them.
import { DV_NETWORKS, ECHO_LABEL, isDVNetwork } from './dvnetworks.ts';
import { modeAvailable } from './dvnetform.ts';
import { m17Display } from './dvpanel.ts';
import type { DVResume, DVSnapshot, DVTuneMode } from './types';

/** The notice card above the mode cards. */
export const RESUME_NOTICE = 'ASLCP remembers the last mode and talkgroup or reflector, and the engine starts in that mode after a restart, so there are no startup modes to set.';

/** The hint under the resume switch. */
export function resumeHint(on: boolean): string {
  return on
    ? 'On: after a restart ASLCP also re-tunes the last talkgroup or reflector of that mode within a few seconds.'
    : 'Off: the engine still starts in the last mode, but nothing is re-tuned; pick a talkgroup or reflector on the dashboard.';
}

/** The one-line hint of the resume switch in the Engine card: RESUME_NOTICE and resumeHint in short. */
export function resumeShort(on: boolean): string {
  return on
    ? 'The engine restarts in the last mode, and ASLCP re-tunes its last talkgroup or reflector.'
    : 'The engine restarts in the last mode; nothing is re-tuned, so pick a talkgroup or reflector on the dashboard.';
}

/** The toast after the resume switch changed. */
export function resumeToast(on: boolean): string {
  return on ? 'The last talkgroup or reflector is re-tuned after a restart.' : 'After a restart the engine starts in the last mode, without re-tuning.';
}

/** The name of each mode: the echo test, the DMR networks' registry labels, then the gateways. */
export const MODE_NAME: Record<DVTuneMode, string> = {
  LOOP: ECHO_LABEL,
  ...(Object.fromEntries(DV_NETWORKS.map((n) => [n.id, n.label])) as Record<'BM' | 'TGIF' | 'DMR', string>),
  YSF: 'System Fusion (YSF)', P25: 'P25', NXDN: 'NXDN', M17: 'M17',
};

/** "just now", "5 min ago", "3 h ago", "2 d ago". */
export function timeAgo(iso: string, now: number): string {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return '';
  const s = Math.max(0, Math.round((now - t) / 1000));
  if (s < 60) return 'just now';
  if (s < 3600) return `${Math.floor(s / 60)} min ago`;
  if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
  return `${Math.floor(s / 86400)} d ago`;
}

/** "Last used: BrandMeister TG 91 TS 2 (5 min ago)" for the last mode; "Last used: P25" without a tune; '' when nothing is remembered. */
export function lastUsed(r: DVResume | null, now: number): string {
  const m = r?.lastMode as DVTuneMode | undefined;
  if (!r || !m || !Object.hasOwn(MODE_NAME, m)) return '';
  const t = r.perMode?.[m];
  if (!t || !t.tune) return `Last used: ${MODE_NAME[m]}`;
  const what = isDVNetwork(m) ? `TG ${t.tune}${t.ts ? ` TS ${t.ts}` : ''}` : m === 'M17' ? m17Display(t.tune) : t.tune;
  const ago = timeAgo(t.at, now);
  return `Last used: ${MODE_NAME[m]} ${what}${ago ? ` (${ago})` : ''}`;
}

/** The cards of the setup page, in order. */
export type DVCard = DVTuneMode;
export const UNAVAILABLE_TEXT = 'Not yet available in this build';

/** The snapshot's view of one mode: whether the engine carries it and whether it is on. Without a snapshot nothing is available; a mode the list omits follows the static native list. */
export function cardState(modes: Pick<DVSnapshot, 'modes'>['modes'] | undefined, id: DVTuneMode): { available: boolean; enabled: boolean } {
  const m = modes?.find((x) => x.id === id);
  const available = !!modes && modeAvailable(modes, id);
  return { available, enabled: available && !!m?.enabled };
}
