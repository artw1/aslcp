// In-memory /api/nowcalling and the radioid.net user list for `npm run dev:mock` (docs/API.md "Now Calling"). Dev only.
// A 40 s cycle: a DMR talker for 14 s, a short AllStar call, a YSF call, then quiet. Placeholder data only.
const T0 = Date.now();
interface MockCall { id: string; kind: string; call: string; first?: string; name?: string; city?: string; state?: string; country?: string; location?: string; dmrId?: string; node?: string; desc?: string; local?: string; network?: string; tg?: string; tgName?: string; slot?: number; room?: string }
const SCRIPT: { at: number; len: number; c: MockCall }[] = [
  { at: 2, len: 14, c: { id: 'dmr', kind: 'dmr', call: 'N0CALL', first: 'Alex', name: 'Alex Example', city: 'Exampleton', state: 'Example State', country: 'Exampleland', dmrId: '1234567', network: 'BrandMeister', tg: '91', tgName: 'Example Worldwide', slot: 2 } },
  { at: 20, len: 6, c: { id: 'asl', kind: 'allstar', call: 'N1CALL', location: 'Example Hill, Anytown', node: '1998', desc: 'Example hub', local: '2001' } },
  { at: 30, len: 8, c: { id: 'ysf', kind: 'ysf', call: 'N0CALL', first: '', name: '', city: '', state: '', country: '', room: 'Example Room' } },
];
const CYCLE = 40;
// The single shared timeline: the one link that is keyed right now, or null. The DMR and YSF talkers come in through
// the digital modes bridge link (node 1997) and the AllStar talker is the hub link 1998, all on local node 2001.
export const BRIDGE_NODE = '1997';
export function talkerLink(now = Date.now()): { local: string; node: string; len: number } | null {
  const c = (nowCalling(now).current as MockCall | null);
  if (!c) return null;
  return { local: '2001', node: c.kind === 'allstar' ? c.node! : BRIDGE_NODE, len: SCRIPT.find((s) => c.id.startsWith(s.c.id + '-'))?.len ?? 0 };
}
const iso = (ms: number) => new Date(ms).toISOString();

export function nowCalling(now = Date.now()) {
  const n = Math.floor((now - T0) / 1000 / CYCLE), recent: unknown[] = [];
  let current: unknown = null;
  for (let cyc = n; cyc >= Math.max(0, n - 1); cyc--) {
    for (let i = SCRIPT.length - 1; i >= 0; i--) {
      const s = SCRIPT[i], start = T0 + (cyc * CYCLE + s.at) * 1000, end = start + s.len * 1000;
      if (start > now) continue;
      const base = { ...s.c, id: `${s.c.id}-${cyc}` };
      if (now < end) current = { ...base, start: iso(start), active: true };
      else recent.push({ ...base, start: iso(start), end: iso(end), active: false });
    }
  }
  return { current, recent: recent.slice(0, 5) };
}

const userList = { enabled: true, entries: 3, updated: iso(Date.now() - 86400e3), checked: iso(Date.now() - 3600e3), error: '', busy: false, size: 220 };
let busyUntil = 0;
const view = () => ({ ...userList, busy: Date.now() < busyUntil });

export function handleNowCalling(c: { m: string; p: string; body?: unknown; user?: { role: string }; json: (code: number, body?: unknown) => void; err: (code: number, msg: string, c: string) => void }): boolean {
  if (c.m === 'GET' && c.p === '/nowcalling') { if (!c.user) c.err(401, 'authentication required', 'unauthorized'); else c.json(200, nowCalling()); return true; }
  if (!c.p.startsWith('/dv/userlist')) return false;
  if (c.user?.role !== 'admin') { c.err(c.user ? 403 : 401, 'insufficient permissions', c.user ? 'forbidden' : 'unauthorized'); return true; }
  if (c.m === 'GET' && c.p === '/dv/userlist') { c.json(200, view()); return true; }
  if (c.m === 'PUT' && c.p === '/dv/userlist') { userList.enabled = !!(c.body as { enabled?: boolean } | undefined)?.enabled; c.json(200, view()); return true; }
  if (c.m === 'POST' && c.p === '/dv/userlist/download') {
    if (!userList.enabled) { c.err(409, 'the user list is off', 'userlist_off'); return true; }
    busyUntil = Date.now() + 3000;
    setTimeout(() => { userList.updated = userList.checked = iso(Date.now()); }, 3000);
    c.json(202, view());
    return true;
  }
  return false;
}
