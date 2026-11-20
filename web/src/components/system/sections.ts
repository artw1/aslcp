// The System tab's sub-sections: THE one list. A new section is one component in
// this folder plus one line here (nothing else in the shell changes).
import type { Component } from 'svelte';

export interface SystemSection {
  /** URL part: #/system/<id>. Lowercase letters, digits and hyphens. */
  id: string;
  label: string;
  /** Page heading under the list; defaults to the label. */
  title?: string;
  /** Sections with the same group sit together; a gap separates groups. */
  group?: number;
  /** Lazy, so a section's code loads only when it is opened. */
  load: () => Promise<{ default: Component }>;
}

export const sections: SystemSection[] = [
  { id: 'overview', label: 'Overview', title: 'Host and time', group: 1, load: () => import('./Overview.svelte') },
  { id: 'updates', label: 'Updates', group: 1, load: () => import('./Updates.svelte') },
  { id: 'network', label: 'Network', group: 2, load: () => import('./Network.svelte') },
  { id: 'wifi', label: 'WiFi', group: 2, load: () => import('./Wifi.svelte') },
  { id: 'asl-services', label: 'ASL services', title: 'ASL web services', group: 3, load: () => import('./AslServices.svelte') },
  { id: 'web-port', label: 'Web port', title: 'Web port of ASLCP', group: 3, load: () => import('./WebPort.svelte') },
  { id: 'terminal', label: 'Terminal', group: 4, load: () => import('./Terminal.svelte') },
  { id: 'logs', label: 'Logs & services', group: 5, load: () => import('./LogsServices.svelte') },
  { id: 'diagnostics', label: 'Diagnostics', group: 5, load: () => import('./Diagnostics.svelte') },
  { id: 'audit', label: 'Audit log', group: 5, load: () => import('./AuditLog.svelte') },
];

export const defaultSection = sections[0].id;

/** Splits a list into runs of consecutive sections that share a group. */
export function groupSections(list: SystemSection[]): SystemSection[][] {
  const groups: SystemSection[][] = [];
  for (const s of list) {
    const last = groups[groups.length - 1];
    if (last && last[0].group === s.group) last.push(s);
    else groups.push([s]);
  }
  return groups;
}
