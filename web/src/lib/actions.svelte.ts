import { api } from './api';
import { app } from './store.svelte';
import { isLinked } from './pins';
import { toast, toastError } from './toast.svelte';
import type { ConnectMode } from './types';

/** Targets whose connect is in flight (accepted but link not shown yet), keyed `${node}:${target}`. */
export const pending = $state<{ keys: Record<string, number> }>({ keys: {} });
const timers = new Map<string, ReturnType<typeof setTimeout>>();

export function isPending(local: string, target: string) { return !!pending.keys[`${local}:${target}`]; }
function clearPending(key: string) {
  delete pending.keys[key];
  clearTimeout(timers.get(key)); timers.delete(key);
}
/** Called whenever node state changes: drop pending entries whose link has appeared. */
export function reconcilePending() {
  for (const key of Object.keys(pending.keys)) {
    const [local, target] = key.split(':');
    if (isLinked(app.nodes, local, target)) clearPending(key);
  }
}

export async function connectTo(local: string, target: string, mode: ConnectMode, permanent: boolean, exclusive: boolean): Promise<boolean> {
  target = target.trim();
  if (!local || !target) return false;
  const key = `${local}:${target}`;
  // Already linked: send the request (mode/permanent may change) but wait for nothing.
  if (!isLinked(app.nodes, local, target)) {
    pending.keys[key] = Date.now();
    timers.set(key, setTimeout(() => {
      if (pending.keys[key]) { clearPending(key); toast(`Node ${target} did not appear in the links after 10s`, 'info'); }
    }, 10000));
  }
  // Links that will be dropped first (the target itself is kept).
  const dropsOthers = exclusive && mode !== 'localmonitor';
  const others = dropsOthers ? (app.nodes.find((n) => n.node === local)?.links.filter((l) => l.node !== target).length ?? 0) : 0;
  try {
    await api.connect(local, target, mode, permanent, dropsOthers);
    reconcilePending();
    toast(others > 0 ? `Disconnecting ${others} link${others === 1 ? '' : 's'}, connecting ${local} to ${target}…` : `Connecting ${local} to ${target}…`);
    return true;
  } catch (e) { clearPending(key); toastError(e); return false; }
}

export async function disconnectFrom(local: string, target: string, permanent = false): Promise<boolean> {
  try { await api.disconnect(local, target, permanent); toast(`Disconnecting ${target}${permanent ? ' (permanent)' : ''}…`); return true; }
  catch (e) { toastError(e); return false; }
}
export async function disconnectAll(local: string) {
  try { await api.disconnectAll(local); toast('Disconnecting all links…'); } catch (e) { toastError(e); }
}
