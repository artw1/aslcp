<script lang="ts">
  // Digital modes (route /dv): enable or disable the native engine (aslcp-dv with md380-emu), its status and
  // the enable job's log. Setup (private node, resume, mode cards) appears once the engine is enabled.
  import { onMount } from 'svelte';
  import { api } from '../lib/api';
  import { toast, toastError } from '../lib/toast.svelte';
  import type { DVJob, DVStatus } from '../lib/types';
  import { app } from '../lib/store.svelte';
  import { dismissKey, isDismissed, jobSummary, jobView, setDismissed } from '../lib/dvjob';
  import Dialog from './Dialog.svelte';
  import DvSetup from './DvSetup.svelte';
  import UserListCard from './UserListCard.svelte';
  import Refresh from '@lucide/svelte/icons/refresh-cw';
  import Power from '@lucide/svelte/icons/power';

  let st = $state<DVStatus | null>(null), jobData = $state<DVJob | null>(null), err = $state(''), busy = $state(false);
  let enableOpen = $state(false), disableOpen = $state(false);
  const job = $derived(jobView(jobData ?? (st ? { job: st.job } : null)));
  const running = $derived(st?.job === 'running' || job.state === 'running');
  const armhf = $derived(!!st && !st.supported && st.reason === 'armhf_unsupported');
  const snap = $derived(app.dv);
  // A job seen running (or just finished) on this page keeps its log open; an old one is a summary.
  let watched = $state(false), expanded = $state(false), dismissedKey = $state('');
  const fmtAt = (unix: number) => new Date(unix * 1000).toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' });
  const summary = $derived(jobSummary(job, fmtAt));
  const key = $derived(dismissKey(job));
  const dismissed = $derived(dismissedKey === key || isDismissed(key));
  const logOpen = $derived(running || watched || expanded);
  $effect(() => { void key; expanded = false; });
  $effect(() => { if (running) watched = true; });

  async function loadJob() { try { jobData = await api.dvJob(); } catch { /* no job log yet */ } }
  async function load() {
    try { st = await api.dvStatus(); err = ''; await loadJob(); } catch (e) { err = e instanceof Error ? e.message : String(e); }
  }
  onMount(load);

  // Poll while the enable job runs (also after a reload mid-job), then refresh the status once.
  $effect(() => {
    if (!running) return;
    let alive = true;
    const t = setInterval(async () => {
      try {
        const [j, s] = await Promise.all([api.dvJob(), api.dvStatus()]);
        if (!alive) return;
        jobData = j; st = s;
        if (s.job !== 'running' && jobView(j).state !== 'running') {
          const ok = jobView(j).state !== 'failed';
          toast(ok ? 'Digital modes enabled' : 'Enabling digital modes failed. See the log.', ok ? 'info' : 'error');
        }
      } catch { /* try again */ }
    }, 2000);
    return () => { alive = false; clearInterval(t); };
  });

  async function enable() {
    busy = true; watched = true;
    try { st = await api.dvEnable(); err = ''; await loadJob(); } catch (e) { toastError(e); await load(); } finally { busy = false; }
  }
  async function disable() {
    busy = true;
    try { st = await api.dvDisable(); err = ''; toast('Digital modes disabled', 'info'); } catch (e) { toastError(e); await load(); } finally { busy = false; }
  }
  const yes = (b: boolean) => (b ? 'yes' : 'no');
  const unitState = (active: boolean, enabled: boolean | null) => `${active ? 'active' : 'inactive'}${enabled === null ? '' : enabled ? ', enabled' : ', disabled'}`;
</script>
<div class="space-y-4">
  <h1 class="text-xl font-bold tracking-tight">Digital modes</h1>
  {#if !st}
    <p class="text-sm text-muted">{err || 'Loading…'}</p>
  {:else}
  <div class="grid grid-cols-1 items-start gap-4 lg:grid-cols-2">
    <section class="card space-y-3 p-4" aria-label="Status">
      <h2 class="text-base font-semibold">Status</h2>
      <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
        <dt class="text-muted">Platform</dt><dd>{st.supported ? 'supported' : 'not supported'}</dd>
        <dt class="text-muted">Enabled</dt><dd>{yes(st.enabled)}</dd>
        <dt class="text-muted">Engine version</dt><dd class="font-mono">{snap?.engine?.version || 'n/a'}</dd>
        <dt class="text-muted">Vocoder</dt><dd>{#if !snap?.vocoder}n/a{:else if snap.vocoder.ok}<span class="text-ok">ok</span>{:else}<span class="text-warn">not responding</span>{/if}{#if snap?.vocoder?.timeouts} <span class="text-xs text-muted">({snap.vocoder.timeouts} timeouts)</span>{/if}</dd>
      </dl>
      {#if armhf}
        <p class="rounded border border-warn/60 p-2 text-sm text-warn" role="alert">Digital modes are not supported on a 32-bit (armhf) system: ASL3 and ASLCP need a 64-bit OS. Reinstall with Raspberry Pi OS Lite (64-bit).</p>
      {:else if !st.supported}
        <p class="text-sm text-muted" role="status">Digital modes are not supported on this platform{st.reason ? ` (${st.reason})` : ''}.</p>
      {/if}
      {#if st.legacyUnits?.length}
        <p class="rounded border border-line p-2 text-sm text-muted" role="status">The previous digital voice services were stopped. ASLCP removes the ones it installed after the upgrade; run Enable again to retry. <span class="font-mono text-xs">{st.legacyUnits.join(', ')}</span></p>
      {/if}
      <h3 class="text-sm font-semibold">Services</h3>
      <ul class="divide-y divide-line rounded border border-line text-xs" aria-label="Services">
        <li class="flex flex-wrap items-baseline gap-x-2 px-2 py-1"><span class="font-mono font-semibold">aslcp-dv</span><span class={st.active ? 'text-ok' : 'text-muted'}>{unitState(st.active, st.enabled)}</span></li>
        <li class="flex flex-wrap items-baseline gap-x-2 px-2 py-1"><span class="font-mono font-semibold">md380-emu</span><span class="text-muted">{st.md380Installed ? unitState(st.md380Active, null) : 'not installed'}</span></li>
      </ul>
      <div class="flex flex-wrap gap-2">
        <button class="btn" disabled={busy || running} onclick={load}><Refresh size={14} />Refresh</button>
        {#if st.enabled}
          <button class="btn" disabled={busy || running} onclick={() => (disableOpen = true)}><Power size={14} />Disable digital modes</button>
        {:else}
          <button class="btn btn-primary" disabled={busy || running || !st.supported} onclick={() => (enableOpen = true)}><Power size={14} />Enable digital modes</button>
        {/if}
      </div>
    </section>

    <section class="card space-y-3 p-4" aria-label="Job">
      <h2 class="text-base font-semibold">Enable log</h2>
      {#if running}
        <p class="flex items-center gap-2 text-sm" role="status"><Refresh size={14} class="animate-spin" />Enabling…</p>
      {:else if job.state === 'succeeded'}
        <p class="text-sm text-ok" role="status">Finished.</p>
      {:else if job.state === 'failed'}
        <p class="text-sm text-danger" role="alert">Failed. Nothing more was changed after the failing step; see the log.</p>
      {:else}
        <p class="text-sm text-muted">No job has run.</p>
      {/if}
      {#if job.log}
        {#if logOpen}
          {#if !running && !watched}<button class="btn" onclick={() => (expanded = false)}>Hide log</button>{/if}
          <pre class="max-h-96 overflow-auto rounded border border-line bg-surface-2/40 p-2 text-xs whitespace-pre-wrap" aria-label="Enable log">{job.log}</pre>
        {:else if summary && !dismissed}
          <div class="flex flex-wrap items-center gap-2 text-sm">
            <span class={job.state === 'failed' ? 'font-semibold text-danger' : 'text-muted'} role={job.state === 'failed' ? 'alert' : 'status'}>{summary}</span>
            <button class="btn" onclick={() => (expanded = true)}>Show log</button>
            <button class="btn" onclick={() => { setDismissed(key); dismissedKey = key; }}>Dismiss</button>
          </div>
        {/if}
      {/if}
    </section>
  </div>
  {#if st.enabled && !running}
    <DvSetup />
  {/if}
  <UserListCard />
  {/if}
</div>

<Dialog bind:open={enableOpen} title="Enable digital modes?">
  <div class="space-y-3 text-sm">
    <p>This adds the package repository (its key is checked against a pinned fingerprint), installs the md380 voice engine and starts the digital modes engine, <span class="font-mono">aslcp-dv</span>.</p>
    <p>Any previous digital voice services found on this node are stopped and disabled (the ones ASLCP installed are removed; md380-emu is kept).</p>
    <p class="text-xs text-muted">It does not touch Asterisk or app_rpt. It needs internet access and a few minutes.</p>
  </div>
  {#snippet footer()}
    <button class="btn" onclick={() => (enableOpen = false)}>Cancel</button>
    <button class="btn btn-primary" onclick={() => { enableOpen = false; enable(); }}>Enable</button>
  {/snippet}
</Dialog>

<Dialog bind:open={disableOpen} title="Disable digital modes?">
  <div class="space-y-3 text-sm">
    <p>This stops and disables <span class="font-mono">aslcp-dv</span>. The md380 voice engine and your settings stay, so enabling again is quick.</p>
  </div>
  {#snippet footer()}
    <button class="btn" onclick={() => (disableOpen = false)}>Cancel</button>
    <button class="btn btn-primary" onclick={() => { disableOpen = false; disable(); }}>Disable</button>
  {/snippet}
</Dialog>
