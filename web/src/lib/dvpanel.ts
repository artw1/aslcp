// Pure logic of the dashboard digital modes card (docs/DV.md "Dashboard card"): the state line,
// favourite and tune validation (the same rules as the server), and the pending/confirm logic.
import { ECHO_RUNNING } from './dvecho.ts';
import { isDVNetwork, networkLabel } from './dvnetworks.ts';
import { slotLabel } from './nowcalling.ts';
import type { DVBridge, DVCaller, DVFavorite, DVSnapshot, DVTuneMode, NodeStatus } from './types';

export { PANEL_MODES } from './dvnetworks.ts';
/** The DMR networks (BrandMeister, TGIF, DMR - Other; web/src/lib/dvnetworks.ts) are all DMR on the air: talkgroups, private calls. */
export const isDMRFamily = (mode: string): boolean => isDVNetwork(mode);
/** The name of a panel mode: the registry label of a DMR network ("BrandMeister", "TGIF", "DMR - Other"), else the mode itself. */
export const modeLabel = (mode: string): string => networkLabel(mode);
/** The name of a panel mode button: the snapshot's label when it has one, else modeLabel. */
export const modeButtonLabel = (m: { id: string; label?: string }): string => m.label || modeLabel(m.id);
/** One button of the DV tab's mode buttons card: dot tone (ok green, err red, warn amber, off grey) and the short state. `idle` (BrandMeister outside its mode, no session on purpose) is a grey "Not in use". A network the engine reports no login for (YSF, P25, NXDN, M17) shows "Enabled". */
export function netChip(m: { enabled: boolean; login?: string }): { tone: 'ok' | 'err' | 'warn' | 'off'; text: string } {
  if (!m.enabled) return { tone: 'off', text: 'Disabled' };
  if (m.login === 'ok') return { tone: 'ok', text: 'Logged in' };
  if (m.login === 'failed') return { tone: 'err', text: 'Login failed' };
  if (m.login === 'pending') return { tone: 'warn', text: 'Logging in' };
  if (m.login === 'idle') return { tone: 'off', text: 'Not in use' };
  return { tone: 'off', text: 'Enabled' };
}
/** The networks of the mode buttons card: every available network except the echo test. */
export const stripModes = <M extends { id: string; available: boolean }>(modes: M[]): M[] => modes.filter((m) => m.available && m.id !== 'LOOP');

/** The mode buttons of the dashboard card: the echo test has none (it starts in Digital modes setup), and a disabled DMR network has none. */
export const dashboardModes = <M extends { id: string; enabled: boolean }>(modes: M[]): M[] => modes.filter((m) => m.id !== 'LOOP' && (!isDMRFamily(m.id) || m.enabled));
/** How long a mode switch may stay unconfirmed before the card says so. */
export const CONFIRM_MS = 5000;
export const FAV_NAME_MAX = 40;
export const FAV_PER_MODE = 50;

/** The modules of an M17 reflector: A to Z. */
export const M17_MODULES: readonly string[] = Array.from({ length: 26 }, (_, i) => String.fromCharCode(65 + i));
const M17_REFLECTOR = /^M17-[A-Z0-9]{3}$/;
const M17_TARGET = /^M17-[A-Z0-9]{3} [A-Z]$/;
/** The M17 tune value ("M17-XXX C", what the server takes) from the picked or typed reflector and the module. A text that already ends in a module is taken as it is; '' when the reflector is not an M17 designator. */
export function m17Tune(text: string, module: string): string {
  const t = text.trim().replace(/\s+/g, ' ').toUpperCase();
  if (M17_TARGET.test(t)) return t;
  return M17_REFLECTOR.test(t) ? `${t} ${module.toUpperCase()}` : '';
}
/** An M17 value split into reflector and module ("M17-XXX C" gives M17-XXX and C); null when it is not one. */
export function m17Split(value: string): { reflector: string; module: string } | null {
  const v = value.trim();
  return M17_TARGET.test(v) ? { reflector: v.slice(0, 7), module: v.slice(8) } : null;
}
/** How an M17 value reads on screen: "M17-XXX module C"; any other text is returned as it is. */
export function m17Display(value: string): string {
  const s = m17Split(value);
  return s ? `${s.reflector} module ${s.module}` : value;
}

/** True when a YSF, P25, NXDN or M17 reflector is tuned and sends nothing back (the engine's `silent` flag of that mode). Nothing tuned never counts. */
export function reflectorSilent(s: Pick<DVSnapshot, 'bridge' | 'modes'> | null | undefined): boolean {
  const b = s?.bridge;
  if (!s || !b || b.source === 'none' || !b.mode || !b.tune) return false;
  return !!s.modes?.some((m) => m.id === b.mode && m.silent);
}

/** "Mode YSF, tuned to Example room (192.0.2.10:42000)", "Mode DMR, tuned to 91", "Mode P25", or "Mode unknown"; "..., Reflector not answering" when `silent` (see reflectorSilent). */
export function stateLine(b: Pick<DVBridge, 'mode' | 'tune' | 'targetName' | 'source'> | null | undefined, silent = false): string {
  if (!b || b.source === 'none' || !b.mode) return 'Mode unknown';
  if (b.mode === 'LOOP') return ECHO_RUNNING;
  const m = modeLabel(b.mode);
  if (!b.tune) return `Mode ${m}`;
  if (b.mode === 'M17') {
    const t = m17Display(b.tune);
    return silent ? `Mode ${m}, tuned to ${t}, Reflector not answering` : `Mode ${m}, tuned to ${t}`;
  }
  const line = b.targetName ? `Mode ${m}, tuned to ${b.targetName} (${b.tune})` : `Mode ${m}, tuned to ${b.tune}`;
  return silent ? `${line}, Reflector not answering` : line;
}

/** Longest master name the dashboard shows; the title holds the whole name. */
export const MASTER_MAX = 40;

/**
 * The master line of the Digital modes card: "Logged in to <master>" when the engine's login is ok, else "Master <master>".
 * Only BrandMeister and DMR - Other have one (the server sends `master` for those two); '' for any other mode or no name.
 * A long name is cut to MASTER_MAX characters with an ellipsis.
 */
export function masterLine(s: Pick<DVSnapshot, 'modes'> | null | undefined, mode: string): string {
  if (mode !== 'BM' && mode !== 'DMR') return '';
  const m = s?.modes?.find((x) => x.id === mode);
  const name = (m?.master ?? '').trim();
  if (!name) return '';
  const short = name.length > MASTER_MAX ? `${name.slice(0, MASTER_MAX - 1)}…` : name;
  return m?.login === 'ok' ? `Logged in to ${short}` : `Master ${short}`;
}

/** The unit dots: a unit that is down, or no snapshot of the bridge at all, is amber. */
export function serviceDots(s: DVSnapshot | null | undefined): { unit: string; ok: boolean }[] {
  if (!s) return [];
  const unknown = s.bridge.source === 'none';
  return s.services.map((u) => ({ unit: u.unit, ok: u.active && !(unknown && u.unit === 'analog_bridge') }));
}
export const allOk = (s: DVSnapshot | null | undefined): boolean => !!s && s.bridge.source !== 'none' && serviceDots(s).every((d) => d.ok);

/** Colour of the state line: warn when a service is down, the state is unknown or the reflector does not answer, ok when all is well and a target is tuned, else neutral. */
export function stateTone(s: DVSnapshot | null | undefined): 'ok' | 'warn' | '' {
  if (!s || !allOk(s) || !s.bridge.mode) return 'warn';
  if (reflectorSilent(s)) return 'warn';
  return s.bridge.tune ? 'ok' : '';
}

/** Hover text of the state line: the units that are down, or that all run. */
export function stateTitle(s: DVSnapshot | null | undefined): string {
  if (!s) return 'No service state';
  const down = serviceDots(s).filter((d) => !d.ok).map((d) => `${d.unit}: not running`);
  if (down.length) return down.join(', ');
  if (s.bridge.source === 'none' || !s.bridge.mode) return 'The bridge state is unknown';
  if (reflectorSilent(s)) return 'The reflector has sent nothing back for a while';
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

/** Same rules as the server's tune validator: a DMR network's TG 1-8 digits (a trailing # makes it a private call), P25 and NXDN 1-5 digits, YSF a 5-digit id, a name or host:port, M17 a reflector and module (M17-XXX C). */
export function validTune(mode: string, value: string): boolean {
  const v = value.trim();
  if (!v || v.startsWith('-') || v.includes('=')) return false;
  if (isDMRFamily(mode)) return /^[0-9]{1,8}#?$/.test(v);
  switch (mode) {
    case 'M17': return M17_TARGET.test(v);
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

/** BM Parrot: DMR 9990, called as a private call. */
export const PARROT_ID = '9990';
export const PRIVATE_MARK = '#';
/** True for a DMR network's private call value ("9990#"). */
export const isPrivateTune = (mode: string, value: string): boolean => isDMRFamily(mode) && value.trim().endsWith(PRIVATE_MARK) && validTune(mode, value);
/** The value to send: the DMR networks get the private mark when asked, other modes never. */
export function withPrivate(mode: string, value: string, priv: boolean): string {
  const v = value.trim();
  if (!isDMRFamily(mode)) return v;
  const bare = v.endsWith(PRIVATE_MARK) ? v.slice(0, -1) : v;
  return priv ? bare + PRIVATE_MARK : bare;
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
export const DEFAULT_NODE_NAME = 'Digital modes linked node';
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

/** A mode's tune is linked when it is set and is not the mode's unlink value (the "nothing linked" target). */
export const isTargetLinked = (target: string, unlinkValue: string): boolean => !!target && target !== unlinkValue;

/** The mode and target the bridge is tuned to: "BrandMeister TG 91", "DMR TG 1", "YSF Example room", "P25 TG 10100", "DMR" with no target; null without a bridge node or mode. Not what the talker sent. */
export function bridgeRxTarget(s: Pick<DVSnapshot, 'bridgeNode' | 'bridge'> | null | undefined): string | null {
  const bn = s?.bridgeNode;
  if (!s || !bn?.present || !bn.node || s.bridge.source === 'none' || !s.bridge.mode) return null;
  const { mode, tune, targetName } = s.bridge;
  const m = modeLabel(mode);
  if (isDMRFamily(mode) && s.bridge.private) return `${m} private call`;
  if (!tune) return m;
  if (mode === 'M17') return `M17 ${m17Display(tune)}`;
  return mode === 'YSF' ? `YSF ${targetName || tune}` : `${m} TG ${tune}`;
}

/** "RX: DMR TG 3100" while the bridge node is keyed (its link on a local node, or the node itself when monitored), else null. */
export function bridgeRx(nodes: Pick<NodeStatus, 'node' | 'rxKeyed' | 'links'>[], s: Pick<DVSnapshot, 'bridgeNode' | 'bridge'> | null | undefined): string | null {
  const t = bridgeRxTarget(s);
  const bn = s?.bridgeNode;
  if (t === null || !bn) return null;
  const keyed = nodes.some((n) => (n.node === bn.node && n.rxKeyed) || n.links.some((l) => l.node === bn.node && l.keyed));
  return keyed ? `RX: ${t}` : null;
}

/** The callsign of whoever is talking on the bridge, shown only while RX is keyed (the caller of the log's last call that has not ended). null without one. */
export const rxCaller = (s: Pick<DVSnapshot, 'caller'> | null | undefined): string | null => (s?.caller ? callerLabel(s.caller) || null : null);

/** Who a caller is: "Parrot (private call)" for a private call (the BM Parrot's reply), else the callsign, else the DMR ID (no callsign known), else "". */
export function callerLabel(c: DVCaller): string {
  if (c.private) return c.dmrId === PARROT_ID ? 'Parrot (private call)' : 'Private call';
  return c.call?.trim() || c.dmrId || '';
}

/** The target of a heard call: "YSF <reflector name>" for YSF, the module for M17, else "TG 91". Empty when there is no destination. */
function heardTarget(c: DVCaller): string {
  return !c.dst ? '' : c.mode === 'YSF' ? `YSF ${c.dstName || c.dst}` : /^M17-/.test(c.dst) ? m17Display(c.dst) : `TG ${c.dst}`;
}

/** The cells of one Last heard table row: callsign (or DMR ID, or "Private call"), mode (the call's own, else M17 for an M17 target, DMR for a talkgroup), target, time slot and the log time (HH:MM:SS). Empty strings for what is not known. */
export function heardRow(c: DVCaller): { call: string; mode: string; target: string; slot: string; time: string } {
  const mode = c.mode || (!c.dst ? '' : /^M17-/.test(c.dst) ? 'M17' : 'DMR');
  return { call: callerLabel(c), mode, target: heardTarget(c), slot: slotLabel(c.slot), time: c.at ? c.at.slice(11, 19) : '' };
}

/** One line for the dashboard's Last heard list: callsign, target, time slot, log time. */
export function heardLine(c: DVCaller): string {
  const r = heardRow(c);
  return [r.call, r.target, r.slot, r.time].filter(Boolean).join(' · ');
}

/** The Frequency field in MHz as Hz (an integer), or null when it is not a number in the 30 to 3000 MHz range the server accepts. Hz input (30000000 to 3000000000) is taken as it is. */
export function freqToHz(input: string): number | null {
  const t = input.trim().replace(',', '.');
  if (!/^[0-9]+(\.[0-9]{1,6})?$/.test(t)) return null;
  const n = Number(t);
  const hz = n >= 30_000_000 ? Math.round(n) : Math.round(n * 1e6);
  return hz >= 30_000_000 && hz <= 3_000_000_000 ? hz : null;
}
/** Hz as the MHz text the form shows: 434000000 gives "434", 434012500 gives "434.0125". */
export const hzToMHz = (hz: number): string => (hz > 0 ? String(hz / 1e6) : '');

/** Latitude or longitude text: a decimal with up to six places, within +/- max degrees. */
export function validCoord(v: string, max: number): boolean {
  const t = v.trim();
  if (!/^-?[0-9]{1,3}(\.[0-9]{1,6})?$/.test(t)) return false;
  return Math.abs(Number(t)) <= max;
}

/** [Info] text: 1 to `max` printable ASCII characters, starting with a letter or digit, no trailing blank, none of ; # = " (the server's rule; Location 20, Description 19). */
export const validInfoText = (v: string, max: number): boolean => v.length >= 1 && v.length <= max && /^[A-Za-z0-9][\x20-\x7e]*$/.test(v) && v === v.trimEnd() && !/[;#="]/.test(v);
/** [Info] URL: empty, or http(s) with no space, quote, ; or #, at most 124 characters. */
export const validInfoUrl = (v: string): boolean => v === '' || (v.length <= 124 && /^https?:\/\/[A-Za-z0-9._~:/?&=%+-]{1,116}$/.test(v));

/** How long a DMR - Other talkgroup may stay quiet before the card suggests a retune (DMR+ masters drop an idle key-up talkgroup after about 15 minutes). */
export const QUIET_MS = 15 * 60 * 1000;
/** How often the card re-checks quietHint (no server frame is needed). */
export const QUIET_TICK_MS = 30_000;
export const QUIET_TEXT = 'No traffic for 15 min, the master may have dropped this talkgroup';

/** The retune hint of DMR - Other: the tune to resend when that mode is active, a (non-private) talkgroup is tuned and the snapshot's quietSince is 15 minutes old at `nowMs`; else null. */
export function quietHint(s: Pick<DVSnapshot, 'bridge' | 'modes'> | null | undefined, nowMs: number): { text: string; tune: string } | null {
  const b = s?.bridge;
  if (!s || !b || b.source === 'none' || b.mode !== 'DMR' || !b.tune || b.private) return null;
  const since = Date.parse(s.modes?.find((m) => m.id === 'DMR')?.quietSince ?? '');
  if (!Number.isFinite(since) || nowMs - since < QUIET_MS) return null;
  return { text: QUIET_TEXT, tune: b.tune };
}
