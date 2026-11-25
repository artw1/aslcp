// The Settings area: THE one list. Seven groups (#/settings/<group>); a group is either a page of stacked cards or a
// set of tabs (#/settings/<group>/<tab>). A new page is one component plus one line here; the shell does not change.
import type { Component } from 'svelte';

type Loader = () => Promise<{ default: Component }>;

export interface SettingsTab {
  /** URL part: #/settings/<group>/<id>. Lowercase letters, digits and hyphens. */
  id: string;
  label: string;
  /** Longer name of the page; the shell draws no heading, so the page uses it itself if it wants one. */
  title?: string;
  /** The component draws its own heading, so the shell adds none. */
  bare?: boolean;
  /** Shown only when the node has audio devices (the page itself shows an empty state otherwise). */
  needsAudio?: boolean;
  /** Lazy, so a page's code loads only when it is opened. */
  load: Loader;
}

export interface SettingsGroup {
  /** URL part: #/settings/<id>. */
  id: string;
  label: string;
  /** Admins only. Without it everyone (operators and viewers too) can open the group. */
  admin?: boolean;
  /** Tabbed group: a tab bar, one page per tab. The first visible tab opens when the URL names none. */
  tabs?: SettingsTab[];
  /** Single-page group: these components, stacked in this order. */
  pages?: Loader[];
}

export const groups: SettingsGroup[] = [
  { id: 'allstarlink', label: 'AllStarLink', admin: true, tabs: [
    { id: 'asterisk', label: 'Asterisk files', bare: true, load: () => import('../Config.svelte') },
    { id: 'audio-levels', label: 'Audio levels', bare: true, needsAudio: true, load: () => import('../Tune.svelte') },
    { id: 'node-list', label: 'Node list', bare: true, load: () => import('./NodeList.svelte') },
  ] },
  { id: 'digital-modes', label: 'Digital Modes', admin: true, pages: [() => import('./DigitalModes.svelte')] },
  { id: 'access', label: 'Access', admin: true, pages: [() => import('./Users.svelte'), () => import('../PublicSettings.svelte')] },
  { id: 'system', label: 'System', admin: true, tabs: [
    { id: 'overview', label: 'Overview', title: 'Host and time', load: () => import('./Overview.svelte') },
    { id: 'updates', label: 'Updates', bare: true, load: () => import('./Updates.svelte') },
    { id: 'network', label: 'Network', load: () => import('./Network.svelte') },
    { id: 'wifi', label: 'WiFi', load: () => import('./Wifi.svelte') },
    { id: 'web-port', label: 'Web port', title: 'Web port of ASLCP', load: () => import('./WebPort.svelte') },
    { id: 'asl-services', label: 'ASL web services', load: () => import('./AslServices.svelte') },
    { id: 'terminal', label: 'Terminal', load: () => import('./Terminal.svelte') },
  ] },
  { id: 'maintenance', label: 'Maintenance', admin: true, tabs: [
    { id: 'backup', label: 'Backup & restore', load: () => import('../Backup.svelte') },
    { id: 'logs', label: 'Logs & services', load: () => import('./LogsServices.svelte') },
    { id: 'diagnostics', label: 'Diagnostics', load: () => import('./Diagnostics.svelte') },
    { id: 'audit', label: 'Audit log', load: () => import('./AuditLog.svelte') },
  ] },
  { id: 'about', label: 'About', pages: [() => import('./About.svelte')] },
  { id: 'account', label: 'Account', pages: [() => import('./Account.svelte')] },
];

/** Where "#/settings" lands: System for an admin, Account for everyone else. */
export const defaultFor = (admin: boolean): string => (admin ? 'system' : 'account');

/** The groups a viewer may open. */
export function visibleGroups(list: readonly SettingsGroup[], admin: boolean): SettingsGroup[] {
  return list.filter((g) => admin || !g.admin);
}

/** A group's tabs. `audio` false hides the ones that need audio devices (for the tab bar only). */
export function visibleTabs(g: SettingsGroup, audio = true): SettingsTab[] {
  return (g.tabs ?? []).filter((t) => audio || !t.needsAudio);
}
