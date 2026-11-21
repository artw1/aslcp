// The mode cards, the resume notice and the remembered tunes of the DVSwitch setup page
// (docs/DVSWITCH.md, "Boot mode", "DMR networks" and "Tune and resume"). Pure helpers so node --test can cover them.
import { DV_NETWORKS, isDVNetwork, netView } from './dvnetworks.ts';
import type { DVResume, DVSwitchSetup, DVTuneMode } from './types';

/** The notice card between Identity and the mode cards. */
export const RESUME_NOTICE = 'ASLCP remembers the last mode and talkgroup or reflector, and DVSwitch starts in that mode after a restart, so there are no startup modes to set.';

/** The warning next to restarts and saved changes that need one. */
export const RESTART_WARNING = 'After a restart DVSwitch starts in the last mode; with "Resume after restart" on, ASLCP also re-tunes the last talkgroup or reflector.';

/** The hint under the resume switch. */
export function resumeHint(on: boolean): string {
  return on
    ? 'On: after a restart ASLCP also re-tunes the last talkgroup or reflector of that mode within a few seconds.'
    : 'Off: DVSwitch still starts in the last mode, but nothing is re-tuned; pick a talkgroup or reflector on the dashboard.';
}

/** The toast after the resume switch changed. */
export function resumeToast(on: boolean): string {
  return on ? 'The last talkgroup or reflector is re-tuned after a restart.' : 'After a restart DVSwitch starts in the last mode, without re-tuning.';
}

/** The name of each mode: the DMR networks' registry labels, then the gateways. */
export const MODE_NAME: Record<DVTuneMode, string> = {
  ...(Object.fromEntries(DV_NETWORKS.map((n) => [n.id, n.label])) as Record<'BM' | 'TGIF' | 'DMR', string>),
  YSF: 'System Fusion (YSF)', P25: 'P25', NXDN: 'NXDN',
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
  const what = isDVNetwork(m) ? `TG ${t.tune}${t.ts ? ` TS ${t.ts}` : ''}` : t.tune;
  const ago = timeAgo(t.at, now);
  return `Last used: ${MODE_NAME[m]} ${what}${ago ? ` (${ago})` : ''}`;
}

export type DVCard = 'BM' | 'TGIF' | 'DMR' | 'YSF' | 'P25' | 'NXDN';

/** Whether a card is on: a DMR network's own Enable, or the gateway mode's flag. */
export function cardEnabled(v: Pick<DVSwitchSetup, 'modes' | 'networks'>, card: DVCard): boolean {
  switch (card) {
    case 'YSF': return v.modes.ysf;
    case 'P25': return v.modes.p25;
    case 'NXDN': return v.modes.nxdn;
    default: return !!netView(v, card)?.enabled;
  }
}

/** The PUT /api/dvswitch/modes body of a YSF, P25 or NXDN Enable/Disable (a DMR network has its own endpoint). */
export function modesBody(card: 'YSF' | 'P25' | 'NXDN', on: boolean): { ysf?: boolean; p25?: boolean; nxdn?: boolean } {
  return { [card.toLowerCase()]: on };
}
