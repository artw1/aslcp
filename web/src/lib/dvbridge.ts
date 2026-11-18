// Pure logic of the bridge node (docs/DVSWITCH.md "Private bridge node"): node validation and suggestion
// (the same rules as the server), the port prefill, the card's link text and buttons, and the confirmation texts.
import type { DVBridgeSetup, DVLink, DVPlan, DVSnapshot } from './types';

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

/** A warning for a node that is valid but unwise (1998 and 1999 are what other DVSwitch guides use), else null. */
export function nodeWarning(node: string): string | null {
  const n = node.trim();
  if (n === '1998' || n === '1999') return `${n} is the number other DVSwitch guides use for a public node. Pick one in ${BRIDGE_MIN}-${BRIDGE_MAX - 2} to avoid a clash.`;
  return null;
}

/** Why `node` cannot be the bridge node (null: fine). `own` is the bridge node that already exists, if any. */
export function nodeError(node: string, used: string[], own = ''): string | null {
  const n = node.trim();
  if (!/^[0-9]{1,7}$/.test(n) || Number(n) < 1) return 'Enter a node number (digits only).';
  if (Number(n) >= 2000) return 'A private node must be below 2000: 2000 and up are public AllStar nodes.';
  if (own && n !== own) return `This node already has the bridge node ${own}. Change its ports or link, or uninstall DVSwitch first.`;
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
    localNode: b.link.localNode || b.localNodes.find((n) => n !== b.node) || '',
  };
}

/** Why the form cannot be planned yet (null: it can). */
export function formError(f: { node: string; rxPort: string; txPort: string; linkMode: string; localNode: string }, b: Pick<DVBridgeSetup, 'node' | 'used'>): string | null {
  const e = nodeError(f.node, b.used, b.node);
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
    : { label: 'Link node', action: 'connect', title: `Connect bridge node ${node} to ${l.localNode}` };
}

/** A permanent link that was disconnected by hand: say how to get it back. */
export const pausedHint = (l: DVLink): string => (l.mode === 'permanent' && l.paused && !l.linked ? 'The permanent link is paused until you save the bridge node setup or restart Asterisk.' : '');

/** What the confirmation says before an apply. */
export function planConfirm(p: Pick<DVPlan, 'node' | 'restartAsterisk' | 'restarts'>): string {
  const parts: string[] = [];
  if (p.restartAsterisk) parts.push('This restarts Asterisk, which drops every connection and call on this node for a moment');
  const r = p.restarts.filter((x) => x !== 'asterisk');
  if (r.length) parts.push(`${p.restartAsterisk ? 'then restarts' : 'This restarts'} ${r.join(', ')}`);
  if (!parts.length) return `Save the bridge node ${p.node}. Nothing needs a restart.`;
  return `${parts.join(', ')}. Bridge node ${p.node} is added or updated.`;
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
