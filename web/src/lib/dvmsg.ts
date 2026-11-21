// Human messages for DVSwitch save errors, and "did anything change" checks for the setup page.
const FIELD: Record<string, string> = {
  'identity/callsign': 'Callsign', 'identity/dmrId': 'DMR ID', 'identity/essid': 'ESSID', 'identity/nxdnId': 'NXDN ID',
  'DMR Network/Address': 'DMR master address', 'DMR Network/Port': 'DMR master port', 'DMR Network/Password': 'DMR master password',
  'General/Callsign': 'Callsign', 'General/Id': 'DMR ID', 'NXDN/Id': 'NXDN ID',
  'brandmeister/server': 'BrandMeister master', 'brandmeister/password': 'BrandMeister password', 'brandmeister/talkerAlias': 'Talker alias',
  'brandmeister/startTg': 'Start talkgroup', 'brandmeister/ports': 'STFU ports 36100/36103 (in use)', 'brandmeister/action': 'BrandMeister action', brandmeister: 'BrandMeister request',
  modes: 'Modes', enabled: 'Enable or Disable', 'network/address': 'DMR master address', 'network/port': 'DMR master port', 'network/password': 'DMR network password',
  'network/fields': 'DMR network settings', 'network/id': 'DMR network', 'network/action': 'DMR network action', 'modes/dmr': 'DMR (use the network card)',
  credentials: 'DMR master', file: 'File', base: 'File version',
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

/** Messages for the conflict codes of the mode cards (docs/API.md "DVSwitch setup"). */
const CODE: Record<string, string> = {
  mode_disabled: 'That mode is disabled. Enable it on its card in the DVSwitch tab first.',
  net_not_configured: 'Save this DMR network\'s settings first, then Enable.',
  net_master: 'That master belongs to another DMR network: set it up on that network\'s card.',
  bm_master: 'That is a BrandMeister master: set BrandMeister up on its own card.',
  bm_not_configured: 'BrandMeister is not set up yet: save the hotspot security password and set the 7-digit DMR ID under Identity, then Enable.',
};

/** What to show for a failed save: for invalid_field, which field; for a known conflict, what to do; otherwise the server's message. */
export function dvErrorMessage(e: unknown): string {
  const code = e instanceof Error ? (e as { code?: string }).code : undefined;
  if (code && CODE[code]) return CODE[code];
  if (e instanceof Error && code === 'invalid_field') {
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
