// Types and pure helpers for the Access lists card and form (docs/CONFIG.md "Access lists", docs/API.md "Access lists").

export interface AccessEntry { family: 'allowlist' | 'denylist'; local: string; remote: string; web?: boolean; note?: string }
export interface AccessLists {
  node: string; mode: 'allow' | 'deny' | 'open'; allow: AccessEntry[]; deny: AccessEntry[];
  /** The dialplan in extensions.conf checks the lists. */
  enforced: boolean; limit: number; noteMax: number;
}

/** Node number the server accepts for an entry: 1 to 7 digits, no leading zero. */
export const validAccessNode = (s: string): boolean => /^[1-9][0-9]{0,6}$/.test(s);
/** A note: letters, digits, space, . _ -; at most 40 characters. */
export const validAccessNote = (s: string): boolean => /^[A-Za-z0-9 ._-]{0,40}$/.test(s);

/** Error text for the add row, '' when the entry can be sent. */
export function accessAddError(l: AccessLists, family: 'allowlist' | 'denylist', remote: string, note: string): string {
  const r = remote.trim();
  if (r === '') return '';
  if (!validAccessNode(r)) return 'A node number of 1 to 7 digits';
  if (r === l.node) return 'A node cannot be on its own list';
  const list = family === 'allowlist' ? l.allow : l.deny;
  if (list.some((e) => e.remote === r)) return 'Already on the list';
  if (list.length >= l.limit) return `A list holds at most ${l.limit} nodes`;
  if (!validAccessNote(note.trim())) return 'Note: letters, digits, space, . _ - (at most 40)';
  return '';
}

/** What the dialplan does for this node, in a sentence. */
export function accessModeText(mode: AccessLists['mode']): string {
  switch (mode) {
    case 'allow': return 'Allow list: only the nodes on it can connect.';
    case 'deny': return 'Deny list: every node can connect except the ones on it.';
    default: return 'Open: every node can connect.';
  }
}

/** One line for the overview card. */
export function accessSummary(l: AccessLists | null): string {
  if (!l) return 'Loading…';
  if (!l.enforced) return 'The dialplan does not check access lists, so entries have no effect.';
  const a = l.allow.length, d = l.deny.length;
  if (a) return `Allow list with ${a} node${a === 1 ? '' : 's'}: only they can connect.` + (d ? ` The deny list (${d}) is ignored while there is an allow list.` : '');
  if (d) return `Deny list with ${d} node${d === 1 ? '' : 's'}: they cannot connect.`;
  return 'Open: every node can connect.';
}

/** The warning shown before an entry is added or removed, or ''. */
export function accessWarning(l: AccessLists, op: 'add' | 'remove', family: 'allowlist' | 'denylist'): string {
  if (op === 'add' && family === 'allowlist' && l.allow.length === 0)
    return 'This starts an allow list: from now on only the nodes on it can connect to this node. Add every node that should still connect.';
  if (op === 'add' && family === 'denylist' && l.allow.length > 0)
    return 'The deny list is not used while the allow list has entries.';
  if (op === 'remove' && family === 'allowlist' && l.allow.length === 1)
    return 'This is the last allow list entry: removing it opens this node to every node (except those on the deny list).';
  return '';
}
