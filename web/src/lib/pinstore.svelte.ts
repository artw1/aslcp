import { PIN_KEY, parsePins, togglePin } from './pins';
import type { PinRef } from './pins';

// Pinned favourites live in this browser only (localStorage), as references to favourite ids.
function load(): PinRef[] {
  try { return parsePins(localStorage.getItem(PIN_KEY)); } catch { return []; }
}
export const pinned = $state<{ list: PinRef[] }>({ list: load() });
export function togglePinned(ref: PinRef) {
  pinned.list = togglePin(pinned.list, ref);
  try { localStorage.setItem(PIN_KEY, JSON.stringify(pinned.list)); } catch { /* private window or blocked storage: the pin lasts until reload */ }
}
