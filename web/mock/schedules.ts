// In-memory schedules for `npm run dev:mock` (docs/SCHEDULES.md). Dev only. Uses the mock process's local time zone.
import { conflictMessage, findConflicts } from '../src/lib/schedules.ts';
interface Sched {
  id: number; name: string; enabled: boolean; node: string; target: string; action: 'connect' | 'disconnect'; mode: string;
  permanent: boolean; exclusive: boolean; days: number[]; date: string; start: string; end: string; lastRun: string | null; lastResult: string;
}
interface Deps {
  locals: Record<string, { links: { node: string; mode: string }[] }>;
  link: (local: string, target: string, mode: string) => void;
  unlink: (local: string, target: string) => void;
  dropOthers: (local: string, target: string) => void;
  broadcast: (ev: string, f: (role: string) => unknown) => void;
  pushNodes: () => void;
}
const p2 = (n: number) => String(n).padStart(2, '0');
const iso = (d: Date) => {
  const o = -d.getTimezoneOffset(), a = Math.abs(o);
  return `${d.getFullYear()}-${p2(d.getMonth() + 1)}-${p2(d.getDate())}T${p2(d.getHours())}:${p2(d.getMinutes())}:${p2(d.getSeconds())}${o < 0 ? '-' : '+'}${p2(Math.floor(a / 60))}:${p2(a % 60)}`;
};
const at = (day: Date, hhmm: string) => new Date(day.getFullYear(), day.getMonth(), day.getDate(), +hhmm.slice(0, 2), +hhmm.slice(3, 5));

export function createSchedules(d: Deps) {
  let list: Sched[] = [];
  let nextId = 1;
  let enabled = true, pausedReason = '', pausedAt: string | null = null, resumeAt: number | null = null;
  let manualAction: 'pause' | 'pause_today' | 'cancel' = 'pause';
  let lastCancel: { reason: string; at: string; until: number; ids: number[] } | null = null;
  const cancelled = new Set<string>(); // `${id}@${start ms}`
  let lastTick = Date.now();
  const add = (s: Partial<Sched>) => { list.push({ id: nextId++, name: '', enabled: true, node: '1999', target: '', action: 'connect', mode: 'transceive', permanent: false, exclusive: false, days: [], date: '', start: '19:00', end: '', lastRun: null, lastResult: '', ...s }); };
  add({ name: 'Tuesday net', target: '2185', days: [2], start: '19:00', end: '20:30', exclusive: true });
  add({ name: 'Morning check-in', target: '2222', mode: 'monitor', days: [1, 2, 3, 4, 5], start: '07:00', end: '07:30' });

  type Occ = { s: Sched; start: Date; end: Date | null };
  function occurrences(s: Sched, from: Date, to: Date): Occ[] {
    const out: Occ[] = [];
    for (let k = -2; k <= 9; k++) {
      const day = new Date(from.getFullYear(), from.getMonth(), from.getDate() + k);
      const ds = `${day.getFullYear()}-${p2(day.getMonth() + 1)}-${p2(day.getDate())}`;
      if (s.date ? s.date !== ds : !s.days.includes(day.getDay())) continue;
      const start = at(day, s.start);
      let end: Date | null = null;
      if (s.action === 'connect' && s.end) { end = at(day, s.end); if (s.end < s.start) end = new Date(end.getTime() + 86400000); if (end.getTime() <= start.getTime()) end = new Date(end.getTime() + 86400000); }
      out.push({ s, start, end });
    }
    return out.filter((o) => o.start <= to);
  }
  const key = (o: Occ) => `${o.s.id}@${o.start.getTime()}`;
  function snapshot() {
    const now = new Date(), n = now.getTime();
    const sorted = [...list].sort((a, b) => a.node.localeCompare(b.node) || a.start.localeCompare(b.start) || a.id - b.id);
    const ev: { t: number; o: unknown }[] = [], active: unknown[] = [];
    for (const s of list) {
      if (!s.enabled) continue;
      for (const o of occurrences(s, now, new Date(n + 8 * 86400000))) {
        const base = { id: s.id, name: s.name, node: s.node, target: s.target, mode: s.mode };
        if (o.start.getTime() > n) ev.push({ t: o.start.getTime(), o: { at: iso(o.start), ...base, action: s.action } });
        if (o.end && o.end.getTime() > n) {
          if (o.start.getTime() <= n && !cancelled.has(key(o))) active.push({ id: s.id, start: iso(o.start), end: iso(o.end) });
          if (!cancelled.has(key(o))) ev.push({ t: o.end.getTime(), o: { at: iso(o.end), ...base, action: 'disconnect' } });
        }
      }
    }
    ev.sort((a, b) => a.t - b.t);
    const notice = lastCancel && lastCancel.until > n ? lastCancel.ids.filter((id) => list.some((s) => s.id === id && s.enabled)) : [];
    const lc = lastCancel && notice.length ? { reason: lastCancel.reason, at: lastCancel.at, until: iso(new Date(lastCancel.until)), ids: notice, names: notice.map((id) => { const s = list.find((x) => x.id === id)!; return s.name || `Connect ${s.target}`; }) } : null;
    return { enabled, pausedReason, pausedAt, resumeAt: !enabled && resumeAt ? iso(new Date(resumeAt)) : null, manualAction, lastCancel: lc, timezone: Intl.DateTimeFormat().resolvedOptions().timeZone, now: iso(now), schedules: sorted, active, upcoming: ev.slice(0, 10).map((e) => e.o) };
  }
  const push = () => d.broadcast('schedules', () => snapshot());
  const linked = (s: Sched) => d.locals[s.node]?.links.some((l) => l.node === s.target);
  const done = (s: Sched, r: string) => { s.lastRun = iso(new Date()); s.lastResult = r; };

  function runDue() {
    const now = Date.now(), from = lastTick; lastTick = now;
    if (!enabled && resumeAt && now >= resumeAt) { enabled = true; pausedReason = ''; pausedAt = null; resumeAt = null; push(); }
    if (!enabled) return;
    const due: { t: number; s: Sched; kind: 'start' | 'end'; o: Occ }[] = [];
    for (const s of list) {
      if (!s.enabled) continue;
      for (const o of occurrences(s, new Date(now), new Date(now))) {
        if (o.start.getTime() > from && o.start.getTime() <= now) due.push({ t: o.start.getTime(), s, kind: 'start', o });
        if (o.end && o.end.getTime() > from && o.end.getTime() <= now) due.push({ t: o.end.getTime(), s, kind: 'end', o });
      }
    }
    // disconnects first (window ends, then disconnect schedules), then connects
    const rank = (x: (typeof due)[0]) => (x.kind === 'end' || x.s.action === 'disconnect' ? 0 : 1);
    due.sort((a, b) => rank(a) - rank(b) || a.s.id - b.s.id);
    for (const x of due) {
      const s = x.s;
      if (cancelled.has(key(x.o))) continue;
      if (!d.locals[s.node]) { done(s, 'error: unknown node'); continue; }
      if (x.kind === 'end' || s.action === 'disconnect') {
        if (linked(s)) { d.unlink(s.node, s.target); done(s, 'ok'); } else done(s, 'skipped: not connected');
      } else if (linked(s)) done(s, 'skipped: already connected');
      else { if (s.exclusive && s.mode !== 'localmonitor') d.dropOthers(s.node, s.target); d.link(s.node, s.target, s.mode); done(s, 'ok'); }
      if (s.date && !(x.kind === 'start' && x.o.end)) s.enabled = false;
    }
    if (due.length) { d.pushNodes(); push(); }
  }
  setInterval(runDue, 15000);

  function validate(b: any): string {
    if (typeof b.name === 'string' && b.name.length > 60) return 'name is at most 60 characters';
    if (!d.locals[b.node]) return 'unknown_node';
    if (!/^[1-9][0-9]{0,9}$/.test(b.target ?? '')) return 'target must be a node number';
    if (b.target === b.node) return 'target must differ from node';
    if (b.action !== 'connect' && b.action !== 'disconnect') return 'action must be connect or disconnect';
    if (b.action === 'connect' && !['transceive', 'monitor', 'localmonitor'].includes(b.mode)) return 'invalid mode';
    const t = /^([01]\d|2[0-3]):[0-5]\d$/;
    if (!t.test(b.start ?? '')) return 'start must be HH:MM';
    if (b.end && (b.action !== 'connect' || !t.test(b.end) || b.end === b.start)) return 'invalid end';
    const days: number[] = Array.isArray(b.days) ? b.days : [];
    if (b.date ? days.length > 0 || !/^\d{4}-\d\d-\d\d$/.test(b.date) : days.length === 0 || days.some((x) => !Number.isInteger(x) || x < 0 || x > 6)) return 'set exactly one of days or date';
    return '';
  }
  const clean = (b: any) => ({ name: String(b.name ?? ''), enabled: b.enabled !== false, node: b.node, target: b.target, action: b.action, mode: b.action === 'connect' ? b.mode : '', permanent: !!b.permanent, exclusive: !!b.exclusive && b.action === 'connect', days: [...new Set<number>(b.days ?? [])].sort((a, c) => a - c), date: b.date ?? '', start: b.start, end: b.action === 'connect' ? b.end ?? '' : '' });

  return {
    /** Manual connect/disconnect through the panel: applies the manual-action setting (pause, pause for today, cancel on that node). */
    manual(node: string, reason: string) {
      const now = new Date();
      const hit: { s: Sched; end: number }[] = [];
      for (const s of list) {
        if (manualAction === 'cancel' && s.node !== node) continue;
        for (const o of occurrences(s, now, now)) if (o.end && o.start <= now && o.end > now) { cancelled.add(key(o)); if (s.enabled) hit.push({ s, end: o.end.getTime() }); }
      }
      if (manualAction === 'cancel') {
        if (hit.length) { lastCancel = { reason, at: iso(now), until: Math.max(...hit.map((h) => h.end)), ids: hit.map((h) => h.s.id) }; push(); }
        return;
      }
      if (enabled) {
        enabled = false; pausedReason = reason; pausedAt = iso(now); resumeAt = null;
        if (manualAction === 'pause_today') resumeAt = new Date(now.getFullYear(), now.getMonth(), now.getDate() + 1).getTime();
      } else if (manualAction === 'pause') resumeAt = null;
      push();
    },
    /** Returns true when the request was handled. */
    handle(m: string, p: string, body: any, canOp: boolean, json: (c: number, b?: unknown) => void, err: (c: number, msg: string, code: string) => void, forbid: () => void): boolean {
      let mm: RegExpExecArray | null;
      if (p === '/schedules' && m === 'GET') { json(200, snapshot()); return true; }
      if (!p.startsWith('/schedules')) return false;
      if (!canOp) { forbid(); return true; }
      if (p === '/schedules/enabled' && m === 'PUT') {
        if (typeof body.enabled !== 'boolean') { err(400, 'enabled (bool) is required', 'bad_request'); return true; }
        enabled = body.enabled; pausedReason = ''; pausedAt = null; resumeAt = null; if (enabled) lastTick = Date.now();
        push(); json(200, snapshot()); return true;
      }
      if (p === '/schedules/settings' && m === 'PUT') {
        if (!['pause', 'pause_today', 'cancel'].includes(body.manualAction)) { err(400, 'manualAction must be "pause", "pause_today" or "cancel"', 'bad_request'); return true; }
        manualAction = body.manualAction; push(); json(200, snapshot()); return true;
      }
      const clash = (b: any, id?: number) => { const c = findConflicts(clean(b) as any, list as any, id); return c.length ? conflictMessage(b.node, c) : ''; };
      if (p === '/schedules' && m === 'POST') {
        const e = validate(body); if (e) { err(400, e, e === 'unknown_node' ? 'unknown_node' : 'bad_request'); return true; }
        const c = clash(body); if (c) { err(409, c, 'schedule_conflict'); return true; }
        const s: Sched = { id: nextId++, ...clean(body), lastRun: null, lastResult: '' }; list.push(s); push(); json(201, s); return true;
      }
      if ((mm = /^\/schedules\/(\d+)$/.exec(p))) {
        const s = list.find((x) => x.id === +mm![1]); if (!s) { err(404, 'not found', 'not_found'); return true; }
        if (m === 'PUT') { const e = validate(body); if (e) { err(400, e, e === 'unknown_node' ? 'unknown_node' : 'bad_request'); return true; } const c = clash(body, s.id); if (c) { err(409, c, 'schedule_conflict'); return true; } Object.assign(s, clean(body)); push(); json(200, s); return true; }
        if (m === 'DELETE') { list = list.filter((x) => x !== s); push(); json(204); return true; }
      }
      return false;
    },
    snapshot,
  };
}
