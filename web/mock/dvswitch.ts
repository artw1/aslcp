// In-memory /api/dvswitch/* for `npm run dev:mock` (docs/API.md "DVSwitch"). Dev only.
const PKGS = ['dvswitch-base', 'analog-bridge', 'mmdvm-bridge', 'md380-emu', 'ysfgateway', 'p25gateway', 'nxdngateway', 'wget'];
const UNITS = ['analog_bridge', 'mmdvm_bridge', 'netcheck', 'ysfgateway', 'p25gateway', 'nxdngateway', 'md380-emu'];
const PATHS = ['/var/lib/mmdvm', '/var/lib/dvswitch', '/var/log/dvswitch', '/var/log/mmdvm', '/opt/Analog_Bridge', '/opt/MMDVM_Bridge', '/etc/cron.d/dvswitch', '/etc/cron.daily/dvswitch-log-cleanup', '/tmp/ABInfo_*.json', '/etc/apt/sources.list.d/dvswitch.list', '/usr/share/keyrings/dvswitch-keyring.gpg'];
const FP = '6DDFC33CD5FC678659642DF572147EC1E788D4C3';
const DURATION = 6000;

let installed = false;
// Starts with an old failed install (3 weeks ago) so the collapsed, failed summary can be seen; start a job to see the expanded log.
let job: { kind: 'install' | 'uninstall'; at: number; old?: boolean } | null = { kind: 'install', at: Date.now() - 21 * 86400e3, old: true };
const LOG = {
  install: ['Hit:1 http://deb.debian.org/debian bookworm InRelease', 'Get:2 https://dvswitch.org/apt bookworm InRelease', 'recorded 9 newly installed packages: analog-bridge dvswitch-base md380-emu mmdvm-bridge nxdngateway p25gateway qemu-user-static wget ysfgateway', 'Setting up dvswitch-base ...', 'Setting up analog-bridge ...', 'host files ok', 'dvswitch install finished'],
  uninstall: ['purge plan: analog-bridge dvswitch-base md380-emu mmdvm-bridge nxdngateway p25gateway qemu-user-static ysfgateway', 'Stopping analog_bridge.service ...', 'Removing analog-bridge ...', 'Purging configuration files for dvswitch-base ...', 'dvswitch uninstall finished'],
};

function progress() {
  const idle = { job: 'idle', log: '' };
  if (!job) return { install: idle, uninstall: idle };
  const lines = LOG[job.kind], el = Date.now() - job.at;
  let cur;
  const at = Math.floor(job.at / 1000);
  if (job.old) cur = { job: 'failed', log: LOG.install.slice(0, 3).join('\n'), at };
  else if (el >= DURATION) { installed = job.kind === 'install'; cur = { job: 'succeeded', log: lines.join('\n'), at: Math.floor((job.at + DURATION) / 1000) }; }
  else cur = { job: 'running', log: lines.slice(0, Math.floor((el / DURATION) * lines.length)).join('\n'), at };
  return job.kind === 'install' ? { install: cur, uninstall: idle } : { install: idle, uninstall: cur };
}

// DMR login simulation: each (re)start of mmdvm_bridge takes 4 s, then the master answers with the next
// outcome of the cycle. The fifth leaves the unit stopped (a crash), so the Start button can be tried.
const DMR_CYCLE = [
  { state: 'logged_in', line: 'DMR, Logged into the master successfully: 192.0.2.10:62031' },
  { state: 'login_failed', line: 'DMR, Login to the master has failed, retrying login ...' },
  { state: 'timed_out', line: 'DMR, Connection to the master has timed out, retrying connection' },
  { state: 'socket_failed', line: 'DMR, Socket has failed, retrying connection to the master' },
  { state: 'not_running', line: '' },
];
let dmrRuns = 0, dmrAt = 0, dmrDown = false;
function dmrStatus() {
  if (!installed) return { state: 'not_installed' };
  if (!cfg.modes.dmr) return { state: 'disabled' };
  const o = DMR_CYCLE[dmrRuns % DMR_CYCLE.length], since = dmrAt || Date.now() - 3.6e6;
  if (Date.now() - since < 4000) return { state: 'connecting', since: new Date(since).toISOString() };
  if (o.state === 'not_running') { dmrDown = true; return { state: 'not_running', since: new Date(since).toISOString() }; }
  return { state: o.state, since: new Date(since).toISOString(), at: new Date(since + 4000).toISOString(), lastLine: o.line, ...(o.state === 'logged_in' ? { master: '192.0.2.10:62031' } : {}) };
}

function status() {
  const j = progress(), running = j.install.job === 'running' || j.uninstall.job === 'running';
  return {
    installed, installedBy: installed ? 'aslcp' : '', version: installed ? '1.7.0' : '', arch: 'arm64', osCodename: 'bookworm', supported: true,
    repoPresent: installed, keyPresent: installed, keyOk: installed, ...(installed ? { keyFingerprint: FP } : {}),
    packages: PKGS.map((name) => ({ name, installed, ...(installed ? { version: '1.7.0' } : {}) })),
    units: UNITS.map((name) => ({ name, loaded: installed, active: installed && !(name === 'mmdvm_bridge' && dmrDown) ? 'active' : 'inactive', enabled: installed ? 'enabled' : '' })),
    hostFiles: ['DMRIds.dat', 'YSFHosts.txt', 'P25Hosts.txt', 'NXDNHosts.txt'].map((name) => ({ name, present: installed, size: installed ? 4096 : 0 })), hostFilesOk: installed,
    marker: installed ? { version: 1, installedAt: new Date().toISOString(), wgetPreinstalled: false, packages: ['qemu-user-static', 'wget'] } : null,
    canInstall: !installed && !running, canUninstall: installed && !running,
    uninstallPackages: installed ? [...PKGS, 'qemu-user-static'] : [], uninstallPaths: PATHS, uninstallUnits: UNITS, ...j,
  };
}

// M4 setup: in-memory ini values. The password is never stored, only whether one is set.
const FILES = ['analog_bridge', 'mmdvm_bridge', 'ysfgateway', 'p25gateway', 'nxdngateway'];
const UNIT_OF: Record<string, string> = { analog_bridge: 'analog_bridge', mmdvm_bridge: 'mmdvm_bridge', ysfgateway: 'ysfgateway', p25gateway: 'p25gateway', nxdngateway: 'nxdngateway' };
const cfg = {
  identity: { callsign: 'N0CALL', dmrId: '1234567', essid: '01', nxdnId: '12346', consistent: true },
  dmr: { address: '192.0.2.10', port: 62031, passwordSet: false },
  usrp: { address: '127.0.0.1', rxPort: 34001, txPort: 32001 },
  // DMR, YSF and P25 are enabled so the dashboard card's mode buttons can be tried; NXDN stays off (greyed out).
  modes: { dmr: true, ysf: true, p25: true, nxdn: false },
  startup: { ambeMode: 'DMR', txTg: '9', txTs: 2, colorCode: 1 },
  gateways: { ysf: { present: true, startup: 'US-Example' }, p25: { present: true, startup: '' }, nxdn: { present: true, startup: '' } },
};
const rev: Record<string, number> = {}, hist: Record<string, number> = {};
const hash = (id: string) => `mock${id.length}${(rev[id] ?? 0).toString(16).padStart(60, '0')}`;
function setup(extra: Record<string, unknown> = {}) {
  return {
    ...JSON.parse(JSON.stringify(cfg)),
    bridgeNode: bridgeSetup(),
    warnings: cfg.modes.nxdn && !cfg.identity.nxdnId ? ['NXDN needs your NXDN ID from radioid.net'] : [],
    files: FILES.map((id) => ({ id, present: installed, writable: installed, sha256: installed ? hash(id) : '', history: hist[id] ?? 0 })),
    current: { found: installed, version: '1.6.4', mode: cfg.startup.ambeMode, tg: cfg.startup.txTg, slot: String(cfg.startup.txTs), colorCode: String(cfg.startup.colorCode), tune: '', mute: 'OFF', gatewayId: cfg.identity.dmrId, repeaterId: cfg.identity.dmrId + cfg.identity.essid, updated: new Date().toISOString() },
    ...extra,
  };
}
const stale = (files: string[], base: Record<string, string> | undefined) => files.some((f) => base?.[f] && base[f] !== hash(f));
function touch(files: string[]) { for (const f of files) { rev[f] = (rev[f] ?? 0) + 1; hist[f] = Math.min((hist[f] ?? 0) + 1, 10); } }
const bad = (c: DVCtx, what: string) => c.err(400, `invalid_field: a value is not allowed (${what})`, 'invalid_field');
type Body = Record<string, unknown>;

function write(c: DVCtx, body: Body): boolean {
  progress();
  if (!installed) { c.err(409, 'not_installed: DVSwitch is not installed (its ini files are missing)', 'not_installed'); return true; }
  const base = body.base as Record<string, string> | undefined;
  const touched = c.p === '/dvswitch/identity' ? FILES : c.p === '/dvswitch/credentials' || c.p === '/dvswitch/modes' ? ['mmdvm_bridge'] : c.p === '/dvswitch/startup' ? ['analog_bridge'] : c.p === '/dvswitch/rollback' ? [String(body.file ?? '')] : [];
  if (stale(touched, base)) { c.err(409, 'a file changed on disk since it was read: reload and try again', 'conflict'); return true; }
  const done = (files: string[], hint: string[]) => { touch(files); c.json(200, setup({ changed: files, skipped: [], restartHint: hint })); };
  if (c.p === '/dvswitch/identity') {
    const { callsign, dmrId, essid, nxdnId } = body as { callsign: string; dmrId: string; essid: string; nxdnId?: string };
    if (!/^[A-Z0-9/]{3,10}$/.test(callsign ?? '')) return bad(c, 'identity/callsign'), true;
    if (!/^[0-9]{7}$/.test(dmrId ?? '')) return bad(c, 'identity/dmrId'), true;
    if (!/^[0-9]{2}$/.test(essid ?? '')) return bad(c, 'identity/essid'), true;
    if (nxdnId) { const n = Number(nxdnId); if (!/^[0-9]{1,5}$/.test(nxdnId) || nxdnId[0] === '0' || n > 65519) return bad(c, 'identity/nxdnId'), true; }
    cfg.identity = { callsign, dmrId, essid, nxdnId: nxdnId || (cfg.identity.nxdnId === '12345' ? '' : cfg.identity.nxdnId), consistent: true };
    done(FILES, FILES.map((f) => UNIT_OF[f]));
  } else if (c.p === '/dvswitch/credentials') {
    const { dmrAddress, dmrPort, dmrPassword, clearPassword } = body as { dmrAddress?: string; dmrPort?: number; dmrPassword?: string; clearPassword?: boolean };
    if (dmrAddress === undefined && dmrPort === undefined && dmrPassword === undefined && !clearPassword) return bad(c, 'credentials'), true;
    if (dmrAddress !== undefined && !/^[A-Za-z0-9.-]{1,253}$/.test(dmrAddress)) return bad(c, 'DMR Network/Address'), true;
    if (dmrPort !== undefined && !(Number.isInteger(dmrPort) && dmrPort >= 1 && dmrPort <= 65535)) return bad(c, 'DMR Network/Port'), true;
    if (dmrPassword !== undefined && !/^[\x21\x24-\x3a\x3c-\x7e]{1,64}$/.test(dmrPassword)) return bad(c, 'DMR Network/Password'), true;
    if (clearPassword && dmrPassword !== undefined) return bad(c, 'DMR Network/Password'), true;
    if (dmrAddress !== undefined) cfg.dmr.address = dmrAddress;
    if (dmrPort !== undefined) cfg.dmr.port = dmrPort;
    if (dmrPassword !== undefined) cfg.dmr.passwordSet = true;
    if (clearPassword) cfg.dmr.passwordSet = false;
    done(['mmdvm_bridge'], ['mmdvm_bridge']);
  } else if (c.p === '/dvswitch/modes') {
    const m = body as Record<string, unknown>; let any = false;
    for (const k of ['dmr', 'ysf', 'p25', 'nxdn'] as const) if (typeof m[k] === 'boolean') { cfg.modes[k] = m[k] as boolean; any = true; }
    if (!any) return bad(c, 'modes'), true;
    done(['mmdvm_bridge'], ['analog_bridge', 'mmdvm_bridge']);
  } else if (c.p === '/dvswitch/startup') {
    const b = body as { ambeMode?: string; txTg?: string; txTs?: number; colorCode?: number; ysf?: string; p25?: string; nxdn?: string };
    if (b.ambeMode !== undefined) { if (!['DMR', 'YSFN', 'YSFW', 'P25', 'NXDN'].includes(b.ambeMode)) return bad(c, 'AMBE_AUDIO/ambeMode'), true; cfg.startup.ambeMode = b.ambeMode; }
    if (b.txTg !== undefined) { if (!/^[0-9]{1,8}$/.test(b.txTg)) return bad(c, 'AMBE_AUDIO/txTg'), true; cfg.startup.txTg = b.txTg; }
    if (b.txTs !== undefined) { if (b.txTs !== 1 && b.txTs !== 2) return bad(c, 'AMBE_AUDIO/txTs'), true; cfg.startup.txTs = b.txTs; }
    if (b.colorCode !== undefined) { if (!Number.isInteger(b.colorCode) || b.colorCode < 0 || b.colorCode > 15) return bad(c, 'AMBE_AUDIO/colorCode'), true; cfg.startup.colorCode = b.colorCode; }
    if (b.ysf !== undefined) cfg.gateways.ysf.startup = b.ysf;
    if (b.p25 !== undefined) cfg.gateways.p25.startup = b.p25;
    if (b.nxdn !== undefined) cfg.gateways.nxdn.startup = b.nxdn;
    done(['analog_bridge'], ['analog_bridge']);
  } else if (c.p === '/dvswitch/rollback') {
    const f = String(body.file ?? '');
    if (!FILES.includes(f)) return bad(c, 'file'), true;
    if (!hist[f]) { c.err(404, 'no_history: there is no earlier version of this file', 'no_history'); return true; }
    hist[f]--; rev[f] = (rev[f] ?? 0) + 1;
    c.json(200, setup({ changed: [f], skipped: [], restartHint: [UNIT_OF[f]] }));
  } else if (c.p === '/dvswitch/restart') {
    const scope = String(body.scope ?? ''), units = ({ dmr: ['mmdvm_bridge'], bridge: ['analog_bridge', 'mmdvm_bridge'], gateways: ['ysfgateway', 'p25gateway', 'nxdngateway'], all: ['analog_bridge', 'mmdvm_bridge', 'ysfgateway', 'p25gateway', 'nxdngateway'] } as Record<string, string[]>)[scope];
    if (!units) return bad(c, 'scope'), true;
    if (units.includes('mmdvm_bridge')) { dmrRuns++; dmrAt = Date.now(); dmrDown = false; }
    c.json(200, { action: body.action === 'start' ? 'start' : 'restart', units, skipped: [], states: status().units, warning: 'A restart resets the running mode and talkgroup to the startup values in the ini files.' });
  }
  return true;
}

// Private bridge node (PUT /dvswitch/private-node). Starts without one so "No bridge node yet" and the
// suggestion can be seen. Local nodes of the mock: 2001. Nothing here touches a real Asterisk.
const LOCALS = ['2001'];
const pb = { node: '', owned: false, rxPort: 34001, txPort: 32001, mode: 'ondemand', local: '', paused: false, linked: false, exclusive: true };
// the stock [USRP] ports that a removal puts back (the mock never recorded others)
const STOCK = { rx: 31001, tx: 31001 };
let pbUndo: { node: string; owned: boolean; rxPort: number; txPort: number; mode: string; local: string } | null = null;
const bridgeSetup = () => ({
  node: pb.node, owned: pb.owned, suggested: pb.node ? '' : '1900', used: [...LOCALS, ...(pb.node ? [pb.node] : [])], localNodes: LOCALS,
  rxPort: pb.rxPort, txPort: pb.txPort, link: { mode: pb.mode, localNode: pb.local, bridge: pb.node, exclusive: pb.exclusive }, paused: pb.paused,
});
const linkView = () => ({ mode: pb.mode, localNode: pb.local, linked: !!pb.node && (pb.mode === 'permanent' ? !pb.paused : pb.linked), paused: pb.paused, exclusive: pb.exclusive });
/** plugin.ts calls this for a node connect or disconnect so the card's link state follows the Link/Unlink node button. */
export function dvNodeLink(local: string, target: string, on: boolean, push?: DVCtx['push']) {
  if (!pb.node || local !== pb.local || target !== pb.node) return;
  pb.linked = on;
  if (pb.mode === 'permanent') pb.paused = !on;
  push?.('dvswitch', snapshot());
}
function privateNode(c: DVCtx, body: Body): boolean {
  progress();
  if (!installed) { c.err(409, 'not_installed: DVSwitch is not installed (its ini files are missing)', 'not_installed'); return true; }
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
  const diff = (t: string) => `--- a/${t}\n+++ b/${t}\n@@\n+[${node}](node-main)\n+rxchannel = USRP/127.0.0.1:${rx}:${tx}\n+duplex = 0\n+callerid = "DVSwitch ${node}" <0000000000>\n+hangtime = 0\n+althangtime = 0\n+holdofftelem = 1\n+telemdefault = 0\n+telemdynamic = 0\n+nounkeyct = 1\n+linktolink = yes\n+idtime = 0\n+tailmessagetime = 0\n`;
  if (body.dryRun === true) {
    c.json(200, {
      node, rxPort: rx, txPort: tx, layout: 'template',
      files: [
        { path: '/etc/asterisk/rpt.conf', diff: asteriskChange ? diff('rpt.conf') : '', changed: asteriskChange },
        { path: '/etc/asterisk/modules.conf', diff: asteriskChange ? '--- a/modules.conf\n+++ b/modules.conf\n-noload = chan_usrp.so\n+load   = chan_usrp.so\n' : '', changed: asteriskChange },
        { path: '/opt/Analog_Bridge/Analog_Bridge.ini', diff: `--- a\n+++ b\n@@ [USRP]\n+rxPort = ${tx}\n+txPort = ${rx}\n`, changed: rx !== pb.rxPort || tx !== pb.txPort },
      ],
      kind: renumber ? 'renumber' : 'set', oldNode: renumber ? pb.node : '', unlinks: renumber && (pb.mode === 'permanent' || pb.linked) && pb.local ? [pb.local] : [], keepRecord: false,
      restartAsterisk: asteriskChange, restarts: [...(asteriskChange ? ['asterisk'] : []), 'analog_bridge'], warnings: [], base: {}, link: { mode, localNode: local, bridge: node, exclusive },
    });
    return true;
  }
  if (asteriskChange && body.restart !== true) { c.err(409, 'restart_required: this change needs an Asterisk restart: send restart: true to confirm', 'restart_required'); return true; }
  pbUndo = { node: pb.node, owned: pb.owned, rxPort: pb.rxPort, txPort: pb.txPort, mode: pb.mode, local: pb.local };
  if (asteriskChange) pb.owned = true;
  Object.assign(pb, { node, rxPort: rx, txPort: tx, mode, local, exclusive, paused: false, linked: mode === 'permanent' });
  const steps = [{ name: 'asterisk config', ok: asteriskChange }, { name: 'analog_bridge ini', ok: true }, { name: 'asterisk restart', ok: asteriskChange }, { name: 'bridge restart', ok: true }, { name: 'health', ok: true }];
  c.json(200, { applied: true, kind: renumber ? 'renumber' : 'set', node, steps, warnings: [], keepRecord: false, health: { ok: true }, rollback: { available: true, before: asteriskChange, ini: true } });
  c.push?.('dvswitch', snapshot());
  return true;
}

/** DELETE /dvswitch/private-node: the plan (dryRun) or the removal of the bridge node ASLCP added. */
function removeNode(c: DVCtx, body: Body): boolean {
  progress();
  if (!installed) { c.err(409, 'not_installed: DVSwitch is not installed (its ini files are missing)', 'not_installed'); return true; }
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
        { path: '/opt/Analog_Bridge/Analog_Bridge.ini', changed: true, diff: `--- a\n+++ b\n@@ [USRP]\n-rxPort = ${pb.txPort}\n-txPort = ${pb.rxPort}\n+rxPort = ${STOCK.tx}\n+txPort = ${STOCK.rx}\n` },
      ],
      restartAsterisk: true, restarts: ['asterisk', 'analog_bridge'], warnings: [], base: {}, link: { mode: 'ondemand', localNode: '', bridge: '', exclusive: pb.exclusive },
    });
    return true;
  }
  if (!confirmed) { c.err(409, 'restart_required: this change needs an Asterisk restart: send restart: true to confirm', 'restart_required'); return true; }
  pbUndo = { node: pb.node, owned: pb.owned, rxPort: pb.rxPort, txPort: pb.txPort, mode: pb.mode, local: pb.local };
  Object.assign(pb, { node: '', owned: false, rxPort: STOCK.rx, txPort: STOCK.tx, mode: 'ondemand', local: '', paused: false, linked: false });
  const steps = [{ name: 'disconnect', ok: true }, { name: 'asterisk config', ok: true }, { name: 'analog_bridge ini', ok: true }, { name: 'asterisk restart', ok: true }, { name: 'bridge restart', ok: true }];
  c.json(200, { applied: true, kind: 'remove', node, steps, warnings: [], keepRecord: false, rollback: { available: true, before: true, ini: true } });
  c.push?.('dvswitch', snapshot());
  return true;
}

// Remembered tunes (GET /dvswitch/resume): placeholders, the first two seeded so the cards show "Last used".
let resume = false, lastMode = 'DMR';
const remembered: Record<string, { tune: string; ts?: string; at: string }> = {
  DMR: { tune: '91', ts: '2', at: new Date(Date.now() - 5 * 60e3).toISOString() },
  P25: { tune: '10100', at: new Date(Date.now() - 3 * 3600e3).toISOString() },
};
const resumeView = () => ({ resume, lastMode, perMode: remembered });

export interface DVCtx {
  /** Pushes an SSE event to every client (the dashboard card's `dvswitch` snapshot). */
  push?: (ev: string, data: unknown) => void;
  m: string; p: string; url?: URL; body?: Body; user?: { username: string; role: string };
  json: (code: number, body?: unknown) => void; err: (code: number, msg: string, c: string) => void;
}

// Placeholder host and TG lists (RFC 5737 addresses, made-up names) for GET /dvswitch/lists.
const LISTS: Record<string, { value: string; name: string; detail?: string; address: string; port: number }[]> = {
  dmr: [
    { value: 'example-master.invalid', name: 'BM_0000_Example', detail: 'ID 2001', address: 'example-master.invalid', port: 62031 },
    { value: '192.0.2.11', name: 'BM_3100_Example', detail: 'ID 3100', address: '192.0.2.11', port: 62031 },
    { value: '198.51.100.20', name: 'XLX_000', address: '198.51.100.20', port: 62030 },
    { value: '203.0.113.30', name: 'TGIF_Example', address: '203.0.113.30', port: 62031 },
  ],
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

// Dashboard panel (GET /dvswitch/snapshot, /mode, /unlink, favourites). The bridge answers like ABInfo:
// a mode switch shows up after a delay (1.2 s; P25 takes 7 s so the card's "Not confirmed" notice and its
// recovery can be seen), and last_tune of another mode is not shown.
type PMode = 'DMR' | 'YSF' | 'P25' | 'NXDN';
const UNLINK: Record<PMode, string> = { DMR: '4000', YSF: 'disconnect', P25: '9999', NXDN: '9999' };
const bridge: { mode: PMode; tune: string; slot: number; muted: boolean } = { mode: 'DMR', tune: '91', slot: 2, muted: false };
const favs: { id: number; mode: PMode; value: string; name: string; sort: number }[] = [
  { id: 1, mode: 'DMR', value: '91', name: 'Worldwide', sort: 0 },
  { id: 2, mode: 'DMR', value: '3100', name: 'USA', sort: 1 },
  { id: 3, mode: 'YSF', value: '00001', name: 'Example room', sort: 0 },
];
let favSeq = 3;
const lcMode = (m: PMode) => m.toLowerCase();
function targetName(): string | undefined {
  const t = bridge.tune;
  if (!t) return undefined;
  const items = bridge.mode === 'DMR' ? [] : LISTS[lcMode(bridge.mode)];
  const hit = items.find((i) => i.value === t || `${i.address}:${i.port}` === t);
  return hit?.name ?? favs.find((f) => f.mode === bridge.mode && f.value === t)?.name;
}
function snapshot() {
  progress();
  const modeOn: Record<PMode, boolean> = { DMR: cfg.modes.dmr, YSF: cfg.modes.ysf, P25: cfg.modes.p25, NXDN: cfg.modes.nxdn };
  const unit = (n: string) => status().units.find((u) => u.name === n)?.active === 'active';
  const gw = (['YSF', 'P25', 'NXDN'] as PMode[]).filter((m) => modeOn[m]).map((m) => `${lcMode(m)}gateway`);
  const running = installed && unit('analog_bridge');
  const name = targetName();
  return {
    installed, running, configured: installed && running,
    bridge: running
      ? { mode: bridge.mode, tune: bridge.tune, ...(name ? { targetName: name } : {}), slot: bridge.slot, muted: bridge.muted, source: 'abinfo', updated: new Date().toISOString() }
      : { mode: '', tune: '', slot: 0, muted: false, source: 'none', updated: '' },
    modes: (['DMR', 'YSF', 'P25', 'NXDN'] as PMode[]).map((id) => ({ id, enabled: modeOn[id], unlink: UNLINK[id] })),
    services: installed ? ['analog_bridge', 'mmdvm_bridge', ...gw].map((u) => ({ unit: u, active: unit(u) })) : [],
    bridgeNode: pb.node ? { present: true, node: pb.node } : { present: false },
    link: linkView(),
  };
}
const tuneRe: Record<PMode, RegExp> = { DMR: /^[0-9]{1,8}$/, YSF: /^(disconnect|[A-Za-z0-9._-]{1,32}|[A-Za-z0-9.-]{1,253}:[0-9]{1,5})$/, P25: /^[0-9]{1,5}$/, NXDN: /^[0-9]{1,5}$/ };
const favView = (f: (typeof favs)[number]) => ({ id: f.id, mode: f.mode, value: f.value, name: f.name, sort: f.sort });
// What ABInfo's last_tune shows: a YSF id or name resolves to host:port like the helper does.
function abTune(mode: PMode, v: string) {
  if (mode !== 'YSF') return v;
  const hit = LISTS.ysf.find((i) => i.value === v || i.name === v);
  return hit ? `${hit.address}:${hit.port}` : v;
}
function applyMode(m: PMode, delay: number, push?: DVCtx['push']) {
  setTimeout(() => { if (bridge.mode !== m) { bridge.mode = m; bridge.tune = ''; } push?.('dvswitch', snapshot()); }, delay);
}
function panelHandler(c: DVCtx): boolean {
  const op = c.user!.role !== 'viewer';
  const forbid = () => (c.err(403, 'insufficient permissions', 'forbidden'), true);
  const reply = (mode: PMode, switched: boolean, confirmed: boolean) => c.json(200, { mode, switched, confirmed, snapshot: snapshot() });
  if (c.m === 'GET' && c.p === '/dvswitch/snapshot') { c.json(200, snapshot()); return true; }
  if (c.m === 'GET' && c.p === '/dvswitch/favorites') { c.json(200, favs.map(favView)); return true; }
  if (!op) return forbid();
  const body = (c.body ?? {}) as Record<string, unknown>;
  const running = () => snapshot().running;
  if (c.m === 'POST' && c.p === '/dvswitch/mode') {
    const m = String(body.mode ?? '').toUpperCase() as PMode;
    if (!(m in UNLINK)) return c.err(400, 'mode must be DMR, YSF, P25 or NXDN', 'bad_request'), true;
    if (!running()) return c.err(409, 'no_info: the bridge is not running', 'no_info'), true;
    if (!snapshot().modes.find((x) => x.id === m)?.enabled) return c.err(409, `mode_disabled: ${m} is not enabled in MMDVM_Bridge`, 'mode_disabled'), true;
    if (bridge.mode === m) return reply(m, false, true), true;
    const delay = m === 'P25' ? 7000 : 1200;
    applyMode(m, delay, c.push);
    setTimeout(() => reply(m, true, delay <= 1800), Math.min(delay, 1800));
    return true;
  }
  if (c.m === 'POST' && c.p === '/dvswitch/unlink') {
    if (!running()) return c.err(409, 'no_info: the bridge is not running', 'no_info'), true;
    bridge.tune = UNLINK[bridge.mode];
    reply(bridge.mode, false, true);
    c.push?.('dvswitch', snapshot());
    return true;
  }
  if (c.m === 'POST' && c.p === '/dvswitch/tune') {
    const mode = String(body.mode ?? '').toUpperCase() as PMode, tune = body.tune;
    if (!(mode in tuneRe) || typeof tune !== 'string' || !tuneRe[mode].test(tune)) return bad(c, 'tune'), true;
    if (!running()) return c.err(409, 'no_info: the bridge is not running', 'no_info'), true;
    const switched = bridge.mode !== mode;
    bridge.mode = mode; bridge.tune = abTune(mode, tune);
    if (!UNLINK[mode] || tune !== UNLINK[mode]) remembered[mode] = { tune, ...(mode === 'DMR' ? { ts: '2' } : {}), at: new Date().toISOString() };
    lastMode = mode;
    reply(mode, switched, true);
    c.push?.('dvswitch', snapshot());
    return true;
  }
  if (c.m === 'POST' && c.p === '/dvswitch/favorites') {
    const mode = String(body.mode ?? '').toUpperCase() as PMode, value = typeof body.value === 'string' ? body.value : '', name = typeof body.name === 'string' ? body.name.trim() : '';
    if (!(mode in tuneRe) || !tuneRe[mode].test(value) || [...name].length < 1 || [...name].length > 40) return c.err(400, 'bad_request: check the mode, the value and the name (1 to 40 characters)', 'bad_request'), true;
    if (favs.some((f) => f.mode === mode && f.value === value)) return c.err(409, 'duplicate: this target is already a favourite', 'duplicate'), true;
    if (favs.filter((f) => f.mode === mode).length >= 50) return c.err(409, 'limit: at most 50 favourites per mode', 'limit'), true;
    const f = { id: ++favSeq, mode, value, name, sort: favs.filter((x) => x.mode === mode).length };
    favs.push(f);
    c.json(201, favView(f));
    return true;
  }
  const m = /^\/dvswitch\/favorites\/(\d+)$/.exec(c.p);
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

export function handleDVSwitch(c: DVCtx): boolean {
  if (!c.p.startsWith('/dvswitch')) return false;
  if (!c.user) { c.err(401, 'authentication required', 'unauthorized'); return true; }
  if (c.m === 'GET' && c.p === '/dvswitch/lists') {
    if (c.user.role === 'viewer') { c.err(403, 'insufficient permissions', 'forbidden'); return true; }
    const mode = c.url?.searchParams.get('mode') ?? '';
    if (!(mode in LISTS)) { c.err(400, 'mode must be dmr, ysf, p25 or nxdn', 'bad_request'); return true; }
    c.json(200, { mode, items: LISTS[mode], etag: 'mock' });
    return true;
  }
  if (c.m === 'GET' && c.p === '/dvswitch/dmr-status') {
    if (c.user.role === 'viewer') { c.err(403, 'insufficient permissions', 'forbidden'); return true; }
    progress();
    c.json(200, dmrStatus());
    return true;
  }
  if (c.p === '/dvswitch/resume' && c.m === 'GET') {
    if (c.user.role === 'viewer') { c.err(403, 'insufficient permissions', 'forbidden'); return true; }
    c.json(200, resumeView());
    return true;
  }
  if (c.p === '/dvswitch/resume' && c.m === 'PUT') {
    if (c.user.role !== 'admin') { c.err(403, 'insufficient permissions', 'forbidden'); return true; }
    const r = (c.body ?? {}).resume;
    if (typeof r !== 'boolean') { c.err(400, 'resume must be true or false', 'bad_request'); return true; }
    resume = r;
    c.json(200, resumeView());
    return true;
  }
  if (c.p === '/dvswitch/resume' && c.m === 'PUT') {
    if (c.user.role !== 'admin') { c.err(403, 'insufficient permissions', 'forbidden'); return true; }
    const r = (c.body ?? {}).resume;
    if (typeof r !== 'boolean') { c.err(400, 'resume must be true or false', 'bad_request'); return true; }
    resume = r;
    c.json(200, resumeView());
    return true;
  }
  if (c.p === '/dvswitch/link' && c.m === 'PUT') {
    if (c.user.role === 'viewer') { c.err(403, 'insufficient permissions', 'forbidden'); return true; }
    const x = (c.body ?? {}).exclusive;
    if (typeof x !== 'boolean') { c.err(400, 'exclusive is required', 'bad_request'); return true; }
    pb.exclusive = x;
    c.json(200, { exclusive: x });
    return true;
  }
  if (panelHandler(c)) return true;
  if (c.p === '/dvswitch/private-node' || c.p === '/dvswitch/private-node/rollback') {
    if (c.user.role !== 'admin') { c.err(403, 'insufficient permissions', 'forbidden'); return true; }
    if (c.m === 'PUT' && c.p === '/dvswitch/private-node') return privateNode(c, c.body ?? {});
    if (c.m === 'DELETE' && c.p === '/dvswitch/private-node') return removeNode(c, c.body ?? {});
    if (c.m === 'POST' && c.p === '/dvswitch/private-node/rollback') {
      if (!pbUndo) { c.err(404, 'no_rollback: there is no change to roll back', 'no_rollback'); return true; }
      Object.assign(pb, pbUndo, { paused: false, linked: false }); pbUndo = null;
      c.json(200, { rolledBack: true, problems: [], steps: [{ name: 'asterisk config', ok: true }, { name: 'analog_bridge ini', ok: true }] });
      c.push?.('dvswitch', snapshot());
      return true;
    }
  }
  if (c.user.role !== 'admin') { c.err(403, 'insufficient permissions', 'forbidden'); return true; }
  if (c.m === 'GET' && c.p === '/dvswitch') { c.json(200, status()); return true; }
  if (c.m === 'GET' && c.p === '/dvswitch/job') { c.json(200, progress()); return true; }
  if (c.m === 'GET' && c.p === '/dvswitch/setup') { progress(); c.json(200, setup()); return true; }
  if ((c.m === 'PUT' && ['/dvswitch/identity', '/dvswitch/credentials', '/dvswitch/modes', '/dvswitch/startup'].includes(c.p)) || (c.m === 'POST' && ['/dvswitch/restart', '/dvswitch/rollback'].includes(c.p))) return write(c, c.body ?? {});
  if (c.m === 'POST' && (c.p === '/dvswitch/install' || c.p === '/dvswitch/uninstall')) {
    const kind = c.p === '/dvswitch/install' ? 'install' : 'uninstall';
    const s = status();
    if (s.install.job === 'running' || s.uninstall.job === 'running') c.err(409, 'an install, uninstall or update is already running', 'job_running');
    else if (kind === 'install' && installed) c.err(409, 'already_installed: DVSwitch is already installed', 'already_installed');
    else if (kind === 'uninstall' && !installed) c.err(409, 'DVSwitch was installed outside ASLCP', 'not_aslcp_installed');
    else { if (kind === 'uninstall') { Object.assign(pb, { node: '', owned: false, mode: 'ondemand', local: '', paused: false, linked: false }); pbUndo = null; } job = { kind, at: Date.now() }; c.json(202, status()); setTimeout(() => c.push?.('dvswitch', snapshot()), DURATION + 200); }
    return true;
  }
  return false;
}
