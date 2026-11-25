// Pure logic of the overview dashboard: pinned favourites, the bridge-node filter for the node tiles,
// the alerts strip and the merged activity feed. Tested in pins.test.ts.
import { bridgeLinked, heardLine, modeLabel } from './dvpanel.ts';
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
        if (m.enabled && m.login === 'failed') out.push({ key: `login:${m.id}`, text: `${modeLabel(m.id)} login failed${m.detail ? `: ${m.detail}` : ''}`, href: '/dv' });
      }
      if (dv.bridgeNode?.present && dv.link && !bridgeLinked(dv)) {
        out.push({ key: 'bridge', text: dv.link.mode === 'permanent' && !dv.link.paused ? 'The bridge node link is down.' : 'The bridge node is not linked to a local node.', href: '/dv' });
      }
    }
  }
  return out;
}

export interface Activity { key: string; src: 'ASL' | 'DV'; text: string; at: number }
const dvAt = (c: DVCaller): number => { const t = c.at ? Date.parse(c.at.replace(' ', 'T')) : NaN; return Number.isFinite(t) ? t : 0; };
const aslText = (c: NowCall): string => [(c.call ?? '').trim() || (c.node ? `Node ${c.node}` : 'Unknown'), c.node && c.call ? `node ${c.node}` : '', (c.desc ?? '').trim()].filter(Boolean).join(' · ');
/** AllStar last heard (the Now Calling list, AllStar calls only) and DV last heard merged, newest first. */
export function mergeActivity(recent: NowCall[] | undefined, heard: DVCaller[] | undefined, limit = 12): Activity[] {
  const out: Activity[] = [];
  for (const c of recent ?? []) {
    if (c.kind !== 'allstar') continue;
    const t = Date.parse(c.end || c.start);
    out.push({ key: `asl:${c.id}`, src: 'ASL', text: aslText(c), at: Number.isFinite(t) ? t : 0 });
  }
  (heard ?? []).forEach((c, i) => out.push({ key: `dv:${i}:${c.at ?? ''}`, src: 'DV', text: heardLine({ ...c, at: undefined }), at: dvAt(c) }));
  return out.sort((a, b) => b.at - a.at).slice(0, limit);
}
