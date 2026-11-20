<script lang="ts">
  import '@xterm/xterm/css/xterm.css';
  import { onDestroy } from 'svelte';
  import { api } from '../../lib/api';
  import { toast, toastError } from '../../lib/toast.svelte';
  import { wsURL, resizeMsg, endText, mockSession } from '../../lib/termconn';
  import type { TerminalStatus } from '../../lib/types';
  import Toggle from '../Toggle.svelte';

  let st = $state<TerminalStatus | null>(null), err = $state(''), busy = $state(false);
  let phase = $state<'idle' | 'connecting' | 'open' | 'ended'>('idle'), note = $state('');
  let host: HTMLDivElement | undefined = $state();

  // Loaded only when a session starts: the section itself stays small.
  type Term = import('@xterm/xterm').Terminal;
  type Fit = import('@xterm/addon-fit').FitAddon;
  let term: Term | null = null, fit: Fit | null = null, ws: WebSocket | null = null, ro: ResizeObserver | null = null;
  let mock: { send: (b: Uint8Array) => void } | null = null;

  async function load() {
    try { st = await api.terminal(); err = ''; } catch (e) { err = e instanceof Error ? e.message : String(e); }
  }
  $effect(() => { load(); });

  async function toggle(on: boolean) {
    busy = true;
    try { st = await api.setTerminal(on); if (!on) stop(); toast(on ? 'Terminal turned on' : 'Terminal turned off', 'info'); } catch (e) { toastError(e); } finally { busy = false; }
  }

  function cleanup() {
    ro?.disconnect(); ro = null;
    term?.dispose(); term = null; fit = null;
    mock = null;
  }
  function stop() {
    const w = ws; ws = null;
    if (w) { w.onclose = null; w.close(1000); }
    cleanup();
    if (phase === 'open' || phase === 'connecting') { phase = 'idle'; note = ''; }
  }
  onDestroy(stop);

  async function start() {
    if (phase === 'connecting' || phase === 'open') return;
    phase = 'connecting'; note = '';
    try {
      const [{ Terminal }, { FitAddon }, t] = await Promise.all([import('@xterm/xterm'), import('@xterm/addon-fit'), api.terminalTicket()]);
      if (!host) { phase = 'idle'; return; }
      term = new Terminal({ cursorBlink: true, fontFamily: 'ui-monospace, Menlo, Consolas, monospace', fontSize: 14, scrollback: 2000, convertEol: false });
      fit = new FitAddon();
      term.loadAddon(fit);
      host.replaceChildren();
      term.open(host);
      fit.fit();
      const enc = new TextEncoder();
      const sendResize = () => { if (ws?.readyState === WebSocket.OPEN && term) ws.send(resizeMsg(term.rows, term.cols)); };

      if (t.ticket === 'mock') {
        mock = mockSession((b) => term?.write(b));
        term.onData((d) => mock?.send(enc.encode(d)));
        phase = 'open'; term.focus();
        return;
      }
      const sock = new WebSocket(wsURL(location, t.ticket, term.rows, term.cols));
      sock.binaryType = 'arraybuffer';
      ws = sock;
      sock.onopen = () => { phase = 'open'; term?.focus(); };
      sock.onmessage = (ev) => { if (ev.data instanceof ArrayBuffer) term?.write(new Uint8Array(ev.data)); };
      sock.onclose = (ev) => {
        if (ws !== sock) return;
        ws = null;
        // Close code 1006 with no reason: the upgrade itself was refused.
        note = ev.reason ? endText(ev.reason) : phase === 'connecting' ? 'Could not open the terminal.' : 'Session closed.';
        phase = 'ended';
        cleanup();
        load();
      };
      term.onData((d) => { if (sock.readyState === WebSocket.OPEN) sock.send(enc.encode(d)); });
      term.onBinary((d) => { if (sock.readyState === WebSocket.OPEN) sock.send(Uint8Array.from(d, (c) => c.charCodeAt(0))); });
      term.onResize(sendResize);
      ro = new ResizeObserver(() => { try { fit?.fit(); } catch { /* hidden */ } });
      ro.observe(host);
    } catch (e) {
      cleanup();
      phase = 'idle';
      toastError(e);
      load();
    }
  }
</script>

<section class="card space-y-3 p-4" aria-label="Terminal settings">
  <div class="flex items-center justify-between gap-3">
    <div>
      <h3 class="text-base font-semibold">Browser terminal</h3>
      <p class="text-sm text-muted">A login prompt on this node, in the browser. It asks for a Linux account name and password; no root shell is handed out. Administrators only. Keystrokes are never recorded; each session's start and end are in the audit log.</p>
    </div>
    {#if st}<Toggle checked={st.enabled} disabled={busy || !st.available} label="Allow the browser terminal" onchange={toggle} />{/if}
  </div>
  {#if err}
    <p class="text-sm text-danger" role="alert">{err}</p>
  {:else if !st}
    <p class="text-sm text-muted" role="status">Loading…</p>
  {:else if !st.available}
    <p class="text-sm text-muted">Not available: the helper on this system has no terminal support.</p>
  {:else}
    <p class="text-xs text-muted">Off by default. Closes after {st.idleMinutes} minutes without typing, at most {st.maxHours} hours, {st.maxSessions} sessions at a time ({st.sessions} open now). Turning it off ends open sessions.</p>
    <p class="text-xs text-muted">Updating ASLCP from this terminal ends the session; use the Updates page or run it with <code>systemd-run</code> or <code>tmux</code>.</p>
  {/if}
</section>

{#if st?.enabled && st.available}
  <section class="card space-y-3 p-4" aria-label="Terminal">
    <div class="flex flex-wrap items-center gap-3">
      <h3 class="mr-auto text-base font-semibold">Session</h3>
      {#if phase === 'open' || phase === 'connecting'}
        <button class="btn btn-sm" onclick={stop}>Disconnect</button>
      {:else}
        <button class="btn btn-sm btn-primary" onclick={start}>{phase === 'ended' ? 'Connect again' : 'Connect'}</button>
      {/if}
    </div>
    {#if note}<p class="text-sm text-muted" role="status">{note}</p>{/if}
    {#if phase === 'connecting'}<p class="text-sm text-muted" role="status">Connecting…</p>{/if}
    <div bind:this={host} class="h-[26rem] overflow-hidden rounded border border-line bg-black p-1 {phase === 'open' || phase === 'connecting' ? '' : 'hidden'}" aria-label="Terminal screen"></div>
  </section>
{/if}
