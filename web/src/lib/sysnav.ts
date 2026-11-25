// Pure routing helpers for the Settings area (#/settings/<section>).

/** Old section ids of the former System tab (#/system/<id>); every one kept its id under Settings. */
const OLD_SYSTEM_IDS = ['overview', 'updates', 'network', 'wifi', 'asl-services', 'web-port', 'terminal', 'logs', 'diagnostics', 'audit'];

/** Old top-level routes that moved; the router rewrites them so old links and bookmarks keep working. */
export const MOVED: Readonly<Record<string, string>> = {
  '/updates': '/settings/updates',
  '/config': '/settings/asterisk',
  '/tune': '/settings/audio-levels',
  '/digital': '/settings/dv-engine',
  '/backup': '/settings/backup',
  '/system': '/settings/overview',
  ...Object.fromEntries(OLD_SYSTEM_IDS.map((id) => ['/system/' + id, '/settings/' + id])),
};

export function movedPath(path: string): string | null {
  const p = path.length > 1 ? path.replace(/\/$/, '') : path;
  if (MOVED[p]) return MOVED[p];
  // An unknown old System section lands on its first one, as it did before.
  return p.startsWith('/system/') ? '/settings/overview' : null;
}

export function isSettingsPath(path: string): boolean {
  return path === '/settings' || path.startsWith('/settings/');
}

/** The section id in "/settings/<id>", or `fallback` when missing or not one of `ids`. */
export function sectionFromPath(path: string, ids: readonly string[], fallback: string): string {
  const m = /^\/settings\/([a-z0-9-]+)\/?$/.exec(path);
  return m && ids.includes(m[1]) ? m[1] : fallback;
}

/**
 * The section a settings route shows, or null when the page does not exist for this viewer (the app then shows
 * the Dashboard). `ids` are the sections the viewer may open; `fallback` is where "#/settings" (or an unknown
 * id, for an admin) lands. A viewer who may only open the account pages never gets the fallback for an unknown id.
 */
export function settingsSection(path: string, ids: readonly string[], fallback: string, admin: boolean): string | null {
  if (!isSettingsPath(path)) return null;
  if (path === '/settings' || path === '/settings/') return fallback;
  const id = sectionFromPath(path, ids, '');
  if (id) return id;
  return admin ? fallback : null;
}

/** Line-count clamp, the same bounds as the server (50..1000). */
export function clampLines(n: number): number {
  return Number.isFinite(n) ? Math.min(1000, Math.max(50, Math.trunc(n))) : 200;
}

/** Short summary of an audit detail JSON ({"from":"a","to":"b"}) for the table. */
export function auditSummary(detail: string): string {
  if (!detail) return '';
  try {
    const d = JSON.parse(detail) as Record<string, unknown>;
    if (d && typeof d === 'object' && !Array.isArray(d)) {
      const e = Object.entries(d);
      if (!e.length) return '';
      return e.map(([k, v]) => `${k}: ${typeof v === 'object' ? JSON.stringify(v) : String(v)}`).join(', ');
    }
  } catch { /* not JSON: show as stored */ }
  return detail;
}
