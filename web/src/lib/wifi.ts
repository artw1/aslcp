// Pure helpers of the WiFi section (no DOM, no Svelte): the same checks the
// server makes, so the form can say what is wrong before it asks.

const enc = new TextEncoder();

/** SSID: 1 to 32 bytes of text without control characters. Returns '' when valid. */
export function ssidError(s: string): string {
  const n = enc.encode(s).length;
  if (n < 1 || n > 32) return 'The network name must be 1 to 32 bytes long.';
  // eslint-disable-next-line no-control-regex
  if (/[\u0000-\u001f\u007f-\u009f]/.test(s)) return 'The network name must not contain control characters.';
  return '';
}

/** WPA key: empty (open network), 8 to 63 printable ASCII characters, or 64 hex digits. Returns '' when valid. */
export function pskError(s: string): string {
  if (s === '') return '';
  if (s.length === 64) return /^[0-9a-fA-F]{64}$/.test(s) ? '' : 'A 64-character key must be hexadecimal digits.';
  if (s.length < 8 || s.length > 63) return 'The password must be 8 to 63 characters (or 64 hexadecimal digits).';
  if (!/^[\x20-\x7e]+$/.test(s)) return 'The password may only contain printable ASCII characters.';
  return '';
}

/** Returns a new list with the item at `from` moved to `to` (out of range: unchanged). */
export function moveItem<T>(list: T[], from: number, to: number): T[] {
  if (from === to || from < 0 || to < 0 || from >= list.length || to >= list.length) return list.slice();
  const out = list.slice();
  const [x] = out.splice(from, 1);
  out.splice(to, 0, x);
  return out;
}

/** Signal strength 0..100 as a word for people and screen readers. */
export function signalLabel(n: number): string {
  if (n >= 70) return 'strong';
  if (n >= 45) return 'good';
  if (n >= 25) return 'weak';
  return 'very weak';
}

/** Seconds as "3 minutes" / "90 seconds" for the hotspot delay. */
export function delayLabel(s: number): string {
  if (s % 60 === 0) { const m = s / 60; return `${m} minute${m === 1 ? '' : 's'}`; }
  return `${s} seconds`;
}
