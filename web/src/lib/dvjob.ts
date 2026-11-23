// The enable job of the digital modes engine (plan C.1/C.2): GET /api/dv/job and /status report one job
// (the transient unit aslcp-dv-enable). The state and log helpers here are pure so node --test can cover them.
// Adapted from the old install/uninstall job helpers: one job instead of two, no kind.
import type { DVJob, DVJobState } from './types';

export interface JobView { state: 'idle' | 'running' | 'succeeded' | 'failed'; log: string; at?: number }

/** The job as the panel shows it. The server's state is `job` ("", running, failed, done); `done` and `succeeded` both mean success. The log is `log` text or `lines`. */
export function jobView(j: Partial<DVJob> | null | undefined): JobView {
  const raw = (j?.job ?? '') as DVJobState;
  const state = raw === 'running' ? 'running' : raw === 'failed' ? 'failed' : raw === 'done' || raw === 'succeeded' ? 'succeeded' : 'idle';
  const log = typeof j?.log === 'string' ? j.log : Array.isArray(j?.lines) ? j.lines.join('\n') : '';
  return { state, log, ...(j?.at ? { at: j.at } : {}) };
}

/** One line for a finished job, e.g. "Last enable: succeeded, <date>". Empty for a job that never ran or still runs. */
export function jobSummary(j: JobView, fmtAt: (unix: number) => string): string {
  if (j.state !== 'succeeded' && j.state !== 'failed') return '';
  return `Last enable: ${j.state}${j.at ? ', ' + fmtAt(j.at) : ''}`;
}

/** localStorage key of a dismissed summary: a newer job (other `at`) shows again. */
export function dismissKey(j: JobView): string {
  return `aslcp.dvjob.dismissed.enable.${j.at ?? 0}`;
}

type Store = Pick<Storage, 'getItem' | 'setItem'>;
function store(): Store | null { try { return globalThis.localStorage ?? null; } catch { return null; } }

export function isDismissed(key: string, s: Store | null = store()): boolean {
  try { return s?.getItem(key) === '1'; } catch { return false; }
}
export function setDismissed(key: string, s: Store | null = store()): void {
  try { s?.setItem(key, '1'); } catch { /* storage blocked: the summary just shows again */ }
}
