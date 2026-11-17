import type { ManualAction, Schedule, ScheduleInput, SchedulesSnapshot, FavStatsUpdate, Favorite, ImportPreview, NodeInfo, NodeListStatus, NodeStatus, SystemInfo, User, ConnectMode, Settings, NodeStats, UpdateInfo, About, PublicInfo, PublicSettings, ConfigFileInfo, ConfigFile, ConfigDraft, ConfigPreview, ConfigApplyResult, ConfigCommit, ConfigCommitDetail, BackupInfo, BackupSelection, RestoreUpload, RestoreApplyResult, RestoreResult, TuneList, TuneState, TunePatch, TuneApplyResult, TuneMeter, TuneEcho, TuneSaveResult, DVSwitchStatus, DVSwitchJobs } from './types';

export class ApiError extends Error {
  constructor(public status: number, public code: string, message: string, public body?: Record<string, unknown>) { super(message); }
}

let onUnauthorized: () => void = () => {};
export function setUnauthorizedHandler(fn: () => void) { onUnauthorized = fn; }

async function req<T>(method: string, path: string, body?: unknown, opts: { noAuthRedirect?: boolean } = {}): Promise<T> {
  const headers: Record<string, string> = { Accept: 'application/json' };
  if (method !== 'GET') headers['X-ASLCP'] = '1';
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  let res: Response;
  try {
    res = await fetch('/api' + path, { method, headers, credentials: 'same-origin', body: body === undefined ? undefined : JSON.stringify(body) });
  } catch {
    throw new ApiError(0, 'network', 'Cannot reach the server');
  }
  if (res.status === 401 && !opts.noAuthRedirect) onUnauthorized();
  if (!res.ok) {
    let code = 'error', msg = `${res.status} ${res.statusText}`, extra: Record<string, unknown> | undefined;
    try { const j = await res.json(); if (j.error) msg = j.error; if (j.code) code = j.code; extra = j; } catch { /* not json */ }
    throw new ApiError(res.status, code, msg, extra);
  }
  if (res.status === 204 || res.status === 202 && !res.headers.get('content-type')?.includes('json')) return undefined as T;
  const text = await res.text();
  return (text ? JSON.parse(text) : undefined) as T;
}

/** One GET /api/session with a timeout; resolves with the HTTP status and the server instance id, rejects when unreachable. */
export async function probeServer(timeoutMs = 4000): Promise<{ status: number; instance: string }> {
  const ac = new AbortController();
  const t = setTimeout(() => ac.abort(), timeoutMs);
  try {
    const res = await fetch('/api/session', { headers: { Accept: 'application/json' }, credentials: 'same-origin', cache: 'no-store', signal: ac.signal });
    return { status: res.status, instance: res.headers.get('X-ASLCP-Instance') ?? '' };
  } finally { clearTimeout(t); }
}

const q = encodeURIComponent;

/** Largest backup the server accepts (docs/BACKUP.md). */
export const MAX_BACKUP_BYTES = 256 * 1024 * 1024;

/** POST /api/backup: fetch as a blob and save it through an object URL, named by Content-Disposition. */
async function downloadBackup(sel: BackupSelection): Promise<string> {
  let res: Response;
  try {
    res = await fetch('/api/backup', { method: 'POST', credentials: 'same-origin', headers: { 'X-ASLCP': '1', 'Content-Type': 'application/json', Accept: 'application/gzip' }, body: JSON.stringify(sel) });
  } catch { throw new ApiError(0, 'network', 'Cannot reach the server'); }
  if (res.status === 401) onUnauthorized();
  if (!res.ok) {
    let code = 'error', msg = `${res.status} ${res.statusText}`;
    try { const j = await res.json(); if (j.error) msg = j.error; if (j.code) code = j.code; } catch { /* not json */ }
    throw new ApiError(res.status, code, msg);
  }
  const cd = res.headers.get('content-disposition') ?? '';
  const name = /filename\*=UTF-8''([^;]+)/i.exec(cd)?.[1] ?? /filename="?([^";]+)"?/i.exec(cd)?.[1] ?? 'aslcp-backup.tar.gz';
  const blob = await res.blob();
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url; a.download = decodeURIComponent(name); document.body.appendChild(a); a.click(); a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 10000);
  return decodeURIComponent(name);
}

/** POST /api/restore/upload with progress (fetch cannot report upload progress). */
function restoreUpload(file: File, auth: { password?: string; code?: string }, onProgress: (frac: number) => void): Promise<RestoreUpload> {
  return new Promise((resolve, reject) => {
    const fd = new FormData();
    fd.append('file', file, file.name);
    if (auth.password !== undefined) fd.append('password', auth.password);
    if (auth.code !== undefined) fd.append('code', auth.code);
    const xhr = new XMLHttpRequest();
    xhr.open('POST', '/api/restore/upload');
    xhr.setRequestHeader('X-ASLCP', '1');
    xhr.setRequestHeader('Accept', 'application/json');
    xhr.upload.onprogress = (e) => { if (e.lengthComputable) onProgress(e.loaded / e.total); };
    xhr.onerror = () => reject(new ApiError(0, 'network', 'Cannot reach the server'));
    xhr.onabort = () => reject(new ApiError(0, 'aborted', 'Upload cancelled'));
    xhr.onload = () => {
      let j: any = null;
      try { j = JSON.parse(xhr.responseText); } catch { /* not json */ }
      if (xhr.status >= 200 && xhr.status < 300 && j) return resolve(j as RestoreUpload);
      reject(new ApiError(xhr.status, j?.code ?? 'error', j?.error ?? `${xhr.status} ${xhr.statusText}`, j ?? undefined));
    };
    xhr.send(fd);
  });
}
export const api = {
  setupNeeded: () => req<{ needed: boolean }>('GET', '/setup', undefined, { noAuthRedirect: true }),
  setup: (code: string, username: string, password: string) => req<void>('POST', '/setup', { code, username, password }, { noAuthRedirect: true }),
  login: (username: string, password: string) => req<User>('POST', '/login', { username, password }, { noAuthRedirect: true }),
  logout: () => req<void>('POST', '/logout', undefined, { noAuthRedirect: true }),
  session: () => req<User>('GET', '/session', undefined, { noAuthRedirect: true }),
  password: (current: string, next: string) => req<void>('POST', '/password', { current, new: next }),

  users: () => req<User[]>('GET', '/users'),
  addUser: (username: string, password: string, role: string) => req<User>('POST', '/users', { username, password, role }),
  updateUser: (id: number, patch: { role?: string; password?: string }) => req<User>('PUT', `/users/${id}`, patch),
  deleteUser: (id: number) => req<void>('DELETE', `/users/${id}`),

  nodes: () => req<NodeStatus[]>('GET', '/nodes'),
  connect: (node: string, target: string, mode: ConnectMode, permanent: boolean, exclusive = false) => req<void>('POST', `/nodes/${q(node)}/connect`, { target, mode, permanent, exclusive }),
  disconnect: (node: string, target: string, permanent: boolean) => req<void>('POST', `/nodes/${q(node)}/disconnect`, { target, permanent }),
  disconnectAll: (node: string) => req<void>('POST', `/nodes/${q(node)}/disconnect-all`),

  favorites: () => req<Favorite[]>('GET', '/favorites'),
  favStats: () => req<FavStatsUpdate>('GET', '/favorites/stats'),
  setFavScan: (enabled: boolean) => req<FavStatsUpdate>('PUT', '/favorites/scan', { enabled }),
  addFavorite: (f: { node: string; label?: string; group?: string }) => req<Favorite>('POST', '/favorites', f),
  updateFavorite: (id: number, f: { node?: string; label?: string; group?: string }) => req<Favorite>('PUT', `/favorites/${id}`, f),
  deleteFavorite: (id: number) => req<void>('DELETE', `/favorites/${id}`),
  reorder: (ids: number[]) => req<void>('POST', '/favorites/reorder', { ids }),
  importPreview: () => req<ImportPreview>('GET', '/favorites/import/allscan'),
  importAllscan: () => req<{ imported: number; skipped: number }>('POST', '/favorites/import/allscan'),

  schedules: () => req<SchedulesSnapshot>('GET', '/schedules'),
  addSchedule: (s: ScheduleInput) => req<Schedule>('POST', '/schedules', s),
  updateSchedule: (id: number, s: ScheduleInput) => req<Schedule>('PUT', `/schedules/${id}`, s),
  deleteSchedule: (id: number) => req<void>('DELETE', `/schedules/${id}`),
  setSchedulesEnabled: (enabled: boolean) => req<SchedulesSnapshot>('PUT', '/schedules/enabled', { enabled }),
  setScheduleSettings: (manualAction: ManualAction) => req<SchedulesSnapshot>('PUT', '/schedules/settings', { manualAction }),

  lookup: (node: string) => req<NodeInfo>('GET', `/lookup/${q(node)}`),
  search: (text: string, limit = 50) => req<NodeInfo[]>('GET', `/lookup?q=${q(text)}&limit=${limit}`),
  nodelist: () => req<NodeListStatus>('GET', '/nodelist'),
  refreshNodelist: () => req<void>('POST', '/nodelist/refresh'),
  settings: () => req<Settings>('GET', '/settings'),
  system: () => req<SystemInfo>('GET', '/system'),
  nodeStats: (node: string) => req<NodeStats>('GET', `/nodes/${q(node)}/stats`),
  about: () => req<About>('GET', '/about'),
  updates: () => req<UpdateInfo>('GET', '/updates'),
  checkUpdates: () => req<UpdateInfo>('POST', '/updates/check'),
  applyUpdate: () => req<UpdateInfo>('POST', '/updates/apply'),
  osCheck: () => req<UpdateInfo>('POST', '/updates/os/check'),
  osApply: () => req<UpdateInfo>('POST', '/updates/os/apply'),
  reboot: () => req<{ ok: boolean }>('POST', '/system/reboot'),
  restartAslcp: () => req<{ ok: boolean }>('POST', '/system/restart-aslcp'),
  dvswitch: () => req<DVSwitchStatus>('GET', '/dvswitch'),
  dvswitchJob: () => req<DVSwitchJobs>('GET', '/dvswitch/job'),
  dvswitchInstall: () => req<DVSwitchStatus>('POST', '/dvswitch/install'),
  dvswitchUninstall: () => req<DVSwitchStatus>('POST', '/dvswitch/uninstall'),
  setUpdateChannel: (channel: 'stable' | 'beta') => req<UpdateInfo>('PUT', '/updates/channel', { channel }),
  publicInfo: () => req<PublicInfo>('GET', '/public/info', undefined, { noAuthRedirect: true }),
  publicSettings: () => req<PublicSettings>('GET', '/public'),
  putPublic: (p: Partial<Omit<PublicSettings, 'defaultTitle' | 'localNodes' | 'clients'>>) => req<PublicSettings>('PUT', '/public', p),
  configFiles: () => req<ConfigFileInfo[]>('GET', '/config/files'),
  configFile: (path: string) => req<ConfigFile>('GET', `/config/file?path=${q(path)}`),
  configPreview: (files: ConfigDraft[]) => req<ConfigPreview>('POST', '/config/preview', { files }),
  configApply: (files: ConfigDraft[], message: string, reload: boolean) => req<ConfigApplyResult>('POST', '/config/apply', { files, message, reload }),
  configHistory: (limit: number, offset: number) => req<ConfigCommit[]>('GET', `/config/history?limit=${limit}&offset=${offset}`),
  configCommit: (commit: string, reveal = false) => req<ConfigCommitDetail>('GET', `/config/history/${q(commit)}${reveal ? '?reveal=1' : ''}`),
  configRestore: (commit: string, reload: boolean) => req<ConfigApplyResult>('POST', `/config/history/${q(commit)}/restore`, { reload }),
  restartAsteriskNow: () => req<{ ok: boolean }>('POST', '/system/asterisk/restart'),
  restartAsterisk: () => req<void>('POST', '/system/restart-asterisk'),
  backupInfo: () => req<BackupInfo>('GET', '/backup/info'),
  backup: downloadBackup,
  restoreUpload,
  restoreApply: (id: string, deleteMissing: string[], code?: string) => req<RestoreApplyResult>('POST', `/restore/${q(id)}/apply`, code ? { deleteMissing, code } : { deleteMissing }, { noAuthRedirect: true }),
  restoreCancel: (id: string) => req<void>('DELETE', `/restore/${q(id)}`, undefined, { noAuthRedirect: true }),
  tuneList: () => req<TuneList>('GET', '/radio'),
  tuneGet: (dev: string) => req<TuneState>('GET', `/radio/${q(dev)}`),
  tuneSet: (dev: string, patch: TunePatch) => req<TuneApplyResult>('PUT', `/radio/${q(dev)}/params`, patch),
  tuneMeter: (dev: string) => req<TuneMeter>('GET', `/radio/${q(dev)}/meter`),
  tuneEcho: (dev: string, on: boolean) => req<TuneEcho>('POST', `/radio/${q(dev)}/echo`, { on }),
  tuneSave: (dev: string) => req<TuneSaveResult>('POST', `/radio/${q(dev)}/save`),
  tuneRevert: (dev: string) => req<TuneApplyResult>('POST', `/radio/${q(dev)}/revert`),
  /** Best-effort echo off while the page is going away (keepalive lets the request outlive it). */
  tuneEchoOffOnExit: (dev: string) => { try { void fetch(`/api/radio/${q(dev)}/echo`, { method: 'POST', keepalive: true, credentials: 'same-origin', headers: { 'X-ASLCP': '1', 'Content-Type': 'application/json' }, body: '{"on":false}' }).catch(() => {}); } catch { /* ignore */ } },
  restoreResult: () => req<RestoreResult>('GET', '/restore/result', undefined, { noAuthRedirect: true }),
};

/** True when a request failed without an HTTP answer (the server went away mid-request, e.g. while restarting). */
export const lostConnection = (e: unknown) => !(e instanceof ApiError);
