// Types and pure helpers for the node configuration page (docs/CONFIG.md "Forms", docs/API.md "Node forms").

export interface Source { kind: 'node' | 'template' | 'section' | 'default' | 'unset'; section?: string; file?: string; line?: number }
export interface Field {
  id: string; key: string; value: string; set: boolean; source: Source;
  overrides?: { value: string; section: string };
  masked?: boolean; canSet: boolean; canReset: boolean; canTemplate: boolean; locked?: string;
}
export interface OtherKey { key: string; value: string; source: Source; masked?: boolean }
export interface Refusal { code: string; message: string; file?: string; line?: number }
export interface Finding { level: 'error' | 'warning' | 'info'; code: string; message: string; node?: string; file?: string }
export interface Radio {
  type: 'simpleusb' | 'usbradio' | 'voter' | 'usrp' | 'hub' | 'other' | 'none';
  channel: string; module?: string; moduleState?: string; device?: string; deviceFile?: string; deviceStanza: boolean;
  supported: boolean; advancedFile?: string; note?: string; deviceFields?: Field[];
}
export interface RegLine { file: string; line: number; node: string; host: string; hasPassword: boolean; placeholder?: boolean }
export interface Registration {
  method: 'none' | 'http' | 'iax' | 'both'; http?: RegLine; iax?: RegLine; httpFile?: string; host?: string;
  status?: { state: 'registered' | 'failed' | 'unknown'; detail?: string };
}
export interface EchoLink { present: boolean; moduleState: string; linked: boolean; configured: boolean; enabled: boolean; fields?: Field[] }
export interface NodeModel {
  node: string; private: boolean; placeholder: boolean;
  stanza?: { file: string; line: number; parents: string[]; template?: string };
  callsign: string; radio: Radio; fields: Field[]; other: OtherKey[]; registration: Registration; echolink: EchoLink;
  warnings: Finding[]; refusals: Refusal[]; inNodes: boolean;
}
export interface AMIInfo { present: boolean; enabled?: string; bindaddr?: string; port?: string; hasSecret: boolean; loopback: boolean; fields?: Field[] }

/** One-line summary of the manager interface card; the password is never part of it. */
export function amiSummary(a: AMIInfo | undefined): string {
  if (!a || !a.present) return 'manager.conf does not exist on this system.';
  const on = !a.enabled || /^(yes|true|1|on)$/i.test(a.enabled);
  if (!on) return 'Turned off (ASLCP and other tools cannot reach Asterisk).';
  const where = a.bindaddr ? a.bindaddr : 'all interfaces';
  return `Listens on ${where}, port ${a.port || '5038'}. The admin password is ${a.hasSecret ? 'set (not shown)' : 'not set'}.`;
}
export interface Overview { ami?: AMIInfo; nodes: NodeModel[]; base: Record<string, string>; placeholder: string; warnings: Finding[]; refusal?: Refusal }

export interface SchemaKey {
  id: string; file: string; scope: string; name: string; label: string; form: string;
  type: 'string' | 'int' | 'yesno' | 'flag' | 'enum' | 'node';
  values?: string[]; labels?: string[]; min?: number; max?: number; unit?: string; default?: string;
  desc: string; reload: 'verified' | 'assumed' | 'restart'; secret?: boolean; optional?: boolean;
}
export interface Preset { name: string; label: string; desc: string; values: Record<string, string>; hub?: boolean }
export interface SchemaDoc { keys: SchemaKey[]; forms: string[]; presets: Preset[] }

export interface FormChange { id: string; value: string }
export interface FormRequest {
  node: string; mode?: 'override' | 'reset' | 'template'; preset?: string; changes?: FormChange[];
  callsign?: string; radio?: { type: 'simpleusb' | 'hub' };
  registration?: { method: 'http' | 'iax' | 'none'; host?: string; password?: string };
  link?: boolean; modules?: string[];
}
export interface PlanChange { id?: string; key: string; file: string; section: string; action: 'set' | 'add' | 'remove' | 'unchanged'; from?: string; to?: string; note?: string }
export interface Plan {
  node: string; mode: string;
  files: { path: string; base: string; diff: string; changed: boolean }[];
  base: Record<string, string>; changes: PlanChange[];
  errors: Finding[]; warnings: Finding[];
  notes?: string[]; reload: { command: string; reason: string }[]; restart: boolean; restartReasons: string[]; noop: boolean;
}

/** Link to the raw editor ("Advanced") for a file, optionally a line. */
export function advancedLink(file?: string, line?: number): string {
  const p = new URLSearchParams({ tab: 'advanced' });
  if (file) p.set('file', file);
  if (file && line && line > 0) p.set('line', String(line));
  return '/config?' + p.toString();
}

export type ConfigTab = 'overview' | 'advanced';
/** Reads the Config page's route query (`#/config?tab=advanced&file=rpt.conf&line=12`). */
export function parseConfigQuery(query: string): { tab: ConfigTab; file: string; line: number; history: boolean } {
  const p = new URLSearchParams(query);
  const tab: ConfigTab = p.get('tab') === 'advanced' ? 'advanced' : 'overview';
  const file = (p.get('file') ?? '').trim();
  const line = Math.max(0, parseInt(p.get('line') ?? '', 10) || 0);
  return { tab, file: /^[\w./-]{1,200}$/.test(file) && !file.includes('..') ? file : '', line, history: p.get('view') === 'history' };
}

export const fieldById = (fields: Field[] | undefined, id: string): Field | undefined => (fields ?? []).find((f) => f.id === id);
export const keyById = (doc: SchemaDoc | null, id: string): SchemaKey | undefined => doc?.keys.find((k) => k.id === id);
export const keysForForm = (doc: SchemaDoc | null, ...forms: string[]): SchemaKey[] => (doc?.keys ?? []).filter((k) => forms.includes(k.form));

/** Where a field's value comes from, in plain words, with a tone for the badge. */
export function provenance(f: Field): { label: string; tone: 'node' | 'template' | 'plain' } {
  const s = f.source;
  switch (s.kind) {
    case 'node': return { label: 'set on this node', tone: 'node' };
    case 'template': return { label: `from template ${s.section ?? ''}`.trim(), tone: 'template' };
    case 'section': return { label: s.file ? `set in ${s.file}` : 'set in the file', tone: 'plain' };
    case 'default': return { label: 'default', tone: 'plain' };
    default: return { label: 'not set', tone: 'plain' };
  }
}

/** The enum label of a value, or the value itself. */
export function valueLabel(k: SchemaKey | undefined, v: string): string {
  if (!k) return v;
  if (k.type === 'enum' && k.values && k.labels) { const i = k.values.indexOf(v); if (i >= 0 && k.labels[i]) return k.labels[i]; }
  if (k.type === 'yesno') return /^(yes|true|on|1)$/i.test(v) ? 'Yes' : 'No';
  if (k.type === 'flag') return v === '1' ? 'Yes' : 'No';
  if (k.unit === 'ms' && /^\d+$/.test(v)) return formatMs(+v);
  return v;
}

export function formatMs(ms: number): string {
  if (ms === 0) return '0 s';
  if (ms % 60000 === 0) return `${ms / 60000} min`;
  if (ms % 1000 === 0) return `${ms / 1000} s`;
  return `${ms} ms`;
}

/** Client-side check of one value against the schema; '' when fine. The server checks again. */
export function validateValue(k: SchemaKey, v: string, wasSet = false): string {
  const s = v.trim();
  // Empty means "leave unset" (or "keep" for a secret); clearing a value that is set needs an optional key.
  if (s === '') return wasSet && !k.optional && !k.secret ? 'Required. Use "Remove from this node" to unset it' : '';
  if (/[\n\r;]/.test(s)) return 'No line breaks or semicolons';
  switch (k.type) {
    case 'int': {
      if (!/^-?\d+$/.test(s)) return 'A whole number';
      const n = +s;
      if (k.min !== undefined && n < k.min) return `At least ${k.min}`;
      if (k.max !== undefined && n > k.max) return `At most ${k.max}`;
      return '';
    }
    case 'enum': return k.values?.includes(s) ? '' : 'Choose one of the listed values';
    case 'yesno': return /^(yes|no)$/i.test(s) ? '' : 'yes or no';
    case 'flag': return s === '0' || s === '1' ? '' : '0 or 1';
    case 'node': return /^[1-9]\d{0,9}$/.test(s) ? '' : 'A node number';
    default: return k.name === 'statpost_url' ? statpostError(s) : '';
  }
}

/** The draft starts from the current values; secrets start empty and are never pre-filled. */
export function initialDraft(fields: Field[], keys: SchemaKey[]): Record<string, string> {
  const d: Record<string, string> = {};
  for (const k of keys) {
    const f = fieldById(fields, k.id);
    d[k.id] = k.secret || f?.masked ? '' : f?.set || k.default !== undefined ? (f?.value ?? k.default ?? '') : '';
  }
  return d;
}

/** Changes to send: draft values that differ from what is in effect. Empty secrets mean "keep". */
export function draftChanges(fields: Field[], keys: SchemaKey[], draft: Record<string, string>, skip: (id: string) => boolean = () => false): FormChange[] {
  const out: FormChange[] = [];
  for (const k of keys) {
    if (skip(k.id)) continue;
    const f = fieldById(fields, k.id);
    if (!f || !f.canSet) continue;
    const v = (draft[k.id] ?? '').trim();
    if (k.secret || f.masked) { if (v !== '') out.push({ id: k.id, value: v }); continue; }
    const cur = f.set ? f.value : (k.default ?? '');
    if (v === cur) continue;
    if (v === '' && !f.set) continue;
    out.push({ id: k.id, value: v });
  }
  return out;
}

/** What a preset sets, for the "this will set" list: label, from, to. */
export function presetPreview(p: Preset, doc: SchemaDoc | null, fields: Field[]): { id: string; label: string; from: string; to: string; same: boolean }[] {
  const rows = Object.keys(p.values).sort().map((id) => {
    const k = keyById(doc, id);
    const f = fieldById(fields, id);
    const from = f ? valueLabel(k, f.value) : '';
    const to = valueLabel(k, p.values[id]);
    return { id, label: k?.label ?? id, from, to, same: f?.value === p.values[id] };
  });
  if (p.hub) rows.push({ id: 'radio', label: 'Radio interface', from: '', to: 'No radio (hub)', same: false });
  return rows;
}

/** Request for the Duplex form. Preset values the operator did not change are left to the preset. */
export function duplexRequest(node: string, fields: Field[], keys: SchemaKey[], draft: Record<string, string>, preset: Preset | null): FormRequest {
  const skip = preset ? (id: string) => draft[id] === preset.values[id] : () => false;
  const changes = draftChanges(fields, keys, draft, skip);
  const req: FormRequest = { node, mode: 'override', changes };
  if (preset) req.preset = preset.name;
  return req;
}

/** Request for one field action: reset to the template, or change the template for all nodes. */
export function fieldActionRequest(node: string, mode: 'reset' | 'template', id: string, value: string): FormRequest {
  return { node, mode, changes: [{ id, value: mode === 'reset' ? '' : value.trim() }] };
}

/** Pulls the server's refusal, blocking plan or conflict list out of an ApiError body. */
export function errorParts(body: Record<string, unknown> | undefined): { refusal?: Refusal; plan?: Plan; files?: string[] } {
  if (!body) return {};
  const out: { refusal?: Refusal; plan?: Plan; files?: string[] } = {};
  const r = body.refusal as Refusal | undefined;
  if (r && typeof r.message === 'string') out.refusal = r;
  const p = body.plan as Plan | undefined;
  if (p && Array.isArray(p.files)) out.plan = p;
  if (Array.isArray(body.files)) out.files = body.files.filter((x): x is string => typeof x === 'string');
  return out;
}

const RADIO_NAMES: Record<string, string> = { simpleusb: 'SimpleUSB interface', usbradio: 'USBRadio interface', voter: 'Voter', usrp: 'USRP', hub: 'No radio (hub)', other: 'Other interface', none: 'No radio configured' };
export function radioSummary(r: Radio): string {
  const name = RADIO_NAMES[r.type] ?? r.type;
  return r.channel && r.type !== 'hub' ? `${name} (${r.channel})` : name;
}

export function registrationSummary(r: Registration): string {
  const where = r.host ? ` at ${r.host}` : '';
  const st = r.status ? { registered: ', registered', failed: ', registration failing', unknown: '' }[r.status.state] ?? '' : '';
  switch (r.method) {
    case 'http': return `Web registration${where}${st}`;
    case 'iax': return `IAX registration${where}${st}`;
    case 'both': return 'Registered both ways (a problem: use one)';
    default: return 'Not registered with the AllStar network';
  }
}

export function echolinkSummary(e: EchoLink): string {
  if (!e.present) return 'No echolink.conf';
  if (e.enabled) return 'On';
  if (!e.configured) return 'Off (no EchoLink callsign set)';
  if (!e.linked) return 'Set up for another node';
  return 'Off (driver not loaded)';
}

/** Lists the keys of a node that a card shows read-only. */
export function summaryLine(doc: SchemaDoc | null, fields: Field[], ids: string[]): string {
  const parts: string[] = [];
  for (const id of ids) {
    const f = fieldById(fields, id);
    if (!f || !f.set) continue;
    const k = keyById(doc, id);
    parts.push(`${(k?.label ?? f.key).toLowerCase()}: ${f.masked ? 'set' : valueLabel(k, f.value)}`);
  }
  return parts.join(' · ');
}

/** One-line description of a plan change for the review list. */
export function changeLine(c: PlanChange): string {
  const what = `${c.key} in ${c.file}${c.section && c.section !== 'general' ? ` [${c.section}]` : ''}`;
  switch (c.action) {
    case 'add': return `Add ${what}: ${c.to ?? ''}`;
    case 'remove': return `Remove ${what}${c.from ? ` (was ${c.from})` : ''}`;
    case 'set': return `Change ${what}: ${c.from ?? ''} to ${c.to ?? ''}`;
    default: return `${what} is already as wanted`;
  }
}

const reCallsign = /^[A-Z0-9]{1,3}[0-9][A-Z0-9]{0,4}[A-Z](\/[A-Z0-9]{1,4})?$/;
/** Mirrors the server's callsign check (the server decides). */
export function validCallsign(s: string): boolean {
  const c = s.trim().toUpperCase();
  return c.length >= 3 && c.length <= 12 && reCallsign.test(c);
}

/** True when idrecording names a recording instead of a Morse `|i<call>` ID: the callsign cannot be edited as a callsign. */
export function customIdRecording(idrecording: Field | undefined): boolean {
  return !!idrecording && idrecording.set && !idrecording.value.trim().startsWith('|i');
}

/** Request for the Identity form. The callsign is sent only when it changed. */
export function identityRequest(node: NodeModel, keys: SchemaKey[], draft: Record<string, string>, callsign: string): FormRequest {
  const req: FormRequest = { node: node.node, mode: 'override', changes: draftChanges(node.fields, keys, draft) };
  const cs = callsign.trim().toUpperCase();
  if (cs && cs !== node.callsign.toUpperCase()) req.callsign = cs;
  return req;
}

/** Why the registration form cannot be reviewed yet ('' when it can). */
export function registrationProblem(cur: Registration, method: string, host: string, password: string): string {
  if (method === 'none') return '';
  if (!/^[A-Za-z0-9.-]{1,253}$/.test(host.trim())) return 'Enter the server name, for example register.example.net';
  const had = method === 'http' ? cur.http : cur.iax;
  const hasPass = !!had?.hasPassword || (!!cur.http?.hasPassword || !!cur.iax?.hasPassword);
  if (!password && !hasPass) return 'Enter the node password';
  if (password && !/^[^\s@;\\]{1,64}$/.test(password)) return 'Up to 64 characters, no spaces, @, ; or \\';
  return '';
}

/** Request for the Registration form; null when nothing differs. An empty password keeps the saved one. */
export function registrationRequest(node: NodeModel, method: 'none' | 'http' | 'iax', host: string, password: string): FormRequest | null {
  const cur = node.registration;
  if (method === 'none') return cur.method === 'none' ? null : { node: node.node, mode: 'override', registration: { method } };
  const sameHost = host.trim() === (cur.host ?? '');
  if (cur.method === method && sameHost && !password) return null;
  const r: NonNullable<FormRequest['registration']> = { method, host: host.trim() };
  if (password) r.password = password;
  return { node: node.node, mode: 'override', registration: r };
}

/** Request for the EchoLink form: changed settings, plus linking this node and loading the driver when asked. */
export function echolinkRequest(node: NodeModel, keys: SchemaKey[], draft: Record<string, string>, enable: boolean): FormRequest | null {
  const fields = node.echolink.fields ?? [];
  const changes = draftChanges(fields, keys, draft);
  const req: FormRequest = { node: node.node, mode: 'override', changes };
  const needLink = enable && !node.echolink.linked;
  const needModule = enable && !/^(explicit|auto)$/.test(node.echolink.moduleState);
  if (needLink) req.link = true;
  if (needModule && !needLink) req.modules = ['chan_echolink.so'];
  return changes.length || req.link || req.modules ? req : null;
}

/** True when any draft value is invalid, for enabling "Review changes". */
export function draftInvalid(fields: Field[], keys: SchemaKey[], draft: Record<string, string>): boolean {
  return keys.some((k) => validateValue(k, draft[k.id] ?? '', !!fieldById(fields, k.id)?.set));
}

/** The stock status reporting address (the commented example in the ASL3 rpt.conf). */
export const STATPOST_DEFAULT_URL = 'http://stats.allstarlink.org/uhandler';

/** '' when s is a web address the server accepts for statpost_url. */
export function statpostError(s: string): string {
  if (!/^https?:\/\/[^\s"'`$\\/]+(\/\S*)?$/.test(s) || /[$"'`\\]/.test(s)) return 'A web address starting with http:// or https://';
  return '';
}

export const STATUS_IDS = ['rpt.conf:node:statpost_url', 'rpt.conf:node:statpost_time'];

/** Status reporting as the form shows it: on when a statpost_url is in effect. */
export function statusState(fields: Field[]): { on: boolean; url: string; time: string; fromTemplate: boolean } {
  const u = fieldById(fields, STATUS_IDS[0]), t = fieldById(fields, STATUS_IDS[1]);
  return { on: !!u?.set && u.value !== '', url: u?.set ? u.value : STATPOST_DEFAULT_URL, time: t?.set ? t.value : '60', fromTemplate: !!u?.set && u.source.kind === 'template' };
}

/** One line for the overview card. */
export function statusSummary(fields: Field[]): string {
  const s = statusState(fields);
  if (!s.on) return 'Off: this node does not report its status.';
  const stock = s.url === STATPOST_DEFAULT_URL ? 'the AllStarLink statistics page' : s.url;
  return `On: reports to ${stock} every ${s.time} s${s.fromTemplate ? ' (set for all nodes)' : ''}.`;
}

/**
 * Request of the Status reporting form. Off removes the node's own line (a value that comes from a template
 * stays: the form says so and "Change for all nodes" is a separate, explicit action).
 */
export function statusRequest(node: string, fields: Field[], on: boolean, url: string, time: string): FormRequest | null {
  const cur = statusState(fields);
  const u = fieldById(fields, STATUS_IDS[0]), t = fieldById(fields, STATUS_IDS[1]);
  if (!on) {
    if (!u?.set || u.source.kind !== 'node') return null;
    return { node, mode: 'reset', changes: [{ id: STATUS_IDS[0], value: '' }, ...(t?.set && t.source.kind === 'node' ? [{ id: STATUS_IDS[1], value: '' }] : [])] };
  }
  const changes: FormChange[] = [];
  if (!cur.on || url.trim() !== cur.url) changes.push({ id: STATUS_IDS[0], value: url.trim() });
  if (time.trim() !== cur.time) changes.push({ id: STATUS_IDS[1], value: time.trim() });
  return changes.length ? { node, mode: 'override', changes } : null;
}
