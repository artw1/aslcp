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
    hostFiles: ['YSFHosts.txt', 'P25Hosts.txt', 'NXDNHosts.txt'].map((name) => ({ name, present: installed, size: installed ? 4096 : 0 })), hostFilesOk: installed,
    marker: installed ? { version: 1, installedAt: new Date().toISOString(), wgetPreinstalled: false, packages: ['qemu-user-static', 'wget'] } : null,
    canInstall: !installed && !running, canUninstall: installed && !running,
    uninstallPackages: installed ? [...PKGS, 'qemu-user-static'] : [], uninstallPaths: PATHS, uninstallUnits: UNITS, ...j,
  };
}

export interface DVCtx {
  m: string; p: string; user?: { username: string; role: string };
  json: (code: number, body?: unknown) => void; err: (code: number, msg: string, c: string) => void;
}

export function handleDVSwitch(c: DVCtx): boolean {
  if (!c.p.startsWith('/dvswitch')) return false;
  if (!c.user) { c.err(401, 'authentication required', 'unauthorized'); return true; }
  if (c.user.role !== 'admin') { c.err(403, 'insufficient permissions', 'forbidden'); return true; }
  if (c.m === 'GET' && c.p === '/dvswitch') { c.json(200, status()); return true; }
  if (c.m === 'GET' && c.p === '/dvswitch/job') { c.json(200, progress()); return true; }
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
