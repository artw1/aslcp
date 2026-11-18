// Startup cards and the remembered tunes of the DVSwitch setup page (docs/DVSWITCH.md, "Tune and resume").
import type { DVResume } from './types';

export type StartupMode = 'DMR' | 'YSF' | 'P25' | 'NXDN';
export const STARTUP_MODES: StartupMode[] = ['DMR', 'YSF', 'P25', 'NXDN'];
export const MODE_LABEL: Record<StartupMode, string> = { DMR: 'DMR', YSF: 'System Fusion (YSF)', P25: 'P25', NXDN: 'NXDN' };

/** The startup mode choice for an ambeMode value in Analog_Bridge.ini (YSFN and YSFW are both YSF). */
export function startupModeOf(ambeMode: string): StartupMode {
  if (ambeMode === 'YSFN' || ambeMode === 'YSFW' || ambeMode === 'YSF') return 'YSF';
  return ambeMode === 'P25' || ambeMode === 'NXDN' ? ambeMode : 'DMR';
}

/** The ambeMode to write for a choice. YSF keeps the file's YSFN or YSFW (narrow or wide) and defaults to YSFN. */
export function ambeModeFor(choice: StartupMode, current: string): string {
  if (choice !== 'YSF') return choice;
  return current === 'YSFW' ? 'YSFW' : 'YSFN';
}

/** The tab an arrow key, Home or End moves to from `cur`; null for any other key. */
export function nextTab(tabs: StartupMode[], cur: StartupMode, key: string): StartupMode | null {
  const i = tabs.indexOf(cur), n = tabs.length;
  if (i < 0 || n === 0) return null;
  if (key === 'ArrowRight' || key === 'ArrowDown') return tabs[(i + 1) % n];
  if (key === 'ArrowLeft' || key === 'ArrowUp') return tabs[(i - 1 + n) % n];
  if (key === 'Home') return tabs[0];
  if (key === 'End') return tabs[n - 1];
  return null;
}

/** The hint under the resume toggle. */
export function resumeHint(on: boolean): string {
  return on
    ? 'After Analog_Bridge restarts, ASLCP returns to your last mode and tune within a few seconds. Startup values are only used if nothing has been tuned yet.'
    : 'Off: after a restart ASLCP uses the startup values. Turn it on to return to your last tune instead.';
}

export interface StartupForm { ambeMode: string; txTg: string; txTs: string; colorCode: string; ysf: string; p25: string; nxdn: string }

/** The request body of one card: only that card's fields, or null when none of them differs from what is saved. */
export function cardPatch(mode: StartupMode, snap: StartupForm, st: StartupForm): Record<string, unknown> | null {
  const b: Record<string, unknown> = {};
  if (mode === 'DMR') {
    if (st.txTg !== snap.txTg) b.txTg = st.txTg;
    if (st.txTs !== snap.txTs) b.txTs = Number(st.txTs);
    if (st.colorCode !== snap.colorCode) b.colorCode = Number(st.colorCode);
  } else {
    const k = mode.toLowerCase() as 'ysf' | 'p25' | 'nxdn';
    if (st[k] !== snap[k]) b[k] = st[k];
  }
  return Object.keys(b).length ? b : null;
}

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

/** "Last used: TG 91 (5 min ago)" for a mode card; '' when no tune is remembered. */
export function lastUsed(r: DVResume | null, mode: StartupMode, now: number): string {
  const t = r?.perMode?.[mode];
  if (!t || !t.tune) return '';
  const what = mode === 'DMR' ? `TG ${t.tune}${t.ts ? ` TS ${t.ts}` : ''}` : t.tune;
  const ago = timeAgo(t.at, now);
  return `Last used: ${what}${ago ? ` (${ago})` : ''}`;
}
