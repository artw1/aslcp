// Pure helpers for the browser terminal (System > Terminal). The wire format
// is in docs/API.md: binary WebSocket messages are keyboard input going out
// and program output coming in; the one text message is a JSON resize.

export interface Loc { protocol: string; host: string }

/** The WebSocket URL for a one-time ticket. */
export function wsURL(loc: Loc, ticket: string, rows: number, cols: number): string {
  const scheme = loc.protocol === 'https:' ? 'wss:' : 'ws:';
  const d = (n: number) => (Number.isFinite(n) && n >= 1 ? Math.min(500, Math.trunc(n)) : 0);
  let q = `ticket=${encodeURIComponent(ticket)}`;
  if (d(rows) && d(cols)) q += `&rows=${d(rows)}&cols=${d(cols)}`;
  return `${scheme}//${loc.host}/api/system/terminal/ws?${q}`;
}

/** The text message that tells the server the terminal changed size. */
export function resizeMsg(rows: number, cols: number): string {
  const c = (n: number) => Math.min(500, Math.max(1, Math.trunc(n)));
  return JSON.stringify({ type: 'resize', rows: c(rows), cols: c(cols) });
}

/** Why a session ended, in words (the close reason sent by the server). */
export function endText(reason: string): string {
  switch (reason) {
    case 'idle': return 'Closed after 15 minutes without typing.';
    case 'limit': return 'Closed at the maximum session length.';
    case 'disabled': return 'The terminal was turned off.';
    case 'logout': return 'Closed because you were logged out.';
    case 'error': return 'The connection to the helper failed.';
    default: return 'Session closed.';
  }
}

/** A stand-in session for `npm run dev:mock`, which has no WebSocket: it only echoes. */
export function mockSession(onData: (b: Uint8Array) => void): { send: (b: Uint8Array) => void } {
  const enc = new TextEncoder();
  onData(enc.encode('Mock mode: this is not a real login.\r\n\r\nnode2001 login: '));
  return {
    send(b) {
      const out: number[] = [];
      for (const c of b) {
        if (c === 13) out.push(...enc.encode('\r\nLogin incorrect (mock)\r\n\r\nnode2001 login: '));
        else if (c === 127) out.push(...enc.encode('\b \b'));
        else if (c >= 32 && c < 127) out.push(c);
      }
      if (out.length) onData(new Uint8Array(out));
    },
  };
}
