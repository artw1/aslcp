// In-memory backup and restore for `npm run dev:mock` (docs/BACKUP.md, /api/backup/*, /api/restore/*). Dev only.
import type { ServerResponse } from 'node:http';

type User = { username: string; role: string } | undefined;
type Ctx = {
  m: string; p: string; body: any; raw: string; res: ServerResponse; user: User; setupNeeded: boolean; setupDone: () => void;
  json: (code: number, body?: unknown) => void; err: (code: number, msg: string, c: string) => void; restart: () => void;
};

const MOCK_PASSWORD = 'pass', MOCK_CODE = '123456';
const pending = new Map<string, true>();
let lastResult: unknown = null;

const files = [
  ['rpt.conf', 'restore_dist', 'ASL3 changed the default [general] section; compare with rpt.conf.dpkg-dist'],
  ['iax.conf', 'restore', ''], ['simpleusb.conf', 'restore', ''], ['extensions.conf', 'restore', ''], ['allmon.ini.php', 'restore', ''],
  ['savenode.conf', 'restore', ''], ['custom/local-id.conf', 'restore', ''], ['broadcastify/feed.conf', 'restore', ''],
  ['manager.conf', 'restore_dist', 'ASL3 changed the default permissions'],
  ['asterisk.conf', 'keep_stock', 'Stock file, unchanged on the old node'], ['modules.conf', 'keep_stock', ''], ['logger.conf', 'keep_stock', ''], ['http.conf', 'keep_stock', ''],
  ['rpt_http_registrations.conf', 'keep_new', 'Added by a newer ASL3'], ['node-ids.conf', 'keep_new', ''],
  ['voter.conf', 'delete_optional', 'Deleted on the old node'], ['echolink.conf.sample', 'delete_optional', 'Deleted on the old node'],
  ['rpt.conf.dpkg-dist', 'skip', 'Old package leftover'], ['iax.conf.dpkg-old', 'skip', 'Old package leftover'],
].map(([path, action, note]) => ({ path, action, ...(note ? { note } : {}) }));

const preview = () => ({
  backup: { hostname: 'node-demo', created: new Date(Date.now() - 3 * 86400e3).toISOString(), createdBy: 'admin', aslcpVersion: '1.1.2', asl3Version: '3.10.7-1.deb12', asteriskVersion: '22.10.1+asl3-3.10.7-1.deb12', arch: 'arm64', osId: 'debian', osCodename: 'bookworm', osName: 'Debian GNU/Linux 12 (bookworm)', nodes: ['1999', '1998'], channel: 'beta' },
  node: { asl3Version: '3.10.8-1.deb13', asteriskVersion: '22.10.1+asl3-3.10.8-1.deb13', arch: 'arm64', osId: 'debian', osCodename: 'trixie', osName: 'Debian GNU/Linux 13 (trixie)' },
  warnings: [
    { code: 'os_release', message: "This backup was made on Debian 12 (bookworm); this node runs Debian 13 (trixie). Files you edited will replace the new release's defaults; the new defaults are kept next to them as .dpkg-dist. ASL3 changes some configs between releases (for example DAHDI timing is no longer needed), so if Asterisk or app_rpt does not start, compare your files against the .dpkg-dist copies." },
    { code: 'asl3_version', message: 'The backup was made on ASL3 3.10.7; this node runs 3.10.8. Stock files keep the new version.' },
  ],
  files,
  users: [{ username: 'admin', role: 'admin' }, { username: 'op', role: 'operator' }, { username: 'guest', role: 'viewer' }],
  favorites: 5,
  schedules: 2,
  missingPackages: ['allmon3', 'asl3-tts'],
  installCommand: 'sudo apt install allmon3 asl3-tts',
  checklist: [
    { item: 'Hostname', value: 'node-demo' }, { item: 'Timezone', value: 'America/Chicago' },
    { item: 'Network and Wi-Fi', value: 'Not included in the backup' }, { item: 'SSH keys and OS users', value: 'Not included in the backup' },
    { item: 'Other software', value: 'm17-gateway (service)' },
  ],
  radio: { band: 'UHF', freqRx: '446.0000', freqTx: '446.0000', tone: 'CTCSS 123.0 / 123.0', ctcssRx: '123.0', ctcssTx: '123.0', dcsRx: '', dcsTx: '', port: '/dev/serial/by-id/usb-1a86_USB_Serial-if00-port0', portPresent: false },
  parts: { history: true, allmon3: true, sounds: true, sa818: true },
});

/** Returns true when the request was handled. Runs before the login check: the first-run page uploads with a setup code. */
export function handleBackup({ m, p, raw, res, user, setupNeeded, setupDone, body, json, err, restart }: Ctx): boolean {
  if (p === '/backup/info' && m === 'GET') {
    if (!user) return err(401, 'authentication required', 'unauthorized'), true;
    if (user.role !== 'admin') return err(403, 'forbidden', 'forbidden'), true;
    json(200, { parts: [{ id: 'history', present: true, size: 2.4e6 }, { id: 'allmon3', present: true, size: 18e3 }, { id: 'sounds', present: true, size: 5.8e6 }], sa818: true });
    return true;
  }
  if (p === '/backup' && m === 'POST') {
    if (!user) return err(401, 'authentication required', 'unauthorized'), true;
    if (user.role !== 'admin') return err(403, 'forbidden', 'forbidden'), true;
    const d = new Date(), z = (n: number) => String(n).padStart(2, '0');
    res.statusCode = 200; res.setHeader('Content-Type', 'application/gzip');
    res.setHeader('Content-Disposition', `attachment; filename="aslcp-backup-node-demo-${d.getFullYear()}${z(d.getMonth() + 1)}${z(d.getDate())}-${z(d.getHours())}${z(d.getMinutes())}.tar.gz"`);
    res.end(`mock backup (not a real archive) ${JSON.stringify(body)}\n`);
    return true;
  }
  if (p === '/restore/upload' && m === 'POST') {
    const field = (n: string) => new RegExp(`name="${n}"\\r\\n\\r\\n([^\\r]*)\\r\\n`).exec(raw)?.[1];
    const code = field('code'), pw = field('password');
    if (code !== undefined) { if (!setupNeeded || code !== MOCK_CODE) return err(403, 'wrong setup code (mock: 123456)', 'bad_setup_code'), true; }
    else if (!user || user.role !== 'admin') return err(user ? 403 : 401, 'forbidden', 'forbidden'), true;
    else if (pw !== MOCK_PASSWORD) return err(403, 'wrong password (mock: pass)', 'bad_password'), true;
    const name = /filename="([^"]*)"/.exec(raw)?.[1] ?? '';
    if (/bad/i.test(name)) return err(400, 'not a valid backup', 'bad_backup'), true;
    if (/new/i.test(name)) return err(409, 'backup is from a newer ASLCP; update ASLCP first', 'backup_too_new'), true;
    const id = Math.random().toString(36).slice(2, 12); pending.set(id, true);
    setTimeout(() => json(200, { id, preview: preview() }), 600);
    return true;
  }
  let mm: RegExpExecArray | null;
  if ((mm = /^\/restore\/([A-Za-z0-9]+)\/apply$/.exec(p)) && m === 'POST') {
    if (!pending.has(mm[1])) return err(404, 'no such restore', 'not_found'), true;
    if (!user && !(setupNeeded && body.code === MOCK_CODE)) return err(403, 'wrong setup code', 'bad_setup_code'), true;
    pending.delete(mm[1]);
    const steps = [
      { name: 'Stop Asterisk', ok: true, detail: '' }, { name: 'Snapshot current /etc/asterisk to history', ok: true, detail: 'Before restore from backup' },
      { name: 'Restore /etc/asterisk', ok: true, detail: `${files.filter((f) => f.action.startsWith('restore')).length} files, ${(body.deleteMissing ?? []).length} deleted` },
      { name: 'Replace AstDB', ok: true, detail: '' }, { name: 'Start Asterisk', ok: true, detail: '' }, { name: 'Stage ASLCP database', ok: true, detail: 'restart pending' },
    ];
    lastResult = {
      at: new Date().toISOString(), backupCreated: preview().backup.created, steps,
      health: { ok: true, checks: [{ name: 'Asterisk running', ok: true, detail: '' }, { name: 'AMI answers', ok: true, detail: '' }, { name: 'Node 1999 registered', ok: true, detail: '' }, { name: 'Node 1998 registered', ok: false, detail: 'not registered yet' }] },
      checklist: preview().checklist,
    };
    lastResult = { ...(lastResult as object), health: { ok: false, checks: (lastResult as any).health.checks } };
    json(200, { ok: true, steps, restarting: true });
    // Simulate the restart: drop streams, then refuse connections for a few seconds.
    setTimeout(() => { restart(); downUntil = Date.now() + 6000; if (setupNeeded) setupDone(); }, 800);
    return true;
  }
  if ((mm = /^\/restore\/([A-Za-z0-9]+)$/.exec(p)) && m === 'DELETE') { pending.delete(mm[1]); json(204); return true; }
  if (p === '/restore/result' && m === 'GET') {
    if (!lastResult) return err(404, 'no restore result', 'not_found'), true;
    json(200, lastResult); return true;
  }
  return false;
}

/** The plugin asks this to fake a restarting server. */
export let downUntil = 0;
