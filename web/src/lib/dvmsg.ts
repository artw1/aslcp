// Human messages for digital modes errors.
const FIELD: Record<string, string> = {
  mode: 'Mode', tune: 'Target', node: 'Bridge node', rxPort: 'Receive port', txPort: 'Transmit port', file: 'File', base: 'File version',
};

/** The field named in the helper's "invalid_field: a value is not allowed (path)" text, as a label; '' when unknown. */
export function fieldLabel(message: string): string {
  const m = /\(([^()]+)\)\s*$/.exec(message);
  if (!m) return '';
  const path = m[1];
  if (FIELD[path]) return FIELD[path];
  const parts = path.split('/');
  return FIELD[parts.slice(-2).join('/')] ?? FIELD[parts[parts.length - 1]] ?? path;
}

/** Messages for the error codes of the mode and tune calls (plan C.2). */
const CODE: Record<string, string> = {
  mode_disabled: 'That mode is disabled.',
  not_available: 'That mode is not yet available in this build.',
  unknown_target: 'That target is not in the host list.',
  not_enabled: 'Digital modes are not enabled. Enable them on the Digital modes page first.',
  not_supported: 'This node cannot run the digital modes engine.',
  busy: 'The engine is busy with a call. Try again in a moment.',
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
