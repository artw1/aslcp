// Types and pure helpers of the System tab's "ASL services" and "Web port"
// sections (docs/API.md "ASL services and web port"). No imports from the app,
// so node:test runs it directly.

export interface SvcUnit { unit: string; description: string; active: string; sub: string; enabled: string; since: string }
export interface SvcFirewall { service: string; open: boolean }
export interface SvcScope {
  scope: 'cockpit' | 'apache' | 'allmon3'; label: string; installed: boolean; active: boolean; enabled: boolean; recorded: boolean;
  units: SvcUnit[]; firewall: SvcFirewall[];
}
export interface SvcPort { scope: string; label: string; listening: boolean; ports: number[]; addrs: string[] }
export interface SvcStatus {
  available: boolean; scopes: SvcScope[]; portsAvailable: boolean; ports: SvcPort[]; aslcpPort: number; aslcpOnPort80: boolean; firewalld: boolean;
}
export interface SvcSetResult extends SvcStatus { changed: string[]; firewallChanged: string[]; firewallError?: string }
export interface WebPortStatus {
  available: boolean; listen: string; port: number; on80: boolean; canMove: boolean; reason: string; apacheActive: boolean;
  dropIn: boolean; previous: string; backPort: number; pending?: { deadline: string; seconds: number }; reverted?: string; firewall?: string;
}

/** What turning a service off costs (plan 6.3): shown in the confirm dialog. */
export const LOSSES: Record<string, string> = {
  cockpit: 'Cockpit is your fallback terminal, log viewer and WiFi settings if ASLCP ever breaks. Keep it if you rely on its terminal or WiFi settings, and make sure SSH works before you turn it off.',
  apache: 'Apache serves the ASL3 landing page, the /allmon3/master/ proxy path and anything else on ports 80 and 443. Allmon3 itself stays reachable on ports 16080 and 16700+.',
  allmon3: 'Allmon3 is the monitoring daemon and web UI on ports 16080 and 16700+. Other people may use it, even through a public link.',
};

/** Apache is off while Allmon3 still answers on its own ports (plan 6.4). */
export function allmonStillOpen(scopes: SvcScope[]): boolean {
  const apache = scopes.find((s) => s.scope === 'apache');
  const allmon = scopes.find((s) => s.scope === 'allmon3');
  return !!apache && apache.installed && !apache.active && !!allmon && allmon.installed && allmon.active;
}

/** The "also turn Allmon3 off" checkbox is offered when Apache goes off and Allmon3 is running. */
export function offersAllmon(scopes: SvcScope[], scope: string, action: string): boolean {
  if (scope !== 'apache' || action !== 'disable') return false;
  const a = scopes.find((s) => s.scope === 'allmon3');
  return !!a && a.installed && a.active;
}

/** Firewall checkbox text, or '' when firewalld does not run or the service has no firewalld services. */
export function firewallText(s: SvcScope, action: string): string {
  const names = s.firewall.map((f) => f.service);
  if (!names.length) return '';
  const list = names.join(' and ');
  return action === 'disable' ? `Also close ${list} in the firewall` : `Also reopen ${list} in the firewall`;
}

/** One line for a listening-port row: "listening on all addresses", "closed", ... */
export function listenText(p: SvcPort): string {
  if (!p.listening) return 'closed';
  const all = p.addrs.some((a) => a === '*' || a === '0.0.0.0' || a === '::');
  const where = all ? 'all addresses' : p.addrs.join(', ');
  const ports = p.ports.length > 1 ? ` (${p.ports.join(', ')})` : '';
  return `listening on ${where}${ports}`;
}

/** Address of ASLCP on `port`, built from the browser's own host (plan 7.4). Port 80 is left out. */
export function webAddress(loc: { protocol: string; hostname: string }, port: number, path = '/'): string {
  const host = loc.hostname.includes(':') && !loc.hostname.startsWith('[') ? `[${loc.hostname}]` : loc.hostname;
  return `${loc.protocol}//${host}${port === 80 ? '' : ':' + port}${path}`;
}

/** Port the browser page is on (80 when the URL has none). */
export function pagePort(loc: { port: string; protocol: string }): number {
  if (loc.port) return Number(loc.port);
  return loc.protocol === 'https:' ? 443 : 80;
}

/** True when a pending move should be confirmed from this page: the page itself arrived on the new port. */
export function shouldConfirm(st: WebPortStatus, loc: { port: string; protocol: string }): boolean {
  return !!st.pending && st.on80 && pagePort(loc) === 80;
}

/**
 * After a move the old address goes away. This watches it (same origin, so the
 * page's own CSP allows it): `probe` throws while the server is down. When it
 * answers again after having been down, the helper has moved ASLCP back
 * ("returned"). When it stays down until `timeoutMs`, the move stuck ("gone").
 * The new address cannot be probed from this page: the CSP only allows the
 * page's own origin, and another port is another origin.
 */
export async function watchOldAddress(o: { probe: () => Promise<unknown>; timeoutMs: number; intervalMs?: number; sleep?: (ms: number) => Promise<void>; now?: () => number }): Promise<'returned' | 'gone'> {
  const sleep = o.sleep ?? ((ms: number) => new Promise<void>((r) => setTimeout(r, ms)));
  const now = o.now ?? Date.now;
  const end = now() + o.timeoutMs;
  let sawDown = false;
  for (;;) {
    try {
      await o.probe();
      if (sawDown) return 'returned';
    } catch { sawDown = true; }
    if (now() >= end) return 'gone';
    await sleep(o.intervalMs ?? 2000);
  }
}
