import { modeTitle } from './modes.ts';
import type { Schedule, ScheduleEvent, ScheduleInput, SchedulesSnapshot } from './types';

export const DAY_NAMES = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];
/** Display order Monday first; values are the API's day numbers (0 = Sunday). */
export const DAY_ORDER = [1, 2, 3, 4, 5, 6, 0];

/** "Daily", "Mon–Fri", "Tue, Thu"; runs of three or more days collapse to a range (Monday first). */
export function fmtDays(days: number[]): string {
  const set = new Set(days);
  if (set.size === 7) return 'Daily';
  const idx = DAY_ORDER.map((d, i) => (set.has(d) ? i : -1)).filter((i) => i >= 0);
  const out: string[] = [];
  for (let i = 0; i < idx.length;) {
    let j = i;
    while (j + 1 < idx.length && idx[j + 1] === idx[j] + 1) j++;
    const a = DAY_NAMES[DAY_ORDER[idx[i]]], b = DAY_NAMES[DAY_ORDER[idx[j]]];
    if (j - i >= 2) out.push(`${a}–${b}`);
    else for (let k = i; k <= j; k++) out.push(DAY_NAMES[DAY_ORDER[idx[k]]]);
    i = j + 1;
  }
  return out.join(', ');
}

export const crossesMidnight = (s: { start: string; end: string }) => !!s.end && s.end < s.start;

/** "Tue, Thu 19:00–20:30", "Mon–Fri 07:00", "2026-12-01 19:00–21:00". The "(+1 day)" hint is added by the caller via crossesMidnight. */
export function fmtWhen(s: Pick<Schedule, 'days' | 'date' | 'start' | 'end'>): string {
  const day = s.date || fmtDays(s.days);
  return `${day} ${s.start}${s.end ? `–${s.end}` : ''}`;
}

const pad = (n: number) => String(n).padStart(2, '0');

/** Date and time digits of an RFC3339 string (node local time), without any zone conversion. */
export function nodeParts(at: string): { date: string; time: string; wd: number } {
  const date = at.slice(0, 10), time = at.slice(11, 16);
  const [y, m, d] = date.split('-').map(Number);
  return { date, time, wd: new Date(Date.UTC(y, m - 1, d)).getUTCDay() };
}
/** Offset in minutes of an RFC3339 string ("Z" is 0). */
export function offsetMin(at: string): number {
  const m = /([+-])(\d\d):(\d\d)$/.exec(at);
  return m ? (m[1] === '-' ? -1 : 1) * (+m[2] * 60 + +m[3]) : 0;
}
/** True when the browser's current offset differs from the node's (`now` string of the snapshot). */
export function tzDiffers(now: string | undefined): boolean {
  return !!now && offsetMin(now) !== -new Date().getTimezoneOffset();
}
const dayNum = (d: string) => { const [y, m, dd] = d.split('-').map(Number); return Date.UTC(y, m - 1, dd) / 86400000; };

/** "Today 19:00", "Tue 19:00", "Tue 2026-11-24 19:00" relative to the node's current date. */
export function fmtAt(at: string, now: string | undefined): string {
  const p = nodeParts(at);
  if (!now) return `${DAY_NAMES[p.wd]} ${p.time}`;
  const diff = dayNum(p.date) - dayNum(now.slice(0, 10));
  if (diff === 0) return `Today ${p.time}`;
  if (diff === 1) return `Tomorrow ${p.time}`;
  return diff > 0 && diff < 7 ? `${DAY_NAMES[p.wd]} ${p.time}` : `${DAY_NAMES[p.wd]} ${p.date} ${p.time}`;
}

/** "in 2 h 18 min", "in 5 min", "in 40 s", "in 1 d 3 h". */
export function fmtIn(sec: number): string {
  sec = Math.max(0, Math.floor(sec));
  if (sec < 60) return `in ${sec} s`;
  const d = Math.floor(sec / 86400), h = Math.floor((sec % 86400) / 3600), m = Math.floor((sec % 3600) / 60);
  if (d > 0) return `in ${d} d${h ? ` ${h} h` : ''}`;
  if (h > 0) return `in ${h} h${m ? ` ${m} min` : ''}`;
  return `in ${m} min`;
}
export const countdown = (at: string, nowMs: number) => fmtIn((Date.parse(at) - nowMs) / 1000);

/** "Connect AllStar 2001", "Disconnect EchoLink". */
export const evLabel = (e: Pick<ScheduleEvent, 'action' | 'mode' | 'target'>) =>
  [e.action === 'connect' ? 'Connect' : 'Disconnect', modeTitle(e.mode), e.target].filter(Boolean).join(' ');

/** Color class for the last result. */
export function resultClass(r: string): string {
  return r === 'ok' ? 'text-ok' : r.startsWith('error') ? 'text-danger' : 'text-muted';
}

export const emptyInput = (mode: string): ScheduleInput => ({
  name: '', enabled: true, mode, target: '', action: 'connect', days: [], date: '', start: '19:00', end: '',
});

/** Mirrors the server's validation; returns an error message or ''. */
export function validateSchedule(s: ScheduleInput, modes: string[]): string {
  if (s.name.length > 60) return 'Name is at most 60 characters';
  if (!s.mode || !modes.includes(s.mode)) return 'Choose a mode';
  if (s.action === 'connect' && !s.target.trim()) return 'Enter a target';
  const time = /^([01]\d|2[0-3]):[0-5]\d$/;
  if (!time.test(s.start)) return 'Enter a start time';
  if (s.action === 'connect' && s.end) {
    if (!time.test(s.end)) return 'Enter a valid end time';
    if (s.end === s.start) return 'End must differ from start';
  }
  if (s.date) { if (!/^\d{4}-\d\d-\d\d$/.test(s.date) || Number.isNaN(Date.parse(s.date))) return 'Enter a valid date'; }
  else if (s.days.length === 0) return 'Choose at least one day';
  return '';
}

export const activeFor = (snap: SchedulesSnapshot | null, id: number) => snap?.active.find((a) => a.id === id);

// ---- Overlap check (mirrors internal/schedules/conflict.go) ----

const DAY_MIN = 1440, WEEK_MIN = 7 * DAY_MIN;
interface Span { s: number; e: number; instant: boolean }
const mins = (hhmm: string) => +hhmm.slice(0, 2) * 60 + +hhmm.slice(3, 5);
const span = (s: Pick<Schedule, 'start' | 'end'>, base: number): Span => {
  const st = base + mins(s.start);
  if (!s.end) return { s: st, e: st, instant: true };
  let d = mins(s.end) - mins(s.start);
  if (d <= 0) d += DAY_MIN;
  return { s: st, e: st + d, instant: false };
};
const overlaps = (a: Span, b: Span): boolean => {
  if (a.instant && b.instant) return false;
  if (a.instant) return b.s <= a.s && a.s < b.e;
  if (b.instant) return a.s <= b.s && b.s < a.e;
  return a.s < b.e && b.s < a.e;
};
const shifted = (a: Span, by: number): Span => ({ s: a.s + by, e: a.e + by, instant: a.instant });
const weekdayOf = (n: number) => (((n + 4) % 7) + 7) % 7;
type Sched = Pick<Schedule, 'days' | 'date' | 'start' | 'end'>;
const weeklySpans = (s: Sched) => s.days.filter((d) => d >= 0 && d <= 6).map((d) => span(s, d * DAY_MIN));
function onDates(s: Sched, n: number): Span[] {
  const out: Span[] = [];
  for (let m = n - 1; m <= n + 1; m++) for (const d of s.days) if (d === weekdayOf(m)) out.push(span(s, m * DAY_MIN));
  return out;
}
function pairOverlap(a: Sched, b: Sched): boolean {
  if (!a.date && !b.date) {
    for (const x of weeklySpans(a)) for (const y of weeklySpans(b)) for (const sh of [-WEEK_MIN, 0, WEEK_MIN]) if (overlaps(x, shifted(y, sh))) return true;
    return false;
  }
  if (a.date && b.date) return overlaps(span(a, dayNum(a.date) * DAY_MIN), span(b, dayNum(b.date) * DAY_MIN));
  if (a.date) return pairOverlap(b, a);
  const n = dayNum(b.date), x = span(b, n * DAY_MIN);
  return onDates(a, n).some((y) => overlaps(x, y));
}

/** The schedules in `others` that `s` conflicts with: both connects, both enabled, overlapping spans. The node has one radio and one active mode, so the mode does not matter. `selfId` is skipped. */
export function findConflicts(s: ScheduleInput, others: Schedule[], selfId?: number): Schedule[] {
  if (s.action !== 'connect' || !s.enabled) return [];
  if (s.date ? !/^\d{4}-\d\d-\d\d$/.test(s.date) || Number.isNaN(Date.parse(s.date)) : s.days.length === 0) return [];
  if (!/^([01]\d|2[0-3]):[0-5]\d$/.test(s.start) || (s.end && !/^([01]\d|2[0-3]):[0-5]\d$/.test(s.end)) || s.end === s.start) return [];
  return others.filter((o) => o.id !== selfId && o.action === 'connect' && o.enabled && pairOverlap(s, o));
}

/** `overlaps "Tuesday net" (Tue 19:00–20:30)` (same text as the server's). */
export function conflictMessage(clash: Schedule[]): string {
  const parts = clash.map((c) => `${c.name ? JSON.stringify(c.name) : `#${c.id}`} (${fmtWhen(c)})`);
  const list = parts.length > 1 ? `${parts.slice(0, -1).join(', ')} and ${parts[parts.length - 1]}` : parts[0];
  return `overlaps ${list}`;
}

export const MANUAL_ACTIONS: { value: 'pause' | 'pause_today' | 'cancel'; label: string }[] = [
  { value: 'pause', label: 'Pause all until re-enabled' },
  { value: 'pause_today', label: 'Pause all for today' },
  { value: 'cancel', label: 'Cancel the running window, keep schedules on' },
];
export const manualActionLabel = (m: string) => MANUAL_ACTIONS.find((x) => x.value === m)?.label ?? m;

/** `"Tuesday net" canceled by manual connect 41234 on 2006 by op` */
export const cancelText = (n: { names: string[]; reason: string }) => `${n.names.map((x) => JSON.stringify(x)).join(', ')} canceled by ${n.reason}`;
