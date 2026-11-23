// Pure logic of the Add node, Change node number and Change radio interface wizards and of the AMI and
// SimpleUSB forms (docs/CONFIG.md, "Add a node", "Change the node number", "Change the radio interface").
// The answers' checks are the setup wizard's (nodesetup.ts): one set of rules for all three wizards.
import { draftChanges, type Field, type FormRequest, type NodeModel, type Radio, type SchemaKey } from './nodecfg.ts';
import { nodeKind, stepError, validNode, validPassword, type SetupDraft, type SetupRadio, type WizardStep } from './nodesetup.ts';

export interface AddNodeBody {
  node: string; password: string; callsign: string; radio: SetupRadio; preset: string;
  registration?: { method: 'http' | 'iax' | 'none'; host?: string };
  /** The node whose template the new one inherits. */
  from?: string;
}
export interface RenumberBody { oldNode: string; node: string; password?: string; savenode: boolean }

/** Pages of the Add node wizard: the setup wizard's, without EchoLink. */
export const ADD_STEPS = ['node', 'identity', 'radio', 'duplex', 'registration', 'review'] as const;
export type AddStep = (typeof ADD_STEPS)[number] & WizardStep;

/** The first problem on an Add node page, or '' when it can be left. `existing` are the node numbers already in use. */
export function addStepError(step: AddStep, d: SetupDraft, presets: Parameters<typeof stepError>[2], existing: string[]): string {
  const e = stepError(step, d, presets);
  if (e) return e;
  if (step === 'node' && existing.includes(d.node.trim())) return `Node ${d.node.trim()} already exists on this system.`;
  return '';
}

export function addNodeBody(d: SetupDraft, from?: string): AddNodeBody {
  const b: AddNodeBody = { node: d.node.trim(), password: d.password, callsign: d.callsign.trim().toUpperCase(), radio: d.radio, preset: d.preset };
  b.registration = nodeKind(d.node) === 'public' ? { method: d.regMethod, ...(d.regHost.trim() ? { host: d.regHost.trim() } : {}) } : { method: 'none' };
  if (from) b.from = from;
  return b;
}

export interface RenumberDraft { node: string; password: string; savenode: boolean }
export const emptyRenumber = (): RenumberDraft => ({ node: '', password: '', savenode: false });

/** What the renumber question needs: the password is needed to register a public node and to write savenode.conf. */
export function renumberNeedsPassword(registered: boolean, d: RenumberDraft): boolean {
  return d.savenode || (registered && nodeKind(d.node) === 'public');
}

export function renumberError(old: string, d: RenumberDraft, registered: boolean, existing: string[]): string {
  const n = d.node.trim();
  if (!validNode(n)) return 'A node number is 1 to 7 digits, for example 1999 (private) or 2001 (public).';
  if (n === old) return 'That is the current number.';
  if (existing.includes(n)) return `Node ${n} already exists on this system.`;
  if (renumberNeedsPassword(registered, d) && !d.password) return 'Enter the new node\'s password.';
  if (d.password && !validPassword(d.password)) return 'The password may use letters, digits and . _ + = , : @ % - (up to 64).';
  return '';
}

export function renumberBody(old: string, d: RenumberDraft, registered: boolean): RenumberBody {
  const b: RenumberBody = { oldNode: old, node: d.node.trim(), savenode: d.savenode };
  if (d.password && renumberNeedsPassword(registered, d)) b.password = d.password;
  return b;
}

/** What a renumber changes in words, by what the node has today (the review page repeats it from the server's plan). */
export function renumberKindNote(oldNode: string, d: RenumberDraft, registered: boolean): string {
  const k = nodeKind(d.node);
  if (!k) return '';
  const was = nodeKind(oldNode);
  if (k === 'private' && registered) return 'A private number (1 to 1999) is not registered with AllStarLink: the registration line is removed.';
  if (k === 'public' && was === 'private') return 'A public number (2000 and up) is registered with AllStarLink: get the number and password from the portal first.';
  return '';
}

export interface RadioOption { value: SetupRadio; label: string; desc: string; current: boolean }
/** The interfaces the Change radio interface wizard offers (SimpleUSB and hub only), marking the current one. */
export function radioOptions(cur: Radio): RadioOption[] {
  return [
    { value: 'simpleusb', label: 'USB sound interface (SimpleUSB)', desc: 'A CM1xx style USB sound card with a radio. Its device section in simpleusb.conf is created when missing.', current: cur.type === 'simpleusb' },
    { value: 'hub', label: 'Hub (no radio)', desc: 'A node that only links other nodes. The SimpleUSB device section and the driver stay as they are, so you can switch back.', current: cur.type === 'hub' },
  ];
}

/** Why the radio wizard is not offered for this node ('' when it is). */
export function radioWizardBlock(n: NodeModel): string {
  if (n.refusals?.length) return n.refusals[0].message;
  if (!n.radio.supported) return n.radio.note ?? 'This interface is edited in Advanced; the wizard supports SimpleUSB and hub only.';
  return '';
}

/** Request of the Change radio interface wizard; null when the node already uses that interface. */
export function radioChangeRequest(node: NodeModel, to: SetupRadio): FormRequest | null {
  return node.radio.type === to ? null : { node: node.node, mode: 'override', radio: { type: to } };
}

/** Request of the SimpleUSB device form: changed device settings only. */
export function deviceRequest(node: NodeModel, keys: SchemaKey[], draft: Record<string, string>): FormRequest | null {
  const changes = draftChanges(node.radio.deviceFields ?? [], keys, draft);
  return changes.length ? { node: node.node, mode: 'override', changes } : null;
}

/** True when an AMI listen address is this computer only (mirrors the server; empty means every interface). */
export function bindIsLoopback(addr: string): boolean {
  let h = addr.trim();
  if (!h) return false;
  const m = /^\[(.*)\](?::\d+)?$/.exec(h);
  if (m) h = m[1]; else if ((h.match(/:/g) ?? []).length === 1) h = h.split(':')[0];
  return h === 'localhost' || h === '::1' || /^127(\.\d{1,3}){3}$/.test(h);
}

/** The warning shown next to the AMI listen address, or ''. */
export function bindWarning(addr: string): string {
  return bindIsLoopback(addr) ? '' : 'This lets other computers reach the manager interface. Anyone who can reach it and knows the password controls Asterisk. Use 127.0.0.1 unless another computer needs it.';
}

/** Request of the AMI form. The password is sent only when one was typed or generated; an empty one keeps the saved one. */
export function amiRequest(node: string, fields: Field[], keys: SchemaKey[], draft: Record<string, string>): FormRequest | null {
  const changes = draftChanges(fields, keys, draft);
  return changes.length ? { node, mode: 'override', changes } : null;
}

/** A random manager password for the "Generate" button: it goes into a hidden input and is never shown. */
export function randomSecret(len = 32, rnd: (n: number) => number = (n) => crypto.getRandomValues(new Uint32Array(1))[0] % n): string {
  const abc = 'abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789';
  let s = '';
  for (let i = 0; i < len; i++) s += abc[rnd(abc.length)];
  return s;
}

/** The linking card's keys (rpt.conf, per node) in schema order. */
export const linkingKeys = (keys: SchemaKey[]): SchemaKey[] => keys.filter((k) => k.form === 'linking' && k.file === 'rpt.conf' && k.scope === 'node');
