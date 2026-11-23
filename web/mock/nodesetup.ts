// Dev-only mock of the "Set up this node" API (docs/API.md, "Node setup"):
//   GET/PUT /config/setup/skip, POST /config/setup/plan, /apply, /rollback.
// It edits the in-memory /etc/asterisk of ./config.ts with simple line edits and follows the real
// API's shapes and checks (internal/nodecfg/setup.go), without the full planner.
import schemaDoc from './schema.json';
import { tree, mtimes, sha, unified, mask, snapshot, runReload, treeDiffers, commits } from './config.ts';

interface Preset { name: string; values: Record<string, string>; hub?: boolean }
const PRESETS = (schemaDoc as unknown as { presets: Preset[] }).presets;
let skipped = false;
let amiSecrets = new Map<string, string>();
// Registration comes up on the third status poll after an apply, so the wizard's wait is visible.
export const regPolls = new Map<string, number>();
export function registrationStatus(node: string): { state: string; detail?: string } {
  const n = regPolls.get(node);
  if (n === undefined) return { state: 'registered', detail: `${node} Registered` };
  regPolls.set(node, n + 1);
  return n >= 2 ? { state: 'registered', detail: `${node} Registered` } : { state: 'unknown' };
}

export const hex = (n: number) => Array.from({ length: n }, () => Math.floor(Math.random() * 16).toString(16)).join('');
export class Bad extends Error { constructor(public status: number, public code: string, msg: string, public extra: object = {}) { super(msg); } }
export const bad = (m: string) => new Bad(400, 'bad_request', m);

type Req = {
  oldNode: string; node: string; password: string; callsign: string; radio: string; preset: string; ami?: boolean;
  registration?: { method: string; host?: string }; echolink?: { call: string; password: string; email: string; name: string; qth: string };
};
export const reNode = /^[1-9]\d{0,6}$/, reSecret = /^[A-Za-z0-9._+=,%-]{1,64}$/;

export function localNodes(): string[] {
  return [...(tree['rpt.conf'] ?? '').matchAll(/^\[(\d+)\]/gm)].map((m) => m[1]);
}
const esc = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
export const setKey = (text: string, sec: string, key: string, value: string) => {
  const lines = text.split('\n');
  let i = lines.findIndex((l) => l.trim() === `[${sec}]`);
  if (i < 0) return text;
  let end = i + 1;
  while (end < lines.length && !/^\s*\[/.test(lines[end])) end++;
  const k = new RegExp(`^\\s*${esc(key)}\\s*=`, 'i');
  for (let j = i + 1; j < end; j++) if (k.test(lines[j])) { lines[j] = lines[j].replace(/=\s*[^;]*?(\s*;.*)?$/, (_, c) => `= ${value}${c ?? ''}`); return lines.join('\n'); }
  let ins = end; while (ins > i + 1 && lines[ins - 1].trim() === '') ins--;
  lines.splice(ins, 0, `${key} = ${value}`);
  return lines.join('\n');
};

function plan(b: Req, amiSecret: string) {
  if (!reNode.test(b.node ?? '')) throw bad('the node number must be 1 to 7 digits');
  if (!localNodes().includes(b.oldNode)) throw bad(`node "${b.oldNode}" is not a local node`);
  if (b.node !== b.oldNode && localNodes().includes(b.node)) throw bad(`node ${b.node} already exists in rpt.conf`);
  if (!reSecret.test(b.password ?? '')) throw bad('the node password may use letters, digits and . _ + = , % - (1 to 64)');
  if (!/^[A-Za-z0-9]{3,12}$/.test(b.callsign ?? '')) throw bad('the callsign must be letters and digits');
  if (!['simpleusb', 'hub'].includes(b.radio)) throw bad('radio must be simpleusb or hub');
  const pr = PRESETS.find((p) => p.name === b.preset);
  if (!pr) throw bad('choose a duplex preset');
  if (!!pr.hub !== (b.radio === 'hub')) throw bad('the hub preset goes with a hub radio only');
  const pub = +b.node >= 2000;
  const reg = b.registration ?? { method: pub ? 'http' : 'none' };
  if (!['http', 'iax', 'none'].includes(reg.method)) throw bad('registration method must be http, iax or none');
  if (!pub && reg.method !== 'none') throw bad('a private node (1 to 1999) is not registered');
  if (b.echolink && !/^[A-Z0-9]{3,10}-[LR]$/.test(b.echolink.call)) throw bad('the EchoLink call must end in -L or -R');

  const work: Record<string, string> = {};
  const get = (f: string) => (work[f] ??= tree[f] ?? '');
  const o = b.oldNode, n = b.node;
  // rpt.conf: everything about the node, then the renumber last
  let r = get('rpt.conf');
  r = setKey(r, o, 'idrecording', `|i${b.callsign.toUpperCase()}`);
  r = setKey(r, o, 'idtalkover', `|i${b.callsign.toUpperCase()}`);
  r = setKey(r, o, 'rxchannel', b.radio === 'hub' ? 'Local/pseudo' : 'SimpleUSB/usb');
  for (const [id, v] of Object.entries(pr.values)) { const [, , key] = id.split(':'); r = setKey(r, o, key, v); }
  if (n !== o) {
    r = r.replace(new RegExp(`^\\[${o}\\]`, 'm'), `[${n}]`).replace(new RegExp(`^${o}(\\s*=\\s*\\S*?/)${o},`, 'm'), `${n}$1${n},`);
    const x = get('extensions.conf'); work['extensions.conf'] = x.replace(new RegExp(`(exten\\s*=>\\s*)${o},1,Rpt\\(${o}\\)`, 'g'), `$1${n},1,Rpt(${n})`);
  }
  work['rpt.conf'] = r;
  // registration: the old line goes, a public node gets a new one
  for (const f of ['iax.conf', 'rpt_http_registrations.conf']) {
    if (f in tree) work[f] = get(f).split('\n').filter((l) => !new RegExp(`^\\s*register\\s*=>\\s*${o}:`).test(l)).join('\n');
  }
  if (reg.method !== 'none') {
    const f = reg.method === 'http' ? 'rpt_http_registrations.conf' : 'iax.conf';
    const line = `register => ${n}:${b.password}@${reg.host || 'register.example.net'}`;
    const t = get(f).replace(/\s*$/, '\n');
    work[f] = t.includes('[general]') ? t.replace(/(\[general\][^\[]*?)(\n*)(?=\n\[|$)/, `$1\n${line}\n`) : `[general]\n${line}\n`;
  }
  if (b.echolink) {
    let e = get('echolink.conf');
    for (const [k, v] of [['call', b.echolink.call], ['pwd', b.echolink.password], ['name', b.echolink.name], ['qth', b.echolink.qth], ['email', b.echolink.email], ['astnode', n]]) e = setKey(e, 'el0', k, v);
    work['echolink.conf'] = e;
    work['modules.conf'] = get('modules.conf').replace(/^noload\s*=\s*chan_echolink\.so/m, 'load = chan_echolink.so');
  }
  if (b.ami) work['manager.conf'] = setKey(get('manager.conf'), 'admin', 'secret', amiSecret);

  const files = Object.keys(work).sort().map((path) => {
    const cur = tree[path] ?? '';
    const diff = work[path] === cur ? '' : mask(unified(path, cur, work[path]));
    return { path, base: sha(cur), diff, changed: work[path] !== cur };
  });
  const changedFiles = files.filter((f) => f.changed).map((f) => f.path);
  const steps = [
    { id: 'files', label: 'Write the configuration files' },
    { id: 'savenode', label: 'Update savenode.conf (NODE, PASSWORD, ENABLE)' },
    { id: 'ami', label: 'Switch ASLCP to the new AMI secret' },
    { id: 'restart', label: 'Restart Asterisk' },
    { id: 'allmon3', label: 'Update Allmon3 (when installed)' },
    { id: 'health', label: 'Health check' },
    ...(reg.method !== 'none' ? [{ id: 'registration', label: 'Check that the registration comes up' }] : []),
  ];
  const noop = !changedFiles.length;
  return {
    work,
    plan: {
      node: n, mode: 'setup', files, base: Object.fromEntries(files.map((f) => [f.path, f.base])),
      changes: [{ key: 'node', file: 'rpt.conf', section: n, action: n === o ? 'unchanged' : 'set', from: o, to: n }],
      errors: [], warnings: pub ? [] : [{ level: 'info', code: 'private_node', message: `Node ${n} is a private node: it is not registered with AllStarLink.` }],
      reload: [], restart: !noop, restartReasons: noop ? [] : [...(n !== o ? ['the node number changed'] : []), ...(b.ami ? ['the AMI secret changed'] : [])],
      noop, oldNode: o, public: pub, renumber: n !== o, steps,
    },
  };
}

type Planned = { work: Record<string, string>; plan: { node: string; oldNode: string; base: Record<string, string>; noop: boolean; files: { path: string; changed: boolean }[] } };
/** Shared apply of the wizards' mock: stale check, write, snapshot, reload, the fixed step list and the rollback handle. */
export function applyWizard(r: Planned, body: any, user: { username: string }, json: (code: number, body?: unknown) => void, o: { ami: boolean; message: string; added?: boolean; savenode?: boolean }) {
  const stale = Object.entries(body.base ?? {}).filter(([f, b]) => f in r.plan.base && b && b !== r.plan.base[f]).map(([f]) => f);
  if (stale.length) throw new Bad(409, 'conflict', 'a file changed on disk since the plan was made: plan again', { files: stale });
  if (r.plan.noop) { json(200, { plan: r.plan, steps: [], restarted: false }); return; }
  const msg = (typeof body.message === 'string' && body.message.trim()) || o.message;
  if (treeDiffers(tree, commits[0].tree)) snapshot('aslcp', `Before: ${msg}`);
  const before = commits[0].commit;
  const written = r.plan.files.filter((f) => f.changed).map((f) => f.path);
  for (const f of written) { tree[f] = r.work[f]; mtimes[f] = new Date().toISOString(); }
  const id = snapshot(user.username, msg);
  regPolls.set(r.plan.node, 0);
  const ran = runReload(written, false);
  const health = runReload(written, true).health;
  json(200, {
    plan: r.plan, result: { commit: id, before, ...ran, health },
    steps: [
      { id: 'files', status: 'done' },
      o.savenode === false ? { id: 'savenode', status: 'skipped', detail: 'not requested' } : { id: 'savenode', status: 'done' },
      o.ami ? { id: 'ami', status: 'done' } : { id: 'ami', status: 'skipped', detail: 'the AMI secret was not changed' },
      { id: 'restart', status: 'done' },
      { id: 'allmon3', status: 'skipped', detail: 'Allmon3 is not installed' }, { id: 'health', status: health?.ok ? 'done' : 'failed' },
    ],
    restarted: true, rollback: { commit: before, node: r.plan.node, oldNode: r.plan.oldNode, ami: o.ami, restart: true, ...(o.added ? { added: true } : {}) },
  });
}

export type Ctx = { m: string; p: string; body: any; url: URL; user: { username: string; role: string }; json: (code: number, body?: unknown) => void; err: (code: number, msg: string, c: string, extra?: object) => void };

/** Returns true when the request was handled. Must run before handleConfig. */
export function handleNodeSetup({ m, p, body, user, json, err }: Ctx): boolean {
  if (!p.startsWith('/config/setup/')) return false;
  if (user.role !== 'admin') { err(403, 'forbidden', 'forbidden'); return true; }
  if (p === '/config/setup/skip') {
    if (m === 'PUT') skipped = body?.skipped === true;
    else if (m !== 'GET') { err(405, 'method not allowed', 'method_not_allowed'); return true; }
    json(200, { skipped }); return true;
  }
  if (m !== 'POST') { err(405, 'method not allowed', 'method_not_allowed'); return true; }
  try {
    if (p === '/config/setup/plan') {
      let token = String(body.amiToken ?? '');
      if (!amiSecrets.has(token)) { token = hex(24); amiSecrets.set(token, hex(32)); }
      json(200, { plan: plan(body, amiSecrets.get(token)!).plan, amiToken: token }); return true;
    }
    if (p === '/config/setup/apply') {
      const secret = amiSecrets.get(String(body.amiToken ?? ''));
      if (!secret) throw bad('the generated AMI secret expired: plan again');
      const r = plan(body, secret);
      applyWizard(r, body, user, json, { ami: true, message: `Setup wizard: node ${r.plan.node}` });
      amiSecrets.delete(String(body.amiToken));
      return true;
    }
    if (p === '/config/setup/rollback') {
      const c = commits.find((x) => x.commit === body.commit);
      if (!c || !reNode.test(body.node) || !reNode.test(body.oldNode)) throw bad('invalid rollback request');
      const written = Object.keys(c.tree).filter((f) => tree[f] !== c.tree[f]);
      for (const f of written) { tree[f] = c.tree[f]; mtimes[f] = new Date().toISOString(); }
      const id = snapshot(user.username, `Restore ${c.commit.slice(0, 7)}: ${c.message}`);
      regPolls.delete(body.node);
      json(200, { result: { commit: id, before: commits[1]?.commit ?? '', ...runReload(written, false), health: runReload(written, true).health },
        steps: [{ id: 'files', status: 'done' }, { id: 'ami', status: 'done' }, { id: 'restart', status: 'done' }, { id: 'allmon3', status: 'skipped', detail: 'Allmon3 is not installed' }] });
      return true;
    }
  } catch (e) {
    if (e instanceof Bad) { err(e.status, e.message, e.code, e.extra); return true; }
    throw e;
  }
  err(404, 'not found', 'not_found');
  return true;
}
