import type { Settings } from './types';

/** Server-wide settings (from the SSE hello / GET /api/settings). */
export const settings = $state<Settings>({});

export function applySettings(s: Settings) {
  Object.assign(settings, s);
}
