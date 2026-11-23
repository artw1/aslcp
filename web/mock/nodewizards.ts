// Dev-only mock of the Add node and Change node number wizards (docs/API.md, "Node wizards"):
//   POST /config/wizard/add-node/{plan,apply}, /config/wizard/renumber/{plan,apply}.
// Simple line edits of the in-memory tree; rollback is the setup wizard's (nodesetup.ts).
import schemaDoc from './schema.json';
import { tree, sha, unified, mask } from './config.ts';
import { Bad, applyWizard, bad, localNodes, reNode, reSecret, setKey, type Ctx } from './nodesetup.ts';

interface Preset { name: string; values: Record<string, string>; hub?: boolean }
const PRESETS = (schemaDoc as unknown as { presets: Preset[] }).presets;

function finish(work: Record<string, string>, node: string, oldNode: string, o: { warnings?: object[]; manual?: string[]; notes?: string[]; reasons: string[]; steps: { id: string; label: string }[] }) {
  const files = Object.keys(work).sort().map((path) => {
    const cur = tree[path] ?? '';
    return { path, base: sha(cur), diff: work[path] === cur ? '' : mask(unified(path, cur, work[path])), changed: work[path] !== cur };
  });
  const changed = files.filter((f) => f.changed);
  const noop = !changed.length;
  return {
    work,
    plan: {
      node, mode: 'setup', files, base: Object.fromEntries(files.map((f) => [f.path, f.base])),
      changes: [{ key: 'node', file: 'rpt.conf', section: node, action: oldNode === node ? 'added' : 'set', from: oldNode, to: node }],
      errors: [], warnings: o.warnings ?? [], reload: [], restart: !noop, restartReasons: noop ? [] : o.reasons,
      noop, oldNode, public: +node >= 2000, renumber: oldNode !== node, steps: o.steps, manual: o.manual ?? [], notes: o.notes ?? [],
    },
  };
}

function addPlan(b: any) {
  if (!reNode.test(b.node ?? '')) throw bad('the node number must be 1 to 7 digits');
  const existing = [...localNodes(), ...[...(tree['rpt.conf'] ?? '').matchAll(/^(\d+)\s*=/gm)].map((m) => m[1])];
  if (existing.includes(b.node)) throw bad(`node ${b.node} already exists on this system`);
  if (!reSecret.test(b.password ?? '')) throw bad('the node password may use letters, digits and . _ + = , : @ % - (1 to 64)');
  if (!/^[A-Za-z0-9]{3,12}$/.test(b.callsign ?? '')) throw bad('the callsign must be letters and digits');
  if (!['simpleusb', 'hub'].includes(b.radio)) throw bad('radio must be simpleusb or hub');
  const pr = PRESETS.find((p) => p.name === b.preset);
  if (!pr) throw bad('choose a duplex preset');
  if (!!pr.hub !== (b.radio === 'hub')) throw bad('the hub preset goes with a hub radio only');
  const from = b.from || localNodes()[0];
  if (b.from && !localNodes().includes(b.from)) throw bad(`node "${b.from}" is not a local node`);
  const n = b.node, pub = +n >= 2000;
  const reg = b.registration ?? { method: pub ? 'http' : 'none' };
  if (!pub && reg.method !== 'none') throw bad('a private node (1 to 1999) is not registered');
  const work: Record<string, string> = {};
  let r = tree['rpt.conf'];
  const template = /^\[\d+\]\(([^)]+)\)/m.exec(r.slice(r.indexOf(`[${from}]`)))?.[1] ?? 'node-main';
  const dev = b.radio === 'hub' ? 'Local/pseudo' : `SimpleUSB/usb${n}`;
  let stanza = `\n[${n}](${template})\nrxchannel = ${dev}\nidrecording = |i${b.callsign.toUpperCase()}\nidtalkover = |i${b.callsign.toUpperCase()}\n`;
  for (const [id, v] of Object.entries(pr.values)) { const [, , key] = id.split(':'); stanza += `${key} = ${v}\n`; }
  r = r.replace(/(\[nodes\][^\[]*?)(\n*)(?=\n\[)/, `$1\n${n} = radio@127.0.0.1/${n},NONE\n`).replace(/\s*$/, '\n') + stanza;
  work['rpt.conf'] = r;
  if (b.radio === 'simpleusb') work['simpleusb.conf'] = tree['simpleusb.conf'].replace(/\s*$/, '\n') + `\n[usb${n}]\ndevstr =\ncarrierfrom = usbinvert\nctcssfrom = no\ntxmixa = voice\nrxboostset = no\n`;
  if (reg.method !== 'none') {
    const f = reg.method === 'http' ? 'rpt_http_registrations.conf' : 'iax.conf';
    const line = `register => ${n}:${b.password}@${reg.host || 'register.example.net'}`;
    const t = (tree[f] ?? '[general]\n').replace(/\s*$/, '\n');
    work[f] = t.includes('[general]') ? t.replace(/(\[general\][^\[]*?)(\n*)(?=\n\[|$)/, `$1\n${line}\n`) : `[general]\n${line}\n`;
  }
  const steps = [
    { id: 'files', label: 'Write the configuration files' }, { id: 'savenode', label: 'Update savenode.conf' },
    { id: 'ami', label: 'Switch ASLCP to the new AMI secret' }, { id: 'restart', label: 'Restart Asterisk' },
    { id: 'allmon3', label: 'Update Allmon3 (when installed)' }, { id: 'health', label: 'Health check' },
    ...(reg.method !== 'none' ? [{ id: 'registration', label: 'Check that the registration comes up' }] : []),
  ];
  return finish(work, n, n, {
    reasons: ['a node was added'], steps,
    notes: b.radio === 'hub' ? ['A hub has no radio of its own: it uses Local/pseudo and needs no SimpleUSB device.'] : [`The new node gets its own SimpleUSB device section [usb${n}]; set its devstr under Radio.`],
    warnings: pub ? [] : [{ level: 'info', code: 'private_node', message: `Node ${n} is a private node: it is not registered with AllStarLink.` }],
  });
}

function renumberPlan(b: any) {
  const o = b.oldNode, n = b.node;
  if (!localNodes().includes(o)) throw bad(`node "${o}" is not a local node`);
  if (!reNode.test(n ?? '')) throw bad('the node number must be 1 to 7 digits');
  if (n === o) throw bad('that is the current number');
  if (localNodes().includes(n)) throw bad(`node ${n} already exists on this system`);
  const regRe = new RegExp(`^\\s*register\\s*=>\\s*${o}:`);
  const regFiles = ['iax.conf', 'rpt_http_registrations.conf'].filter((f) => (tree[f] ?? '').split('\n').some((l) => regRe.test(l)));
  if ((b.savenode || (regFiles.length && +n >= 2000)) && !reSecret.test(b.password ?? '')) throw bad('enter the new node\'s password');
  const work: Record<string, string> = {};
  const esc = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  work['rpt.conf'] = tree['rpt.conf'].replace(new RegExp(`^\\[${o}\\]`, 'm'), `[${n}]`).replace(new RegExp(`^${o}(\\s*=\\s*\\S*?/)${o},`, 'm'), `${n}$1${n},`);
  if (/Rpt\(/.test(tree['extensions.conf'] ?? '')) work['extensions.conf'] = tree['extensions.conf'].replace(new RegExp(`(exten\\s*=>\\s*)${esc(o)},1,Rpt\\(${esc(o)}\\)`, 'g'), `$1${n},1,Rpt(${n})`);
  for (const f of regFiles) {
    work[f] = tree[f].split('\n').map((l) => regRe.test(l) ? (+n >= 2000 && b.password ? l.replace(new RegExp(`${o}:[^@]*@`), `${n}:${b.password}@`) : '') : l).filter((l, i, a) => l !== '' || a[i - 1] !== '').join('\n');
  }
  const steps = [
    { id: 'files', label: 'Write the configuration files' }, { id: 'savenode', label: 'Update savenode.conf' },
    { id: 'ami', label: 'Switch ASLCP to the new AMI secret' }, { id: 'restart', label: 'Restart Asterisk' },
    { id: 'allmon3', label: 'Update Allmon3 (when installed)' }, { id: 'health', label: 'Health check' },
  ];
  return finish(work, n, o, {
    reasons: ['the node number changed'], steps,
    manual: ['voter.conf is not changed: edit the node number there if you use a voter.', 'allmon.ini.php is not changed: update the node in Allmon.', 'The AstDB (favorites, tailored entries) keeps the old number.', `Other systems that link to node ${o} must link to ${n}.`],
    notes: [],
  });
}

export function handleNodeWizards({ m, p, body, user, json, err }: Ctx): boolean {
  const mm = /^\/config\/wizard\/(add-node|renumber)\/(plan|apply)$/.exec(p);
  if (!mm) return false;
  if (user.role !== 'admin') { err(403, 'forbidden', 'forbidden'); return true; }
  if (m !== 'POST') { err(405, 'method not allowed', 'method_not_allowed'); return true; }
  try {
    const add = mm[1] === 'add-node';
    const r = add ? addPlan(body) : renumberPlan(body);
    if (mm[2] === 'plan') { json(200, { plan: r.plan }); return true; }
    applyWizard(r, body, user, json, { ami: false, message: add ? `Add node ${r.plan.node}` : `Renumber node ${r.plan.oldNode} to ${r.plan.node}`, added: add, savenode: !!body.savenode || add });
  } catch (e) {
    if (e instanceof Bad) { err(e.status, e.message, e.code, e.extra); return true; }
    throw e;
  }
  return true;
}
