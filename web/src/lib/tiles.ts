// What a node tile shows, for the Dashboard (NodeStatus, admin API) and the public page (PubNode, public API). Both build the same
// TileView, so NodeTile.svelte looks the same on both and the public one can never show more than the public API sends. Tested in tiles.test.ts.
import { tileLinks, tileTalker } from './pins.ts';
import { qrzUrl, who } from './nowcalling.ts';
import type { DVSnapshot, NodeStatus, NowCall, PubDVCall, PubNode, PubSnapshot } from './types';
import type { TileLink } from './pins.ts';

export interface TileView {
  node: string;
  /** Callsign or description of the node. */
  name: string;
  stale: boolean; txKeyed: boolean; rxKeyed: boolean;
  linkCount: number;
  /** Who is talking ('' when idle or not known). */
  talker: string;
  shown: TileLink[];
  more: number;
}

export const tileState = (t: Pick<TileView, 'stale' | 'txKeyed' | 'rxKeyed'>): string => (t.stale ? 'Stale' : t.txKeyed ? 'TX keyed' : t.rxKeyed ? 'RX keyed' : 'Idle');
export const tileBorder = (t: Pick<TileView, 'stale' | 'txKeyed' | 'rxKeyed'>): string => (t.stale ? 'border-warn/50' : t.txKeyed ? 'border-danger' : t.rxKeyed ? 'border-rx' : 'border-line');

/** The tile of a local node on the Dashboard. */
export function nodeTile(n: NodeStatus, current: NowCall | null | undefined, dv: Pick<DVSnapshot, 'bridgeNode'> | null | undefined): TileView {
  const tl = tileLinks(n, dv);
  return {
    node: n.node, name: n.info?.callsign || n.info?.description || '', stale: n.stale, txKeyed: n.txKeyed, rxKeyed: n.rxKeyed,
    linkCount: n.links.length, talker: tileTalker(n, current, dv), shown: tl.shown, more: tl.more,
  };
}

/** A link of the public page: a label (the bridge node's display name, else the callsign when shown, else the description) next to the number. */
function pubLinkLabel(l: PubNode['links'][number]): string {
  return l.label || l.info?.callsign || l.info?.description || (l.kind === 'echolink' ? 'EchoLink' : '');
}

/** The keyed link that carries a talker: the first keyed link that is not the digital modes bridge node (that one is the DV call). */
const keyedAsl = (n: PubNode) => n.links.find((l) => l.keyed && !l.label);
const keyedBridge = (n: PubNode) => n.links.find((l) => l.keyed && l.label);

/** The tile of a node on the public page. `dvNow` is the DV call in progress, when the digital modes setting is on. */
export function pubTile(n: PubNode, dvNow: PubDVCall | null | undefined, max = 3): TileView {
  const sorted = [...n.links].sort((a, b) => Number(b.keyed) - Number(a.keyed) || a.connected - b.connected);
  let talker = '';
  if (n.txKeyed || n.rxKeyed) {
    const a = keyedAsl(n);
    if (a) talker = a.info?.callsign || `Node ${a.node}`;
    else if (keyedBridge(n) && dvNow?.callsign) talker = dvNow.callsign;
  }
  return {
    node: n.node, name: n.info?.callsign || n.info?.description || '', stale: n.stale, txKeyed: n.txKeyed, rxKeyed: n.rxKeyed,
    linkCount: n.links.length, talker,
    shown: sorted.slice(0, max).map((l) => ({ node: l.node, label: pubLinkLabel(l) })), more: Math.max(0, sorted.length - max),
  };
}

/** The Now Calling banner of the public page. */
export interface PubBanner { source: string; main: string; sub: string; qrz: string; /** When the call started, in the page's clock (ms, browser time); 0 when not known. */ startMs: number }

/** The call in progress: the DV call when the setting is on and there is one, else a keyed AllStar link (its callsign when callsigns are shown, else its node number). */
export function pubBanner(snap: Pick<PubSnapshot, 'nodes' | 'dv' | 'showDV'> | null | undefined, skewMs: number, nodeAt: Record<string, number>): PubBanner | null {
  if (!snap) return null;
  const d = snap.showDV ? snap.dv?.now : null;
  if (d) {
    return {
      source: d.mode, main: d.callsign || `${d.mode} caller`,
      sub: [d.name, d.location, d.target, d.slot ? `TS${d.slot}` : ''].filter(Boolean).join(' · '),
      qrz: qrzUrl(d.callsign), startMs: d.at * 1000 - skewMs,
    };
  }
  for (const n of snap.nodes) {
    const l = keyedAsl(n);
    if (!l) continue;
    const call = l.info?.callsign ?? '';
    return {
      source: 'ASL', main: call || who({ node: l.node }),
      sub: [l.info?.description, l.info?.location, `Node ${l.node}`].filter(Boolean).join(' · '),
      qrz: qrzUrl(call), startMs: (nodeAt[n.node] ?? 0) - (l.sinceKey ?? 0) * 1000,
    };
  }
  return null;
}

