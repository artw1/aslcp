// In-memory /api/dvswitch/* for `npm run dev:mock` (docs/API.md "DVSwitch"). Dev only.
const PKGS = ['dvswitch-base', 'analog-bridge', 'mmdvm-bridge', 'md380-emu', 'ysfgateway', 'p25gateway', 'nxdngateway', 'wget'];
const UNITS = ['analog_bridge', 'mmdvm_bridge', 'netcheck', 'ysfgateway', 'p25gateway', 'nxdngateway', 'md380-emu'];
const PATHS = ['/var/lib/mmdvm', '/var/lib/dvswitch', '/var/log/dvswitch', '/var/log/mmdvm', '/opt/Analog_Bridge', '/opt/MMDVM_Bridge', '/etc/cron.d/dvswitch', '/etc/cron.daily/dvswitch-log-cleanup', '/tmp/ABInfo_*.json', '/etc/apt/sources.list.d/dvswitch.list', '/usr/share/keyrings/dvswitch-keyring.gpg'];
const FP = '6DDFC33CD5FC678659642DF572147EC1E788D4C3';
const DURATION = 6000;

let installed = false;
let job: { kind: 'install' | 'uninstall'; at: number } | null = null;
const LOG = {
  install: ['Hit:1 http://deb.debian.org/debian bookworm InRelease', 'Get:2 https://dvswitch.org/apt bookworm InRelease', 'recorded 9 newly installed packages: analog-bridge dvswitch-base md380-emu mmdvm-bridge nxdngateway p25gateway qemu-user-static wget ysfgateway', 'Setting up dvswitch-base ...', 'Setting up analog-bridge ...', 'host files ok', 'dvswitch install finished'],
  uninstall: ['purge plan: analog-bridge dvswitch-base md380-emu mmdvm-bridge nxdngateway p25gateway qemu-user-static ysfgateway', 'Stopping analog_bridge.service ...', 'Removing analog-bridge ...', 'Purging configuration files for dvswitch-base ...', 'dvswitch uninstall finished'],
};

function progress() {
  const idle = { job: 'idle', log: '' };
  if (!job) return { install: idle, uninstall: idle };
  const lines = LOG[job.kind], el = Date.now() - job.at;
  let cur;
  if (el >= DURATION) { installed = job.kind === 'install'; cur = { job: 'succeeded', log: lines.join('\n') }; }
  else cur = { job: 'running', log: lines.slice(0, Math.floor((el / DURATION) * lines.length)).join('\n') };
  return job.kind === 'install' ? { install: cur, uninstall: idle } : { install: idle, uninstall: cur };
}

function status() {
  const j = progress(), running = j.install.job === 'running' || j.uninstall.job === 'running';
  return {
    installed, installedBy: installed ? 'aslcp' : '', version: installed ? '1.7.0' : '', arch: 'arm64', osCodename: 'bookworm', supported: true,
    repoPresent: installed, keyPresent: installed, keyOk: installed, ...(installed ? { keyFingerprint: FP } : {}),
    packages: PKGS.map((name) => ({ name, installed, ...(installed ? { version: '1.7.0' } : {}) })),
    units: UNITS.map((name) => ({ name, loaded: installed, active: installed ? 'active' : 'inactive', enabled: installed ? 'enabled' : '' })),
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
  modes: { dmr: true, ysf: false, p25: false, nxdn: false },
  startup: { ambeMode: 'DMR', txTg: '9', txTs: 2, colorCode: 1 },
  gateways: { ysf: { present: true, startup: 'US-Example' }, p25: { present: true, startup: '' }, nxdn: { present: true, startup: '' } },
};
const rev: Record<string, number> = {}, hist: Record<string, number> = {};
const hash = (id: string) => `mock${id.length}${(rev[id] ?? 0).toString(16).padStart(60, '0')}`;
function setup(extra: Record<string, unknown> = {}) {
  return {
    ...JSON.parse(JSON.stringify(cfg)),
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
    const { dmrAddress, dmrPort, dmrPassword } = body as { dmrAddress?: string; dmrPort?: number; dmrPassword?: string };
    if (dmrAddress === undefined && dmrPort === undefined && dmrPassword === undefined) return bad(c, 'credentials'), true;
    if (dmrAddress !== undefined && !/^[A-Za-z0-9.-]{1,253}$/.test(dmrAddress)) return bad(c, 'DMR Network/Address'), true;
    if (dmrPort !== undefined && !(Number.isInteger(dmrPort) && dmrPort >= 1 && dmrPort <= 65535)) return bad(c, 'DMR Network/Port'), true;
    if (dmrPassword !== undefined && !/^[\x21\x24-\x3a\x3c-\x7e]{1,64}$/.test(dmrPassword)) return bad(c, 'DMR Network/Password'), true;
    if (dmrAddress !== undefined) cfg.dmr.address = dmrAddress;
    if (dmrPort !== undefined) cfg.dmr.port = dmrPort;
    if (dmrPassword !== undefined) cfg.dmr.passwordSet = true;
    done(['mmdvm_bridge'], ['mmdvm_bridge']);
  } else if (c.p === '/dvswitch/modes') {
    const m = body as Record<string, unknown>; let any = false;
    for (const k of ['dmr', 'ysf', 'p25', 'nxdn'] as const) if (typeof m[k] === 'boolean') { cfg.modes[k] = m[k] as boolean; any = true; }
    if (!any) return bad(c, 'modes'), true;
    done(['mmdvm_bridge'], ['analog_bridge', 'mmdvm_bridge']);
  } else if (c.p === '/dvswitch/startup') {
    const b = body as { useCurrent?: boolean; ambeMode?: string; txTg?: string; txTs?: number; colorCode?: number; ysf?: string; p25?: string; nxdn?: string };
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
    const scope = String(body.scope ?? ''), units = ({ bridge: ['analog_bridge', 'mmdvm_bridge'], gateways: ['ysfgateway', 'p25gateway', 'nxdngateway'], all: ['analog_bridge', 'mmdvm_bridge', 'ysfgateway', 'p25gateway', 'nxdngateway'] } as Record<string, string[]>)[scope];
    if (!units) return bad(c, 'scope'), true;
    c.json(200, { action: body.action === 'start' ? 'start' : 'restart', units, skipped: [], states: status().units, warning: 'A restart resets the running mode and talkgroup to the startup values in the ini files.' });
  }
  return true;
}

export interface DVCtx {
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
    else { job = { kind, at: Date.now() }; c.json(202, status()); }
    return true;
  }
  return false;
}
