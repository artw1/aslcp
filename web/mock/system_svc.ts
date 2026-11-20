// In-memory ASL services switch and web port for `npm run dev:mock` (docs/API.md
// "ASL services and web port"). Dev only.
type Ctx = {
  m: string; p: string; url: URL; body: any; user: { username: string; role: string } | undefined;
  json: (code: number, body?: unknown) => void; err: (code: number, msg: string, c: string) => void;
};

const SCOPES = [
  { scope: 'cockpit', label: 'Cockpit', units: ['cockpit.socket', 'cockpit.service'], fw: ['cockpit'] },
  { scope: 'apache', label: 'Apache', units: ['apache2'], fw: ['http', 'https'] },
  { scope: 'allmon3', label: 'Allmon3', units: ['allmon3'], fw: [] as string[] },
];
const off: Record<string, boolean> = {};
let port = 8080;
let pending = false;

function ports() {
  const rows: [string, string, number[]][] = [['apache', '80', [80]], ['apache', '443', [443]], ['cockpit', '9090', [9090]], ['allmon3', '16080', [16080]], ['allmon3', '16700+', [16700, 16701]]];
  return rows.map(([scope, label, ps]) => ({ scope, label, listening: !off[scope], ports: off[scope] ? [] : ps, addrs: off[scope] ? [] : ['*'] }));
}
function status() {
  return {
    available: true, portsAvailable: true, aslcpPort: port, aslcpOnPort80: port === 80, firewalld: true, ports: ports(),
    scopes: SCOPES.map((s) => ({
      scope: s.scope, label: s.label, installed: true, active: !off[s.scope], enabled: !off[s.scope], recorded: !!off[s.scope],
      units: s.units.map((unit) => ({ unit, description: '', active: off[s.scope] ? 'inactive' : 'active', sub: off[s.scope] ? 'dead' : 'running', enabled: unit === 'cockpit.service' ? 'static' : 'enabled', since: '' })),
      firewall: s.fw.map((service) => ({ service, open: !off[s.scope] })),
    })),
  };
}
function webPort() {
  const busy = !off.apache && port !== 80;
  return {
    available: true, listen: ':' + port, port, on80: port === 80, canMove: port !== 80 && !busy,
    reason: port === 80 ? 'already' : busy ? 'busy' : '', apacheActive: !off.apache, dropIn: port === 80, previous: ':8080', backPort: 8080,
    ...(pending ? { pending: { deadline: new Date(Date.now() + 60e3).toISOString(), seconds: 60 } } : {}),
  };
}

export function handleSvc(c: Ctx): boolean {
  const { m, p } = c;
  const set = p.match(/^\/system\/asl-services\/([a-z0-9]+)$/);
  if (p !== '/system/asl-services' && p !== '/system/web-port' && !set) return false;
  if (c.user?.role !== 'admin') { c.err(403, 'forbidden', 'forbidden'); return true; }
  if (p === '/system/asl-services' && m === 'GET') { c.json(200, status()); return true; }
  if (set && m === 'PUT') {
    const sc = SCOPES.find((s) => s.scope === set[1]);
    if (!sc) { c.err(404, 'unknown service', 'not_found'); return true; }
    const a = c.body?.action;
    if (a !== 'enable' && a !== 'disable') { c.err(400, 'action must be enable or disable', 'bad_request'); return true; }
    if (a === 'enable' && sc.scope === 'apache' && port === 80) { c.err(409, 'ASLCP is using port 80. Move ASLCP to another port first.', 'port80_held'); return true; }
    off[sc.scope] = a === 'disable';
    c.json(200, { ...status(), changed: sc.units, firewallChanged: c.body?.firewall ? sc.fw : [] }); return true;
  }
  if (p === '/system/web-port' && m === 'GET') { c.json(200, webPort()); return true; }
  if (p === '/system/web-port' && m === 'PUT') {
    const a = c.body?.action;
    if (a === 'move') {
      if (port === 80) { c.err(409, 'ASLCP already uses port 80', 'already_port80'); return true; }
      if (!off.apache) { c.err(409, 'port 80 is in use by another program (Apache?). Turn it off first.', 'port80_busy'); return true; }
      port = 80; pending = true;
    } else if (a === 'back') {
      if (port !== 80) { c.err(409, 'ASLCP is not on port 80', 'not_port80'); return true; }
      port = 8080; pending = false;
    } else if (a === 'confirm') pending = false;
    else { c.err(400, 'action must be move, back or confirm', 'bad_request'); return true; }
    c.json(200, webPort()); return true;
  }
  return false;
}
