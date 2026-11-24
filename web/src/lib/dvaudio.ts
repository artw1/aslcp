// Pure helpers of the audio settings and the level meter of the digital modes card (docs/DV.md "Audio level and AGC").
import type { DVAudio, DVAudioDir, DVAudioSummary } from './types';

export const METER_FLOOR = -90;
export const METER_HINT = 'Use the echo test (LOOP) to check your own audio level.';

export const AUDIO_DEFAULTS: DVAudio = { agcTx: false, agcRx: false, agcTargetDb: -22, agcMaxGainDb: 18, gainRxDb: 0, gainTxDb: 0 };

/** The width of the meter bar in percent: the peak over a 90 dB range, as the Tune page does. */
export function meterPct(pk: number): number { return Math.max(0, Math.min(100, ((pk + 90) / 90) * 100)); }

/** The colour class of the bar: danger on clipping, warn above -6 dBFS, ok otherwise. */
export function meterColor(pk: number, clipping = false): 'bg-danger' | 'bg-warn' | 'bg-ok' {
  return clipping || pk >= -0.5 ? 'bg-danger' : pk > -6 ? 'bg-warn' : 'bg-ok';
}

/** A level as text: "-28", "-90" is shown as "silent". */
export function dbText(v: number): string { return v <= METER_FLOOR ? 'silent' : String(Math.round(v)); }

export type Tone = 'ok' | 'warn' | 'danger';
const VERDICT: Record<string, string> = { low: 'Low', ok: 'OK', high: 'High' };

export function verdictLabel(s: DVAudioSummary): string { return s.clipped > 0 ? 'Clipping' : VERDICT[s.verdict] ?? s.verdict; }
export function verdictTone(s: DVAudioSummary): Tone { return s.clipped > 0 ? 'danger' : s.verdict === 'ok' ? 'ok' : 'warn'; }

/** "Last over: -28 dBFS speech, peak -4, Low" (and the clip count when there was any). */
export function summaryLine(s: DVAudioSummary, what = 'over'): string {
  const clip = s.clipped > 0 ? `, ${s.clipped} clipped frame${s.clipped === 1 ? '' : 's'}` : '';
  return `Last ${what}: ${dbText(s.speechDb)}${s.speechDb <= METER_FLOOR ? '' : ' dBFS'} speech, peak ${dbText(s.peakDb)}${clip}`;
}

/** "-> -22 after AGC", only when the AGC was on for that transmission. */
export function agcLine(s: DVAudioSummary): string { return s.agc && s.speechDb > METER_FLOOR ? `→ ${dbText(s.outSpeechDb)} after AGC` : ''; }

/** A direction with anything to show: audio now or a previous summary. */
export function dirShown(d: DVAudioDir | undefined): boolean { return !!d && (d.active || !!d.last); }

/** The settings form as numbers; an error text or '' when it is valid. */
export function audioError(f: { agcTargetDb: string; agcMaxGainDb: string; gainRxDb: string; gainTxDb: string }): string {
  const chk = (v: string, lo: number, hi: number, name: string) => {
    const n = Number(v);
    return v.trim() !== '' && Number.isInteger(n) && n >= lo && n <= hi ? '' : `${name}: a whole number from ${lo} to ${hi}.`;
  };
  return chk(f.agcTargetDb, -40, -6, 'AGC target level') || chk(f.agcMaxGainDb, 0, 30, 'AGC maximum gain') || chk(f.gainRxDb, -20, 20, 'Node audio gain') || chk(f.gainTxDb, -20, 20, 'Network audio gain');
}
