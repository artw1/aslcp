import { api, probeServer, setUnauthorizedHandler } from './api';
import type { DVSnapshot, ManualAction, SchedulesSnapshot, UpdatesBadge, FavActivity, FavStatsUpdate, Favorite, NodeStatus, SystemInfo, TuneDevice, User } from './types';
import { navigate } from './router.svelte';
import { applySettings } from './settings.svelte';
import { toast, toastError } from './toast.svelte';
import type { Settings } from './types';

export const app = $state<{
  ready: boolean; setupNeeded: boolean; user: User | null;
  /** Id of the server process (SSE hello); changes when the panel restarts. */
  instance: string;
  nodes: NodeStatus[]; favorites: Favorite[]; schedules: SchedulesSnapshot | null; dvswitch: DVSnapshot | null; system: SystemInfo | null;
  conn: 'connecting' | 'live' | 'reconnecting' | 'offline'; stateAt: number; version: string;
  selectedNode: string; haveNodes: boolean; haveFavorites: boolean;
  /** Set while an ASLCP update is installing; cleared when the server returns with a new version. */
  updating: { from: string; target: string } | null;
  /** Admins only: what the header badge and the Updates tab show (SSE "updates"). */
  updates: UpdatesBadge;
  /** Set while the panel or the node restarts: the page waits for the server and reloads. */
  restarting: { kind: 'aslcp' | 'reboot'; since: number } | null;
  /** The public status view is on (admin setting); the app is the chrome-less /embed view. */
  publicEnabled: boolean; embed: boolean;
  /** SimpleUSB devices (admins only); the Audio Tuning tab shows only when there is one. */
  tune: { loaded: boolean; devices: TuneDevice[]; reason: string };
  /** Favorites activity scan: switch, state and the latest values per node. */
  favstats: { enabled: boolean; rate: number; effRate: number; backoff: boolean; backoffReason: string; retryAt: number | null; updated: number | null; stats: Record<string, FavActivity>; checking: string; next: string; nextAt: number | null; checked: number; total: number | null; checkedAt: Record<string, number> };
}>({
  ready: false, setupNeeded: false, user: null, instance: '', nodes: [], favorites: [], schedules: null, dvswitch: null, system: null,
  conn: 'offline', stateAt: Date.now(), version: '', selectedNode: '', haveNodes: false, haveFavorites: false, updating: null,
  updates: { aslcp: false, aslcpVersion: '', os: 0, security: 0, reboot: false }, restarting: null,
  favstats: { enabled: false, rate: 20, effRate: 20, backoff: false, backoffReason: '', retryAt: null, updated: null, stats: {}, checking: '', next: '', nextAt: null, checked: 0, total: null, checkedAt: {} },
  tune: { loaded: false, devices: [], reason: '' }, publicEnabled: false, embed: location.pathname === '/embed' || location.pathname === '/embed/',
});

/** Ticking clock (ms) so durations advance between updates. */
export const clock = $state<{ now: number }>({ now: Date.now() });
setInterval(() => { clock.now = Date.now(); }, 1000);

export const canOperate = () => app.user?.role === 'admin' || app.user?.role === 'operator';
export const isAdmin = () => app.user?.role === 'admin';

let es: EventSource | null = null;
let retry = 0;
let retryTimer: ReturnType<typeof setTimeout> | undefined;

function setNodes(n: NodeStatus[]) {
  app.nodes = n;
  app.stateAt = Date.now();
  app.haveNodes = true;
  if (!n.some((x) => x.node === app.selectedNode)) app.selectedNode = n[0]?.node ?? '';
}

/** Merge a favstats update (SSE sends only the changed nodes plus `removed` for cleared ones; a snapshot replaces all). */
export function applyFavStats(u: FavStatsUpdate, full = false) {
  const f = app.favstats;
  f.enabled = u.enabled; f.rate = u.rate; f.effRate = u.effRate ?? u.rate; f.backoff = u.backoff; f.backoffReason = u.backoff ? u.backoffReason ?? '' : '';
  f.retryAt = u.backoff && u.retryAt ? Date.parse(u.retryAt) : null;
  if (u.updated) f.updated = Date.parse(u.updated);
  else if (!u.enabled || full) f.updated = null;
  f.checking = u.enabled ? u.checking ?? '' : '';
  f.next = u.enabled ? u.next ?? '' : '';
  f.nextAt = u.enabled && u.nextAt ? Date.parse(u.nextAt) : null;
  f.checked = u.checked ?? 0; f.total = u.enabled && u.total != null ? u.total : null;
  if (!u.enabled) { f.stats = {}; f.checkedAt = {}; return; }
  const at: Record<string, number> = {};
  for (const [n, t] of Object.entries(u.checkedAt ?? {})) at[n] = Date.parse(t);
  const stats = u.stats ?? {};
  if (full) { f.stats = { ...stats }; f.checkedAt = at; return; }
  const m = { ...f.stats, ...stats };
  for (const n of u.removed ?? []) if (!(n in stats)) delete m[n];
  f.stats = m;
  f.checkedAt = { ...f.checkedAt, ...at };
}
export async function refreshFavStats() { try { applyFavStats(await api.favStats(), true); } catch { /* ignore */ } }
export async function setFavScan(on: boolean) { applyFavStats(await api.setFavScan(on), true); }

/**
 * The panel or the node restarts. Poll the session until the server answers
 * with a different instance id (or answers again after having been down), then
 * reload (this also loads a new UI after an upgrade). A reboot is retried for
 * as long as it takes; a panel restart gives up after a minute.
 */
/**
 * `before` is the server instance captured BEFORE the restart request was sent:
 * the panel can restart (and the event stream reconnect with the new instance)
 * before that request even returns.
 */
export function awaitRestart(kind: 'aslcp' | 'reboot', before: string = app.instance) {
  if (app.restarting) return;
  const since = Date.now();
  app.restarting = { kind, since };
  restartFrom = before;
  let sawDown = false;
  const tick = async () => {
    if (!app.restarting) return;
    try {
      const r = await probeServer(4000);
      if (r.status === 401) { location.reload(); return; }
      if (sawDown || (r.instance && before && r.instance !== before)) { location.reload(); return; }
    } catch {
      sawDown = true;
    }
    if (kind === 'aslcp' && Date.now() - since > 60000) {
      app.restarting = null; restartFrom = '';
      toastError(new Error('ASLCP did not restart. Check `journalctl -u aslcp`.'));
      return;
    }
    setTimeout(tick, 2000);
  };
  setTimeout(tick, 1000);
}
let restartFrom = '';

export function startEvents() {
  stopEvents();
  app.conn = retry === 0 ? 'connecting' : 'reconnecting';
  es = new EventSource('/api/events');
  const parse = <T>(e: Event) => JSON.parse((e as MessageEvent).data) as T;
  es.addEventListener('hello', (e) => {
    retry = 0; app.conn = 'live';
    const h = parse<{ user: User; version: string; instance?: string; settings?: Settings }>(e);
    if (app.restarting && h.instance && restartFrom && h.instance !== restartFrom) { location.reload(); return; }
    if (h.instance) app.instance = h.instance;
    if (app.updating && h.version && h.version !== app.updating.from) {
      // The server came back running the new version: reload to get the new UI.
      try { sessionStorage.setItem('aslcp-updated', h.version); } catch { /* ignore */ }
      location.reload();
      return;
    }
    app.version = h.version; if (h.user) app.user = h.user;
    if (h.settings) applySettings(h.settings);
    app.favstats.enabled = !!h.settings?.favoritesScan;
    if (app.favstats.enabled) void refreshFavStats(); else app.favstats.stats = {};
  });
  es.addEventListener('nodes', (e) => { app.conn = 'live'; setNodes(parse<NodeStatus[]>(e)); });
  es.addEventListener('node', (e) => {
    const n = parse<NodeStatus>(e);
    const i = app.nodes.findIndex((x) => x.node === n.node);
    if (i >= 0) app.nodes[i] = n; else app.nodes.push(n);
    setNodes(app.nodes.slice());
  });
  es.addEventListener('updates', (e) => { app.updates = parse<UpdatesBadge>(e); });
  es.addEventListener('system', (e) => { app.system = parse<SystemInfo>(e); });
  es.addEventListener('favstats', (e) => applyFavStats(parse<FavStatsUpdate>(e)));
  es.addEventListener('favorites', (e) => { app.favorites = parse<Favorite[]>(e); app.haveFavorites = true; });
  es.addEventListener('schedules', (e) => { app.schedules = parse<SchedulesSnapshot>(e); });
  es.addEventListener('dvswitch', (e) => { app.dvswitch = parse<DVSnapshot>(e); });
  es.onerror = () => {
    es?.close(); es = null;
    app.conn = 'reconnecting';
    // If the session expired the stream fails with 401; check before looping.
    api.session().then(() => {
      const delay = Math.min(15000, 1000 * 2 ** retry++) + Math.random() * 400;
      retryTimer = setTimeout(startEvents, delay);
    }).catch((err) => {
      if (err?.status === 401) { app.user = null; stopEvents(); navigate('/login'); }
      else { const delay = Math.min(15000, 1000 * 2 ** retry++); retryTimer = setTimeout(startEvents, delay); }
    });
  };
}
export function stopEvents() {
  clearTimeout(retryTimer);
  es?.close(); es = null; app.conn = 'offline';
}

/** Admins only: find out whether any node has a SimpleUSB device (the Audio Tuning tab depends on it). */
export async function refreshTune() {
  if (!isAdmin()) { app.tune = { loaded: true, devices: [], reason: '' }; return; }
  try { const l = await api.tuneList(); app.tune = { loaded: true, devices: l.devices, reason: l.reason ?? '' }; }
  catch (e) { app.tune = { loaded: true, devices: [], reason: e instanceof Error ? e.message : String(e) }; }
}

export async function refreshSchedules() { app.schedules = await api.schedules(); }
/** The global switch (operators). The response is the new snapshot. */
export async function setSchedulesEnabled(on: boolean) { app.schedules = await api.setSchedulesEnabled(on); }
/** What a manual connect/disconnect does to the schedules (operators). */
export async function setManualAction(m: ManualAction) { app.schedules = await api.setScheduleSettings(m); }

export async function refreshFavorites() { app.favorites = await api.favorites(); app.haveFavorites = true; }

export async function logout() {
  try { await api.logout(); } catch { /* ignore */ }
  stopEvents(); app.user = null; app.nodes = []; app.favorites = []; app.schedules = null; app.dvswitch = null; app.haveNodes = false; app.haveFavorites = false; app.tune = { loaded: false, devices: [], reason: '' };
  app.favstats = { enabled: false, rate: 20, effRate: 20, backoff: false, backoffReason: '', retryAt: null, updated: null, stats: {}, checking: '', next: '', nextAt: null, checked: 0, total: null, checkedAt: {} };
  navigate('/login');
}

export function onLoggedIn(u: User) {
  app.user = u; retry = 0; startEvents();
  api.favorites().then((f) => { app.favorites = f; app.haveFavorites = true; }).catch(() => {});
  api.schedules().then((s) => { app.schedules = s; }).catch(() => {});
  api.dvswitchSnapshot().then((s) => { app.dvswitch = s; }).catch(() => {});
  api.nodes().then(setNodes).catch(() => {});
  api.settings().then(applySettings).catch(() => {});
  void refreshTune();
  api.system().then((s) => { app.system = s; }).catch(() => {});
}

/** Learn whether the public view is on (it may be, for visitors without a session). */
export async function refreshPublicInfo(): Promise<boolean> {
  try { const i = await api.publicInfo(); app.publicEnabled = i.enabled; return i.loggedIn; } catch { app.publicEnabled = false; return true; }
}

export async function boot() {
  if (app.embed) { await refreshPublicInfo(); app.ready = true; return; }
  setUnauthorizedHandler(() => { if (app.user) { app.user = null; stopEvents(); } navigate('/login'); });
  try {
    const s = await api.setupNeeded();
    app.setupNeeded = s.needed;
    if (s.needed) { navigate('/setup'); app.ready = true; return; }
  } catch { /* backend down: fall through to login */ }
  // The info call says whether a session exists, so visitors never trigger a 401.
  if (await refreshPublicInfo()) {
    try { onLoggedIn(await api.session()); } catch { if (!app.publicEnabled) navigate('/login'); }
  } else if (!app.publicEnabled) navigate('/login');
  app.ready = true;
  try {
    const v = sessionStorage.getItem('aslcp-updated');
    if (v) { sessionStorage.removeItem('aslcp-updated'); toast(`Updated to ${v}`); }
  } catch { /* ignore */ }
}
