// Pure logic of the overview dashboard: pinned favourites, the bridge-node filter for the node tiles,
// the alerts strip and the merged activity feed. Tested in pins.test.ts.
import { bridgeLinked, callerLabel, isDMRFamily, modeLabel, nodeLabel, PRIVATE_MARK } from './dvpanel.ts';
import { who } from './nowcalling.ts';
import type { DVCaller, DVFavorite, DVSnapshot, DVTuneMode, Favorite, NodeStatus, NowCall } from './types';

// A pin is the `pinned` flag of a favourite, stored on the server (GET/PUT /api/favorites, /api/dv/favorites).

/** The local node that shows as a tile: every node except the digital modes bridge node (it only links DV in). */
export function bridgeNodeNumber(dv: Pick<DVSnapshot, 'bridgeNode'> | null | undefined): string {
  return dv?.bridgeNode?.present && dv.bridgeNode.node ? dv.bridgeNode.node : '';
}
export function tileNodes<N extends { node: string }>(nodes: N[], dv: Pick<DVSnapshot, 'bridgeNode'> | null | undefined): N[] {
  const bridge = bridgeNodeNumber(dv);
  return bridge ? nodes.filter((n) => n.node !== bridge) : nodes;
}

/** The local node an AllStar pin connects from: the selected one, else the first tile node; '' when there is none. */
export function pinLocalNode(selected: string, nodes: { node: string }[]): string {
  return nodes.find((n) => n.node === selected)?.node ?? nodes[0]?.node ?? '';
}

export interface PinItem {
  key: string; kind: 'asl' | 'dv'; label: string; sub: string;
  /** asl: the node to connect to. dv: the mode and value to tune. */
  node?: string; fav?: DVFavorite;
}
/** The pinned favourites: AllStar ones first, then digital modes ones, each in the order of its list. A deleted favourite takes its pin with it. */
export function buildPins(asl: Favorite[], dv: DVFavorite[]): PinItem[] {
  const out: PinItem[] = [];
  for (const f of asl) if (f.pinned) out.push({ key: `asl:${f.id}`, kind: 'asl', label: f.label || f.info?.callsign || f.node, sub: f.node, node: f.node });
  for (const f of dv) if (f.pinned) out.push({ key: `dv:${f.id}`, kind: 'dv', label: f.name, sub: modeLabel(f.mode), fav: f });
  return out;
}

/** What one tap on a DV pin does: switch the mode when it differs, tune the target, link the bridge node when it is not linked. */
export interface DvPinPlan { mode: DVTuneMode | null; mode0: string; tune: string; link: { local: string; node: string; exclusive: boolean } | null }
export function dvPinPlan(snap: DVSnapshot | null | undefined, fav: Pick<DVFavorite, 'mode' | 'value'>): DvPinPlan | null {
  if (!snap?.installed || !snap.configured) return null;
  const cur = snap.bridge.source !== 'none' ? snap.bridge.mode : '';
  const bn = snap.bridgeNode, l = snap.link;
  const link = bn?.present && bn.node && l && l.mode === 'ondemand' && l.localNode && !bridgeLinked(snap)
    ? { local: l.localNode, node: bn.node, exclusive: l.exclusive === true } : null;
  return { mode: cur === fav.mode ? null : fav.mode, mode0: cur, tune: fav.value, link };
}

export interface Alert { key: string; text: string; href?: string }
/** What is wrong, for the alerts strip. Empty when all is well (or still loading). */
export function buildAlerts(nodes: NodeStatus[], haveNodes: boolean, dv: DVSnapshot | null | undefined): Alert[] {
  const out: Alert[] = [];
  if (haveNodes && nodes.length === 0) out.push({ key: 'nonodes', text: 'No local nodes found. Is Asterisk running with app_rpt?', href: '/allstar' });
  for (const n of nodes) if (n.stale) out.push({ key: `stale:${n.node}`, text: `Node ${n.node} status is stale${n.staleReason ? ` (${n.staleReason})` : ''}`, href: '/allstar' });
  if (dv?.installed) {
    if (!dv.running) out.push({ key: 'dvdown', text: 'The digital modes engine is not running.', href: '/dv' });
    else {
      for (const m of dv.modes ?? []) {
        if (m.enabled && m.login === 'failed' && isDMRFamily(m.id)) out.push({ key: `login:${m.id}`, text: `${modeLabel(m.id)} login failed${m.detail ? `: ${m.detail}` : ''}`, href: '/dv' });
      }
      if (dv.bridgeNode?.present && dv.link && !bridgeLinked(dv)) {
        out.push({ key: 'bridge', text: dv.link.mode === 'permanent' && !dv.link.paused ? 'The bridge node link is down.' : 'The bridge node is not linked to a local node.', href: '/dv' });
      }
    }
  }
  return out;
}

export const dvAt = (c: DVCaller): number => { const t = c.at ? Date.parse(c.at.replace(' ', 'T')) : NaN; return Number.isFinite(t) ? t : 0; };

/** One connected link on a node tile: its node number and the callsign or name when known. */
export interface TileLink { node: string; label: string }
/** The first `max` links of a node for its tile (keyed first, then the newest connection, as in the status table) and how many more there are. */
export function tileLinks(n: Pick<NodeStatus, 'links'>, dv: Pick<DVSnapshot, 'bridgeNode'> | null | undefined, max = 3): { shown: TileLink[]; more: number } {
  const sorted = [...n.links].sort((a, b) => Number(b.keyed) - Number(a.keyed) || a.connected - b.connected);
  const shown = sorted.slice(0, max).map((l) => ({
    node: l.node,
    label: l.info?.callsign || nodeLabel(dv, l.node) || l.info?.description || (l.kind === 'echolink' ? 'EchoLink' : ''),
  }));
  return { shown, more: Math.max(0, sorted.length - shown.length) };
}

/** Who is talking on a keyed node tile: the callsign (or ID) of the Now Calling talker; '' when the node is idle or the talker is not known.
 *  An AllStar call belongs to the local node it came in on; a digital call comes in over the bridge node's link, so it belongs to the node whose bridge link is keyed. */
export function tileTalker(n: Pick<NodeStatus, 'node' | 'rxKeyed' | 'txKeyed' | 'links'>, current: NowCall | null | undefined, dv: Pick<DVSnapshot, 'bridgeNode'> | null | undefined): string {
  if (!current || !(n.rxKeyed || n.txKeyed)) return '';
  if (current.kind === 'allstar') return current.local === n.node ? who(current) : '';
  const bridge = bridgeNodeNumber(dv);
  return bridge && n.links.some((l) => l.node === bridge && l.keyed) ? who(current) : '';
}

/** The newest DV caller for the DV tile, with when it was heard (ms, 0 when the log time is unknown); null when nobody was heard or there is no name. */
export function dvLastCaller(dv: Pick<DVSnapshot, 'heard'> | null | undefined): { who: string; at: number } | null {
  const h = dv?.heard?.[0];
  const w = h ? callerLabel(h) : '';
  return h && w ? { who: w, at: dvAt(h) } : null;
}

/** An AllStar pin is active when its target node is linked to any local node. */
export function aslPinActive(target: string | undefined, nodes: Pick<NodeStatus, 'links'>[]): boolean {
  return !!target && nodes.some((n) => n.links.some((l) => l.node === target));
}
/** A DV pin is active when the mode and the target match what is tuned now. */
export function dvPinActive(snap: Pick<DVSnapshot, 'bridge'> | null | undefined, fav: Pick<DVFavorite, 'mode' | 'value'> | undefined): boolean {
  if (!snap || !fav || snap.bridge.source === 'none' || snap.bridge.mode !== fav.mode) return false;
  const tuned = isDMRFamily(fav.mode) && snap.bridge.private && snap.bridge.tune ? snap.bridge.tune + PRIVATE_MARK : snap.bridge.tune;
  return tuned !== '' && tuned === fav.value;
}
/** Whether a pin shows the green dot. */
export function pinActive(p: PinItem, nodes: Pick<NodeStatus, 'links'>[], snap: Pick<DVSnapshot, 'bridge'> | null | undefined): boolean {
  return p.kind === 'asl' ? aslPinActive(p.node, nodes) : dvPinActive(snap, p.fav);
}
