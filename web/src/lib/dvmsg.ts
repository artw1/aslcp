// Human messages for DVSwitch save errors, and "did anything change" checks for the setup page.
const FIELD: Record<string, string> = {
  'identity/callsign': 'Callsign', 'identity/dmrId': 'DMR ID', 'identity/essid': 'ESSID', 'identity/nxdnId': 'NXDN ID',
  'DMR Network/Address': 'DMR master address', 'DMR Network/Port': 'DMR master port', 'DMR Network/Password': 'DMR master password',
  'AMBE_AUDIO/ambeMode': 'Start-up mode', 'AMBE_AUDIO/txTg': 'Start-up talkgroup', 'AMBE_AUDIO/txTs': 'Start-up slot', 'AMBE_AUDIO/colorCode': 'Color code',
  'General/Callsign': 'Callsign', 'General/Id': 'DMR ID', 'NXDN/Id': 'NXDN ID', 'Network/Startup': 'Gateway start-up reflector or talkgroup',
  'dmrVia/server': 'BrandMeister server', 'dmrVia/talkerAlias': 'Talker alias', 'dmrVia/startTg': 'Start talkgroup', 'dmrVia/network': 'DMR network',
  'dmrVia/via': 'DMR connection', dmrVia: 'DMR connection', 'dmrVia/ports': 'STFU ports (no free pair)', 'modes/dmr': 'Turning DMR off while STFU carries it',
  modes: 'Modes', startup: 'Start-up values', credentials: 'DMR master', file: 'File', base: 'File version',
};

/** The field named in the helper's "invalid_field: a value is not allowed (path)" text, as a label; '' when unknown. */
export function fieldLabel(message: string): string {
  const m = /\(([^()]+)\)\s*$/.exec(message);
  if (!m) return '';
  const path = m[1];
  if (FIELD[path]) return FIELD[path];
  const tail = path.split('/').slice(-2).join('/');
  return FIELD[tail] ?? path;
}

/** What to show for a failed save: for invalid_field, which field; otherwise the server's message. */
export function dvErrorMessage(e: unknown): string {
  if (e instanceof Error && (e as { code?: string }).code === 'invalid_field') {
    const f = fieldLabel(e.message);
    return f ? `${f} is not valid. Check the value and try again.` : 'A value is not valid. Check the fields and try again.';
  }
  return e instanceof Error ? e.message : String(e);
}

export interface DVNetForm { address: string; port: string; password: string }
export interface DVCredsPatch { dmrAddress?: string; dmrPort?: number; dmrPassword?: string; clearPassword?: true }

/**
 * The credentials body for the fields that changed, or null when nothing did. A typed password and "clear"
 * are never sent together: clear wins (the box is disabled while it is selected).
 */
export function credentialsPatch(snap: DVNetForm, net: DVNetForm, clearPassword: boolean): DVCredsPatch | null {
  const b: DVCredsPatch = {};
  if (net.address !== snap.address) b.dmrAddress = net.address;
  if (net.port !== snap.port && net.port.trim() !== '') b.dmrPort = Number(net.port);
  if (clearPassword) b.clearPassword = true;
  else if (net.password) b.dmrPassword = net.password;
  return Object.keys(b).length ? b : null;
}

/** True when the two forms differ in any field (a typed password always counts as a change). */
export function formChanged(before: unknown, after: unknown): boolean {
  return JSON.stringify(before) !== JSON.stringify(after);
}
