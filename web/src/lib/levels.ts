// Pure helpers for the live input meter and the echo test report on the Radio tune page.
import type { EchoReport } from './types';

/** The bottom of the meter, in dBFS. Anything quieter shows as an empty bar. */
export const METER_FLOOR_DB = -60;
/** The target band for speech peaks, in dBFS. */
export const TARGET_LOW_DB = -20;
export const TARGET_HIGH_DB = -6;
/** At or above this level the bar turns red. */
export const HOT_DB = -3;

/** Maps a dBFS value to 0..100 along the meter (METER_FLOOR_DB to 0). Not finite values count as the floor. */
export function barPercent(db: number): number {
  if (!Number.isFinite(db)) return 0;
  const p = ((db - METER_FLOOR_DB) / -METER_FLOOR_DB) * 100;
  return Math.min(100, Math.max(0, p));
}

/** The shaded target band as left and width percentages of the meter. */
export function targetBand(): { left: number; width: number } {
  const left = barPercent(TARGET_LOW_DB);
  return { left, width: barPercent(TARGET_HIGH_DB) - left };
}

/** True when the level is near full scale, where the bar is drawn red. */
export const isHot = (db: number): boolean => Number.isFinite(db) && db >= HOT_DB;

/** A dBFS value as text: "-12.3 dBFS", or a dash when there is no signal. */
export function dbText(db: number | undefined): string {
  if (db === undefined || !Number.isFinite(db) || db <= -90) return '–';
  return `${db.toFixed(1)} dBFS`;
}

/** A warning of the echo test report as a sentence the operator can act on. */
export function warningText(w: string): string {
  switch (w) {
    case 'clipping': return 'The audio clipped. Lower the receive volume.';
    case 'too quiet': return 'The audio is too quiet. Raise the receive volume.';
    default: return w;
  }
}

/** A duration in milliseconds as seconds with one decimal: "3.4 s". */
export const durationText = (ms: number): string => `${(Math.max(0, ms) / 1000).toFixed(1)} s`;

/** One line for the echo test report, or "" when none was recorded. */
export function reportSummary(r: EchoReport | undefined): string {
  if (!r) return '';
  const w = r.warnings?.length ? r.warnings.map(warningText).join(' ') : 'Levels look good.';
  return `Peak ${dbText(r.peakDb)}, RMS ${dbText(r.rmsDb)}, ${durationText(r.durationMs)}. ${w}`;
}
