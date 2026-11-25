// Live data of the public status view: SSE with backoff, falling back to
// polling /api/public/status every 10 s while the stream is down.
import type { PubActivity, PubDV, PubNetNode, PubNode, PubSnapshot } from './types';

export const pub = $state<{
  snap: PubSnapshot | null;
  conn: 'connecting' | 'live' | 'polling' | 'offline';
  /** When each node's data arrived (ms), so ages can tick between updates. */
  nodeAt: Record<string, number>;
  /** Server clock minus browser clock (ms), from the last snapshot. */
  skew: number;
  updated: number;
}>({ snap: null, conn: 'connecting', nodeAt: {}, skew: 0, updated: 0 });

let es: EventSource | null = null;
let retry = 0;
let retryTimer: ReturnType<typeof setTimeout> | undefined;
let pollTimer: ReturnType<typeof setInterval> | undefined;
let running = false;
let onDisabled: () => void = () => {};

function setSnapshot(s: PubSnapshot) {
  const now = Date.now();
  pub.snap = s;
  pub.skew = s.now * 1000 - now;
  pub.nodeAt = Object.fromEntries(s.nodes.map((n) => [n.node, now]));
  pub.updated = now;
}

async function poll() {
  try {
    const res = await fetch('/api/public/status', { headers: { Accept: 'application/json' }, cache: 'no-store' });
    if (res.status === 404) { onDisabled(); return; }
    if (!res.ok) return;
    setSnapshot(await res.json());
    if (!es) pub.conn = 'polling';
  } catch { /* offline: keep what is shown */ }
}

function connect() {
  if (!running) return;
  es?.close();
  pub.conn = pub.snap ? 'offline' : 'connecting';
  const src = (es = new EventSource('/api/public/events'));
  const parse = <T>(e: Event) => JSON.parse((e as MessageEvent).data) as T;
  src.addEventListener('snapshot', (e) => {
    retry = 0; pub.conn = 'live';
    clearInterval(pollTimer); pollTimer = undefined;
    setSnapshot(parse<PubSnapshot>(e));
  });
  src.addEventListener('node', (e) => {
    if (!pub.snap) return;
    const n = parse<PubNode>(e);
    const i = pub.snap.nodes.findIndex((x) => x.node === n.node);
    if (i < 0) return;
    n.network = pub.snap.nodes[i].network;
    pub.snap.nodes[i] = n;
    pub.nodeAt[n.node] = Date.now(); pub.updated = Date.now();
  });
  src.addEventListener('network', (e) => {
    if (!pub.snap) return;
    const m = parse<{ node: string; network: PubNetNode[] }>(e);
    const n = pub.snap.nodes.find((x) => x.node === m.node);
    if (n) n.network = m.network;
  });
  src.addEventListener('activity', (e) => {
    if (!pub.snap) return;
    const a = parse<{ now: number; activity: PubActivity[] }>(e);
    pub.snap.activity = a.activity;
    pub.skew = a.now * 1000 - Date.now();
  });
  src.addEventListener('dv', (e) => {
    if (!pub.snap) return;
    pub.snap.dv = parse<PubDV>(e);
    pub.updated = Date.now();
  });
  src.onerror = () => {
    src.close(); if (es === src) es = null;
    if (!running) return;
    pub.conn = 'offline';
    // Fall back to polling right away (it also notices that the page was switched off).
    poll();
    if (!pollTimer) pollTimer = setInterval(poll, 10000);
    retryTimer = setTimeout(connect, Math.min(30000, 1000 * 2 ** retry++) + Math.random() * 500);
  };
}

export function startPublic(disabled: () => void) {
  stopPublic();
  running = true; retry = 0; onDisabled = disabled;
  connect();
}

export function stopPublic() {
  running = false;
  clearTimeout(retryTimer); clearInterval(pollTimer); pollTimer = undefined;
  es?.close(); es = null;
}
