// Human messages for DVSwitch save errors, and "did anything change" checks for the setup page.
const FIELD: Record<string, string> = {
  'identity/callsign': 'Callsign', 'identity/dmrId': 'DMR ID', 'identity/essid': 'ESSID', 'identity/nxdnId': 'NXDN ID',
  'DMR Network/Address': 'DMR master address', 'DMR Network/Port': 'DMR master port', 'DMR Network/Password': 'DMR master password',
  'AMBE_AUDIO/ambeMode': 'Start-up mode', 'AMBE_AUDIO/txTg': 'Start-up talkgroup', 'AMBE_AUDIO/txTs': 'Start-up slot', 'AMBE_AUDIO/colorCode': 'Color code',
  'General/Callsign': 'Callsign', 'General/Id': 'DMR ID', 'NXDN/Id': 'NXDN ID', 'Network/Startup': 'Gateway start-up reflector or talkgroup',
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

/** True when the two forms differ in any field (a typed password always counts as a change). */
export function formChanged(before: unknown, after: unknown): boolean {
  return JSON.stringify(before) !== JSON.stringify(after);
}
