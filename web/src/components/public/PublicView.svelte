<script lang="ts">
  import { untrack } from 'svelte';
  import { app, clock } from '../../lib/store.svelte';
  import { navigate } from '../../lib/router.svelte';
  import { theme, cycleTheme } from '../../lib/theme.svelte';
  import { pub, startPublic, stopPublic } from '../../lib/publicLive.svelte';
  import { REPO_URL } from '../../lib/util';
  import NodeTile from '../NodeTile.svelte';
  import LastHeardTable from '../LastHeardTable.svelte';
  import PublicNetwork from './PublicNetwork.svelte';
  import PublicNowCalling from './PublicNowCalling.svelte';
  import { pubTile } from '../../lib/tiles';
  import { pubNow } from '../../lib/pubnow';
  import { pubHeardItems } from '../../lib/lastheard';
  import Radio from '@lucide/svelte/icons/radio-tower';
  import Sun from '@lucide/svelte/icons/sun';
  import Moon from '@lucide/svelte/icons/moon';
  import Monitor from '@lucide/svelte/icons/monitor';
  import Wifi from '@lucide/svelte/icons/wifi';
  import WifiOff from '@lucide/svelte/icons/wifi-off';
  import LogIn from '@lucide/svelte/icons/log-in';

  // visitor: logged out, with a Log in button; preview: an admin looking at it; embed: chrome-less, for iframes.
  let { mode }: { mode: 'visitor' | 'preview' | 'embed' } = $props();
  $effect(() => {
    untrack(() => startPublic(() => { app.publicEnabled = false; if (mode === 'preview') navigate('/'); }));
    return stopPublic;
  });
  const snap = $derived(pub.snap);
  $effect(() => { if (snap?.title) document.title = snap.title; });
  const live = $derived(pub.conn === 'live' || pub.conn === 'polling');
  const connLabel = $derived({ live: 'Live', polling: 'Live (polling)', connecting: 'Connecting…', offline: 'Reconnecting…' }[pub.conn]);
  const nextTheme = $derived(theme.value === 'dark' ? 'light' : theme.value === 'light' ? 'system' : 'dark');
  const themeLabel = $derived(`Theme: ${theme.value}. Switch to ${nextTheme}.`);
  const updatedAt = $derived(pub.updated ? new Date(pub.updated).toLocaleTimeString() : '');
  const banner = $derived(pubNow(snap, pub.skew, pub.nodeAt));
  const heard = $derived(pubHeardItems(snap));
  // `clock` ticks once a second so the age of the data is current.
  const staleData = $derived(pub.updated > 0 && clock.now - pub.updated > 45000);
</script>

<div class="flex min-h-dvh flex-col">
  <header class="border-b border-line bg-surface">
    <div class="mx-auto flex max-w-[1100px] items-center gap-2 px-3 py-1.5 sm:gap-3 sm:px-4">
      <Radio size={18} class="shrink-0 text-accent" aria-hidden="true" />
      <h1 class="min-w-0 flex-1 truncate text-base font-bold tracking-tight">{snap?.title ?? 'AllStarLink node status'}</h1>
      <span class="badge shrink-0 {live && !staleData ? 'text-ok' : 'text-warn'}" role="status">
        {#if live && !staleData}<Wifi size={13} aria-hidden="true" />{:else}<WifiOff size={13} aria-hidden="true" />{/if}{staleData && live ? 'No recent data' : connLabel}
      </span>
      {#if mode !== 'embed'}
        <button class="btn btn-sm btn-icon shrink-0" onclick={cycleTheme} aria-label={themeLabel} title={themeLabel}>
          {#if theme.value === 'dark'}<Moon size={16} />{:else if theme.value === 'light'}<Sun size={16} />{:else}<Monitor size={16} />{/if}
        </button>
      {/if}
      {#if mode === 'visitor'}
        <button class="btn btn-sm btn-primary shrink-0" onclick={() => navigate('/login')}><LogIn size={14} aria-hidden="true" />Log in</button>
      {:else if mode === 'preview'}
        <button class="btn btn-sm shrink-0" onclick={() => navigate('/')}>Back</button>
      {/if}
    </div>
    {#if snap && snap.nodes.length > 0 && banner}<PublicNowCalling call={banner} />{/if}
  </header>

  <main class="mx-auto w-full max-w-[1100px] flex-1 space-y-3 px-3 py-2 sm:px-4">
    {#if !snap}
      <p class="py-10 text-center text-sm text-muted" role="status">Loading…</p>
    {:else if snap.nodes.length === 0}
      <p class="py-10 text-center text-sm text-muted" role="status">No node data yet.</p>
    {:else}
      <section class="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3" aria-label="Nodes" data-testid="tiles">
        {#each snap.nodes as n, i (n.node + '/' + i)}<NodeTile tile={pubTile(n, snap.showDV ? snap.dv?.now : null)} />{/each}
      </section>
      {#if snap.showNetwork}
        {#each snap.nodes as n, i (n.node + '/' + i)}
          {#if n.network && n.network.length > 0}<PublicNetwork node={n} showNode={snap.nodes.length > 1} />{/if}
        {/each}
      {/if}
      {#if snap.showActivity || snap.showDV}
        <section aria-label="Last heard">
          <h2 class="mb-1 text-sm font-semibold uppercase tracking-wide text-muted">Last heard</h2>
          <div class="rounded-md border border-line bg-surface px-3 py-1">
            {#if heard.length === 0}<p class="py-3 text-center text-sm text-muted">Nothing heard yet.</p>
            {:else}<LastHeardTable rows={heard} readonly testid="public-heard" />{/if}
          </div>
        </section>
      {/if}
    {/if}
  </main>

  <footer class="mx-auto flex w-full max-w-[1100px] flex-wrap items-center gap-x-4 gap-y-1 border-t border-line px-3 py-2 text-xs text-muted sm:px-4">
    <span>Powered by <a class="underline hover:text-fg" href={REPO_URL} target="_blank" rel="noopener noreferrer">ASLCP</a></span>
    {#if updatedAt}<span class="ml-auto tabular-nums">Last update {updatedAt}</span>{/if}
  </footer>
</div>
