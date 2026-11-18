// Pure logic of the dashboard DVSwitch card (docs/DVSWITCH.md "Dashboard card"): the state line,
// favourite and tune validation (the same rules as the server), and the pending/confirm logic.
import type { DVBridge, DVFavorite, DVSnapshot, DVTuneMode } from './types';

export const PANEL_MODES: DVTuneMode[] = ['DMR', 'YSF', 'P25', 'NXDN'];
/** How long a mode switch may stay unconfirmed before the card says so. */
export const CONFIRM_MS = 5000;
export const FAV_NAME_MAX = 40;
export const FAV_PER_MODE = 50;

/** "Mode YSF, tuned to Example room (192.0.2.10:42000)", "Mode DMR, tuned to 91", "Mode P25", or "Mode unknown". */
export function stateLine(b: Pick<DVBridge, 'mode' | 'tune' | 'targetName' | 'source'> | null | undefined): string {
  if (!b || b.source === 'none' || !b.mode) return 'Mode unknown';
  if (!b.tune) return `Mode ${b.mode}`;
  return b.targetName ? `Mode ${b.mode}, tuned to ${b.targetName} (${b.tune})` : `Mode ${b.mode}, tuned to ${b.tune}`;
}

/** The unit dots: a unit that is down, or no snapshot of the bridge at all, is amber. */
export function serviceDots(s: DVSnapshot | null | undefined): { unit: string; ok: boolean }[] {
  if (!s) return [];
  const unknown = s.bridge.source === 'none';
  return s.services.map((u) => ({ unit: u.unit, ok: u.active && !(unknown && u.unit === 'analog_bridge') }));
}
export const allOk = (s: DVSnapshot | null | undefined): boolean => !!s && s.bridge.source !== 'none' && serviceDots(s).every((d) => d.ok);

const printable = (s: string) => ![...s].some((c) => { const n = c.codePointAt(0)!; return n < 0x20 || (n >= 0x7f && n < 0xa0); });
/** The trimmed favourite name, or null when it is not 1 to 40 printable characters. */
export function validFavName(name: string): string | null {
  const t = name.trim();
  const n = [...t].length;
  return n >= 1 && n <= FAV_NAME_MAX && printable(t) ? t : null;
}

/** Same rules as the server's tune validator: DMR TG 1-8 digits, P25 and NXDN 1-5 digits, YSF a 5-digit id, a name or host:port. */
export function validTune(mode: string, value: string): boolean {
  const v = value.trim();
  if (!v || v.startsWith('-') || v.includes('=')) return false;
  switch (mode) {
    case 'DMR': return /^[0-9]{1,8}$/.test(v);
    case 'P25': case 'NXDN': return /^[0-9]{1,5}$/.test(v);
    case 'YSF': {
      const hp = /^[A-Za-z0-9.-]{1,253}:([0-9]{1,5})$/.exec(v);
      if (hp) { const p = Number(hp[1]); return p >= 1 && p <= 65535; }
      if (/^[0-9]{5}$/.test(v)) return true;
      return [...v].length <= 32 && printable(v) && !/[;#=]/.test(v);
    }
    default: return false;
  }
}

/** The favourite of `mode` with this value, if any (decides whether the star is filled). */
export function findFavorite(favs: DVFavorite[], mode: string, value: string): DVFavorite | undefined {
  return favs.find((f) => f.mode === mode && f.value === value);
}
export const favoritesFor = (favs: DVFavorite[], mode: string): DVFavorite[] => favs.filter((f) => f.mode === mode).sort((a, b) => a.sort - b.sort || a.id - b.id);

/** Why a favourite cannot be saved now (null: it can). */
export function favoriteBlock(favs: DVFavorite[], mode: string, value: string): string | null {
  if (!validTune(mode, value)) return 'Nothing to save: no valid target is tuned';
  if (findFavorite(favs, mode, value)) return 'Already a favourite';
  if (favs.filter((f) => f.mode === mode).length >= FAV_PER_MODE) return `At most ${FAV_PER_MODE} favourites per mode`;
  return null;
}

/** The name the prompt starts with: the resolved name, else the value. */
export const defaultFavName = (b: Pick<DVBridge, 'tune' | 'targetName'>): string => (b.targetName || b.tune || '').trim().slice(0, FAV_NAME_MAX);

/** A mode switch waiting for the snapshot to show it. */
export interface Pending { mode: DVTuneMode; since: number }
export type Outcome = 'none' | 'waiting' | 'confirmed' | 'unconfirmed';

/** `confirmed` once the bridge reports the mode, `unconfirmed` after CONFIRM_MS without it, else `waiting`. */
export function pendingOutcome(p: Pending | null, bridgeMode: string | undefined, now: number): Outcome {
  if (!p) return 'none';
  if (bridgeMode === p.mode) return 'confirmed';
  return now - p.since >= CONFIRM_MS ? 'unconfirmed' : 'waiting';
}

/** The mode the buttons highlight: the pending one while waiting, else what the bridge reports. */
export function shownMode(p: Pending | null, outcome: Outcome, bridgeMode: string | undefined): string {
  return p && outcome === 'waiting' ? p.mode : bridgeMode ?? '';
}
