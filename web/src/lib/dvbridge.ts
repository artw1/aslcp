// Pure logic of the bridge node (docs/DVSWITCH.md "Private bridge node"): node validation and suggestion
// (the same rules as the server), the port prefill, the card's link text and buttons, and the confirmation texts.
import type { DVBridgeSetup, DVLink, DVPlan, DVPlanKind, DVSnapshot } from './types';

/** The text that replaces the disabled Duplex input: duplex stays 0 for a bridge node. */
export const DUPLEX_TEXT = 'Duplex 0: fixed for a bridge node (a higher value repeats DMR audio back to DMR and adds hang time and courtesy tones).';
/** The hint under the node field of an existing bridge node. */
export const NODE_HINT = 'To use a different number, use Change number. Remove bridge node takes it out completely.';

export const BRIDGE_MIN = 1900;
export const BRIDGE_MAX = 1997;
export const DEFAULT_RX = 34001;
export const DEFAULT_TX = 32001;

/** The first free node of 1900-1997 that is not in `used` ("" when none is left). */
export function suggestNode(used: string[]): string {
  const u = new Set(used);
  for (let n = BRIDGE_MIN; n <= BRIDGE_MAX; n++) if (!u.has(String(n))) return String(n);
  return '';
}

/** Why `node` cannot be the bridge node (null: fine). `own` is the bridge node that already exists, if any. */
/** `renumbering` is the Change number mode: a different number than `own` is then what the form is for. */
export function nodeError(node: string, used: string[], own = '', renumbering = false): string | null {
  const n = node.trim();
  if (!/^[0-9]{1,7}$/.test(n) || Number(n) < 1) return 'Enter a node number (digits only).';
  if (Number(n) >= 2000) return 'A private node must be below 2000: 2000 and up are public AllStar nodes.';
  if (own && n !== own && !renumbering) return `This node already has the bridge node ${own}. Change its ports or link, use Change number for another number, or remove it.`;
  if (n !== own && used.includes(n)) return `Node ${n} is already in use on this node.`;
  return null;
}

export const validPort = (p: string | number): boolean => { const s = String(p).trim(); const n = Number(s); return /^[0-9]{1,5}$/.test(s) && n >= 1024 && n <= 65535; };

/** The form's first values: the existing bridge node and ports, else the suggestion and the stock ports. */
export function prefill(b: Pick<DVBridgeSetup, 'node' | 'suggested' | 'rxPort' | 'txPort' | 'link' | 'localNodes'>) {
  return {
    node: b.node || b.suggested,
    rxPort: String(b.rxPort || DEFAULT_RX),
    txPort: String(b.txPort || DEFAULT_TX),
    linkMode: b.link.mode,
    linkExclusive: b.link.exclusive,
    localNode: b.link.localNode || b.localNodes.find((n) => n !== b.node) || '',
  };
}

/** Why the form cannot be planned yet (null: it can). */
export function formError(f: { node: string; rxPort: string; txPort: string; linkMode: string; localNode: string }, b: Pick<DVBridgeSetup, 'node' | 'used'>, renumbering = false): string | null {
  const e = nodeError(f.node, b.used, b.node, renumbering);
  if (e) return e;
  if (!validPort(f.rxPort) || !validPort(f.txPort)) return 'Ports are numbers from 1024 to 65535.';
  if (f.rxPort.trim() === f.txPort.trim()) return 'The receive and transmit ports must differ.';
  if (!f.localNode) return 'Pick the local node to link the bridge node to.';
  if (f.localNode === f.node.trim()) return 'The bridge node cannot link to itself.';
  return null;
}

/** The card's bridge-node line: "Bridge node 1999 linked to 2006 (permanent)". */
export function linkLine(node: string, l: DVLink): string {
  const how = l.mode === 'permanent' ? 'permanent' : 'on demand';
  if (l.mode === 'permanent') {
    if (l.linked) return `Bridge node ${node} linked to ${l.localNode} (${how})`;
    if (l.paused) return `Bridge node ${node} not linked to ${l.localNode} (${how}, paused: you disconnected it)`;
    return `Bridge node ${node} not linked to ${l.localNode} (${how}, reconnecting)`;
  }
  if (!l.localNode) return `Bridge node ${node}, no local node chosen`;
  return l.linked ? `Bridge node ${node} linked to ${l.localNode} (${how})` : `Bridge node ${node} not linked to ${l.localNode} (${how})`;
}

/** The node link button of the card. Ondemand mode only; the talkgroup button stays "Unlink" and is a different thing. */
export function linkButton(l: DVLink, node: string): { label: string; action: 'connect' | 'disconnect'; title: string } | null {
  if (!node || !l.localNode) return null;
  return l.linked
    ? { label: 'Unlink node', action: 'disconnect', title: `Disconnect bridge node ${node} from ${l.localNode}` }
    : { label: 'Link node', action: 'connect', title: `Connect bridge node ${node} to ${l.localNode}${l.exclusive ? ' (disconnects other links first)' : ''}` };
}

/** Whether the Link node click sends an exclusive connect (the saved setting; the analog Connect's flag, same backend path). */
export const linkExclusiveOnClick = (l: Pick<DVLink, 'exclusive'>): boolean => l.exclusive === true;

/** The hint under the "Disconnect other links first" checkbox: where it applies and where it does not. */
export function exclusiveHint(mode: string, on: boolean): string {
  if (!on) return 'Other links of the local node are left alone.';
  return mode === 'permanent'
    ? 'Applies when you save here: the other links of the local node are disconnected, then the bridge node is linked. Automatic relinks after an Asterisk restart never disconnect anything.'
    : 'The Link node button on the dashboard disconnects the other links of the local node first. Permanent links stay.';
}

/** A permanent link that was disconnected by hand: say how to get it back. */
export const pausedHint = (l: DVLink): string => (l.mode === 'permanent' && l.paused && !l.linked ? 'The permanent link is paused until you save the bridge node setup or restart Asterisk.' : '');

/** Change number and Remove bridge node exist only for a bridge node that ASLCP added (the server refuses the rest). */
export const canChange = (b: Pick<DVBridgeSetup, 'node' | 'owned'>): boolean => !!b.node && b.owned;

type PlanLike = Pick<DVPlan, 'node' | 'restartAsterisk' | 'restarts'> & Partial<Pick<DVPlan, 'kind' | 'oldNode' | 'unlinks' | 'keepRecord'>>;

/** The review dialog's title. */
export function planTitle(kind: DVPlanKind | undefined): string {
  return kind === 'remove' ? 'Remove the bridge node?' : kind === 'renumber' ? 'Change the bridge node number?' : 'Apply the bridge node?';
}

/** The confirm button of the review dialog. */
export function planApplyLabel(p: Pick<PlanLike, 'kind' | 'restartAsterisk'>, busy: boolean): string {
  const verb = p.kind === 'remove' ? 'Remove' : p.kind === 'renumber' ? 'Change number' : 'Apply';
  if (busy) return p.kind === 'remove' ? 'Removing…' : 'Applying…';
  return p.restartAsterisk ? `${verb} and restart Asterisk` : verb;
}

/** The order the server works in, for the dialog's footer line. */
export function planOrder(p: Pick<PlanLike, 'kind' | 'unlinks'>): string {
  const first = p.unlinks?.length ? `Disconnect ${p.unlinks.join(', ')} from the bridge node, then ` : '';
  const rest = p.kind === 'remove'
    ? 'revert the Asterisk config, restore the Analog_Bridge ini ports, restart Asterisk once and the bridge, then clear the setup. If it fails after the restart you can roll back.'
    : 'Asterisk config, the Analog_Bridge ini, then the restarts and a health check. If the check fails you can roll back both files.';
  return first ? first + rest : rest.charAt(0).toUpperCase() + rest.slice(1);
}

/** The toast after a successful apply. */
export function doneText(r: { kind?: DVPlanKind; node: string; keepRecord?: boolean; warnings?: string[] }): string {
  if (r.kind === 'remove') return r.keepRecord ? `Bridge node ${r.node} removed with problems: see the warnings. ASLCP keeps its record of what it could not revert.` : `Bridge node ${r.node} removed.`;
  return r.kind === 'renumber' ? `Bridge node renumbered to ${r.node}.` : 'Bridge node saved.';
}

/** What the confirmation says before an apply. */
export function planConfirm(p: PlanLike): string {
  const parts: string[] = [];
  if (p.restartAsterisk) parts.push('This restarts Asterisk, which drops every connection and call on this node for a moment');
  const r = p.restarts.filter((x) => x !== 'asterisk');
  if (r.length) parts.push(`${p.restartAsterisk ? 'then restarts' : 'This restarts'} ${r.join(', ')}`);
  const what = p.kind === 'remove' ? `Bridge node ${p.node} is removed completely.` : p.kind === 'renumber' ? `Bridge node ${p.oldNode ?? ''} becomes ${p.node}.` : `Bridge node ${p.node} is added or updated.`;
  if (!parts.length) return `Save the bridge node ${p.node}. Nothing needs a restart.`;
  return `${parts.join(', ')}. ${what}`;
}

/** The uninstall confirmation's extra lines for the bridge node (Asterisk restarts, a permanent link is removed). */
export function uninstallNote(s: Pick<DVSnapshot, 'bridgeNode' | 'link'> | null | undefined, owned: boolean): string[] {
  const out: string[] = [];
  if (s?.bridgeNode?.present && owned) {
    out.push(`Removes the bridge node ${s.bridgeNode.node ?? ''} that ASLCP added from rpt.conf, and restores modules.conf if ASLCP changed it, then restarts Asterisk. That drops every connection on this node for a moment.`.replace('  ', ' '));
    if (s.link.mode === 'permanent') out.push('The permanent link is disconnected first.');
  } else if (s?.bridgeNode?.present) out.push('The bridge node was not added by ASLCP and stays as it is.');
  return out;
}
