// The Settings area's sections: THE one list. A new section is one component plus one line here
// (nothing else in the shell changes). Groups are shown in the order of their first section.
import type { Component } from 'svelte';

export interface SettingsSection {
  /** URL part: #/settings/<id>. Lowercase letters, digits and hyphens. */
  id: string;
  label: string;
  /** Page heading above the section; defaults to the label. */
  title?: string;
  /** Group heading in the list. Consecutive sections with the same group sit together. */
  group?: string;
  /** Admins only. Without it everyone (operators and viewers too) can open the section. */
  admin?: boolean;
  /** The component draws its own heading, so the shell adds none. */
  bare?: boolean;
  /** Listed only when the node has audio devices (the page itself shows an empty state otherwise). */
  needsAudio?: boolean;
  /** Lazy, so a section's code loads only when it is opened. */
  load: () => Promise<{ default: Component }>;
}

export const sections: SettingsSection[] = [
  { id: 'asterisk', label: 'Asterisk files', group: 'AllStarLink', admin: true, bare: true, load: () => import('../Config.svelte') },
  { id: 'audio-levels', label: 'Audio levels', group: 'AllStarLink', admin: true, bare: true, needsAudio: true, load: () => import('../Tune.svelte') },
  { id: 'node-list', label: 'Node list', group: 'AllStarLink', admin: true, bare: true, load: () => import('./NodeList.svelte') },

  { id: 'dv-engine', label: 'Engine', group: 'Digital Modes', admin: true, bare: true, load: () => import('./DvEngine.svelte') },
  { id: 'dv-identity', label: 'Identity', group: 'Digital Modes', admin: true, bare: true, load: () => import('./DvIdentitySection.svelte') },
  { id: 'dv-bridge', label: 'Bridge node', group: 'Digital Modes', admin: true, bare: true, load: () => import('./DvBridgeSection.svelte') },
  { id: 'dv-audio', label: 'Audio', group: 'Digital Modes', admin: true, bare: true, load: () => import('./DvAudioSection.svelte') },
  { id: 'dv-user-list', label: 'User list (radioid.net)', title: 'User list', group: 'Digital Modes', admin: true, bare: true, load: () => import('./DvUserListSection.svelte') },
  { id: 'dv-callsigns', label: 'Callsign lookup (hamdb.org)', title: 'Callsign lookup', group: 'Digital Modes', admin: true, load: () => import('./DvCallsignsSection.svelte') },

  { id: 'users', label: 'Users', group: 'Access', admin: true, bare: true, load: () => import('./Users.svelte') },
  { id: 'public', label: 'Public page', group: 'Access', admin: true, bare: true, load: () => import('../PublicSettings.svelte') },

  { id: 'overview', label: 'Overview', title: 'Host and time', group: 'System', admin: true, load: () => import('./Overview.svelte') },
  { id: 'updates', label: 'Updates', group: 'System', admin: true, bare: true, load: () => import('./Updates.svelte') },
  { id: 'network', label: 'Network', group: 'System', admin: true, load: () => import('./Network.svelte') },
  { id: 'wifi', label: 'WiFi', group: 'System', admin: true, load: () => import('./Wifi.svelte') },
  { id: 'web-port', label: 'Web port', title: 'Web port of ASLCP', group: 'System', admin: true, load: () => import('./WebPort.svelte') },
  { id: 'asl-services', label: 'ASL web services', group: 'System', admin: true, load: () => import('./AslServices.svelte') },
  { id: 'terminal', label: 'Terminal', group: 'System', admin: true, load: () => import('./Terminal.svelte') },

  { id: 'backup', label: 'Backup & restore', group: 'Maintenance', admin: true, load: () => import('../Backup.svelte') },
  { id: 'logs', label: 'Logs & services', group: 'Maintenance', admin: true, load: () => import('./LogsServices.svelte') },
  { id: 'diagnostics', label: 'Diagnostics', group: 'Maintenance', admin: true, load: () => import('./Diagnostics.svelte') },
  { id: 'audit', label: 'Audit log', group: 'Maintenance', admin: true, load: () => import('./AuditLog.svelte') },

  { id: 'about', label: 'About', group: 'About', bare: true, load: () => import('./About.svelte') },
  { id: 'account', label: 'Account', group: 'Account', bare: true, load: () => import('./Account.svelte') },
];

/** Where "#/settings" lands: Overview for an admin, Account for everyone else. */
export const defaultFor = (admin: boolean): string => (admin ? 'overview' : 'account');

/** The sections a viewer may open. `audio` false hides the ones that need audio devices (for the list only). */
export function visibleSections(list: readonly SettingsSection[], admin: boolean, audio = true): SettingsSection[] {
  return list.filter((s) => (admin || !s.admin) && (audio || !s.needsAudio));
}

/** Splits a list into runs of consecutive sections that share a group. */
export function groupSections(list: readonly SettingsSection[]): SettingsSection[][] {
  const groups: SettingsSection[][] = [];
  for (const s of list) {
    const last = groups[groups.length - 1];
    if (last && last[0].group === s.group) last.push(s);
    else groups.push([s]);
  }
  return groups;
}
