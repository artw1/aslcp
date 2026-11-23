// In-memory /etc/asterisk + snapshot history for `npm run dev:mock` (docs/CONFIG.md, /api/config/*). Dev only.
import { createHash } from 'node:crypto';

export const sha = (s: string) => createHash('sha256').update(s).digest('hex');
type Tree = Record<string, string>;

const RPT = `; rpt.conf (anonymized sample)
[general]

[nodes]
1999 = radio@127.0.0.1/1999,NONE
1000 = radio@192.0.2.20:4569/1000,192.0.2.20

[1999](node-main)
rxchannel = SimpleUSB/usb
duplex = 1            ; half duplex
idrecording = |iN0CALL
idtalkover = |iN0CALL
controlstates = controlstates
scheduler = schedule
morse = morse
telemetry = telemetry
functions = functions
macro = macro
tailmessagetime = 600000
hangtime = 5000

[node-main](!)
context = radio
callerid = "Repeater" <0000000000>
nounkeyct = 0
`;
const BASE: Tree = {
  'rpt.conf': RPT,
  'iax.conf': `[general]
bindport = 4569
disallow = all
allow = ulaw
allow = adpcm
jitterbuffer = no
tos = ef
register => 1999:hunter2secret@register.example.invalid

[radio](!)
type = user
context = radio
transfer = no
auth = md5
secret = s3cr3t-iax-pass
`,
  'simpleusb.conf': `[general]

[usb]
devstr = 1-1.4:1.0
rxboost = 0
rxmixerset = 500
txmixaset = 500
carrierfrom = usbinvert
ctcssfrom = no
`,
  'manager.conf': `[general]
enabled = yes
port = 5038
bindaddr = 127.0.0.1

[admin]
secret = hunter2-fake-ami-secret
read = system,call,log,verbose,command,agent,user,config,command,dtmf,reporting,cdr,dialplan
write = system,call,agent,user,config,command,reporting,originate
`,
  'modules.conf': `[modules]
autoload = yes
noload = pbx_gtkconsole.so
load = chan_simpleusb.so
noload = chan_echolink.so
`,
  'echolink.conf': `[el0]
call = INVALID
pwd = INVALID
name = YOUR NAME
qth = INVALID
email = INVALID
astnode = 1999
maxstns = 20
`,
  'rpt_http_registrations.conf': `[general]
`,
  'extensions.conf': `[general]
static = yes
writeprotect = no

[radio]
exten => 1999,1,Rpt(1999)
`,
  'asl-local.conf': '; local scratch file (not part of the standard layout)\n',
  'custom/rpt/macros.conf': `; custom macros
[macro]
1 = *81
2 = *3 1000
`,
};

const AUTHORS = ['admin', 'admin', 'n0call', 'admin', 'n0call'];
const t0 = Date.now();
export let tree: Tree = {};
interface Commit { commit: string; date: string; author: string; message: string; tree: Tree }
export const commits: Commit[] = []; // newest first
let seq = 0;
export const mtimes: Record<string, string> = {};
function commit(author: string, message: string, ago: number) {
  seq++;
  commits.unshift({ commit: createHash('sha1').update(`${seq}:${message}`).digest('hex'), date: new Date(t0 - ago * 60e3).toISOString(), author, message, tree: { ...tree } });
}
function seed() {
  tree = { ...BASE };
  // Older states first, so history shows real diffs.
  tree['simpleusb.conf'] = tree['simpleusb.conf'].replace('rxboost = 0', 'rxboost = 1').replace('rxmixerset = 500', 'rxmixerset = 450');
  tree['rpt.conf'] = tree['rpt.conf'].replace('tailmessagetime = 600000', 'tailmessagetime = 300000').replace('hangtime = 5000', 'hangtime = 4000');
  tree['manager.conf'] = BASE['manager.conf'].replace('hunter2-fake-ami-secret', 'changeme-fake-secret');
  commit('asl3-menu', 'Initial snapshot', 60 * 24 * 9);
  tree['iax.conf'] = tree['iax.conf'].replace('allow = adpcm\n', '');
  commit(AUTHORS[0], 'Edit iax.conf', 60 * 24 * 6);
  tree['simpleusb.conf'] = BASE['simpleusb.conf'];
  commit(AUTHORS[2], 'Edit simpleusb.conf', 60 * 24 * 3);
  tree['rpt.conf'] = BASE['rpt.conf'];
  commit(AUTHORS[3], 'Before: Edit rpt.conf', 60 * 30);
  tree['custom/rpt/macros.conf'] = BASE['custom/rpt/macros.conf'].replace('2 = *3 1000\n', '');
  commit(AUTHORS[4], 'Edit custom/rpt/macros.conf', 60 * 2);
  tree = { ...BASE };
  commit('n0call', 'Edit manager.conf, custom/rpt/macros.conf', 25);
  for (const p of Object.keys(tree)) mtimes[p] = new Date(t0 - 3600e3).toISOString();
}
seed();

const GROUPS: [RegExp, string][] = [
  [/^rpt\.conf$/, 'Core'],
  [/^(simpleusb|usbradio|res_usbradio|voter)\.conf$/, 'Radio'],
  [/^(iax|echolink|rpt_http_registrations|rpt_auth)\.conf$/, 'Linking'],
  [/^(manager|modules|extensions|logger|http|gps|asterisk)\.conf$/, 'Platform'],
  [/^custom\//, 'Custom'],
];
const groupOf = (p: string) => GROUPS.find(([re]) => re.test(p))?.[1];

// Reload plan, as in docs/CONFIG.md; null = needs a restart.
export function reloadFor(p: string): string | null {
  if (p === 'rpt.conf' || p.startsWith('custom/rpt/')) return 'module reload app_rpt.so';
  if (p === 'iax.conf' || p.startsWith('custom/iax/')) return 'iax2 reload';
  if (p === 'extensions.conf' || p.startsWith('custom/extensions/')) return 'dialplan reload';
  const m: Record<string, string> = { 'manager.conf': 'manager reload', 'simpleusb.conf': 'module reload chan_simpleusb.so', 'usbradio.conf': 'module reload chan_usbradio.so', 'echolink.conf': 'module reload chan_echolink.so', 'rpt_http_registrations.conf': 'module reload res_rpt_http_registrations.so', 'logger.conf': 'logger reload' };
  return m[p] ?? null;
}

// ---- unified diff (LCS) ----
function lines(s: string): string[] { if (!s) return []; const l = s.split('\n'); if (l[l.length - 1] === '') l.pop(); return l; }
export function unified(path: string, a: string | null, b: string | null, ctx = 3): string {
  const al = lines(a ?? ''), bl = lines(b ?? '');
  const n = al.length, m = bl.length;
  const dp: Uint32Array[] = Array.from({ length: n + 1 }, () => new Uint32Array(m + 1));
  for (let i = n - 1; i >= 0; i--) for (let j = m - 1; j >= 0; j--) dp[i][j] = al[i] === bl[j] ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1]);
  const ops: { t: ' ' | '-' | '+'; s: string }[] = [];
  let i = 0, j = 0;
  while (i < n || j < m) {
    if (i < n && j < m && al[i] === bl[j]) { ops.push({ t: ' ', s: al[i] }); i++; j++; }
    else if (j < m && (i === n || dp[i][j + 1] >= dp[i + 1][j])) { ops.push({ t: '+', s: bl[j++] }); }
    else ops.push({ t: '-', s: al[i++] });
  }
  const idx = ops.map((o, k) => (o.t !== ' ' ? k : -1)).filter((k) => k >= 0);
  if (!idx.length) return '';
  const ranges: [number, number][] = [];
  for (const k of idx) {
    const lo = Math.max(0, k - ctx), hi = Math.min(ops.length - 1, k + ctx);
    const last = ranges[ranges.length - 1];
    if (last && lo <= last[1] + 1) last[1] = Math.max(last[1], hi); else ranges.push([lo, hi]);
  }
  const oldBefore: number[] = [], newBefore: number[] = [];
  let o = 0, nn = 0;
  ops.forEach((x, k) => { oldBefore[k] = o; newBefore[k] = nn; if (x.t !== '+') o++; if (x.t !== '-') nn++; });
  let out = `--- ${a === null ? '/dev/null' : 'a/' + path}\n+++ ${b === null ? '/dev/null' : 'b/' + path}\n`;
  for (const [lo, hi] of ranges) {
    const seg = ops.slice(lo, hi + 1);
    const oc = seg.filter((x) => x.t !== '+').length, nc = seg.filter((x) => x.t !== '-').length;
    out += `@@ -${oc ? oldBefore[lo] + 1 : oldBefore[lo]},${oc} +${nc ? newBefore[lo] + 1 : newBefore[lo]},${nc} @@\n`;
    for (const x of seg) out += x.t + x.s + '\n';
  }
  return out;
}

export function mask(diff: string): string {
  return diff.split('\n').map((l) => {
    if (l.startsWith('+++') || l.startsWith('---') || !/^[ +-]/.test(l)) return l;
    const body = l.slice(1);
    let mm = /^(\s*(?:secret|password|passwd|pass|md5secret|dbsecret)\s*=>?\s*)(.*)$/i.exec(body);
    if (mm) return l[0] + mm[1] + '********';
    mm = /^(\s*register\s*=>?\s*[^:\s]*:)[^@]*(@.*)$/i.exec(body);
    return mm ? l[0] + mm[1] + '********' + mm[2] : l;
  }).join('\n');
}

// Rough syntax check standing in for astconf.Parse.
export function syntax(content: string): { line: number; message: string } | null {
  const ls = content.split('\n');
  let block = false;
  for (let i = 0; i < ls.length; i++) {
    let l = ls[i];
    if (block) { if (l.includes('--;')) block = false; continue; }
    if (l.trimStart().startsWith(';--')) { block = !l.includes('--;'); continue; }
    l = l.replace(/;.*$/, '').trim();
    if (!l || /^#(try)?include\b/.test(l) || /^#exec\b/.test(l)) continue;
    if (l.startsWith('[')) { if (!/^\[[^\]\s][^\]]*\](\([^)]*\))?$/.test(l)) return { line: i + 1, message: 'malformed section header' }; continue; }
    if (!l.includes('=')) return { line: i + 1, message: `expected "key = value", found "${l.slice(0, 40)}"` };
  }
  return null;
}

const healthy = () => !Object.values(tree).some((c) => c.includes('BADCONFIG'));
export function runReload(paths: string[], doReload: boolean) {
  const restart = paths.some((c) => !reloadFor(c));
  if (!doReload) return { files: paths, restart, reload: null, health: null };
  const cmds = [...new Set(paths.map(reloadFor).filter((c): c is string => !!c))];
  const ok = healthy();
  return {
    files: paths, restart,
    reload: cmds.map((c) => ({ command: c, ok: true, output: c.startsWith('module reload') ? `Module '${c.split(' ')[2]}' reloaded successfully.` : 'Reloaded.' })),
    health: { ok, checks: [{ name: 'AMI connected', ok: true, detail: 'Asterisk 22.10.1' }, { name: 'Node 1999 answers rpt status', ok, detail: ok ? 'answered in 0.2 s' : 'no answer after 15 s' }, { name: 'Node 2000 answers rpt status', ok: true, detail: 'answered in 0.2 s' }] },
  };
}

export function snapshot(author: string, message: string, ago = 0) { commit(author, message, ago); return commits[0].commit; }
export const treeDiffers = (a: Tree, b: Tree) => Object.keys({ ...a, ...b }).some((p) => a[p] !== b[p]);
const changedPaths = (a: Tree, b: Tree) => Object.keys({ ...a, ...b }).filter((p) => a[p] !== b[p]).sort();

type Ctx = { m: string; p: string; body: any; url: URL; user: { username: string; role: string }; json: (code: number, body?: unknown) => void; err: (code: number, msg: string, c: string, extra?: object) => void };

/** Returns true when the request was handled. */
export function handleConfig({ m, p, body, url, user, json, err }: Ctx): boolean {
  let mm: RegExpExecArray | null;
  if (p === '/system/asterisk/restart' && m === 'POST') { if (user.role !== 'admin') { err(403, 'forbidden', 'forbidden'); return true; } json(200, { ok: true }); return true; }
  if (p === '/mock/config-touch' && m === 'POST') { // test helper: simulate an edit on disk by another tool
    const f = String(body.path ?? 'rpt.conf'); if (f in tree) { tree[f] += '; edited outside ASLCP\n'; mtimes[f] = new Date().toISOString(); } json(204); return true;
  }
  if (!p.startsWith('/config/')) return false;
  if (user.role !== 'admin') { err(403, 'forbidden', 'forbidden'); return true; }
  const files = (b: any[]) => (Array.isArray(b) ? b.filter((f) => f && typeof f.path === 'string' && typeof f.content === 'string') : []);

  if (p === '/config/files' && m === 'GET') {
    json(200, Object.keys(tree).sort().map((path) => ({ path, size: tree[path].length, mtime: mtimes[path] ?? new Date(t0).toISOString(), sha256: sha(tree[path]), known: !!groupOf(path), group: groupOf(path) ?? 'Other' })));
    return true;
  }
  if (p === '/config/file' && m === 'GET') {
    const path = url.searchParams.get('path') ?? '';
    if (!(path in tree)) { err(404, 'no such file', 'not_found'); return true; }
    json(200, { path, content: tree[path], sha256: sha(tree[path]) }); return true;
  }
  if (p === '/config/preview' && m === 'POST') {
    const fs = files(body.files);
    if (!fs.length) { err(400, 'files is required', 'bad_request'); return true; }
    const out = fs.map((f) => {
      const e = syntax(f.content), cur = tree[f.path] ?? null;
      return { path: f.path, ok: !e, ...(e ? { error: e } : {}), diff: e ? '' : unified(f.path, cur, f.content), changed: cur !== f.content };
    });
    const changed = fs.filter((f) => tree[f.path] !== f.content).map((f) => f.path);
    const plan = new Map<string, string[]>();
    let restart = false;
    for (const c of changed) { const r = reloadFor(c); if (!r) restart = true; else plan.set(r, [...(plan.get(r) ?? []), c]); }
    json(200, { files: out, reload: [...plan].map(([command, ps]) => ({ command, reason: `${ps.join(', ')} changed` })), restart });
    return true;
  }
  if (p === '/config/apply' && m === 'POST') {
    const fs = files(body.files);
    if (!fs.length || typeof body.message !== 'string' || !body.message.trim()) { err(400, 'files and message are required', 'bad_request'); return true; }
    const bad = fs.find((f) => syntax(f.content));
    if (bad) { err(422, `syntax error in ${bad.path}`, 'syntax_error', { issues: fs.filter((f) => syntax(f.content)).map((f) => ({ path: f.path, ...syntax(f.content) })) }); return true; }
    const conflicted = fs.filter((f) => (f.path in tree ? sha(tree[f.path]) : '') !== (f.base ?? '')).map((f) => f.path);
    if (conflicted.length) { err(409, `file changed on disk: ${conflicted.join(', ')}`, 'conflict', { files: conflicted }); return true; }
    if (treeDiffers(tree, commits[0].tree)) snapshot('aslcp', `Before: ${body.message}`);
    const before = commits[0].commit;
    for (const f of fs) { tree[f.path] = f.content; mtimes[f.path] = new Date().toISOString(); }
    const id = snapshot(user.username, body.message.trim());
    json(200, { commit: id, before, ...runReload(fs.map((f) => f.path), body.reload === true) });
    return true;
  }
  if (p === '/config/history' && m === 'GET') {
    const limit = Math.min(200, Math.max(1, +(url.searchParams.get('limit') ?? 50))), off = Math.max(0, +(url.searchParams.get('offset') ?? 0));
    json(200, commits.slice(off, off + limit).map((c, k) => ({ commit: c.commit, date: c.date, author: c.author, message: c.message, files: changedPaths(commits[off + k + 1]?.tree ?? {}, c.tree) })));
    return true;
  }
  if ((mm = /^\/config\/history\/([0-9a-f]{7,40})(\/restore)?$/.exec(p))) {
    const idx = commits.findIndex((c) => c.commit.startsWith(mm![1]));
    if (idx < 0) { err(404, 'no such commit', 'not_found'); return true; }
    const c = commits[idx];
    if (!mm[2] && m === 'GET') {
      const reveal = url.searchParams.get('reveal') === '1', parent = commits[idx + 1]?.tree ?? {};
      json(200, { commit: c.commit, date: c.date, author: c.author, message: c.message, files: changedPaths(parent, c.tree).map((path) => {
        const d = unified(path, parent[path] ?? null, c.tree[path] ?? null);
        return { path, status: !(path in parent) ? 'added' : !(path in c.tree) ? 'deleted' : 'modified', diff: reveal ? d : mask(d) };
      }) });
      return true;
    }
    if (mm[2] && m === 'POST') {
      if (treeDiffers(tree, commits[0].tree)) snapshot('aslcp', `Before: Restore ${c.commit.slice(0, 7)}`);
      const before = commits[0].commit;
      const written = Object.keys(c.tree).filter((f) => tree[f] !== c.tree[f]);
      const skipped = Object.keys(tree).filter((f) => !(f in c.tree));
      for (const f of written) { tree[f] = c.tree[f]; mtimes[f] = new Date().toISOString(); }
      const id = snapshot(user.username, `Restore ${c.commit.slice(0, 7)}: ${c.message}`);
      json(200, { commit: id, before, ...runReload(written, body.reload === true), skipped });
      return true;
    }
  }
  err(404, 'not found', 'not_found');
  return true;
}
