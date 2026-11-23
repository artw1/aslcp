// Dev-only mock of the node forms API (docs/CONFIG.md, "Forms"):
//   GET /config/nodes, GET /config/schema, POST /config/form.
// It reads and edits the in-memory /etc/asterisk of ./config.ts with a small
// line-based parser. It follows the real API's shapes (see internal/nodecfg)
// but is simpler: it refuses nothing exotic and runs a subset of the checks.
import schemaDoc from './schema.json';
import { tree, mtimes, sha, unified, mask, reloadFor, snapshot, runReload, treeDiffers, commits } from './config.ts';

interface Key {
  id: string; file: string; scope: string; name: string; label: string; form: string;
  type: 'string' | 'int' | 'yesno' | 'flag' | 'enum' | 'node';
  values?: string[]; min?: number; max?: number; default?: string; secret?: boolean; optional?: boolean;
  reload: 'verified' | 'assumed' | 'restart';
}
interface Preset { name: string; values: Record<string, string>; hub?: boolean }
const KEYS = (schemaDoc as unknown as { keys: Key[] }).keys;
const PRESETS = (schemaDoc as unknown as { presets: Preset[] }).presets;
const byId = new Map(KEYS.map((k) => [k.id, k]));
const MASKED = '********';

// ---- parsing
interface Entry { key: string; value: string; line: number }
interface Sec { name: string; parents: string[]; template: boolean; line: number; entries: Entry[]; end: number }

function stripComment(s: string): string {
  let out = '';
  for (let i = 0; i < s.length; i++) {
    if (s[i] === '\\' && s[i + 1] === ';') { out += ';'; i++; continue; }
    if (s[i] === ';') break;
    out += s[i];
  }
  return out.trim();
}

function parse(text: string): Sec[] {
  const secs: Sec[] = [];
  const lines = text.split('\n');
  let cur: Sec | null = null;
  lines.forEach((raw, i) => {
    const l = raw.trim();
    const h = /^\[([^\]\s]+)\](?:\(([^)]*)\))?/.exec(l);
    if (h) {
      const opts = (h[2] ?? '').split(',').map((x) => x.trim()).filter(Boolean);
      cur = { name: h[1], parents: opts.filter((o) => o !== '!' && o !== '+'), template: opts.includes('!'), line: i + 1, entries: [], end: i + 1 };
      secs.push(cur);
      return;
    }
    if (!cur) return;
    const kv = /^([^;=\s][^=]*?)\s*=>?\s*(.*)$/.exec(l);
    if (kv && !l.startsWith(';') && !l.startsWith('#')) { cur.entries.push({ key: kv[1], value: stripComment(kv[2]), line: i + 1 }); cur.end = i + 1; }
  });
  return secs;
}

interface Eff { key: string; value: string; section: string; inherited: boolean; line: number; file: string }

function effective(file: string, secs: Sec[], name: string, seen: string[] = []): Eff[] | null {
  const s = secs.filter((x) => x.name === name);
  if (s.length !== 1 || seen.includes(name)) return null;
  let out: Eff[] = [];
  for (const p of s[0].parents) {
    const pe = effective(file, secs, p, [...seen, name]);
    if (!pe) return null;
    out = out.concat(pe.map((e) => ({ ...e, inherited: true })));
  }
  return out.concat(s[0].entries.map((e) => ({ key: e.key, value: e.value, section: name, inherited: false, line: e.line, file })));
}

const last = <T extends { key: string }>(es: T[], key: string) => [...es].reverse().find((e) => e.key.toLowerCase() === key.toLowerCase());

// ---- text edits
function setLine(file: string, text: string, sec: string, key: string, value: string, op = '='): string {
  const secs = parse(text).filter((s) => s.name === sec);
  const s = secs[0];
  const lines = text.split('\n');
  const e = [...s.entries].reverse().find((x) => x.key.toLowerCase() === key.toLowerCase());
  if (e) {
    const raw = lines[e.line - 1];
    const m = /^(\s*[^=]*?=>?\s*)([^;]*?)(\s*;.*)?$/.exec(raw);
    lines[e.line - 1] = m ? `${m[1]}${value}${m[3] ?? ''}` : `${key} ${op} ${value}`;
  } else {
    lines.splice(s.end, 0, `${key} ${op} ${value}`);
  }
  void file;
  return lines.join('\n');
}
function removeLine(text: string, sec: string, key: string): string {
  const s = parse(text).find((x) => x.name === sec);
  const e = s && [...s.entries].reverse().find((x) => x.key.toLowerCase() === key.toLowerCase());
  if (!s || !e) return text;
  const lines = text.split('\n');
  lines.splice(e.line - 1, 1);
  return lines.join('\n');
}

// ---- model
type Src = { kind: string; section?: string; file?: string; line?: number };
interface Field {
  id: string; key: string; value: string; set: boolean; source: Src; overrides?: { value: string; section: string };
  masked?: boolean; canSet: boolean; canReset: boolean; canTemplate: boolean; locked?: string;
}

function fieldOf(k: Key, file: string, secs: Sec[], name: string): Field {
  const f: Field = { id: k.id, key: k.name, value: k.default ?? '', set: false, source: { kind: k.default ? 'default' : 'unset' }, canSet: true, canReset: false, canTemplate: false };
  if (k.secret) f.masked = true;
  const eff = effective(file, secs, name);
  if (!eff) return { ...f, canSet: false, locked: `[${name}] cannot be resolved` };
  const own = secs.find((x) => x.name === name)!;
  const e = last(eff, k.name);
  if (e) {
    f.value = e.value; f.set = true;
    f.source = { kind: e.inherited ? 'template' : 'node', section: e.section, file, line: e.line };
    if (!e.inherited) {
      const inh = last(eff.filter((x) => x.inherited), k.name);
      if (inh) f.overrides = { value: k.secret ? MASKED : inh.value, section: inh.section };
    }
    if (k.secret) f.value = MASKED;
  }
  f.canReset = f.set && !f.source.kind.startsWith('t') && f.source.kind === 'node';
  f.canTemplate = own.parents.length > 0;
  return f;
}

function directField(k: Key, file: string, text: string | undefined): Field {
  const f: Field = { id: k.id, key: k.name, value: k.default ?? '', set: false, source: { kind: k.default ? 'default' : 'unset' }, canSet: text !== undefined, canReset: false, canTemplate: false };
  if (k.secret) f.masked = true;
  if (text === undefined) return { ...f, locked: `${file} does not exist` };
  const s = parse(text).find((x) => x.name === k.scope);
  const e = s && last(s.entries, k.name);
  if (e) { f.value = k.secret ? MASKED : e.value; f.set = true; f.source = { kind: 'section', section: k.scope, file, line: e.line }; }
  return f;
}

const callOf = (idrec: string) => { const m = /^\|i(.*)$/.exec(idrec.trim()); return m && m[1].toUpperCase() !== 'NOTSET' ? m[1].trim() : ''; };
const isPrivate = (n: string) => +n >= 1 && +n <= 1999;
const CALLSIGN = /^[A-Z0-9]{1,3}[0-9][A-Z0-9]{0,4}[A-Z](\/[A-Z0-9]{1,4})?$/;

function moduleState(mod: string): string {
  const s = parse(tree['modules.conf'] ?? '').filter((x) => x.name === 'modules');
  const has = (key: string) => s.some((x) => x.entries.some((e) => e.key === key && e.value === mod));
  if (has('noload')) return 'noload';
  if (has('load')) return 'explicit';
  const al = s.flatMap((x) => x.entries).filter((e) => e.key === 'autoload').pop();
  return al && /^(yes|true|on|1)$/i.test(al.value) ? 'auto' : 'off';
}
const loaded = (st: string) => st === 'explicit' || st === 'auto';

const DRIVERS: Record<string, { type: string; module: string; file: string }> = {
  simpleusb: { type: 'simpleusb', module: 'chan_simpleusb.so', file: 'simpleusb.conf' },
  radio: { type: 'usbradio', module: 'chan_usbradio.so', file: 'usbradio.conf' },
  usbradio: { type: 'usbradio', module: 'chan_usbradio.so', file: 'usbradio.conf' },
  voter: { type: 'voter', module: 'chan_voter.so', file: 'voter.conf' },
  usrp: { type: 'usrp', module: 'chan_usrp.so', file: '' },
};

function regOf(node: string) {
  const find = (file: string) => {
    for (const s of parse(tree[file] ?? '')) for (const e of s.entries) if (e.key === 'register') {
      const m = /^([^:@\s]+)(?::([^@]*))?@(\S+)$/.exec(e.value);
      if (m && m[1] === node) return { file, line: e.line, node, host: m[3], hasPassword: !!m[2], pass: m[2] ?? '' };
    }
    return null;
  };
  const http = find('rpt_http_registrations.conf'), iax = find('iax.conf');
  const strip = (l: ReturnType<typeof find>) => (l ? { file: l.file, line: l.line, node: l.node, host: l.host, hasPassword: l.hasPassword } : undefined);
  const method = http && iax ? 'both' : http ? 'http' : iax ? 'iax' : 'none';
  return {
    reg: { method, ...(http ? { http: strip(http) } : {}), ...(iax ? { iax: strip(iax) } : {}), ...('rpt_http_registrations.conf' in tree ? { httpFile: 'rpt_http_registrations.conf' } : {}), ...((http ?? iax) ? { host: (http ?? iax)!.host } : {}) },
    http, iax,
  };
}

interface Finding { level: 'error' | 'warning' | 'info'; code: string; message: string; node?: string; file?: string }

function findings(node: string): Finding[] {
  const out: Finding[] = [];
  const secs = parse(tree['rpt.conf'] ?? '');
  const eff = effective('rpt.conf', secs, node) ?? [];
  const rx = last(eff, 'rxchannel')?.value ?? '';
  const [typ, arg] = [rx.split('/')[0].toLowerCase(), rx.split('/').slice(1).join('/')];
  const d = DRIVERS[typ];
  if (d) {
    const st = moduleState(d.module);
    if (st === 'noload') out.push({ level: 'error', code: 'driver_noload', node, file: 'modules.conf', message: `rxchannel ${rx} needs ${d.module}, but modules.conf has it set to noload.` });
    else if (st === 'off') out.push({ level: 'error', code: 'driver_not_loaded', node, file: 'modules.conf', message: `rxchannel ${rx} needs ${d.module}, but modules.conf does not load it.` });
    if (d.file && d.file in tree && !parse(tree[d.file]).some((s) => s.name === arg && !s.template)) {
      out.push({ level: 'error', code: 'no_device_stanza', node, file: d.file, message: `rxchannel ${rx} has no [${arg}] section in ${d.file}.` });
    }
  }
  const call = callOf(last(eff, 'idrecording')?.value ?? '');
  if (call && !CALLSIGN.test(call)) out.push({ level: 'warning', code: 'callsign_format', node, file: 'rpt.conf', message: `The callsign "${call}" does not look like a callsign (letters and digits, for example N0CALL).` });
  const { reg } = regOf(node);
  if (reg.method === 'both') out.push({ level: 'error', code: 'registered_both', node, file: 'rpt_http_registrations.conf', message: `Node ${node} is registered both over HTTP and over IAX. Use one method.` });
  else if (isPrivate(node) && reg.method !== 'none') out.push({ level: 'warning', code: 'private_registered', node, file: 'iax.conf', message: `Node ${node} is a private node number (1 to 1999) but has a registration line; private nodes are not in the public directory.` });
  else if (!isPrivate(node) && reg.method === 'none') out.push({ level: 'warning', code: 'public_not_registered', node, file: 'rpt_http_registrations.conf', message: `Node ${node} is a public node number but is not registered, so other nodes cannot find it.` });
  const el = echolink(node);
  if (el.linked && el.configured && !loaded(el.moduleState) && el.moduleState !== 'unknown') out.push({ level: 'error', code: 'echolink_module', node, file: 'modules.conf', message: `EchoLink is set up for node ${node}, but chan_echolink.so is not loaded in modules.conf.` });
  return out;
}

function echolink(node: string) {
  const text = tree['echolink.conf'];
  const moduleStateEl = moduleState('chan_echolink.so');
  if (text === undefined) return { present: false, moduleState: moduleStateEl, linked: false, configured: false, enabled: false, fields: [] as Field[] };
  const fields = KEYS.filter((k) => k.form === 'echolink').map((k) => directField(k, 'echolink.conf', text));
  const linked = fields.find((f) => f.key === 'astnode')?.value === node;
  const call = fields.find((f) => f.key === 'call');
  const configured = !!call && call.set && call.value !== '' && call.value.toUpperCase() !== 'INVALID';
  return { present: true, moduleState: moduleStateEl, linked, configured, enabled: linked && configured && loaded(moduleStateEl), fields };
}

function localNodes(): string[] {
  const out: string[] = [];
  for (const s of parse(tree['rpt.conf'] ?? '')) if (!s.template && /^[1-9][0-9]{0,9}$/.test(s.name) && !out.includes(s.name)) out.push(s.name);
  return out;
}

function nodeModel(node: string) {
  const secs = parse(tree['rpt.conf'] ?? '');
  const eff = effective('rpt.conf', secs, node);
  const sec = secs.find((s) => s.name === node)!;
  const fields = KEYS.filter((k) => k.file === 'rpt.conf' && k.scope === 'node').map((k) => fieldOf(k, 'rpt.conf', secs, node));
  const known = new Set(KEYS.filter((k) => k.file === 'rpt.conf' && k.scope === 'node').map((k) => k.name.toLowerCase()));
  const other = (eff ?? []).filter((e) => !known.has(e.key.toLowerCase())).map((e) => {
    const secret = /secret|password|^pass$|passwd|^pwd$/i.test(e.key);
    return { key: e.key, value: secret ? MASKED : e.value, source: { kind: e.inherited ? 'template' : 'node', section: e.section, file: 'rpt.conf', line: e.line }, ...(secret ? { masked: true } : {}) };
  });
  const rx = (eff && last(eff, 'rxchannel')?.value) || '';
  const [typ, ...rest] = rx.split('/');
  const arg = rest.join('/');
  const d = DRIVERS[typ.toLowerCase()];
  const radio: Record<string, unknown> = { type: rx ? 'other' : 'none', channel: rx, deviceStanza: false, supported: false };
  if (/^local$/i.test(typ) && arg === 'pseudo') Object.assign(radio, { type: 'hub', supported: true });
  else if (d) {
    const dev = d.file && d.file in tree ? parse(tree[d.file]).find((s) => s.name === arg && !s.template) : undefined;
    Object.assign(radio, { type: d.type, module: d.module, moduleState: moduleState(d.module), device: arg, deviceFile: d.file, deviceStanza: !!dev, supported: d.type === 'simpleusb' });
    if (d.type === 'usbradio') Object.assign(radio, { advancedFile: 'usbradio.conf', note: 'USBRadio is edited in Advanced (usbradio.conf); forms and wizards do not support it.' });
    if (d.type === 'simpleusb' && dev) radio.deviceFields = KEYS.filter((k) => k.file === 'simpleusb.conf' && k.scope === 'device').map((k) => fieldOf(k, 'simpleusb.conf', parse(tree['simpleusb.conf']), arg));
  } else if (rx) Object.assign(radio, { advancedFile: 'rpt.conf', note: 'This radio channel is edited in Advanced (rpt.conf).' });
  const id = eff && last(eff, 'idrecording')?.value;
  const callsign = id ? callOf(id) : '';
  const tmpl = [...sec.parents].reverse().find((p) => secs.find((s) => s.name === p)?.template) ?? '';
  const nodesSec = secs.find((s) => s.name === 'nodes');
  return {
    node, private: isPrivate(node), placeholder: node === '1999' && !callsign && !!id && /NOTSET/i.test(id),
    stanza: { file: 'rpt.conf', line: sec.line, parents: sec.parents, ...(tmpl ? { template: tmpl } : {}) },
    callsign, radio, fields, other, registration: regOf(node).reg, echolink: echolink(node), warnings: findings(node), refusals: [] as unknown[],
    inNodes: !!nodesSec?.entries.some((e) => e.key === node),
  };
}

function overview(withStatus: boolean) {
  const nodes = localNodes().map(nodeModel);
  if (withStatus) for (const n of nodes) if (n.registration.method === 'http' || n.registration.method === 'iax') (n.registration as Record<string, unknown>).status = { state: 'registered', detail: `${n.node} Registered` };
  const base: Record<string, string> = {};
  for (const f of ['rpt.conf', 'modules.conf', 'simpleusb.conf', 'echolink.conf', 'rpt_http_registrations.conf', 'iax.conf', 'manager.conf', 'extensions.conf']) if (f in tree) base[f] = sha(tree[f]);
  return { nodes, base, placeholder: nodes.find((n) => n.placeholder)?.node ?? '', warnings: [] as Finding[] };
}

// ---- planning
class Bad extends Error { constructor(public status: number, public code: string, msg: string, public extra: object = {}) { super(msg); } }
const bad = (msg: string) => new Bad(400, 'bad_request', msg);
const refused = (file: string, message: string, code = 'refused_stanza') => new Bad(422, 'refused', message, { refusal: { code, message, file } });

function validate(k: Key, v: string) {
  if (v === '') { if (k.optional) return; throw bad(`${k.name}: a value is required`); }
  if (/[\r\n\0]/.test(v)) throw bad(`${k.name}: must be on one line`);
  if (v.includes(';') && !v.includes('\\;')) throw bad(`${k.name}: must not contain ';' (it starts a comment)`);
  if (v.trim() !== v) throw bad(`${k.name}: must not start or end with a space`);
  if (k.type === 'int') {
    if (!/^-?\d+$/.test(v)) throw bad(`${k.name}: must be a whole number`);
    if (k.min !== undefined && +v < k.min) throw bad(`${k.name}: must be at least ${k.min}`);
    if (k.max !== undefined && +v > k.max) throw bad(`${k.name}: must be at most ${k.max}`);
  }
  if (k.type === 'enum' && !(k.values ?? []).includes(v)) throw bad(`${k.name}: must be one of ${(k.values ?? []).join(', ')}`);
  if (k.type === 'yesno' && !/^(yes|no)$/.test(v)) throw bad(`${k.name}: must be yes or no`);
  if (k.type === 'flag' && !/^[01]$/.test(v)) throw bad(`${k.name}: must be 1 or 0`);
  if (k.type === 'node' && !/^[1-9][0-9]{0,9}$/.test(v)) throw bad(`${k.name}: must be a node number`);
}

interface Applied { id?: string; key: string; file: string; section: string; action: string; from?: string; to?: string; note?: string }

function plan(body: any) {
  const node = String(body.node ?? '');
  const mode = body.mode || 'override';
  if (!['override', 'reset', 'template'].includes(mode)) throw bad('mode must be override, reset or template');
  if (!localNodes().includes(node)) throw bad(`node "${node}" has no stanza in rpt.conf`);
  const work: Record<string, string> = {};
  const get = (f: string) => { if (!(f in tree)) throw refused(f, `${f} does not exist.`, 'no_file'); return (work[f] ??= tree[f]); };
  const applied: Applied[] = [];
  const restartWhy: string[] = [];
  const warnings: Finding[] = [];
  const mask1 = (k: Key | undefined, v: string) => (k?.secret && v ? MASKED : v);

  const changes = new Map<string, string>();
  let hub = false;
  if (body.preset) {
    const pr = PRESETS.find((p) => p.name === body.preset);
    if (!pr) throw bad(`unknown preset "${body.preset}"`);
    if (mode !== 'override') throw bad('a preset is applied to the node itself');
    for (const [id, v] of Object.entries(pr.values)) changes.set(id, v);
    hub = !!pr.hub;
  }
  const seen = new Set<string>();
  for (const c of body.changes ?? []) {
    if (seen.has(c.id)) throw bad(`${c.id} appears twice`);
    seen.add(c.id); changes.set(c.id, String(c.value ?? ''));
  }
  const radio = body.radio ?? (hub ? { type: 'hub' } : undefined);
  for (const [id, v] of changes) {
    const k = byId.get(id);
    if (!k) throw bad(`${id} is not a setting forms can change; use Advanced`);
    if (mode !== 'reset') validate(k, v);
    else if (k.scope !== 'node' && k.scope !== 'device') throw bad(`${k.name} has no template to reset to`);
  }

  const templated = (k: Key, file: string, name: string, m: string, value: string) => {
    const text = get(file);
    const secs = parse(text);
    const sec = secs.find((s) => s.name === name);
    if (!sec) throw refused(file, `${file} has no [${name}] section.`, 'no_stanza');
    const eff = effective(file, secs, name);
    if (!eff) throw refused(file, `[${name}] cannot be resolved.`, 'broken_inheritance');
    const cur = last(eff, k.name);
    const own = sec.entries.some((e) => e.key.toLowerCase() === k.name.toLowerCase());
    if (m === 'reset') {
      if (!own) { applied.push({ id: k.id, key: k.name, file, section: name, action: 'unchanged', note: 'not set on the node' }); return; }
      work[file] = removeLine(text, name, k.name);
      applied.push({ id: k.id, key: k.name, file, section: name, action: 'remove', from: mask1(k, cur?.value ?? ''), note: 'reset to the template value' });
    } else if (m === 'override') {
      if ((own ? cur?.value : cur?.value ?? k.default ?? '') === value) { applied.push({ id: k.id, key: k.name, file, section: name, action: 'unchanged', to: mask1(k, value), note: 'already the value in effect' }); return; }
      if (value === '' && !own) throw bad(`${k.name} is set by a template; use "Change for all nodes" to clear it`);
      if (value === '') { work[file] = removeLine(text, name, k.name); applied.push({ id: k.id, key: k.name, file, section: name, action: 'remove', from: mask1(k, cur?.value ?? '') }); return; }
      work[file] = setLine(file, text, name, k.name, value);
      applied.push({ id: k.id, key: k.name, file, section: name, action: own ? 'set' : 'add', ...(own ? { from: mask1(k, cur?.value ?? '') } : {}), to: mask1(k, value) });
    } else {
      if (!sec.parents.length) throw refused(file, `${k.name} cannot be changed for all nodes: this node does not inherit from a template.`, 'no_template');
      const inh = last(eff.filter((e) => e.inherited), k.name);
      const target = inh?.section ?? sec.parents[sec.parents.length - 1];
      if (own) warnings.push({ level: 'warning', code: 'node_overrides', node, file, message: `Node ${node} sets ${k.name} itself, so it keeps its own value; reset it to use the template's.` });
      work[file] = setLine(file, text, target, k.name, value);
      applied.push({ id: k.id, key: k.name, file, section: target, action: inh ? 'set' : 'add', ...(inh ? { from: mask1(k, inh.value) } : {}), to: mask1(k, value), note: `template ${target}` });
    }
  };
  const direct = (k: Key, value: string) => {
    const text = get(k.file);
    const s = parse(text).find((x) => x.name === k.scope);
    if (!s) throw refused(k.file, `${k.file} has no [${k.scope}] section.`, 'no_stanza');
    const e = last(s.entries, k.name);
    if (value === '' && !e) return;
    if (value === '') work[k.file] = removeLine(text, k.scope, k.name);
    else if (e?.value === value) { applied.push({ id: k.id, key: k.name, file: k.file, section: k.scope, action: 'unchanged' }); return; } else work[k.file] = setLine(k.file, text, k.scope, k.name, value);
    applied.push({ id: k.id, key: k.name, file: k.file, section: k.scope, action: value === '' ? 'remove' : e ? 'set' : 'add', ...(e ? { from: mask1(k, e.value) } : {}), ...(value ? { to: mask1(k, value) } : {}) });
  };
  const ensureModule = (mod: string) => {
    const st = moduleState(mod);
    if (loaded(st)) return;
    const text = get('modules.conf');
    work['modules.conf'] = st === 'noload'
      ? text.split('\n').map((l) => (new RegExp(`^\\s*noload\\s*=>?\\s*${mod.replace('.', '\\.')}`).test(l) ? l.replace(/noload(\s*)/, 'load$1  ') : l)).join('\n')
      : setLine('modules.conf', text, 'modules', 'load', mod).replace(/\nload = /g, '\nload = ');
    applied.push({ key: 'load', file: 'modules.conf', section: 'modules', action: 'set', to: mod, note: `${mod} ${st === 'noload' ? 'was set to noload' : 'was not loaded'}` });
  };

  const nodeKey = (name: string) => byId.get(`rpt.conf:node:${name}`)!;
  if (radio) {
    if (mode !== 'override') throw bad('the radio type is set on the node itself');
    const curRadio = nodeModel(node).radio as { type: string; advancedFile?: string; deviceFile?: string };
    if (!['simpleusb', 'hub', 'none'].includes(curRadio.type)) {
      const f = curRadio.advancedFile || curRadio.deviceFile || 'rpt.conf';
      throw refused(f, `This node uses a radio interface (${curRadio.type}) that forms do not change. Edit rxchannel and the device file in Advanced (${f}).`, 'radio_unsupported');
    }
    if (radio.type === 'hub') templated(nodeKey('rxchannel'), 'rpt.conf', node, 'override', 'Local/pseudo');
    else if (radio.type === 'simpleusb') {
      const cur = nodeModel(node).radio as { type: string; device?: string };
      const dev = cur.type === 'simpleusb' && cur.device ? cur.device : node;
      templated(nodeKey('rxchannel'), 'rpt.conf', node, 'override', `SimpleUSB/${dev}`);
      ensureModule('chan_simpleusb.so');
      if (!parse(get('simpleusb.conf')).some((s) => s.name === dev)) {
        work['simpleusb.conf'] = get('simpleusb.conf').replace(/\n*$/, `\n\n[${dev}]\n`);
        applied.push({ key: `[${dev}]`, file: 'simpleusb.conf', section: dev, action: 'add', note: 'new device section' });
      }
    } else throw bad('the radio type must be simpleusb or hub; USBRadio and other interfaces are edited in Advanced');
  }
  if (body.callsign !== undefined && body.callsign !== null) {
    if (mode !== 'override') throw bad('the callsign is set on the node itself');
    const cs = String(body.callsign).trim().toUpperCase();
    if (!CALLSIGN.test(cs)) throw bad(`"${body.callsign}" does not look like a callsign (letters and digits, for example N0CALL)`);
    templated(nodeKey('idrecording'), 'rpt.conf', node, 'override', `|i${cs}`);
  }
  for (const [id, v] of changes) {
    const k = byId.get(id)!;
    if (id === 'rpt.conf:node:rxchannel' && radio) continue;
    if (k.file === 'rpt.conf' && k.scope === 'node') templated(k, 'rpt.conf', node, mode, v);
    else if (k.scope === 'device') {
      const r = nodeModel(node).radio as { type: string; device?: string; deviceStanza: boolean };
      if (r.type !== 'simpleusb' || !r.deviceStanza) throw refused('simpleusb.conf', 'The node has no SimpleUSB device section to edit.', 'no_device_stanza');
      templated(k, 'simpleusb.conf', r.device!, mode, v);
    } else if (mode !== 'override') throw bad(`${k.name} is a single shared setting; reset and "change for all nodes" do not apply to it`);
    else direct(k, v);
  }
  if (body.link === true) { direct(byId.get('echolink.conf:el0:astnode')!, node); ensureModule('chan_echolink.so'); }
  for (const m of body.modules ?? []) { if (!['chan_simpleusb.so', 'chan_echolink.so'].includes(m)) throw bad(`module "${m}" cannot be changed here`); ensureModule(m); }
  if (body.registration) {
    const rc = body.registration as { method: string; host?: string; password?: string };
    if (!['http', 'iax', 'none'].includes(rc.method)) throw bad('registration method must be http, iax or none');
    if (rc.password && !/^[^\s@;\\]{1,64}$/.test(rc.password)) throw bad('the registration password must be 1 to 64 characters without spaces, @, ; or \\');
    const { http, iax } = regOf(node);
    const pass = rc.password || http?.pass || iax?.pass || '';
    const host = rc.host || (rc.method === 'http' ? http?.host : iax?.host) || http?.host || iax?.host || 'register.allstarlink.org';
    for (const [m, file, old] of [['http', 'rpt_http_registrations.conf', http], ['iax', 'iax.conf', iax]] as const) {
      if (m !== rc.method) {
        if (old) { work[file] = get(file).split('\n').filter((_, i) => i + 1 !== old.line).join('\n'); applied.push({ key: 'register', file, section: 'general', action: 'remove', from: `${node}:${MASKED}@${old.host}`, note: `registration moved to ${rc.method}` }); }
        continue;
      }
      if (!pass) throw bad('a registration password is required');
      const text = get(file);
      const val = `${node}:${pass}@${host}`;
      if (old) work[file] = text.split('\n').map((l, i) => (i + 1 === old.line ? `register => ${val}` : l)).join('\n');
      else work[file] = setLine(file, text, 'general', 'register', val, '=>');
      applied.push({ key: 'register', file, section: 'general', action: old ? 'set' : 'add', to: `${node}:${MASKED}@${host}` });
    }
  }

  const changed = Object.keys(work).filter((f) => work[f] !== tree[f]).sort();
  const files = changed.map((path) => ({ path, base: sha(tree[path]), changed: true, diff: mask(unified(path, tree[path], work[path])) }));
  const baseMap = Object.fromEntries(files.map((f) => [f.path, f.base]));
  const result: Record<string, unknown> = { node, mode, files, base: baseMap, changes: applied, errors: [] as Finding[], warnings, reload: [], restart: false, restartReasons: [], noop: files.length === 0 };
  if (!files.length) return { plan: result, work };
  // after-state checks: new errors block
  const before = new Set(findings(node).map((f) => f.code + f.message));
  const saved: Record<string, string> = {};
  for (const f of changed) { saved[f] = tree[f]; tree[f] = work[f]; }
  const after = findings(node);
  for (const f of changed) tree[f] = saved[f];
  const errors: Finding[] = [];
  for (const f of after) {
    if (f.level === 'error' && !before.has(f.code + f.message)) errors.push(f);
    else if (f.level === 'error') warnings.push({ ...f, level: 'warning', message: `${f.message} (already the case before this change)` });
    else if (!before.has(f.code + f.message)) warnings.push(f);
  }
  result.errors = errors;
  const cmds = [...new Set(changed.map(reloadFor).filter((c): c is string => !!c))];
  result.reload = cmds.map((command) => ({ command, reason: `${changed.filter((c) => reloadFor(c) === command).join(', ')} changed` }));
  for (const f of changed) if (!reloadFor(f)) restartWhy.push(`${f} has no reload command`);
  for (const a of applied) { const k = a.id ? byId.get(a.id) : undefined; if (k?.reload === 'restart' && a.action !== 'unchanged') restartWhy.push(`${k.name} needs an Asterisk restart`); }
  result.restartReasons = [...new Set(restartWhy)].sort();
  result.restart = restartWhy.length > 0;
  return { plan: result, work };
}

type Ctx = { m: string; p: string; body: any; url: URL; user: { username: string; role: string }; json: (code: number, body?: unknown) => void; err: (code: number, msg: string, c: string, extra?: object) => void };

/** Returns true when the request was handled. Must run before handleConfig. */
export function handleNodeCfg({ m, p, body, url, user, json, err }: Ctx): boolean {
  if (p !== '/config/nodes' && p !== '/config/schema' && p !== '/config/form') return false;
  if (user.role !== 'admin') { err(403, 'forbidden', 'forbidden'); return true; }
  if (p === '/config/nodes' && m === 'GET') { json(200, overview(url.searchParams.get('status') === '1')); return true; }
  if (p === '/config/schema' && m === 'GET') { json(200, schemaDoc); return true; }
  if (p === '/config/form' && m === 'POST') {
    let r: ReturnType<typeof plan>;
    try { r = plan(body ?? {}); } catch (e) {
      if (e instanceof Bad) { err(e.status, e.message, e.code, e.extra); return true; }
      throw e;
    }
    const pl = r.plan as { noop: boolean; restart: boolean; base: Record<string, string>; errors: unknown[]; files: { path: string }[]; node: string; mode: string };
    if (body.dryRun) { json(200, { plan: pl, restarted: false, restartPending: pl.restart }); return true; }
    if (pl.errors.length) { err(422, 'the change would break the configuration', 'blocked', { plan: pl }); return true; }
    const stale = Object.entries(body.base ?? {}).filter(([f, b]) => f in pl.base && b && b !== pl.base[f]).map(([f]) => f).sort();
    if (stale.length) { err(409, 'a file changed on disk since the plan was made: plan again', 'conflict', { files: stale }); return true; }
    if (pl.noop) { json(200, { plan: pl, restarted: false, restartPending: false }); return true; }
    const msg = (typeof body.message === 'string' && body.message.trim()) || `Form: node ${pl.node}`;
    if (treeDiffers(tree, commits[0].tree)) snapshot('aslcp', `Before: ${msg}`);
    const before = commits[0].commit;
    const restart = body.restart === true && (pl as { restart: boolean }).restart;
    for (const f of pl.files) { tree[f.path] = r.work[f.path]; mtimes[f.path] = new Date().toISOString(); }
    const id = snapshot(user.username, msg);
    json(200, { plan: pl, result: { commit: id, before, ...runReload(pl.files.map((f) => f.path), !restart) }, restarted: restart, restartPending: pl.restart && !restart });
    return true;
  }
  err(405, 'method not allowed', 'method_not_allowed');
  return true;
}
