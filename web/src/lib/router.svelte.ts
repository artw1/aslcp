// Hash router: "#/path?query". `path` excludes the query string.
function parse(): { path: string; query: string } {
  const h = location.hash.replace(/^#/, '');
  if (!h.startsWith('/')) return { path: '/', query: '' };
  const i = h.indexOf('?');
  return i < 0 ? { path: h, query: '' } : { path: h.slice(0, i), query: h.slice(i + 1) };
}
export const router = $state<{ path: string; query: string }>(parse());
addEventListener('hashchange', () => { Object.assign(router, parse()); });
export function navigate(path: string) { if (parse().path !== path || parse().query) location.hash = path; else Object.assign(router, parse()); }
