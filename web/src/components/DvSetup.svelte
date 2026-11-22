<script lang="ts">
  // Digital modes setup: the resume switch, one card per mode and the private bridge node.
  // The modes the build does not carry yet show "Not yet available in this build" with disabled controls.
  import { onMount } from 'svelte';
  import { api } from '../lib/api';
  import { toast, toastError } from '../lib/toast.svelte';
  import type { DVResume, DVSetup, DVTuneMode } from '../lib/types';
  import { app } from '../lib/store.svelte';
  import { cardState, lastUsed, MODE_NAME, RESUME_NOTICE, resumeHint, resumeToast, UNAVAILABLE_TEXT } from '../lib/dvmodes';
  import { PANEL_MODES } from '../lib/dvnetworks';
  import { dvErrorMessage } from '../lib/dvmsg';
  import Toggle from './Toggle.svelte';
  import DvBridge from './DvBridge.svelte';

  let v = $state<DVSetup | null>(null), err = $state(''), busy = $state(false);
  let resume = $state<DVResume | null>(null), now = $state(Date.now());
  const base = () => Object.fromEntries((v?.files ?? []).filter((f) => f.sha256).map((f) => [f.id, f.sha256]));
  const modes = $derived(app.dv?.modes);

  async function load() {
    try { v = await api.dvSetup(); err = ''; } catch (e) { err = e instanceof Error ? e.message : String(e); }
  }
  async function loadResume() { try { resume = await api.dvResume(); now = Date.now(); } catch { /* no tracker: no hints */ } }
  onMount(() => { load(); loadResume(); });

  async function setResume(on: boolean) {
    busy = true;
    try { resume = await api.dvSetResume(on); now = Date.now(); toast(resumeToast(on), 'info'); } catch (e) { toastError(e); await loadResume(); } finally { busy = false; }
  }
  // The parrot is the only mode in this build: tuning it switches the engine to LOOP.
  async function tune(mode: DVTuneMode) {
    busy = true;
    try { const r = await api.dvMode(mode); toast(r.confirmed ? `${MODE_NAME[mode]} is on` : `${MODE_NAME[mode]} requested`, 'info'); } catch (e) { toast(dvErrorMessage(e), 'error'); } finally { busy = false; }
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

    {#each PANEL_MODES as id (id)}
      {@const c = cardState(modes, id)}
      <section class="card space-y-2 p-4" aria-label={MODE_NAME[id]}>
        <div class="flex flex-wrap items-center gap-2">
          <h2 class="text-base font-semibold">{MODE_NAME[id]}</h2>
          <span class="badge {c.enabled ? 'border-ok text-ok' : ''}" role="status" data-testid="dv-{id.toLowerCase()}-state">{c.available ? (c.enabled ? 'Enabled' : 'Disabled') : 'Unavailable'}</span>
          <span class="ml-auto"></span>
          {#if id === 'LOOP'}
            <button class="btn" disabled={busy || !c.available} onclick={() => tune(id)}>Tune parrot</button>
          {:else}
            <button class="btn" disabled>Tune</button>
          {/if}
        </div>
        {#if !c.available}<p class="text-xs text-muted" data-testid="dv-{id.toLowerCase()}-unavailable">{UNAVAILABLE_TEXT}</p>{/if}
      </section>
    {/each}

    <div class="lg:col-span-2"><DvBridge b={v.bridgeNode} base={base()} onsaved={load} /></div>
  </div>
{/if}
