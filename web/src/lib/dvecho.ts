// The echo test of the digital modes (docs/DV.md "Echo test"): the engine's local test network (mode LOOP).
// It starts and stops from the echo test of the Audio card of Digital modes setup; Stop returns to the mode that was
// in effect before. Pure helpers so node --test can cover them.
import type { DVResume, DVSnapshot } from './types';

/** What the dashboard card's state line and the setup control say while the engine is in LOOP. */
export const ECHO_RUNNING = 'Echo test running';
export const ECHO_START = 'Start echo test';
export const ECHO_STOP = 'Stop';
/** The one-line hint under the start button. */
export const ECHO_HINT = 'Key up, talk, unkey: you hear yourself back a second later. Local only, no network.';

/** True while the engine is in the echo test. */
export const echoRunning = (s: Pick<DVSnapshot, 'bridge'> | null | undefined): boolean => !!s && s.bridge.source !== 'none' && s.bridge.mode === 'LOOP';

type ModeList = Pick<DVSnapshot, 'modes'>['modes'];
const usable = (modes: ModeList | undefined, id: string): boolean => {
  if (id === 'LOOP' || !id) return false;
  const m = modes?.find((x) => x.id === id);
  return modes ? !!m && m.enabled && m.available : /^(BM|TGIF|DMR|YSF|P25|NXDN|M17)$/.test(id);
};

/** The mode before the echo test, remembered client-side between Start and Stop (it survives leaving the setup page). */
let before = '';
export const rememberBefore = (mode: string | undefined): void => { if (mode && mode !== 'LOOP') before = mode; };
export const forgetBefore = (): void => { before = ''; };
export const modeBefore = (): string => before;

/**
 * The mode Stop switches to: the mode in effect before Start; else the last mode of the resume tracker
 * (GET /api/dv/resume `lastMode`, which LOOP never replaces); else the first enabled mode. '' when no mode can be used.
 * POST /api/dv/mode re-tunes the mode's remembered talkgroup or reflector, exactly as the dashboard does.
 */
export function stopTarget(prev: string, resume: Pick<DVResume, 'lastMode'> | null | undefined, modes: ModeList | undefined): string {
  if (usable(modes, prev)) return prev;
  const last = resume?.lastMode ?? '';
  if (usable(modes, last)) return last;
  return modes?.find((m) => usable(modes, m.id))?.id ?? '';
}
