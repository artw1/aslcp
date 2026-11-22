// In-memory /api/dv/* for `npm run dev:mock` (plan 2.0 M1: the native engine aslcp-dv with md380-emu). Dev only.
// Placeholders only: callsign N0CALL, nodes 2001 and 1997, DMR ID 1234567, RFC 5737 addresses.
type Body = Record<string, unknown>;
const ENABLE_MS = 6000;
const ENABLE_LOG = ['Hit:1 http://deb.debian.org/debian bookworm InRelease', 'Get:2 https://packages.example.invalid/aslcp bookworm InRelease', 'key fingerprint matches the pinned one', 'Setting up md380-emu ...', 'Setting up aslcp-dv ...', 'previous digital voice services stopped and disabled', 'aslcp-dv started', 'enable finished'];

// The enable job (transient unit aslcp-dv-enable): idle until POST /enable, then 6 s of log lines, then done.
// Starts disabled with two legacy units and settings kept from a restore, so both notices can be seen.
let enabled = false, enableAt = 0, legacy = ['analog_bridge.service', 'mmdvm_bridge.service'], restorePending = true;
let jobFailed = false;
function jobState(): 'idle' | 'running' | 'done' | 'failed' {
  if (!enableAt) return 'idle';
  if (Date.now() - enableAt < ENABLE_MS) return 'running';
  if (jobFailed) return 'failed';
  if (!enabled) { enabled = true; legacy = []; restorePending = false; foundAfterRestore(); bootLastMode(); }
  return 'done';
}
function status() {
  const job = jobState();
  return { supported: true, md380Installed: enabled || job === 'running', enabled, active: enabled, md380Active: enabled, job, legacyUnits: legacy, restorePending };
}
function jobLog() {
  const job = jobState();
  if (job === 'idle') return { job, log: '' };
  const el = Date.now() - enableAt, n = job === 'running' ? Math.floor((el / ENABLE_MS) * ENABLE_LOG.length) : ENABLE_LOG.length;
  return { job, log: ENABLE_LOG.slice(0, n).join('\n'), at: Math.floor(enableAt / 1000) };
}

// Files whose version a private-node write is checked against (the engine config).
const FILES = ['aslcp_dv'];
const rev: Record<string, number> = {};
const hash = (id: string) => `mock${id.length}${(rev[id] ?? 0).toString(16).padStart(60, '0')}`;
function setup() {
  jobState();
  return { bridgeNode: bridgeSetup(), files: FILES.map((id) => ({ id, present: enabled, writable: enabled, sha256: enabled ? hash(id) : '', history: 0 })) };
}
const bad = (c: DVCtx, what: string) => c.err(400, `invalid_field: a value is not allowed (${what})`, 'invalid_field');

// Private bridge node (PUT /dv/private-node). Starts without one so "No bridge node yet" and the
// suggestion can be seen. Local nodes of the mock: 2001. Nothing here touches a real Asterisk.
const LOCALS = ['2001'];
const pb = { name: '', node: '', owned: false, rxPort: 34001, txPort: 32001, mode: 'ondemand', local: '', paused: false, linked: false, exclusive: true, adopt: false };
// the stock [USRP] ports that a removal puts back (the mock never recorded others)
const STOCK = { rx: 31001, tx: 31001 };
let pbUndo: { node: string; owned: boolean; rxPort: number; txPort: number; mode: string; local: string } | null = null;
const NODE_NAME = 'Digital modes linked node';
// The first install of a mock session finds a bridge node 1997 in rpt.conf that has no ASLCP record, like a
// restore of a backup from before 1.2.4: "Manage this bridge node with ASLCP" is offered. Later installs start clean.
let foundOnce = true;
function foundAfterRestore() {
  if (!foundOnce || pb.node) return;
  foundOnce = false;
  Object.assign(pb, { node: '1997', owned: false, adopt: true, rxPort: 34001, txPort: 32001 });
}
const bridgeSetup = () => ({
  node: pb.node, owned: pb.owned, adoptable: !!pb.node && !pb.owned && pb.adopt, suggested: pb.node ? '' : '1997', used: [...LOCALS, ...(pb.node ? [pb.node] : [])], localNodes: LOCALS,
  rxPort: pb.rxPort, txPort: pb.txPort, link: { mode: pb.mode, localNode: pb.local, bridge: pb.node, exclusive: pb.exclusive }, paused: pb.paused, name: pb.name || NODE_NAME,
});
const linkView = () => ({ mode: pb.mode, localNode: pb.local, linked: !!pb.node && (pb.mode === 'permanent' ? !pb.paused : pb.linked), paused: pb.paused, exclusive: pb.exclusive });
/** plugin.ts calls this for a node connect or disconnect so the card's link state follows the Link/Unlink node button. */
export function dvNodeLink(local: string, target: string, on: boolean, push?: DVCtx['push']) {
  if (!pb.node || local !== pb.local || target !== pb.node) return;
  pb.linked = on;
  if (pb.mode === 'permanent') pb.paused = !on;
  push?.('dv', snapshot());
}
function privateNode(c: DVCtx, body: Body): boolean {
  jobState();
  if (!enabled) { c.err(409, 'not_enabled: digital modes are not enabled', 'not_enabled'); return true; }
  const node = String(body.node ?? ''), rx = Number(body.rxPort), tx = Number(body.txPort), mode = String(body.linkMode ?? 'ondemand'), local = String(body.localNode ?? '');
  const exclusive = typeof body.linkExclusive === 'boolean' ? body.linkExclusive : pb.exclusive;
  if (!/^[0-9]{1,7}$/.test(node) || Number(node) < 1 || Number(node) >= 2000) { c.err(400, 'invalid_node: a private node is a number below 2000', 'invalid_node'); return true; }
  const renumber = body.renumber === true && pb.owned && !!pb.node && node !== pb.node;
  if (body.renumber === true && !pb.owned) { c.err(409, 'no_bridge_node: there is no bridge node added by ASLCP to renumber', 'no_bridge_node'); return true; }
  if (node !== pb.node && (LOCALS.includes(node) || (pb.node && !renumber))) { c.err(409, `node_in_use: node ${node} is already in use`, 'node_in_use'); return true; }
  if (!Number.isInteger(rx) || !Number.isInteger(tx) || rx < 1024 || tx < 1024 || rx > 65535 || tx > 65535 || rx === tx) { c.err(400, 'invalid_ports: check the ports', 'invalid_ports'); return true; }
  if (rx === 34002 || tx === 34002) { c.err(409, 'port_conflict: port 34002 is used by another channel', 'port_conflict'); return true; }
  if (!['ondemand', 'permanent'].includes(mode) || !LOCALS.includes(local)) { c.err(400, 'invalid_node: pick the link mode and a local node', 'invalid_node'); return true; }
  const asteriskChange = node !== pb.node;
  const diff = (t: string) => `--- a/${t}\n+++ b/${t}\n@@\n+[${node}](node-main)\n+rxchannel = USRP/127.0.0.1:${rx}:${tx}\n+duplex = 0\n+callerid = "Digital modes ${node}" <0000000000>\n+hangtime = 0\n+althangtime = 0\n+holdofftelem = 1\n+telemdefault = 0\n+telemdynamic = 0\n+nounkeyct = 1\n+linktolink = yes\n+idtime = 0\n+tailmessagetime = 0\n`;
  if (body.dryRun === true) {
    c.json(200, {
      node, rxPort: rx, txPort: tx, layout: 'template',
      files: [
        { path: '/etc/asterisk/rpt.conf', diff: asteriskChange ? diff('rpt.conf') : '', changed: asteriskChange },
        { path: '/etc/asterisk/modules.conf', diff: asteriskChange ? '--- a/modules.conf\n+++ b/modules.conf\n-noload = chan_usrp.so\n+load   = chan_usrp.so\n' : '', changed: asteriskChange },
        { path: '/etc/aslcp/dv.toml', diff: `--- a\n+++ b\n@@ [usrp]\n+rxPort = ${tx}\n+txPort = ${rx}\n`, changed: rx !== pb.rxPort || tx !== pb.txPort },
      ],
      kind: renumber ? 'renumber' : 'set', oldNode: renumber ? pb.node : '', unlinks: renumber && (pb.mode === 'permanent' || pb.linked) && pb.local ? [pb.local] : [], keepRecord: false,
      restartAsterisk: asteriskChange, restarts: [...(asteriskChange ? ['asterisk'] : []), 'aslcp-dv'], warnings: [], base: {}, link: { mode, localNode: local, bridge: node, exclusive },
    });
    return true;
  }
  if (asteriskChange && body.restart !== true) { c.err(409, 'restart_required: this change needs an Asterisk restart: send restart: true to confirm', 'restart_required'); return true; }
  pbUndo = { node: pb.node, owned: pb.owned, rxPort: pb.rxPort, txPort: pb.txPort, mode: pb.mode, local: pb.local };
  if (asteriskChange) pb.owned = true;
  Object.assign(pb, { node, rxPort: rx, txPort: tx, mode, local, exclusive, paused: false, linked: mode === 'permanent' });
  const steps = [{ name: 'asterisk_config', ok: asteriskChange }, { name: 'engine_config', ok: true }, { name: 'asterisk_restart', ok: asteriskChange }, { name: 'engine_reload', ok: true }, { name: 'health', ok: true }];
  c.json(200, { applied: true, kind: renumber ? 'renumber' : 'set', node, steps, warnings: [], keepRecord: false, health: { ok: true }, rollback: { available: true, before: asteriskChange, ini: true } });
  c.push?.('dv', snapshot());
  return true;
}

/** DELETE /dv/private-node: the plan (dryRun) or the removal of the bridge node ASLCP added. */
function removeNode(c: DVCtx, body: Body): boolean {
  jobState();
  if (!enabled) { c.err(409, 'not_enabled: digital modes are not enabled', 'not_enabled'); return true; }
  if (!pb.node) { c.err(409, 'no_bridge_node: there is no bridge node added by ASLCP to remove', 'no_bridge_node'); return true; }
  if (!pb.owned) { c.err(409, `not_owned: bridge node ${pb.node} was not added by ASLCP and is left alone`, 'not_owned'); return true; }
  const dry = body.dryRun === true || c.url?.searchParams.get('dryRun') === 'true';
  const confirmed = body.restart === true || c.url?.searchParams.get('restart') === 'true';
  const node = pb.node;
  if (dry) {
    c.json(200, {
      kind: 'remove', node, oldNode: '', unlinks: pb.local && (pb.mode === 'permanent' || pb.linked) ? [pb.local] : [], keepRecord: false,
      rxPort: pb.rxPort, txPort: pb.txPort, layout: 'template',
      files: [
        { path: '/etc/asterisk/rpt.conf', changed: true, diff: `--- a/rpt.conf\n+++ b/rpt.conf\n@@\n-[${node}](node-main)\n-rxchannel = USRP/127.0.0.1:${pb.rxPort}:${pb.txPort}\n-duplex = 0\n-${node} = radio@127.0.0.1/${node},NONE\n` },
        { path: '/etc/asterisk/modules.conf', changed: true, diff: '--- a/modules.conf\n+++ b/modules.conf\n-load   = chan_usrp.so\n+noload = chan_usrp.so\n' },
        { path: '/etc/aslcp/dv.toml', changed: true, diff: `--- a\n+++ b\n@@ [usrp]\n-rxPort = ${pb.txPort}\n-txPort = ${pb.rxPort}\n+rxPort = ${STOCK.tx}\n+txPort = ${STOCK.rx}\n` },
      ],
      restartAsterisk: true, restarts: ['asterisk', 'aslcp-dv'], warnings: [], base: {}, link: { mode: 'ondemand', localNode: '', bridge: '', exclusive: pb.exclusive },
    });
    return true;
  }
  if (!confirmed) { c.err(409, 'restart_required: this change needs an Asterisk restart: send restart: true to confirm', 'restart_required'); return true; }
  pbUndo = { node: pb.node, owned: pb.owned, rxPort: pb.rxPort, txPort: pb.txPort, mode: pb.mode, local: pb.local };
  Object.assign(pb, { name: '', node: '', owned: false, rxPort: STOCK.rx, txPort: STOCK.tx, mode: 'ondemand', local: '', paused: false, linked: false });
  const steps = [{ name: 'disconnect', ok: true }, { name: 'asterisk_config', ok: true }, { name: 'engine_config', ok: true }, { name: 'asterisk_restart', ok: true }, { name: 'engine_reload', ok: true }];
  c.json(200, { applied: true, kind: 'remove', node, steps, warnings: [], keepRecord: false, rollback: { available: true, before: true, ini: true } });
  c.push?.('dv', snapshot());
  return true;
}

// Remembered tunes (GET /dv/resume). Only the parrot exists in M1.
let resume = false, lastMode = 'LOOP';
const remembered: Record<string, { tune: string; ts?: string; at: string }> = {};
const resumeView = () => ({ resume, lastMode, perMode: remembered });

export interface DVCtx {
  /** Pushes an SSE event to every client (the dashboard card's `dv` snapshot). */
  push?: (ev: string, data: unknown) => void;
  m: string; p: string; url?: URL; body?: Body; user?: { username: string; role: string };
  json: (code: number, body?: unknown) => void; err: (code: number, msg: string, c: string) => void;
}

// Placeholder host and TG lists (RFC 5737 addresses, made-up names) for GET /dv/lists.
const LISTS: Record<string, { value: string; name: string; detail?: string; address: string; port: number }[]> = {
  ysf: [
    { value: '00001', name: 'US-Example', detail: 'Example reflector', address: '192.0.2.21', port: 42000 },
    { value: '00002', name: 'XX-Europe-Example', detail: 'Example link', address: '198.51.100.22', port: 42000 },
    { value: '00003', name: 'XX-World-Example', detail: 'Example world link', address: '203.0.113.23', port: 42500 },
  ],
  p25: [
    { value: '10100', name: 'P25 TG 10100', address: '192.0.2.31', port: 41000 },
    { value: '10200', name: 'P25 TG 10200', address: '198.51.100.32', port: 41000 },
    { value: '31100', name: 'P25 TG 31100', address: '203.0.113.33', port: 41000 },
  ],
  nxdn: [
    { value: '10', name: 'NXDN TG 10', address: '192.0.2.41', port: 41400 },
    { value: '20', name: 'NXDN TG 20', address: '198.51.100.42', port: 41400 },
    { value: '65', name: 'NXDN TG 65', address: '203.0.113.43', port: 41400 },
  ],
};

// Dashboard panel (GET /dv/snapshot, /mode, /unlink, /tune, favourites). Only LOOP (the parrot, shown as
// "Parrot (test)") is available in M1; every other mode answers 409 not_available.
type PMode = 'LOOP' | 'BM' | 'TGIF' | 'DMR' | 'YSF' | 'P25' | 'NXDN';
const ALL: PMode[] = ['LOOP', 'BM', 'TGIF', 'DMR', 'YSF', 'P25', 'NXDN'];
const UNLINK: Record<PMode, string> = { LOOP: '', BM: '4000', TGIF: '4000', DMR: '4000', YSF: 'disconnect', P25: '9999', NXDN: '9999' };
const LABEL: Partial<Record<PMode, string>> = { LOOP: 'Parrot (test)', BM: 'BrandMeister', TGIF: 'TGIF', DMR: 'DMR - Other' };
const bridge: { mode: PMode; tune: string; slot: number; muted: boolean } = { mode: 'LOOP', tune: '', slot: 2, muted: false };
const started = Date.now();
function bootLastMode() { bridge.mode = 'LOOP'; bridge.tune = ''; }
// Placeholder talkers: the parrot plays back whoever last keyed.
const TALKERS = [{ call: 'N0CALL', dmrId: '1234567' }, { call: 'N0DEMO', dmrId: '1234568' }, { call: 'N0TEST', dmrId: '1234569' }];
function callers() {
  const n = Math.floor(Date.now() / 30_000);
  const at = (k: number) => new Date(Date.now() - k * 30_000).toISOString().slice(0, 19).replace('T', ' ');
  const heard = [0, 1, 2].map((k) => ({ ...TALKERS[(n - k + 3 * 100) % 3], dst: 'parrot', slot: 2, at: at(k) }));
  return { caller: heard[0], heard };
}
function snapshot() {
  jobState();
  const running = enabled;
  return {
    installed: enabled, running, configured: running,
    bridge: running
      ? { mode: bridge.mode, tune: bridge.tune, slot: bridge.slot, muted: bridge.muted, source: 'aslcp-dv', updated: new Date().toISOString() }
      : { mode: '', tune: '', slot: 0, muted: false, source: 'none', updated: '' },
    modes: ALL.map((id) => ({ id, enabled: id === 'LOOP' && running, available: id === 'LOOP', unlink: UNLINK[id], ...(LABEL[id] ? { label: LABEL[id] } : {}) })),
    services: enabled ? [{ unit: 'aslcp-dv', active: true }, { unit: 'md380-emu', active: true }] : [],
    ...(running ? { vocoder: { ok: true, timeouts: 0 }, engine: { version: '2.0.0-m1', uptime: Math.floor((Date.now() - started) / 1000) } } : {}),
    bridgeNode: pb.node ? { present: true, node: pb.node, name: pb.name || NODE_NAME } : { present: false },
    link: linkView(),
    ...(running ? callers() : { heard: [] }),
  };
}
const tuneRe: Record<PMode, RegExp> = { LOOP: /^$/, BM: /^[0-9]{1,8}#?$/, TGIF: /^[0-9]{1,8}#?$/, DMR: /^[0-9]{1,8}#?$/, YSF: /^(disconnect|[A-Za-z0-9._-]{1,32}|[A-Za-z0-9.-]{1,253}:[0-9]{1,5})$/, P25: /^[0-9]{1,5}$/, NXDN: /^[0-9]{1,5}$/ };
const favs: { id: number; mode: PMode; value: string; name: string; sort: number }[] = [];
let favSeq = 0;
const favView = (f: (typeof favs)[number]) => ({ id: f.id, mode: f.mode, value: f.value, name: f.name, sort: f.sort });
function panelHandler(c: DVCtx): boolean {
  const op = c.user!.role !== 'viewer';
  const forbid = () => (c.err(403, 'insufficient permissions', 'forbidden'), true);
  const reply = (mode: PMode, switched: boolean) => c.json(200, { mode, switched, confirmed: true, snapshot: snapshot() });
  if (c.m === 'GET' && (c.p === '/dv/snapshot' || c.p === '/dv')) { c.json(200, snapshot()); return true; }
  if (c.m === 'GET' && c.p === '/dv/favorites') { c.json(200, favs.map(favView)); return true; }
  if (!op) return forbid();
  const body = (c.body ?? {}) as Record<string, unknown>;
  const running = () => snapshot().running;
  const unavailable = (m: PMode) => c.err(409, `not_available: ${m} is not yet available in this build`, 'not_available');
  if (c.m === 'POST' && c.p === '/dv/mode') {
    const m = String(body.mode ?? '').toUpperCase() as PMode;
    if (!ALL.includes(m)) return c.err(400, 'mode must be LOOP, BM, TGIF, DMR, YSF, P25 or NXDN', 'bad_request'), true;
    if (!running()) return c.err(409, 'not_enabled: digital modes are not enabled', 'not_enabled'), true;
    if (m !== 'LOOP') return unavailable(m), true;
    const switched = bridge.mode !== m;
    bridge.mode = m; lastMode = m;
    reply(m, switched);
    c.push?.('dv', snapshot());
    return true;
  }
  if (c.m === 'POST' && c.p === '/dv/unlink') {
    if (!running()) return c.err(409, 'not_enabled: digital modes are not enabled', 'not_enabled'), true;
    bridge.tune = UNLINK[bridge.mode];
    reply(bridge.mode, false);
    c.push?.('dv', snapshot());
    return true;
  }
  if (c.m === 'POST' && c.p === '/dv/tune') {
    const mode = String(body.mode ?? '').toUpperCase() as PMode, tune = body.tune;
    if (!(mode in tuneRe) || typeof tune !== 'string' || !tuneRe[mode].test(tune)) return bad(c, 'tune'), true;
    if (!running()) return c.err(409, 'not_enabled: digital modes are not enabled', 'not_enabled'), true;
    if (mode !== 'LOOP') return unavailable(mode), true;
    const switched = bridge.mode !== mode;
    bridge.mode = mode; bridge.tune = ''; lastMode = mode;
    reply(mode, switched);
    c.push?.('dv', snapshot());
    return true;
  }
  if (c.m === 'POST' && c.p === '/dv/favorites') {
    const mode = String(body.mode ?? '').toUpperCase() as PMode, value = typeof body.value === 'string' ? body.value : '', name = typeof body.name === 'string' ? body.name.trim() : '';
    if (!(mode in tuneRe) || !tuneRe[mode].test(value) || [...name].length < 1 || [...name].length > 40) return c.err(400, 'bad_request: check the mode, the value and the name (1 to 40 characters)', 'bad_request'), true;
    if (favs.some((f) => f.mode === mode && f.value === value)) return c.err(409, 'duplicate: this target is already a favourite', 'duplicate'), true;
    const f = { id: ++favSeq, mode, value, name, sort: favs.filter((x) => x.mode === mode).length };
    favs.push(f);
    c.json(201, favView(f));
    return true;
  }
  const m = /^\/dv\/favorites\/(\d+)$/.exec(c.p);
  if (m) {
    const i = favs.findIndex((f) => f.id === Number(m[1]));
    if (i < 0) return c.err(404, 'not_found: no such favourite', 'not_found'), true;
    if (c.m === 'DELETE') { favs.splice(i, 1); c.json(204); return true; }
    if (c.m === 'PUT') {
      if (body.name !== undefined) { const n = typeof body.name === 'string' ? body.name.trim() : ''; if ([...n].length < 1 || [...n].length > 40) return c.err(400, 'bad_request: the name must be 1 to 40 characters', 'bad_request'), true; favs[i].name = n; }
      if (typeof body.sort === 'number') favs[i].sort = body.sort;
      c.json(200, favView(favs[i]));
      return true;
    }
  }
  return false;
}

export function handleDV(c: DVCtx): boolean {
  if (!c.p.startsWith('/dv')) return false;
  if (!c.user) { c.err(401, 'authentication required', 'unauthorized'); return true; }
  const admin = c.user.role === 'admin', viewer = c.user.role === 'viewer';
  const deny = () => (c.err(403, 'insufficient permissions', 'forbidden'), true);
  if (c.m === 'GET' && c.p === '/dv/lists') {
    if (viewer) return deny();
    const mode = c.url?.searchParams.get('mode') ?? '';
    if (!(mode in LISTS)) { c.err(404, 'no_list: no list for this mode', 'no_list'); return true; }
    c.json(200, { mode, items: LISTS[mode], etag: 'mock' });
    return true;
  }
  if (c.p === '/dv/resume' && c.m === 'GET') { if (viewer) return deny(); c.json(200, resumeView()); return true; }
  if (c.p === '/dv/resume' && c.m === 'PUT') {
    if (!admin) return deny();
    const r = (c.body ?? {}).resume;
    if (typeof r !== 'boolean') { c.err(400, 'resume must be true or false', 'bad_request'); return true; }
    resume = r;
    c.json(200, resumeView());
    return true;
  }
  if (c.p === '/dv/private-node/name' && c.m === 'PUT') {
    if (!admin) return deny();
    const n = (c.body ?? {}).name;
    if (typeof n !== 'string' || [...n.trim()].length > 40) { c.err(400, 'the name must be 1 to 40 printable characters, or empty for the default', 'bad_request'); return true; }
    pb.name = n.trim();
    c.json(200, { name: pb.name || NODE_NAME });
    c.push?.('dv', snapshot());
    return true;
  }
  if (c.p === '/dv/link' && c.m === 'PUT') {
    if (viewer) return deny();
    const x = (c.body ?? {}).exclusive;
    if (typeof x !== 'boolean') { c.err(400, 'exclusive is required', 'bad_request'); return true; }
    pb.exclusive = x;
    c.json(200, { exclusive: x });
    return true;
  }
  if (panelHandler(c)) return true;
  if (c.p === '/dv/private-node/adopt' && c.m === 'POST') {
    if (!admin) return deny();
    jobState();
    if (!enabled) { c.err(409, 'not_enabled: digital modes are not enabled', 'not_enabled'); return true; }
    if (pb.owned) { c.err(409, `already_managed: ASLCP already manages the bridge node ${pb.node}`, 'already_managed'); return true; }
    if (!pb.node) { c.err(409, 'no_bridge_node: there is no bridge node in rpt.conf to take over', 'no_bridge_node'); return true; }
    if (!pb.adopt) { c.err(409, `not_adoptable: node ${pb.node} does not look like the node ASLCP adds`, 'not_adoptable'); return true; }
    pb.owned = true; pb.adopt = false;
    c.json(200, { adopted: true, node: pb.node });
    c.push?.('dv', snapshot());
    return true;
  }
  if (c.p === '/dv/private-node' || c.p === '/dv/private-node/rollback') {
    if (!admin) return deny();
    if (c.m === 'PUT' && c.p === '/dv/private-node') return privateNode(c, c.body ?? {});
    if (c.m === 'DELETE' && c.p === '/dv/private-node') return removeNode(c, c.body ?? {});
    if (c.m === 'POST' && c.p === '/dv/private-node/rollback') {
      if (!pbUndo) { c.err(404, 'no_rollback: there is no change to roll back', 'no_rollback'); return true; }
      Object.assign(pb, pbUndo, { paused: false, linked: false }); pbUndo = null;
      c.json(200, { rolledBack: true, problems: [], steps: [{ name: 'asterisk_config', ok: true }, { name: 'engine_config', ok: true }] });
      c.push?.('dv', snapshot());
      return true;
    }
  }
  if (!admin) return deny();
  if (c.m === 'GET' && c.p === '/dv/status') { c.json(200, status()); return true; }
  if (c.m === 'GET' && c.p === '/dv/job') { c.json(200, jobLog()); return true; }
  if (c.m === 'GET' && c.p === '/dv/setup') { c.json(200, setup()); return true; }
  if (c.m === 'DELETE' && c.p === '/dv/restore-pending') { restorePending = false; c.json(200, status()); return true; }
  if (c.m === 'POST' && c.p === '/dv/enable') {
    if (jobState() === 'running') c.err(409, 'job_running: the enable job is already running', 'job_running');
    else if (enabled) c.err(409, 'already_enabled: digital modes are already enabled', 'already_enabled');
    else { enableAt = Date.now(); jobFailed = false; c.json(202, status()); setTimeout(() => c.push?.('dv', snapshot()), ENABLE_MS + 200); }
    return true;
  }
  if (c.m === 'POST' && c.p === '/dv/disable') {
    if (jobState() === 'running') c.err(409, 'job_running: the enable job is running', 'job_running');
    else {
      enabled = false; enableAt = 0;
      Object.assign(pb, { name: '', node: '', owned: false, mode: 'ondemand', local: '', paused: false, linked: false }); pbUndo = null;
      c.json(200, status());
      c.push?.('dv', snapshot());
    }
    return true;
  }
  return false;
}
