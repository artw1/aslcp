<script lang="ts">
  // Digital modes setup, in the order of the setup flow: identity, bridge node and audio. Networks are managed on the DV page.
  import { onMount } from 'svelte';
  import { api } from '../lib/api';
  import { toast } from '../lib/toast.svelte';
  import type { DVAudio, DVIdentity, DVResume, DVSetup, DVTuneMode } from '../lib/types';
  import { app } from '../lib/store.svelte';
  import { cardState, MODE_NAME } from '../lib/dvmodes';
  import { ECHO_HINT, ECHO_RUNNING, ECHO_START, ECHO_STOP, echoRunning, forgetBefore, modeBefore, rememberBefore, stopTarget } from '../lib/dvecho';
  import { dvErrorMessage } from '../lib/dvmsg';
  import DvBridge from './DvBridge.svelte';
  import DvIdentity from './DvIdentity.svelte';
  import DvAudio from './DvAudio.svelte';

  // `resume` (the Engine card's switch) says which mode the echo test returns to; `reloadResume` refreshes it.
  let { resume, reloadResume }: { resume: DVResume | null; reloadResume: () => Promise<void> } = $props();
  let v = $state<DVSetup | null>(null), err = $state(''), busy = $state(false);
  const base = () => Object.fromEntries((v?.files ?? []).filter((f) => f.sha256).map((f) => [f.id, f.sha256]));
  const modes = $derived(app.dv?.modes);

  async function load() {
    try { v = await api.dvSetup(); err = ''; } catch (e) { err = e instanceof Error ? e.message : String(e); }
  }
  // Identity and audio settings (admin only: an operator gets 403).
  let identity = $state<DVIdentity | null>(null), audio = $state<DVAudio | null>(null);
  async function loadNets() {
    try { const r = await api.dvNetworks(); identity = r.identity; audio = r.audio ?? null; } catch { identity = null; audio = null; }
  }
  onMount(() => { load(); loadNets(); });
  // Audio card: the echo test is the engine's LOOP mode, switched with POST /api/dv/mode like any mode.
  // Stop switches back to the mode in effect before Start (the server re-tunes that mode's remembered tune).
  const echoOn = $derived(echoRunning(app.dv));
  const echoReady = $derived(!!app.dv?.running && cardState(modes, 'LOOP').available);
  let prevMode = $state(modeBefore());
  const back = $derived(stopTarget(prevMode, resume, modes));
  async function switchTo(mode: DVTuneMode, ok: string) {
    busy = true;
    try { const r = await api.dvMode(mode); app.dv = r.snapshot; toast(r.confirmed ? ok : `${MODE_NAME[mode]} requested`, 'info'); return true; } catch (e) { toast(dvErrorMessage(e), 'error'); return false; } finally { busy = false; }
  }
  async function startEcho() {
    const prev = app.dv?.bridge.source !== 'none' ? app.dv?.bridge.mode : '';
    await reloadResume();
    if (await switchTo('LOOP', 'Echo test running')) { rememberBefore(prev); prevMode = modeBefore(); }
  }
  async function stopEcho() {
    if (!back) return;
    if (await switchTo(back as DVTuneMode, `${MODE_NAME[back as DVTuneMode]} is on`)) { forgetBefore(); prevMode = ''; }
  }
</script>
{#if !v}
  <p class="text-sm text-muted">{err || 'Loading…'}</p>
{:else}
  <div class="space-y-4">
    {#if identity}
      <DvIdentity {identity} onsaved={(i) => { identity = i; loadNets(); }} />
    {/if}

    <DvBridge b={v.bridgeNode} base={base()} onsaved={load} />

    <section class="card space-y-2 p-4" aria-label="Audio" data-testid="dv-echo">
      <div class="flex flex-wrap items-center gap-2">
        <h2 class="text-base font-semibold">Audio</h2>
        {#if echoOn}<span class="badge border-ok text-ok" role="status" data-testid="dv-echo-state">{ECHO_RUNNING}</span>{/if}
        <span class="ml-auto"></span>
        {#if echoOn}
          <button class="btn min-h-8" disabled={busy || !back} title={back ? `Back to ${MODE_NAME[back as DVTuneMode]}` : 'No other mode is enabled'} data-testid="dv-echo-stop" onclick={stopEcho}>{ECHO_STOP}</button>
        {:else}
          <button class="btn min-h-8" disabled={busy || !echoReady} data-testid="dv-echo-start" onclick={startEcho}>{ECHO_START}</button>
        {/if}
      </div>
      <p class="text-xs text-muted" data-testid="dv-echo-hint">{ECHO_HINT}{#if !echoReady && !echoOn} The digital modes engine is not running.{/if}</p>
      {#if audio}<DvAudio {audio} onsaved={(a) => { audio = a; loadNets(); }} />{/if}
    </section>
  </div>
{/if}
