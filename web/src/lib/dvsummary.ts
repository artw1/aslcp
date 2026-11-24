// The Networks list of the digital modes setup page: the groups, the one-line summary of each row and the
// toast after the row's Enable switch. Pure helpers so node --test can cover them.
import { UNAVAILABLE_TEXT } from './dvmodes.ts';
import type { DVNetView, DVTuneMode } from './types';

/** The rows of the Networks card in order: DMR has a heading, the other modes are rows of their own. */
export const NETWORK_GROUPS: readonly { title?: string; ids: readonly DVTuneMode[] }[] = [
  { title: 'DMR', ids: ['BM', 'TGIF', 'DMR'] },
  { ids: ['YSF', 'P25', 'NXDN', 'M17'] },
];

/** One muted line from the saved values of a network. `v` is undefined when the settings are not readable (an operator). */
export function netSummary(v: DVNetView | undefined, available: boolean, enabled: boolean): string {
  if (!available) return UNAVAILABLE_TEXT;
  if (!v) return enabled ? 'Enabled' : '';
  const val = (k: string) => (v.values[k] ?? '').trim();
  const tune = enabled ? 'Tune on the dashboard' : 'Enable it, then tune on the dashboard';
  const master = (v.address || v.defaultAddress || '').trim();
  switch (v.mode) {
    case 'BM': return v.address.trim() ? `Master: ${v.address.trim()}` : 'Not set up';
    case 'TGIF': return !v.passwordSet && !v.defaultPassword ? 'Not set up' : [master ? `Master: ${master}` : '', val('slot') ? `TS ${val('slot')}` : ''].filter(Boolean).join(', ') || 'Password set';
    case 'DMR': return master ? [`Master: ${master}`, `TS ${val('slot') || '2'}`].join(', ') : 'Not set up';
    case 'NXDN': return val('nxdn_id') ? `NXDN ID ${val('nxdn_id')}` : 'Not set up';
    case 'M17': return val('suffix') ? `Suffix ${val('suffix')}` : tune;
    default: return tune;
  }
}

/** The toast after the Enable switch of a row was saved. */
export function enableToast(label: string, on: boolean, applyError?: string): string {
  const base = `${label} ${on ? 'enabled' : 'disabled'}`;
  return applyError ? `${base}. ${applyError}` : base;
}
