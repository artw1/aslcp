<script lang="ts">
  // Digital modes setup: the resume switch, one card per mode and the private bridge node.
  // The modes the engine does not carry show "Not yet available in this build" with disabled controls.
  import { onMount } from 'svelte';
  import { api } from '../lib/api';
  import { toast, toastError } from '../lib/toast.svelte';
  import type { DVIdentity, DVNetView, DVResume, DVSetup, DVTuneMode } from '../lib/types';
  import { app } from '../lib/store.svelte';
  import { cardState, lastUsed, MODE_NAME, RESUME_NOTICE, resumeHint, resumeToast, UNAVAILABLE_TEXT } from '../lib/dvmodes';
  import { NETWORK_MODES } from '../lib/dvnetworks';
  import { ECHO_HINT, ECHO_RUNNING, ECHO_START, ECHO_STOP, echoRunning, forgetBefore, modeBefore, rememberBefore, stopTarget } from '../lib/dvecho';
  import { dvErrorMessage } from '../lib/dvmsg';
  import Toggle from './Toggle.svelte';
  import DvBridge from './DvBridge.svelte';
  import DvNet from './DvNet.svelte';
  import DvIdentity from './DvIdentity.svelte';

  let v = $state<DVSetup | null>(null), err = $state(''), busy = $state(false);
  let resume = $state<DVResume | null>(null), now = $state(Date.now());
  const base = () => Object.fromEntries((v?.files ?? []).filter((f) => f.sha256).map((f) => [f.id, f.sha256]));
  const modes = $derived(app.dv?.modes);

  async function load() {
    try { v = await api.dvSetup(); err = ''; } catch (e) { err = e instanceof Error ? e.message : String(e); }
  }
  // Network settings and identity (admin only: an operator gets 403 and the cards show the state only).
  let nets = $state<DVNetView[]>([]), identity = $state<DVIdentity | null>(null);
  async function loadNets() {
    try { const r = await api.dvNetworks(); nets = r.modes; identity = r.identity; } catch { nets = []; identity = null; }
  }
  const netOf = (id: string) => nets.find((n) => n.mode === id);
  function netSaved(n: DVNetView) { nets = nets.map((x) => (x.mode === n.mode ? n : x)); loadNets(); }
  async function loadResume() { try { resume = await api.dvResume(); now = Date.now(); } catch { /* no tracker: no hints */ } }
  onMount(() => { load(); loadResume(); loadNets(); });

  async function setResume(on: boolean) {
    busy = true;
    try { resume = await api.dvSetResume(on); now = Date.now(); toast(resumeToast(on), 'info'); } catch (e) { toastError(e); await loadResume(); } finally { busy = false; }
  }
  // Test audio: the echo test is the engine's LOOP mode, switched with POST /api/dv/mode like any mode.
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
    await loadResume();
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
  <div class="grid grid-cols-1 items-start gap-4 lg:grid-cols-2">
    <section class="card space-y-2 p-4 lg:col-span-2" aria-label="Resume" data-testid="dv-notice">
      <p class="text-sm">{RESUME_NOTICE}</p>
      {#if resume}
        <div class="flex flex-wrap items-center gap-x-4 gap-y-1">
          <span class="inline-flex items-center gap-2 text-sm"><Toggle checked={resume.resume} disabled={busy} label="Resume after restart" onchange={setResume} />Resume after restart</span>
          {#if lastUsed(resume, now)}<span class="text-xs text-muted" data-testid="dv-last-used">{lastUsed(resume, now)}</span>{/if}
        </div>
        <p class="text-xs text-muted" data-testid="dv-resume-hint">{resumeHint(resume.resume)}</p>
      {/if}
    </section>

    <section class="card space-y-2 p-4 lg:col-span-2" aria-label="Test audio" data-testid="dv-echo">
      <div class="flex flex-wrap items-center gap-2">
        <h2 class="text-base font-semibold">Test audio</h2>
        {#if echoOn}<span class="badge border-ok text-ok" role="status" data-testid="dv-echo-state">{ECHO_RUNNING}</span>{/if}
        <span class="ml-auto"></span>
        {#if echoOn}
          <button class="btn" disabled={busy || !back} title={back ? `Back to ${MODE_NAME[back as DVTuneMode]}` : 'No other mode is enabled'} data-testid="dv-echo-stop" onclick={stopEcho}>{ECHO_STOP}</button>
        {:else}
          <button class="btn" disabled={busy || !echoReady} data-testid="dv-echo-start" onclick={startEcho}>{ECHO_START}</button>
        {/if}
      </div>
      <p class="text-xs text-muted" data-testid="dv-echo-hint">{ECHO_HINT}{#if !echoReady && !echoOn} The digital modes engine is not running.{/if}</p>
    </section>

    {#if identity}<DvIdentity {identity} onsaved={(i) => { identity = i; loadNets(); }} />{/if}

    {#each NETWORK_MODES as id (id)}
      {@const c = cardState(modes, id)}
      <section class="card space-y-2 p-4" aria-label={MODE_NAME[id]}>
        <div class="flex flex-wrap items-center gap-2">
          <h2 class="text-base font-semibold">{MODE_NAME[id]}</h2>
          <span class="badge {c.enabled ? 'border-ok text-ok' : ''}" role="status" data-testid="dv-{id.toLowerCase()}-state">{c.available ? (c.enabled ? 'Enabled' : 'Disabled') : 'Unavailable'}</span>
          <span class="ml-auto"></span>
          {#if !c.available}
            <button class="btn" disabled>Tune</button>
          {/if}
        </div>
        {#if !c.available}<p class="text-xs text-muted" data-testid="dv-{id.toLowerCase()}-unavailable">{UNAVAILABLE_TEXT}</p>
        {:else if netOf(id)}<DvNet v={netOf(id)!} {identity} onsaved={netSaved} />{/if}
      </section>
    {/each}

    <div class="lg:col-span-2"><DvBridge b={v.bridgeNode} base={base()} onsaved={load} /></div>
  </div>
{/if}
