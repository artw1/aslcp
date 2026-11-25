// In-memory /api/radio/* for `npm run dev:mock` (docs/API.md "Radio tuning"). Dev only. Placeholder node numbers only.
type Params = {
  rxVoice: number; txA: number; txB: number; echo: boolean; rxBoost: boolean; preEmphasis: boolean; deEmphasis: boolean; plFilter: boolean; pttInvert: boolean;
  carrierFrom: string; ctcssFrom: string; rxOnDelayMs: number; txOffDelayMs: number; micPlayMax: number; spkrMax: number; micMax: number;
};
const base = (): Params => ({ rxVoice: 650, txA: 500, txB: 0, echo: false, rxBoost: false, preEmphasis: true, deEmphasis: true, plFilter: true, pttInvert: false, carrierFrom: 'usbinvert', ctcssFrom: 'no', rxOnDelayMs: 0, txOffDelayMs: 0, micPlayMax: 31, spkrMax: 37, micMax: 35 });
interface Dev { name: string; node: string; usbPath: string; card: number; cur: Params; saved: Params; echoUntil: number }
const devs: Dev[] = [
  { name: 'usb0', node: '2000', usbPath: '1-1.1', card: 1, cur: base(), saved: base(), echoUntil: 0 },
  { name: 'usb1', node: '2002', usbPath: '1-1.2', card: 2, cur: base(), saved: base(), echoUntil: 0 },
];
const ECHO_MS = 10 * 60 * 1000;
const SIG = ['no', 'usb', 'usbinvert', 'pp', 'ppinvert'];
const FIELDS = ['rxVoice', 'txA', 'txB', 'rxBoost', 'preEmphasis', 'deEmphasis', 'plFilter', 'pttInvert', 'carrierFrom', 'ctcssFrom', 'rxOnDelayMs', 'txOffDelayMs'] as const;
const dirty = (d: Dev) => FIELDS.filter((f) => d.cur[f] !== d.saved[f]);
const echoOn = (d: Dev) => d.echoUntil > Date.now();

export interface RadioCtx {
  m: string; p: string; body: any; user?: { username: string; role: string };
  json: (code: number, body?: unknown) => void; err: (code: number, msg: string, c: string) => void;
  links: (node: string) => string[];
}

export function handleRadio(c: RadioCtx): boolean {
  const mm = /^\/radio(?:\/([^/]+)(?:\/(params|meter|echo|save|revert))?)?$/.exec(c.p);
  if (!mm) return false;
  if (!c.user) { c.err(401, 'authentication required', 'unauthorized'); return true; }
  if (c.user.role !== 'admin') { c.err(403, 'insufficient permissions', 'forbidden'); return true; }
  const [, name, sub] = mm;
  if (!name) { c.json(200, { available: true, devices: devs.map((d) => ({ name: d.name, node: d.node, usbPath: d.usbPath, card: d.card, links: c.links(d.node) })) }); return true; }
  const d = devs.find((x) => x.name === name);
  if (!d) { c.err(404, 'no such device', 'unknown_device'); return true; }
  if (d.echoUntil && !echoOn(d)) { d.echoUntil = 0; d.cur.echo = false; }
  const echo = () => ({ on: echoOn(d), ...(echoOn(d) ? { expiresAt: new Date(d.echoUntil).toISOString() } : {}) });
  const state = () => ({ device: d.name, node: d.node, params: { ...d.cur, echo: echoOn(d) }, saved: { ...d.saved }, dirty: dirty(d), identity: { device: d.name, usbString: d.usbPath, card: d.card }, echoState: echo() });
  if (!sub && c.m === 'GET') { c.json(200, state()); return true; }
  if (sub === 'meter' && c.m === 'GET') {
    const t = Date.now() / 1000, pk = -30 + 14 * Math.sin(t / 2.1) + 6 * Math.sin(t * 1.3) + (d.cur.rxVoice - 650) / 40;
    c.json(200, { pk: +pk.toFixed(1), avg: Math.round(pk - 17), min: Math.round(pk - 24), max: Math.round(pk - 3), clip: pk > -1 ? 3 : 0, at: new Date().toISOString() });
    return true;
  }
  if (sub === 'params' && c.m === 'PUT') {
    const b = c.body ?? {}, keys = Object.keys(b);
    if (!keys.length) { c.err(400, 'nothing to change', 'invalid'); return true; }
    for (const k of keys) {
      const v = b[k];
      if (!(FIELDS as readonly string[]).includes(k)) { c.err(400, `unknown field ${k}`, 'invalid'); return true; }
      if (['rxVoice', 'txA', 'txB'].includes(k) && !(Number.isInteger(v) && v >= 0 && v <= 999)) { c.err(400, `${k} must be 0 to 999`, 'invalid'); return true; }
      if (['rxOnDelayMs', 'txOffDelayMs'].includes(k) && !(Number.isInteger(v) && v >= 0 && v <= 60000)) { c.err(400, `${k} must be 0 to 60000 ms`, 'invalid'); return true; }
      if (['carrierFrom', 'ctcssFrom'].includes(k) && !SIG.includes(v)) { c.err(400, `${k} must be one of ${SIG.join(', ')}`, 'invalid'); return true; }
    }
    const applied = keys.map((k) => { (d.cur as any)[k] = ['rxOnDelayMs', 'txOffDelayMs'].includes(k) ? Math.round(b[k] / 20) * 20 : b[k]; return { field: k, reply: 'ok' }; });
    c.json(200, { applied, errors: [], params: { ...d.cur, echo: echoOn(d) }, dirty: dirty(d) });
    return true;
  }
  if (sub === 'revert' && c.m === 'POST') {
    for (const f of FIELDS) (d.cur as any)[f] = d.saved[f];
    c.json(200, { applied: FIELDS.map((f) => ({ field: f, reply: 'ok' })), errors: [], params: { ...d.cur, echo: echoOn(d) }, dirty: [] });
    return true;
  }
  if (sub === 'echo' && c.m === 'POST') {
    d.echoUntil = c.body?.on ? Date.now() + ECHO_MS : 0; d.cur.echo = !!c.body?.on;
    c.json(200, echo());
    return true;
  }
  if (sub === 'save' && c.m === 'POST') {
    const changed = dirty(d); d.echoUntil = 0; d.cur.echo = false; d.saved = { ...d.cur };
    c.json(200, { saved: true, params: { ...d.cur }, changed, commit: 'a1b2c3d4e5f6a7b8c9d0a1b2c3d4e5f6a7b8c9d0' });
    return true;
  }
  c.err(405, 'method not allowed', 'method_not_allowed');
  return true;
}
