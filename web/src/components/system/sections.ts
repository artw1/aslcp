// The System tab's sub-sections: THE one list. A new section is one component in
// this folder plus one line here (nothing else in the shell changes).
import type { Component } from 'svelte';

export interface SystemSection {
  /** URL part: #/system/<id>. Lowercase letters, digits and hyphens. */
  id: string;
  label: string;
  /** Page heading under the list; defaults to the label. */
  title?: string;
  /** Lazy, so a section's code loads only when it is opened. */
  load: () => Promise<{ default: Component }>;
}

export const sections: SystemSection[] = [
  { id: 'overview', label: 'Overview', title: 'Host and time', load: () => import('./Overview.svelte') },
  { id: 'updates', label: 'Updates', load: () => import('./Updates.svelte') },
  { id: 'network', label: 'Network', load: () => import('./Network.svelte') },
  { id: 'wifi', label: 'WiFi', load: () => import('./Wifi.svelte') },
  { id: 'logs', label: 'Logs & services', load: () => import('./LogsServices.svelte') },
  { id: 'asl-services', label: 'ASL services', title: 'ASL web services', load: () => import('./AslServices.svelte') },
  { id: 'web-port', label: 'Web port', title: 'Web port of ASLCP', load: () => import('./WebPort.svelte') },
  { id: 'audit', label: 'Audit log', load: () => import('./AuditLog.svelte') },
  { id: 'terminal', label: 'Terminal', load: () => import('./Terminal.svelte') },
];

export const defaultSection = sections[0].id;
