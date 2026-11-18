<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../lib/api';
  import { toast, toastError } from '../lib/toast.svelte';
  import type { DVSwitchStatus } from '../lib/types';
  import { dismissKey, isDismissed, jobSummary, pickJobKind, setDismissed } from '../lib/dvjob';
  import Dialog from './Dialog.svelte';
  import DvswitchSetup from './DvswitchSetup.svelte';
  import Refresh from '@lucide/svelte/icons/refresh-cw';
  import Download from '@lucide/svelte/icons/download';
  import Trash from '@lucide/svelte/icons/trash-2';

  let st = $state<DVSwitchStatus | null>(null), err = $state(''), busy = $state(false);
  let installOpen = $state(false), uninstallOpen = $state(false);
  const running = $derived(st?.install.job === 'running' || st?.uninstall.job === 'running');
  // Show the running job, else the newer of the two (an old uninstall log
  // must not hide a later install).
  const kind = $derived(st ? pickJobKind(st) : 'install');
  const showUninstall = $derived(kind === 'uninstall');
  const log = $derived(st ? st[kind] : null);
  const logTitle = $derived(showUninstall ? 'Uninstall log' : 'Install log');
  // A job seen running (or just finished) on this page keeps its log open; an old one is a summary.
  let watched = $state(false), expanded = $state(false), dismissedKey = $state('');
  const fmtAt = (unix: number) => new Date(unix * 1000).toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' });
  const summary = $derived(log ? jobSummary(kind, log, fmtAt) : '');
  const key = $derived(log ? dismissKey(kind, log) : '');
  const dismissed = $derived(!!key && (dismissedKey === key || isDismissed(key)));
  const logOpen = $derived(running || watched || expanded);
  $effect(() => { void key; expanded = false; });
  $effect(() => { if (running) watched = true; });

  async function load() { try { st = await api.dvswitch(); err = ''; } catch (e) { err = e instanceof Error ? e.message : String(e); } }
  onMount(load);

  // Poll the job while one runs (also after a reload mid-job), then refresh the status once.
  $effect(() => {
    if (!running) return;
    let alive = true, was = st?.install.job === 'running' ? 'install' : 'uninstall';
    const t = setInterval(async () => {
      try {
        const j = await api.dvswitchJob();
        if (!alive || !st) return;
        st = { ...st, install: j.install, uninstall: j.uninstall };
        if (j.install.job !== 'running' && j.uninstall.job !== 'running') {
          const ok = (was === 'install' ? j.install : j.uninstall).job === 'succeeded';
          toast(ok ? `DVSwitch ${was} finished` : `DVSwitch ${was} failed. See the log.`, ok ? 'info' : 'error');
          await load();
        }
      } catch { /* try again */ }
    }, 2000);
    return () => { alive = false; clearInterval(t); };
  });

  async function start(kind: 'install' | 'uninstall') {
    busy = true;
    try { st = await (kind === 'install' ? api.dvswitchInstall() : api.dvswitchUninstall()); err = ''; } catch (e) { toastError(e); await load(); } finally { busy = false; }
  }
  const yes = (b: boolean) => (b ? 'yes' : 'no');
</script>
<div class="space-y-4">
  <h1 class="text-xl font-bold tracking-tight">DVSwitch</h1>
  {#if !st}
    <p class="text-sm text-muted">{err || 'Loading…'}</p>
  {:else}
  <div class="grid grid-cols-1 items-start gap-4 lg:grid-cols-2">
    <section class="card space-y-3 p-4" aria-label="Status">
      <h2 class="text-base font-semibold">Status</h2>
      <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
        <dt class="text-muted">Installed</dt>
        <dd>{#if st.installedBy === 'aslcp'}yes, by ASLCP{:else if st.installedBy === 'external'}yes, <span class="text-warn">installed outside ASLCP</span>{:else}no{/if}</dd>
        <dt class="text-muted">Version</dt><dd class="font-mono">{st.version || 'n/a'}</dd>
        <dt class="text-muted">Platform</dt><dd>{st.osCodename || 'unknown'} / {st.arch || 'unknown'}, {st.supported ? 'supported' : 'not supported'}</dd>
        <dt class="text-muted">Repository</dt><dd>{yes(st.repoPresent)}</dd>
        <dt class="text-muted">Repository key</dt>
        <dd>{#if !st.keyPresent}absent{:else if st.keyOk}fingerprint verified{:else}<span class="text-danger">fingerprint does not match</span>{/if}{#if st.keyFingerprint}<span class="block break-all font-mono text-xs text-muted">{st.keyFingerprint}</span>{/if}</dd>
        <dt class="text-muted">Host files</dt><dd>{#if st.installed}{#if st.hostFilesOk}all present{:else}<span class="text-warn">missing or empty: {st.hostFiles.filter((h) => !h.present || !h.size).map((h) => h.name).join(', ')}</span>{/if}{:else}n/a{/if}</dd>
      </dl>
      {#if !st.supported && st.unsupportedReason}<p class="text-sm text-muted" role="status">{st.unsupportedReason}.</p>{/if}
      {#if st.installedBy === 'external'}
        <p class="text-sm text-muted" role="status">DVSwitch was installed outside ASLCP, so ASLCP does not offer to remove it. Remove it by hand if you want it gone.</p>
      {/if}
      <h3 class="text-sm font-semibold">Services</h3>
      <ul class="divide-y divide-line rounded border border-line text-xs" aria-label="Services">
        {#each st.units as u}
          <li class="flex flex-wrap items-baseline gap-x-2 px-2 py-1"><span class="font-mono font-semibold">{u.name}</span><span class="text-muted">{u.loaded ? `${u.active}, ${u.enabled || 'n/a'}` : 'not installed'}</span></li>
        {/each}
      </ul>
      <div class="flex flex-wrap gap-2">
        <button class="btn" disabled={busy || running} onclick={load}><Refresh size={14} />Refresh</button>
        <button class="btn btn-primary" disabled={busy || running || !st.canInstall} onclick={() => (installOpen = true)}><Download size={14} />Install DVSwitch</button>
        <button class="btn btn-danger" disabled={busy || running || !st.canUninstall} onclick={() => (uninstallOpen = true)}><Trash size={14} />Uninstall DVSwitch</button>
      </div>
    </section>

    <section class="card space-y-3 p-4" aria-label="Job">
      <h2 class="text-base font-semibold">Install and uninstall</h2>
      {#if running}
        <p class="flex items-center gap-2 text-sm" role="status"><Refresh size={14} class="animate-spin" />{st.uninstall.job === 'running' ? 'Uninstalling' : 'Installing'}…</p>
      {:else if log && log.job === 'succeeded'}
        <p class="text-sm text-ok" role="status">Finished.</p>
      {:else if log && log.job === 'failed'}
        <p class="text-sm text-danger" role="alert">Failed. Nothing more was changed after the failing step; see the log.</p>
      {:else}
        <p class="text-sm text-muted">No job has run.</p>
      {/if}
      {#if log && log.log}
        {#if logOpen}
          {#if !running && !watched}<button class="btn" onclick={() => (expanded = false)}>Hide log</button>{/if}
          <pre class="max-h-96 overflow-auto rounded border border-line bg-surface-2/40 p-2 text-xs whitespace-pre-wrap" aria-label={logTitle}>{log.log}</pre>
        {:else if summary && !dismissed}
          <div class="flex flex-wrap items-center gap-2 text-sm">
            <span class={log.job === 'failed' ? 'font-semibold text-danger' : 'text-muted'} role={log.job === 'failed' ? 'alert' : 'status'}>{summary}</span>
            <button class="btn" onclick={() => (expanded = true)}>Show log</button>
            <button class="btn" onclick={() => { setDismissed(key); dismissedKey = key; }}>Dismiss</button>
          </div>
        {/if}
      {/if}
    </section>
  </div>
  {#if st.installed && !running}
    <DvswitchSetup units={st.units} onunits={(u) => { if (st) st = { ...st, units: u }; }} />
  {/if}
  {/if}
</div>

<Dialog bind:open={installOpen} title="Install DVSwitch?">
  <div class="space-y-3 text-sm">
    <p>This adds the DVSwitch apt repository (its key is checked against a pinned fingerprint) and installs these packages:</p>
    <ul class="list-disc pl-5 font-mono text-xs">{#each st?.packages ?? [] as p}<li>{p.name}</li>{/each}</ul>
    <p>Their dependencies come along, <span class="font-mono">qemu-user-static</span> among them on most hardware. ASLCP records what is newly installed so an uninstall can take exactly that back out.</p>
    <p class="text-xs text-muted">It does not touch Asterisk or app_rpt. It needs internet access and a few minutes.</p>
  </div>
  {#snippet footer()}
    <button class="btn" onclick={() => (installOpen = false)}>Cancel</button>
    <button class="btn btn-primary" onclick={() => { installOpen = false; start('install'); }}>Install</button>
  {/snippet}
</Dialog>

<Dialog bind:open={uninstallOpen} title="Uninstall DVSwitch?">
  <div class="space-y-3 text-sm">
    <p class="rounded border border-warn/60 p-2 text-warn" role="alert">This removes DVSwitch completely, including its configuration under the paths below.</p>
    <div><h3 class="font-semibold">Stops and disables</h3><p class="font-mono text-xs">{st?.uninstallUnits.join(', ')}</p></div>
    <div><h3 class="font-semibold">Purges packages</h3><p class="font-mono text-xs">{st?.uninstallPackages.join(', ')}</p>
      <p class="text-xs text-muted">{#if st?.marker?.wgetPreinstalled}wget stays (it was installed before DVSwitch). {/if}A package that something else still needs is kept; the log says which.</p></div>
    <div><h3 class="font-semibold">Deletes</h3><ul class="font-mono text-xs">{#each st?.uninstallPaths ?? [] as p}<li>{p}</li>{/each}</ul></div>
    <p class="text-xs text-muted">If the purge would remove anything else, it is aborted before anything is changed. Asterisk and allstarlink packages are never touched.</p>
  </div>
  {#snippet footer()}
    <button class="btn" onclick={() => (uninstallOpen = false)}>Cancel</button>
    <button class="btn btn-danger" onclick={() => { uninstallOpen = false; start('uninstall'); }}>Uninstall</button>
  {/snippet}
</Dialog>
