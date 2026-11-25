<script lang="ts">
  // Digital Modes, Engine section (#/settings/dv-engine): enable or disable the native engine (aslcp-dv with md380-emu),
  // its status and the enable job's log, the resume switch. The other Digital Modes sections wait for the engine to be enabled.
  import { onMount } from 'svelte';
  import { api } from '../../lib/api';
  import { toast, toastError } from '../../lib/toast.svelte';
  import type { DVJob, DVResume, DVStatus } from '../../lib/types';
  import { app } from '../../lib/store.svelte';
  import { dismissKey, isDismissed, jobSummary, jobView, setDismissed } from '../../lib/dvjob';
  import { lastUsed, resumeShort, resumeToast } from '../../lib/dvmodes';
  import Toggle from '../Toggle.svelte';
  import Dialog from '../Dialog.svelte';
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
  // Resume after restart (admin and operator); no tracker means no switch.
  let resume = $state<DVResume | null>(null), now = $state(Date.now()), resumeBusy = $state(false);
  async function loadResume() { try { resume = await api.dvResume(); now = Date.now(); } catch { /* no tracker: no switch */ } }
  async function setResume(on: boolean) {
    resumeBusy = true;
    try { resume = await api.dvSetResume(on); now = Date.now(); toast(resumeToast(on), 'info'); } catch (e) { toastError(e); await loadResume(); } finally { resumeBusy = false; }
  }
  onMount(() => { load(); loadResume(); });

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
  const unitState = (active: boolean, enabled: boolean | null) => `${active ? 'active' : 'inactive'}${enabled === null ? '' : enabled ? ', enabled' : ', disabled'}`;
</script>
<div class="space-y-4">
  {#if !st}
    <p class="text-sm text-muted">{err || 'Loading…'}</p>
  {:else}
  <section class="card space-y-3 p-4" aria-label="Engine">
    <div class="flex flex-wrap items-center gap-2">
      <h2 class="text-base font-semibold">Engine</h2>
      <span class="ml-auto flex flex-wrap gap-2">
        <button class="btn min-h-8" disabled={busy || running} onclick={load}><Refresh size={14} />Refresh</button>
        {#if st.enabled}
          <button class="btn min-h-8" disabled={busy || running} onclick={() => (disableOpen = true)}><Power size={14} />Disable digital modes</button>
        {:else}
          <button class="btn btn-primary min-h-8" disabled={busy || running || !st.supported} onclick={() => (enableOpen = true)}><Power size={14} />Enable digital modes</button>
        {/if}
      </span>
    </div>
    <dl class="flex flex-wrap gap-x-5 gap-y-1 text-sm">
      <div class="flex gap-1.5"><dt class="text-muted">Platform</dt><dd>{st.supported ? 'supported' : 'not supported'}</dd></div>
      <div class="flex gap-1.5"><dt class="text-muted">Enabled</dt><dd>{st.enabled ? 'yes' : 'no'}</dd></div>
      <div class="flex gap-1.5"><dt class="text-muted">Engine version</dt><dd class="font-mono">{snap?.engine?.version || 'n/a'}</dd></div>
      <div class="flex gap-1.5"><dt class="text-muted">Vocoder</dt><dd>{#if !snap?.vocoder}n/a{:else if snap.vocoder.ok}<span class="text-ok">ok</span>{:else}<span class="text-warn">not responding</span>{/if}{#if snap?.vocoder?.timeouts} <span class="text-xs text-muted">({snap.vocoder.timeouts} timeouts)</span>{/if}</dd></div>
    </dl>
    <ul class="flex flex-wrap gap-2" aria-label="Services">
      <li class="badge font-mono"><span class="font-semibold text-fg">aslcp-dv</span><span class={st.active ? 'text-ok' : ''}>{unitState(st.active, st.enabled)}</span></li>
      <li class="badge font-mono"><span class="font-semibold text-fg">md380-emu</span><span>{st.md380Installed ? unitState(st.md380Active, null) : 'not installed'}</span></li>
    </ul>
    {#if armhf}
      <p class="rounded border border-warn/60 p-2 text-sm text-warn" role="alert">Digital modes are not supported on a 32-bit (armhf) system: ASL3 and ASLCP need a 64-bit OS. Reinstall with Raspberry Pi OS Lite (64-bit).</p>
    {:else if !st.supported}
      <p class="text-sm text-muted" role="status">Digital modes are not supported on this platform{st.reason ? ` (${st.reason})` : ''}.</p>
    {/if}
    {#if st.legacyUnits?.length}
      <p class="rounded border border-line p-2 text-sm text-muted" role="status">The previous digital voice services were stopped. ASLCP removes the ones it installed after the upgrade; run Enable again to retry. <span class="font-mono text-xs">{st.legacyUnits.join(', ')}</span></p>
    {/if}

    {#if st.enabled && resume}
      <div class="space-y-1 border-t border-line pt-3" data-testid="dv-notice">
        <div class="flex flex-wrap items-center gap-x-4 gap-y-1">
          <span class="inline-flex items-center gap-2 text-sm"><Toggle tap checked={resume.resume} disabled={resumeBusy} label="Resume after restart" onchange={setResume} />Resume after restart</span>
          {#if lastUsed(resume, now)}<span class="text-xs text-muted" data-testid="dv-last-used">{lastUsed(resume, now)}</span>{/if}
        </div>
        <p class="text-xs text-muted" data-testid="dv-resume-hint">{resumeShort(resume.resume)}</p>
      </div>
    {/if}

    {#if running || job.state === 'failed' || (job.state === 'succeeded' && (!dismissed || logOpen))}
      <div class="space-y-2 border-t border-line pt-3" aria-label="Job">
        <div class="flex flex-wrap items-center gap-x-3 gap-y-1 text-sm">
          {#if running}
            <span class="flex items-center gap-2" role="status"><Refresh size={14} class="animate-spin" />Enabling…</span>
          {:else if job.state === 'failed'}
            <span class="text-danger" role="alert">Last job: failed. Nothing more was changed after the failing step; see the log.</span>
          {:else}
            <span class={job.state === 'succeeded' ? 'text-ok' : 'text-muted'} role="status">{summary ? summary.replace('Last enable:', 'Last job:') : 'Last job: finished.'}</span>
          {/if}
          {#if job.log && !running && !watched}
            <button class="btn min-h-8" aria-expanded={logOpen} onclick={() => (expanded = !expanded)}>{logOpen ? 'Hide log' : 'Show log'}</button>
          {/if}
          {#if job.state === 'succeeded' && !logOpen && !dismissed}
            <button class="btn min-h-8" onclick={() => { setDismissed(key); dismissedKey = key; }}>Dismiss</button>
          {/if}
        </div>
        {#if job.log && logOpen}
          <pre class="max-h-96 overflow-auto rounded border border-line bg-surface-2/40 p-2 text-xs whitespace-pre-wrap" aria-label="Enable log">{job.log}</pre>
        {/if}
      </div>
    {/if}
  </section>
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
