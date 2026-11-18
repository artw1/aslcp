// Pure logic of the dashboard DVSwitch card (docs/DVSWITCH.md "Dashboard card"): the state line,
// favourite and tune validation (the same rules as the server), and the pending/confirm logic.
import type { DVBridge, DVFavorite, DVSnapshot, DVTuneMode, NodeStatus } from './types';

export const PANEL_MODES: DVTuneMode[] = ['DMR', 'YSF', 'P25', 'NXDN'];
/** How long a mode switch may stay unconfirmed before the card says so. */
export const CONFIRM_MS = 5000;
export const FAV_NAME_MAX = 40;
export const FAV_PER_MODE = 50;

/** "Mode YSF, tuned to Example room (192.0.2.10:42000)", "Mode DMR, tuned to 91", "Mode P25", or "Mode unknown". */
export function stateLine(b: Pick<DVBridge, 'mode' | 'tune' | 'targetName' | 'source'> | null | undefined): string {
  if (!b || b.source === 'none' || !b.mode) return 'Mode unknown';
  if (!b.tune) return `Mode ${b.mode}`;
  return b.targetName ? `Mode ${b.mode}, tuned to ${b.targetName} (${b.tune})` : `Mode ${b.mode}, tuned to ${b.tune}`;
}

/** The unit dots: a unit that is down, or no snapshot of the bridge at all, is amber. */
export function serviceDots(s: DVSnapshot | null | undefined): { unit: string; ok: boolean }[] {
  if (!s) return [];
  const unknown = s.bridge.source === 'none';
  return s.services.map((u) => ({ unit: u.unit, ok: u.active && !(unknown && u.unit === 'analog_bridge') }));
}
export const allOk = (s: DVSnapshot | null | undefined): boolean => !!s && s.bridge.source !== 'none' && serviceDots(s).every((d) => d.ok);

/** Colour of the state line: warn when a service is down or the state is unknown, ok when all is well and a target is tuned, else neutral. */
export function stateTone(s: DVSnapshot | null | undefined): 'ok' | 'warn' | '' {
  if (!s || !allOk(s) || !s.bridge.mode) return 'warn';
  return s.bridge.tune ? 'ok' : '';
}

/** Hover text of the state line: the units that are down, or that all run. */
export function stateTitle(s: DVSnapshot | null | undefined): string {
  if (!s) return 'No service state';
  const down = serviceDots(s).filter((d) => !d.ok).map((d) => `${d.unit}: not running`);
  if (down.length) return down.join(', ');
  if (s.bridge.source === 'none' || !s.bridge.mode) return 'The bridge state is unknown';
  return s.services.length ? 'All services running' : 'No service state';
}

/** True when the bridge node is linked to the local node (the snapshot's `link`, established only). The talkgroup or reflector does not matter. Decides the card's green tint. */
export const bridgeLinked = (s: Pick<DVSnapshot, 'bridgeNode' | 'link'> | null | undefined): boolean => !!s && !!s.bridgeNode?.present && !!s.bridgeNode.node && !!s.link?.linked;

const printable = (s: string) => ![...s].some((c) => { const n = c.codePointAt(0)!; return n < 0x20 || (n >= 0x7f && n < 0xa0); });
/** The trimmed favourite name, or null when it is not 1 to 40 printable characters. */
export function validFavName(name: string): string | null {
  const t = name.trim();
  const n = [...t].length;
  return n >= 1 && n <= FAV_NAME_MAX && printable(t) ? t : null;
}

/** Same rules as the server's tune validator: DMR TG 1-8 digits, P25 and NXDN 1-5 digits, YSF a 5-digit id, a name or host:port. */
export function validTune(mode: string, value: string): boolean {
  const v = value.trim();
  if (!v || v.startsWith('-') || v.includes('=')) return false;
  switch (mode) {
    case 'DMR': return /^[0-9]{1,8}$/.test(v);
    case 'P25': case 'NXDN': return /^[0-9]{1,5}$/.test(v);
    case 'YSF': {
      const hp = /^[A-Za-z0-9.-]{1,253}:([0-9]{1,5})$/.exec(v);
      if (hp) { const p = Number(hp[1]); return p >= 1 && p <= 65535; }
      if (/^[0-9]{5}$/.test(v)) return true;
      return [...v].length <= 32 && printable(v) && !/[;#=]/.test(v);
    }
    default: return false;
  }
}

/** The favourite of `mode` with this value, if any (decides whether the star is filled). */
export function findFavorite(favs: DVFavorite[], mode: string, value: string): DVFavorite | undefined {
  return favs.find((f) => f.mode === mode && f.value === value);
}
export const favoritesFor = (favs: DVFavorite[], mode: string): DVFavorite[] => favs.filter((f) => f.mode === mode).sort((a, b) => a.sort - b.sort || a.id - b.id);

/** Why a favourite cannot be saved now (null: it can). */
export function favoriteBlock(favs: DVFavorite[], mode: string, value: string): string | null {
  if (!validTune(mode, value)) return 'Nothing to save: no valid target is tuned';
  if (findFavorite(favs, mode, value)) return 'Already a favourite';
  if (favs.filter((f) => f.mode === mode).length >= FAV_PER_MODE) return `At most ${FAV_PER_MODE} favourites per mode`;
  return null;
}

/** The name the prompt starts with: the resolved name, else the value. */
export const defaultFavName = (b: Pick<DVBridge, 'tune' | 'targetName'>): string => (b.targetName || b.tune || '').trim().slice(0, FAV_NAME_MAX);

/** A mode switch waiting for the snapshot to show it. */
export interface Pending { mode: DVTuneMode; since: number }
export type Outcome = 'none' | 'waiting' | 'confirmed' | 'unconfirmed';

/** `confirmed` once the bridge reports the mode, `unconfirmed` after CONFIRM_MS without it, else `waiting`. */
export function pendingOutcome(p: Pending | null, bridgeMode: string | undefined, now: number): Outcome {
  if (!p) return 'none';
  if (bridgeMode === p.mode) return 'confirmed';
  return now - p.since >= CONFIRM_MS ? 'unconfirmed' : 'waiting';
}

/** The mode the buttons highlight: the pending one while waiting, else what the bridge reports. */
export function shownMode(p: Pending | null, outcome: Outcome, bridgeMode: string | undefined): string {
  return p && outcome === 'waiting' ? p.mode : bridgeMode ?? '';
}

/** True while a command is in flight or its mode switch still waits for confirmation: the mode buttons, Go, the favourite chips and Unlink are disabled then (one command at a time). */
export const controlsLocked = (busy: boolean, outcome: Outcome): boolean => busy || outcome === 'waiting';

/** The label of the bridge node when the operator set no name (the same default as the server). */
export const DEFAULT_NODE_NAME = 'DVSwitch linked node';
export const NODE_NAME_MAX = 40;

/** The display name of the bridge node: the saved one, else the default. */
export const bridgeNodeName = (s: Pick<DVSnapshot, 'bridgeNode'> | null | undefined): string => s?.bridgeNode?.name?.trim() || DEFAULT_NODE_NAME;

/** The name to show next to `node` wherever the dashboard lists it (the status table, say): the bridge node's display name, or '' for any other node. */
export function nodeLabel(s: Pick<DVSnapshot, 'bridgeNode'> | null | undefined, node: string): string {
  return s?.bridgeNode?.present && s.bridgeNode.node === node ? bridgeNodeName(s) : '';
}

/** The trimmed name when it is empty (use the default) or 1 to 40 printable characters; null when it is not allowed. */
export function validNodeName(name: string): string | null {
  const t = name.trim();
  if (t === '') return '';
  return [...t].length <= NODE_NAME_MAX && printable(t) ? t : null;
}

/** Button class of the node link button: green to link, red to unlink (solid colours, readable in both themes). */
export const nodeLinkClass = (action: 'connect' | 'disconnect'): string => (action === 'connect' ? 'btn-ok' : 'btn-bad');

/** The mode and target the bridge is tuned to: "DMR TG 3100", "YSF Example room", "P25 TG 10100", "DMR" with no target; null without a bridge node or mode. Not what the talker sent. */
export function bridgeRxTarget(s: Pick<DVSnapshot, 'bridgeNode' | 'bridge'> | null | undefined): string | null {
  const bn = s?.bridgeNode;
  if (!s || !bn?.present || !bn.node || s.bridge.source === 'none' || !s.bridge.mode) return null;
  const { mode, tune, targetName } = s.bridge;
  if (!tune) return mode;
  return mode === 'YSF' ? `YSF ${targetName || tune}` : `${mode} TG ${tune}`;
}

/** "RX: DMR TG 3100" while the bridge node is keyed (its link on a local node, or the node itself when monitored), else null. */
export function bridgeRx(nodes: Pick<NodeStatus, 'node' | 'rxKeyed' | 'links'>[], s: Pick<DVSnapshot, 'bridgeNode' | 'bridge'> | null | undefined): string | null {
  const t = bridgeRxTarget(s);
  const bn = s?.bridgeNode;
  if (t === null || !bn) return null;
  const keyed = nodes.some((n) => (n.node === bn.node && n.rxKeyed) || n.links.some((l) => l.node === bn.node && l.keyed));
  return keyed ? `RX: ${t}` : null;
}
