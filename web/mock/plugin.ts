// In-memory implementation of docs/API.md for `npm run dev:mock`. Dev only; never part of the build.
import type { Plugin } from 'vite';
import type { IncomingMessage, ServerResponse } from 'node:http';
import { handleConfig } from './config.ts';
import { handleBackup, downUntil } from './backup.ts';
import { handleRadio } from './radio.ts';
import { dvNodeLink, handleDVSwitch } from './dvswitch.ts';
import { createSchedules } from './schedules.ts';
import { handleSystem } from './system.ts';
import { handleWifi } from './wifi.ts';

type Info = { node: string; callsign: string; description: string; location: string };
const calls = ['N0AAA', 'N0AAB', 'N0AAC', 'N0AAD', 'N0AAE', 'N0AAF', 'N0AAG', 'N0AAH', 'N0AAJ', 'N0AAK', 'N0AAL', 'N0AAM', 'N0AAN', 'N0AAP', 'N0AAQ', 'N0AAR', 'N0AAS', 'N0AAT', 'N0AAU', 'N0AAV'];
const places = ['Springfield, ST', 'Riverton, ST', 'Lakeside, ST', 'Hill Valley, ST', 'Fakeville, ST'];
const descs = ['Hub', 'Repeater 146.520', 'Simplex node', 'Net control', 'Club repeater', 'Linked UHF'];
const NODES: Info[] = calls.map((c, i) => ({ node: String(2000 + i * 37), callsign: c, description: `${descs[i % descs.length]} (fake)`, location: places[i % places.length] }));
NODES.push({ node: '2001', callsign: 'N0LOC', description: 'Local test node', location: 'Springfield, ST' }, { node: '51755', callsign: 'N0LOD', description: 'Second local node', location: 'Springfield, ST' });
const byNode = new Map(NODES.map((n) => [n.node, n]));
const info = (n: string) => byNode.get(n) ?? null;

interface L { node: string; kind: string; direction: 'IN' | 'OUT'; start: number; mode: string; keyedUntil: number; lastKey: number | null; lastUnkey: number | null }
const locals: Record<string, { links: L[]; rx: number; tx: number }> = {};
const now = () => Date.now();
function addLink(local: string, node: string, mode: string, dir: 'IN' | 'OUT', ago = 0) {
  const kind = /^3\d{6}$/.test(node) ? 'echolink' : /^\d{5,}$/.test(node) || byNode.has(node) ? 'allstar' : 'unknown';
  locals[local].links.push({ node, kind, direction: dir, start: now() - ago * 1000, mode: mode === 'monitor' ? 'R' : mode === 'localmonitor' ? 'L' : 'T', keyedUntil: 0, lastKey: null, lastUnkey: null });
}
locals['2001'] = { links: [], rx: 0, tx: 0 };
locals['51755'] = { links: [], rx: 0, tx: 0 };
// Nodes with a SimpleUSB device in the mock (Audio Tuning); placeholder numbers.
NODES.push({ node: '1999', callsign: 'N0TUN', description: 'Tuning test node', location: 'Springfield, ST' }, { node: '1998', callsign: 'N0TUO', description: 'Second tuning node', location: 'Springfield, ST' });
byNode.set('1999', NODES[NODES.length - 2]); byNode.set('1998', NODES[NODES.length - 1]);
locals['1999'] = { links: [], rx: 0, tx: 0 };
locals['1998'] = { links: [], rx: 0, tx: 0 };
addLink('2001', '2000', 'transceive', 'OUT', 5400);
addLink('2001', '2037', 'monitor', 'OUT', 1200);
addLink('2001', '2074', 'transceive', 'IN', 300);
addLink('2001', '2111', 'transceive', 'IN', 61);
addLink('51755', '2148', 'transceive', 'OUT', 800);
addLink('1999', '2000', 'transceive', 'OUT', 900);

let favs: { id: number; node: string; label: string; group: string; position: number }[] = [
  { id: 1, node: '2000', label: 'Main hub', group: 'Hubs', position: 0 },
  { id: 2, node: '2037', label: 'Club repeater', group: 'Hubs', position: 1 },
  { id: 3, node: '2185', label: 'Evening net', group: 'Nets', position: 0 },
  { id: 4, node: '2222', label: 'Simplex friends', group: 'Nets', position: 1 },
  { id: 5, node: '2259', label: 'Test node', group: '', position: 0 },
];
let nextFav = 6;
const sortedFavs = () => [...favs].sort((a, b) => a.group.localeCompare(b.group) || a.position - b.position).map((f) => ({ ...f, info: info(f.node) }));

let users = [{ id: 1, username: 'admin', role: 'admin' }, { id: 2, username: 'op', role: 'operator' }, { id: 3, username: 'guest', role: 'viewer' }];
let nextUser = 4;
// Favorites activity scan (synthetic values, like demo mode): one node every 3 s (20/min), 0.8 s each.
let favScan = false;
let favChecking = '';
let favNextAt = 0;
let favPassStart = 0;
const favCheckedAt: Record<string, number> = {};
const favEligible = () => favs.map((f) => f.node).filter((n) => +n >= 2000 && +n <= 999999);
const favOffline = (n: number) => n % 9 === 4;
const favNext = () => {
  const list = favEligible().filter((n) => n !== favChecking);
  return list.length ? list.reduce((b, n) => ((favCheckedAt[n] ?? 0) < (favCheckedAt[b] ?? 0) ? n : b)) : '';
};
const favActivity = () => {
  const stats: Record<string, unknown> = {};
  const list = favEligible();
  const checkedAt: Record<string, string> = {};
  let checked = 0;
  if (favScan) for (const node of list) {
    const n = +node, c = favCheckedAt[node];
    if (c != null) { checkedAt[node] = new Date(c).toISOString(); if (c >= favPassStart) checked++; }
    if (c == null || favOffline(n)) continue;
    const t = Math.floor(Date.now() / 1000);
    stats[node] = { rxPct: n % 4 === 0 ? null : (n % 23) + 1, linkCnt: n % 7, active: n % 5 !== 0, webTx: n % 3 === 0, keyed: (t + n) % 40 < 8, txAvg: 0, lastSeen: new Date().toISOString() };
  }
  return { enabled: favScan, rate: 20, effRate: 20, backoff: false, backoffReason: '', retryAt: null, updated: favScan && Object.keys(favCheckedAt).length ? new Date().toISOString() : null, stats,
    checking: favScan ? favChecking : '', next: favScan ? favNext() : '', nextAt: favScan && list.length ? new Date(favNextAt || Date.now()).toISOString() : null, checked, total: list.length, checkedAt };
};
function favStep(broadcast: (ev: string, f: (role: string) => unknown) => void) {
  if (!favScan || favChecking) return;
  const list = favEligible();
  if (!list.length || Date.now() < favNextAt) return;
  const node = list.reduce((b, n) => ((favCheckedAt[n] ?? 0) < (favCheckedAt[b] ?? 0) ? n : b));
  if (favCheckedAt[node] != null && favCheckedAt[node] >= favPassStart) favPassStart = Date.now();
  favChecking = node; favNextAt = Date.now() + 3000;
  broadcast('favstats', favActivity);
  setTimeout(() => { favChecking = ''; favCheckedAt[node] = Date.now(); broadcast('favstats', favActivity); }, 800);
}
let setupNeeded = process.env.MOCK_SETUP === '1';
let nodelistUpdated: string | null = new Date().toISOString();

function status(id: string, role = 'admin') {
  const s = locals[id], t = now();
  const sec = (ms: number | null) => (ms == null ? null : Math.floor((t - ms) / 1000));
  const links = s.links.map((l) => {
    const keyed = l.keyedUntil > t;
    return { node: l.node, kind: l.kind, info: info(l.node), ip: role === 'viewer' ? '' : l.kind === 'allstar' ? `192.0.2.${(Number(l.node) % 200) + 10}` : '', direction: l.direction, connected: Math.floor((t - l.start) / 1000), state: 'ESTABLISHED', mode: l.mode, keyed, sinceKey: keyed ? 0 : sec(l.lastKey), sinceUnkey: sec(l.lastUnkey) };
  });
  const network = [...links.map((l) => ({ node: l.node, mode: l.mode, kind: l.kind, info: l.info })), ...NODES.filter((n) => n.node !== id && !s.links.some((l) => l.node === n.node)).slice(0, 18 + Number(id) % 7).map((n) => ({ node: n.node, mode: 'T', kind: 'allstar', info: n }))];
  return { node: id, info: info(id), stale: false, rxKeyed: s.rx > t, txKeyed: s.tx > t || links.some((l) => l.keyed && l.mode === 'T'), extTxKeyed: false, numLinks: network.length, links, network, flags: { txDisabled: false, linkFunsDisabled: false, parrot: false }, updated: new Date().toISOString() };
}
// Public status view (docs/API.md "Public page"): on by default in the mock so the logged-out view can be developed.
const pubCfg = { enabled: true, title: '', nodes: [] as string[], showCallsigns: true, showNetwork: true, showActivity: true, allowEmbedding: false, trustProxy: false };
const activity: { node: string; link: string; at: number; duration: number }[] = [
  { node: '2001', link: '2000', at: Date.now() - 95e3, duration: 14 },
  { node: '2001', link: 'local', at: Date.now() - 260e3, duration: 6 },
  { node: '51755', link: '2148', at: Date.now() - 700e3, duration: 1 },
  { node: '2001', link: '2074', at: Date.now() - 3.2e6, duration: 41 },
];
const pubDefaultTitle = () => `${info('2001')?.callsign} AllStarLink node 2001`;
function pubSnapshot() {
  const ids = Object.keys(locals).filter((id) => !pubCfg.nodes.length || pubCfg.nodes.includes(id));
  const nodes = ids.map((id) => {
    const s = status(id, 'viewer');
    return {
      node: s.node, info: s.info && { callsign: s.info.callsign, description: s.info.description, location: s.info.location }, stale: false,
      txKeyed: s.txKeyed, rxKeyed: s.rxKeyed, numLinks: s.numLinks,
      links: s.links.map((l) => ({ node: l.node, kind: l.kind, ...(pubCfg.showCallsigns && l.info ? { info: { callsign: l.info.callsign, description: l.info.description, location: l.info.location } } : {}), direction: l.direction, mode: l.mode, connected: l.connected, keyed: l.keyed, sinceKey: l.sinceKey, sinceUnkey: l.sinceUnkey })),
      ...(pubCfg.showNetwork ? { network: s.network.map((n) => ({ node: n.node, ...(pubCfg.showCallsigns && n.info ? { callsign: n.info.callsign } : {}) })) } : {}),
    };
  });
  const acts = activity.filter((a) => ids.includes(a.node)).sort((a, b) => b.at - a.at).slice(0, 25)
    .map((a) => ({ node: a.node, link: a.link, ...(pubCfg.showCallsigns && a.link !== 'local' && info(a.link) ? { callsign: info(a.link)!.callsign } : {}), at: Math.floor(a.at / 1000), duration: a.duration, kerchunk: a.duration < 2 }));
  return { title: pubCfg.title || pubDefaultTitle(), now: Math.floor(now() / 1000), callsigns: pubCfg.showCallsigns, showNetwork: pubCfg.showNetwork, showActivity: pubCfg.showActivity, nodes, ...(pubCfg.showActivity ? { activity: acts } : {}) };
}
// Simulated update: apply runs ~6s, then the "server" restarts (SSE streams close) and reports the new version.
let mockVersion = '0.1.0-mock';
let mockInstance = Math.random().toString(16).slice(2, 10);
let mockChannel: 'stable' | 'beta' = 'stable';
const candidate = () => (mockChannel === 'beta' ? '0.3.0~beta.1-mock' : '0.2.0-mock');
let updJob: 'idle' | 'running' | 'succeeded' = 'idle';
let updChecked: string | null = null;
let restartClients: () => void = () => {};
const upgradable = () => updChecked !== null && mockVersion !== candidate();
const updInfo = () => ({
  os: osInfo(),
  available: true, reason: null, applyReason: null, channel: mockChannel, installed: mockVersion, candidate: updChecked ? candidate() : '', upgradable: upgradable(), job: updJob,
  jobLog: updJob === 'running' ? 'Reading package lists...\nUnpacking aslcp (0.2.0) over (0.1.0) ...' : updJob === 'succeeded' ? 'Setting up aslcp (0.2.0) ...' : '',
  notes: upgradable() ? '- New **Updates** section in Settings\n- Faster dashboard\n\nThanks for testing <script>alert(1)</script>' : null, checked: updChecked,
});
// Simulated OS (apt) updates: a security package, an asterisk package, a reboot-required toggle.
const osAll = [
  { name: 'asterisk', from: '1:20.5.0-1', to: '1:20.9.0-1', security: false },
  { name: 'libssl3', from: '3.0.11-1~deb12u1', to: '3.0.13-1~deb12u2', security: true },
  { name: 'linux-image-rpi-v8', from: '6.1.63-1+rpt1', to: '6.1.69-1+rpt1', security: false },
  { name: 'tzdata', from: '2024a-0+deb12u1', to: '2024b-0+deb12u1', security: false },
];
let osChecked: string | null = new Date().toISOString(), osChecking = false, osJob: 'idle' | 'running' | 'succeeded' = 'idle', osApplied = false, osReboot = false;
const osPkgs = () => (osApplied ? [] : osAll);
const osInfo = () => ({
  available: true, reason: null, checkedAt: osChecked, checking: osChecking, error: null, packages: osPkgs(), keptBack: osApplied ? [] : ['raspi-firmware'],
  rebootRequired: osReboot, rebootPkgs: osReboot ? ['linux-image-rpi-v8'] : [], job: osJob,
  jobLog: osJob === 'running' ? 'Reading package lists...\nUnpacking libssl3 (3.0.13-1~deb12u2) over (3.0.11-1~deb12u1) ...' : osJob === 'succeeded' ? 'Setting up libssl3 (3.0.13-1~deb12u2) ...\nSetting up asterisk (1:20.9.0-1) ...' : '',
});
const osBadge = () => ({ aslcp: upgradable(), aslcpVersion: upgradable() ? candidate() : '', os: osPkgs().length, security: osPkgs().filter((p) => p.security).length, reboot: osReboot });
const system = () => ({ updateAvailable: upgradable(), updateVersion: upgradable() ? candidate() : '', hostname: 'node2001', cpuTemp: 44 + Math.random() * 6, load: [0.12, 0.1, 0.05].map((x) => x + Math.random() * 0.1), memTotal: 1024e6, memAvailable: 512e6, uptime: 120660 + Math.floor(process.uptime()), asterisk: { connected: true, version: '22.10.1+asl3-3.10.8-1.deb13', uptime: 120000, error: null }, version: mockVersion });

export function mockApi(): Plugin {
  const sessions = new Map<string, { id: number; username: string; role: string }>();
  const clients = new Set<{ res: ServerResponse; role: string }>();
  const json = (res: ServerResponse, code: number, body?: unknown) => {
    res.statusCode = code;
    if (body !== undefined) { res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify(body)); } else res.end();
  };
  const err = (res: ServerResponse, code: number, msg: string, c: string) => json(res, code, { error: msg, code: c });
  let rawBody = '';
  const readBody = (req: IncomingMessage) => new Promise<any>((ok) => { let d = ''; req.on('data', (c) => (d += c.toString('latin1'))); req.on('end', () => { rawBody = d; try { ok(d ? JSON.parse(d) : {}); } catch { ok({}); } }); });
  const pubClients = new Set<ServerResponse>();
  const sse = (c: { res: ServerResponse }, ev: string, data: unknown) => c.res.write(`event: ${ev}\ndata: ${JSON.stringify(data)}\n\n`);
  restartClients = () => { clients.forEach((c) => c.res.end()); clients.clear(); };
  const broadcast = (ev: string, f: (role: string) => unknown) => clients.forEach((c) => { const d = f(c.role); if (d !== undefined) sse(c, ev, d); });

  const pushNodes = () => broadcast('nodes', (r) => Object.keys(locals).map((id) => status(id, r)));
  const sched = createSchedules({
    locals, broadcast, pushNodes,
    link: (local, target, mode) => addLink(local, target, mode, 'OUT'),
    unlink: (local, target) => { locals[local].links = locals[local].links.filter((l) => l.node !== target); },
    dropOthers: (local, target) => { locals[local].links = locals[local].links.filter((l) => l.node === target); },
  });

  return {
    name: 'aslcp-mock',
    configureServer(server) {
      let tick = 0;
      setInterval(() => {
        tick++;
        const t = now();
        for (const id of Object.keys(locals)) {
          const s = locals[id];
          for (const l of s.links) {
            if (l.keyedUntil > t) { if (l.keyedUntil - t < 1000) { l.lastUnkey = t; } continue; }
            if (Math.random() < 0.12) { l.keyedUntil = t + 2500 + Math.random() * 6000; l.lastKey = t; activity.push({ node: id, link: l.node, at: t, duration: Math.round((l.keyedUntil - t) / 1000) }); activity.splice(0, Math.max(0, activity.length - 200)); }
          }
          if (Math.random() < 0.05) s.rx = t + 2000;
        }
        broadcast('nodes', (r) => Object.keys(locals).map((id) => status(id, r)));
        if (pubCfg.enabled) pubClients.forEach((res) => res.write(`event: snapshot\ndata: ${JSON.stringify(pubSnapshot())}\n\n`));
        if (tick % 10 === 0) broadcast('system', system);
      }, 1000);
      setInterval(() => clients.forEach((c) => c.res.write(': ping\n\n')), 15000);
      setInterval(() => favStep(broadcast), 250);

      server.middlewares.use(async (req, res, next) => {
        const url = new URL(req.url ?? '/', 'http://x');
        if (!url.pathname.startsWith('/api/')) return next();
        if (Date.now() < downUntil) { req.socket.destroy(); return; } // simulated restart after a restore
        res.setHeader('X-ASLCP-Instance', mockInstance);
        const p = url.pathname.slice(4), m = req.method ?? 'GET';
        const cookie = /aslcp_session=([^;]+)/.exec(req.headers.cookie ?? '')?.[1];
        const user = cookie ? sessions.get(cookie) : undefined;
        if (m !== 'GET' && req.headers['x-aslcp'] !== '1') return err(res, 403, 'missing X-ASLCP header', 'csrf');
        const body = m === 'GET' ? {} : await readBody(req);
        const login = (u: { id: number; username: string; role: string }) => { const tok = Math.random().toString(36).slice(2); sessions.set(tok, u); res.setHeader('Set-Cookie', `aslcp_session=${tok}; Path=/; HttpOnly; SameSite=Lax`); };

        if (p === '/setup') {
          if (m === 'GET') return json(res, 200, { needed: setupNeeded, hotspot: process.env.MOCK_HOTSPOT === '1' });
          if (!setupNeeded) return err(res, 409, 'setup has already been completed', 'setup_done');
          if (body.code !== '123456') return err(res, 403, 'wrong setup code (mock: 123456)', 'bad_setup_code');
          users = [{ id: 1, username: body.username, role: 'admin' }]; setupNeeded = false; login(users[0]); return json(res, 204);
        }
        if (p === '/login' && m === 'POST') {
          const u = users.find((x) => x.username === body.username);
          if (!u || body.password !== 'pass') return err(res, 401, 'invalid username or password', 'bad_credentials');
          login(u); return json(res, 200, u);
        }
        if (p === '/logout') { if (cookie) sessions.delete(cookie); return json(res, 204); }
        if (p === '/public/info') return json(res, 200, { enabled: pubCfg.enabled, title: pubCfg.enabled ? pubSnapshot().title : '' });
        if (p === '/public/status') return pubCfg.enabled ? json(res, 200, pubSnapshot()) : err(res, 404, 'no such endpoint', 'not_found');
        if (p === '/public/events') {
          if (!pubCfg.enabled) return err(res, 404, 'no such endpoint', 'not_found');
          res.writeHead(200, { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache', Connection: 'keep-alive' });
          pubClients.add(res); req.on('close', () => pubClients.delete(res));
          res.write(`event: snapshot\ndata: ${JSON.stringify(pubSnapshot())}\n\n`);
          return;
        }
        if (handleBackup({ m, p, body, raw: rawBody, res, user, setupNeeded, setupDone: () => { setupNeeded = false; }, json: (c, b) => json(res, c, b), err: (c, msg, code) => json(res, c, { error: msg, code }), restart: () => restartClients() })) return;
        if (!user) return err(res, 401, 'authentication required', 'unauthorized');
        const isAdmin = user.role === 'admin', canOp = user.role !== 'viewer';
        const forbid = () => err(res, 403, 'forbidden', 'forbidden');
        let mm: RegExpExecArray | null;
        if (handleRadio({ m, p, body, user, json: (c, b) => json(res, c, b), err: (c, msg, code) => json(res, c, { error: msg, code }), links: (n) => locals[n]?.links.map((l) => l.node) ?? [] })) return;
        if (handleWifi({ m, p, url, body, user, json: (c, b) => json(res, c, b), err: (c, msg, code) => json(res, c, { error: msg, code }) })) return;
        if (handleSystem({ m, p, url, body, user, json: (c, b) => json(res, c, b), err: (c, msg, code) => json(res, c, { error: msg, code }) })) return;
        if (handleDVSwitch({ push: (ev, d) => broadcast(ev, () => d), m, p, url, body, user, json: (c, b) => json(res, c, b), err: (c, msg, code) => json(res, c, { error: msg, code }) })) return;
        if (handleConfig({ m, p, body, url, user, json: (c, b) => json(res, c, b), err: (c, msg, code, extra) => json(res, c, { error: msg, code, ...extra }) })) return;

        if (p === '/session') return json(res, 200, user);
        if (p === '/password') { if (body.current !== 'pass') return err(res, 400, 'current password is wrong', 'bad_credentials'); return json(res, 204); }
        if (p === '/events') {
          res.writeHead(200, { 'Content-Type': 'text/event-stream', 'Cache-Control': 'no-cache', Connection: 'keep-alive' });
          const c = { res, role: user.role };
          clients.add(c); req.on('close', () => clients.delete(c));
          sse(c, 'hello', { user, version: mockVersion, instance: mockInstance, settings: { favoritesScan: favScan } });
          sse(c, 'nodes', Object.keys(locals).map((id) => status(id, user.role)));
          if (user.role === 'admin') sse(c, 'updates', osBadge());
          sse(c, 'system', system()); sse(c, 'favorites', sortedFavs()); sse(c, 'schedules', sched.snapshot()); if (favScan) sse(c, 'favstats', favActivity());
          return;
        }
        if (p === '/public') {
          if (!isAdmin) return forbid();
          if (m === 'PUT') {
            for (const k of ['enabled', 'showCallsigns', 'showNetwork', 'showActivity', 'allowEmbedding', 'trustProxy'] as const) if (typeof body[k] === 'boolean') pubCfg[k] = body[k];
            if (typeof body.title === 'string') { if (body.title.length > 80) return err(res, 400, 'title is too long', 'bad_request'); pubCfg.title = body.title; }
            if (Array.isArray(body.nodes)) { if (body.nodes.some((n: string) => !locals[n])) return err(res, 400, 'not a local node', 'bad_request'); pubCfg.nodes = body.nodes.length === Object.keys(locals).length ? [] : body.nodes; }
            if (!pubCfg.enabled) { pubClients.forEach((r) => r.end()); pubClients.clear(); }
          }
          return json(res, 200, { ...pubCfg, nodes: [...pubCfg.nodes], defaultTitle: pubDefaultTitle(), localNodes: Object.keys(locals).map((id) => ({ node: id, info: info(id) })), clients: pubClients.size });
        }
        if (p === '/activity' && m === 'GET') return json(res, 200, [...activity].sort((a, b) => b.at - a.at).slice(0, +(url.searchParams.get('limit') ?? 50)).map((a, i) => ({ id: i, node: a.node, link: a.link, info: a.link === 'local' ? null : info(a.link), start: new Date(a.at).toISOString(), duration: a.duration, kerchunk: a.duration < 1.5 })));
        if (p === '/settings') {
          if (m === 'GET') return json(res, 200, { favoritesScan: favScan });
        }
        if (p === '/users' || p.startsWith('/users/')) {
          if (!isAdmin) return forbid();
          if (p === '/users' && m === 'GET') return json(res, 200, users);
          if (p === '/users' && m === 'POST') {
            if (!body.username || !body.password) return err(res, 400, 'username and password required', 'bad_request');
            if (users.some((u) => u.username === body.username)) return err(res, 409, 'user exists', 'conflict');
            const u = { id: nextUser++, username: body.username, role: body.role || 'viewer' }; users.push(u); return json(res, 201, u);
          }
          if ((mm = /^\/users\/(\d+)$/.exec(p))) {
            const u = users.find((x) => x.id === +mm![1]); if (!u) return err(res, 404, 'not found', 'not_found');
            if (m === 'PUT') { if (body.role) { if (u.role === 'admin' && body.role !== 'admin' && users.filter((x) => x.role === 'admin').length < 2) return err(res, 409, 'cannot demote the last admin', 'last_admin'); u.role = body.role; } return json(res, 200, u); }
            if (m === 'DELETE') { if (u.id === user.id) return err(res, 409, 'cannot delete yourself', 'conflict'); users = users.filter((x) => x !== u); return json(res, 204); }
          }
        }
        if (p === '/about' && m === 'GET') return json(res, 200, { version: mockVersion, asterisk: '22.10.1+asl3-3.10.8-1.deb13', platform: { os: 'linux', arch: 'arm64', model: 'Raspberry Pi 4 Model B Rev 1.5', distro: 'Debian GNU/Linux 13 (trixie)', kernel: '6.12.34+rpt-rpi-v8', hardware: 'Raspberry Pi 4' } });
        if (p === '/diagnostics' && m === 'GET') {
          if (!isAdmin) return forbid();
          res.statusCode = 200; res.setHeader('Content-Type', 'text/plain; charset=utf-8');
          res.setHeader('Content-Disposition', `attachment; filename="aslcp-diagnostics-${new Date().toISOString().slice(0, 10)}.txt"`);
          return res.end(`ASLCP diagnostics (mock)\nASLCP: ${mockVersion}\nPeer <ipv4>, secret = <redacted>\n`);
        }
        if (p === '/updates' || p.startsWith('/updates/')) {
          if (!isAdmin) return forbid();
          if (p === '/updates' && m === 'GET') return json(res, 200, updInfo());
          if (p === '/updates/check' && m === 'POST') { updChecked = new Date().toISOString(); return json(res, 200, updInfo()); }
          if (p === '/updates/channel' && m === 'PUT') {
            if (body.channel !== 'stable' && body.channel !== 'beta') return err(res, 400, 'channel must be "stable" or "beta"', 'bad_request');
            if (updJob === 'running') return err(res, 409, 'an update is already running', 'update_running');
            mockChannel = body.channel; updChecked = new Date().toISOString();
            return json(res, 200, updInfo());
          }
          if (p === '/updates/apply' && m === 'POST') {
            if (updJob === 'running') return err(res, 409, 'an update is already running', 'update_running');
            updJob = 'running';
            const target = candidate();
            setTimeout(() => { mockVersion = target; updJob = 'succeeded'; restartClients(); }, 6000);
            return json(res, 202, updInfo());
          }
        }
        if (p === '/updates/os/check' && m === 'POST') {
          if (!isAdmin) return forbid();
          if (osJob === 'running') return err(res, 409, 'an update is already running', 'update_running');
          osChecking = true; broadcast('updates', (r) => (r === 'admin' ? osBadge() : undefined));
          setTimeout(() => { osChecking = false; osChecked = new Date().toISOString(); broadcast('updates', (r) => (r === 'admin' ? osBadge() : undefined)); }, 2500);
          return json(res, 202, updInfo());
        }
        if (p === '/updates/os/apply' && m === 'POST') {
          if (!isAdmin) return forbid();
          if (osJob === 'running' || updJob === 'running') return err(res, 409, 'an update is already running', 'update_running');
          osJob = 'running';
          setTimeout(() => { osJob = 'succeeded'; osApplied = true; osReboot = true; osChecked = new Date().toISOString(); broadcast('updates', (r) => (r === 'admin' ? osBadge() : undefined)); }, 6000);
          return json(res, 202, updInfo());
        }
        if (p === '/system/reboot' && m === 'POST') {
          if (!isAdmin) return forbid();
          osReboot = false; broadcast('updates', (r) => (r === 'admin' ? osBadge() : undefined));
          setTimeout(() => restartClients(), 500);
          return json(res, 202, { ok: true });
        }
        if (p === '/system/restart-aslcp' && m === 'POST') { if (!isAdmin) return forbid(); setTimeout(() => { mockInstance = Math.random().toString(16).slice(2, 10); restartClients(); }, 500); return json(res, 202, { ok: true }); }
        if (p === '/system/restart-asterisk' && m === 'POST') { if (!isAdmin) return forbid(); return json(res, 202); }
        if ((mm = /^\/nodes\/([^/]+)\/stats$/.exec(p)) && m === 'GET') {
          const local = locals[mm[1]]; if (!local) return err(res, 404, 'no such local node', 'unknown_node');
          const t = now();
          const kv = (k: string, v: string) => ({ key: k, value: v });
          return json(res, 200, { node: mm[1], stats: [kv('IsRemoteBase', 'NO'), kv('NodeState', '0'), kv('SignalOnInput', local.rx > t ? 'YES' : 'NO'), kv('TransmitterKeyed', local.tx > t ? 'YES' : 'NO'), kv('Transmitter', 'ENABLED'), kv('Parrot', 'DISABLED'), kv('KeyupsToday', '12'), kv('KeyupsSinceSystemInitialization', '140'), kv('TxTimeToday', '00:04:12:340'), kv('NodesCurrentlyConnectedToUs', local.links.map((l) => l.node).join(', ') || '<NONE>'), kv('UserLinkingCommands', 'ENABLED')] });
        }
        if (p === '/nodes' && m === 'GET') return json(res, 200, Object.keys(locals).map((id) => status(id, user.role)));
        if ((mm = /^\/nodes\/([^/]+)\/(connect|disconnect|disconnect-all)$/.exec(p)) && m === 'POST') {
          if (!canOp) return forbid();
          const local = locals[mm[1]]; if (!local) return err(res, 404, 'unknown node', 'not_found');
          const push = pushNodes;
          // a manual action pauses or cancels schedules per the manual-action setting (reason like the server's)
          sched.manual(mm[1], mm[2] === 'disconnect-all' ? `manual disconnect all on ${mm[1]} by ${user.username}` : `manual ${mm[2]} ${body.target ?? ''} on ${mm[1]} by ${user.username}`);
          if (mm[2] === 'disconnect-all') { local.links = []; push(); return json(res, 202); }
          dvNodeLink(mm[1], String(body.target ?? ''), mm[2] === 'connect', (ev, d) => broadcast(ev, () => d));
          if (!/^\d{3,}$/.test(body.target ?? '')) return err(res, 400, 'invalid target node', 'bad_target');
          if (mm[2] === 'connect' && body.exclusive === true && (body.mode === 'transceive' || body.mode === 'monitor' || !body.mode)) {
            // like the server: drop every other direct link first (the mock has no permanent links)
            local.links = local.links.filter((l) => l.node === body.target);
          }
          if (mm[2] === 'connect') setTimeout(() => { if (!local.links.some((l) => l.node === body.target)) addLink(mm![1], body.target, body.mode, 'OUT'); push(); }, 1200);
          else { local.links = local.links.filter((l) => l.node !== body.target); push(); }
          return json(res, 202);
        }
        if (sched.handle(m, p, body, canOp, (c, b) => json(res, c, b), (c, msg, code) => err(res, c, msg, code), forbid)) return;
        if (p === '/favorites/stats' && m === 'GET') return json(res, 200, favActivity());
        if (p === '/favorites/scan' && m === 'PUT') {
          if (!canOp) return forbid();
          if (typeof body.enabled !== 'boolean') return err(res, 400, 'enabled (bool) is required', 'bad_request');
          favScan = body.enabled; if (!favScan) { favChecking = ''; for (const k of Object.keys(favCheckedAt)) delete favCheckedAt[k]; favNextAt = favPassStart = 0; } broadcast('favstats', favActivity); return json(res, 200, favActivity());
        }
        if (p === '/favorites/import/allscan') {
          if (m === 'GET') return json(res, 200, { found: true, path: '/var/www/html/allscan/favorites.ini', items: [{ node: '2000', label: 'Main hub' }, { node: '2296', label: 'Imported net' }, { node: '2333', label: 'Imported repeater' }] });
          if (!canOp) return forbid();
          let imported = 0, skipped = 0;
          for (const it of [{ node: '2000', label: 'Main hub' }, { node: '2296', label: 'Imported net' }, { node: '2333', label: 'Imported repeater' }]) {
            if (favs.some((f) => f.node === it.node)) skipped++; else { favs.push({ id: nextFav++, node: it.node, label: it.label, group: '', position: 99 }); imported++; }
          }
          broadcast('favorites', sortedFavs); return json(res, 200, { imported, skipped });
        }
        if (p === '/favorites/reorder' && m === 'POST') {
          if (!canOp) return forbid();
          (body.ids as number[]).forEach((id, i) => { const f = favs.find((x) => x.id === id); if (f) f.position = i; });
          // positions are global order here; sortedFavs sorts by group then position, which preserves intra-group order.
          broadcast('favorites', sortedFavs); return json(res, 204);
        }
        if (p === '/favorites') {
          if (m === 'GET') return json(res, 200, sortedFavs());
          if (!canOp) return forbid();
          if (m === 'POST') {
            if (!/^\d{3,}$/.test(body.node ?? '')) return err(res, 400, 'invalid node', 'bad_target');
            const i = info(body.node);
            const f = { id: nextFav++, node: body.node, label: body.label || (i ? `${i.callsign} ${i.description}` : body.node), group: body.group || '', position: 99 };
            favs.push(f); broadcast('favorites', sortedFavs); return json(res, 201, { ...f, info: i });
          }
        }
        if ((mm = /^\/favorites\/(\d+)$/.exec(p))) {
          if (!canOp) return forbid();
          const f = favs.find((x) => x.id === +mm![1]); if (!f) return err(res, 404, 'not found', 'not_found');
          if (m === 'PUT') { Object.assign(f, Object.fromEntries(Object.entries(body).filter(([k, v]) => ['node', 'label', 'group'].includes(k) && v !== undefined))); broadcast('favorites', sortedFavs); return json(res, 200, { ...f, info: info(f.node) }); }
          if (m === 'DELETE') { favs = favs.filter((x) => x !== f); broadcast('favorites', sortedFavs); return json(res, 204); }
        }
        if (p === '/lookup' && m === 'GET') {
          const q = (url.searchParams.get('q') ?? '').toLowerCase(), lim = +(url.searchParams.get('limit') ?? 50);
          return json(res, 200, NODES.filter((n) => n.node.startsWith(q) || [n.callsign, n.description, n.location].some((s) => s.toLowerCase().includes(q))).slice(0, lim));
        }
        if ((mm = /^\/lookup\/(.+)$/.exec(p))) { const i = info(decodeURIComponent(mm[1])); return i ? json(res, 200, i) : err(res, 404, 'node not found', 'not_found'); }
        if (p === '/nodelist') return json(res, 200, { count: NODES.length, updated: nodelistUpdated, source: 'mock://fake-node-list', error: null });
        if (p === '/nodelist/refresh' && m === 'POST') { if (!isAdmin) return forbid(); nodelistUpdated = new Date().toISOString(); return json(res, 202); }
        if (p === '/system') return json(res, 200, system());
        return err(res, 404, 'not found', 'not_found');
      });
    },
  };
}
