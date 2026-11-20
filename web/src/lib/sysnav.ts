// Pure routing helpers for the System tab (#/system/<section>).

/** Old top-level routes that moved; the router rewrites them. */
const MOVED: Record<string, string> = { '/updates': '/system/updates' };

export function movedPath(path: string): string | null {
  return MOVED[path] ?? null;
}

export function isSystemPath(path: string): boolean {
  return path === '/system' || path.startsWith('/system/');
}

/** The section id in "/system/<id>", or `fallback` when missing or not one of `ids`. */
export function sectionFromPath(path: string, ids: readonly string[], fallback: string): string {
  const m = /^\/system\/([a-z0-9-]+)\/?$/.exec(path);
  return m && ids.includes(m[1]) ? m[1] : fallback;
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
