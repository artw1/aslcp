import type { DVSwitchJob, DVSwitchJobs } from './types';

export type DVJobKind = 'install' | 'uninstall';

/** The job whose log the panel shows: the running one, else the newer of the two (an old uninstall must not hide a later install). */
export function pickJobKind(s: DVSwitchJobs): DVJobKind {
  const i = s.install, u = s.uninstall;
  if (u.job === 'running') return 'uninstall';
  if (i.job === 'running' || u.job === 'idle') return 'install';
  if (i.job === 'idle') return 'uninstall';
  return (u.at ?? 0) > (i.at ?? 0) ? 'uninstall' : 'install';
}

/** One line for a finished job, e.g. "Last install: succeeded, <date>". Empty for a job that never ran. */
export function jobSummary(kind: DVJobKind, j: DVSwitchJob, fmtAt: (unix: number) => string): string {
  if (j.job !== 'succeeded' && j.job !== 'failed') return '';
  return `Last ${kind}: ${j.job === 'succeeded' ? 'succeeded' : 'failed'}${j.at ? ', ' + fmtAt(j.at) : ''}`;
}

/** localStorage key of a dismissed summary: a newer job (other `at`) shows again. */
export function dismissKey(kind: DVJobKind, j: DVSwitchJob): string {
  return `aslcp.dvjob.dismissed.${kind}.${j.at ?? 0}`;
}

type Store = Pick<Storage, 'getItem' | 'setItem'>;
function store(): Store | null { try { return globalThis.localStorage ?? null; } catch { return null; } }

export function isDismissed(key: string, s: Store | null = store()): boolean {
  try { return s?.getItem(key) === '1'; } catch { return false; }
}
export function setDismissed(key: string, s: Store | null = store()): void {
  try { s?.setItem(key, '1'); } catch { /* storage blocked: the summary just shows again */ }
}
