import type { Settings } from './types';

// The connect checkboxes are deliberately not persisted: every page load starts
// from the safe defaults (Disconnect before Connect on, before Monitor off), so
// a choice to cross-link is never carried over to the next session.
const initial = (def: boolean) => ({ value: def, local: false });

/** Server-wide settings (from the SSE hello / GET /api/settings). */
export const settings = $state<Settings>({ favoritesScan: false });
/** "Disconnect before Connect": on at every load; a change lasts until reload. */
export const exclusive = $state(initial(true));
/** "Disconnect before Monitor": off at every load; a change lasts until reload. */
export const exclusiveMonitor = $state(initial(false));

/** Apply server settings (the connect checkboxes are not server settings: fixed defaults, reset on reload). */
export function applySettings(s: Settings) {
  settings.favoritesScan = !!s.favoritesScan;
}
export function setExclusive(v: boolean) { exclusive.value = v; exclusive.local = true; }
export function setExclusiveMonitor(v: boolean) { exclusiveMonitor.value = v; exclusiveMonitor.local = true; }
