<script lang="ts">
  import { api } from '../lib/api';
  import { app, canOperate, refreshFavorites } from '../lib/store.svelte';
  import { connectTo } from '../lib/actions.svelte';
  import { exclusive } from '../lib/settings.svelte';
  import { toast, toastError } from '../lib/toast.svelte';
  import type { NodeInfo } from '../lib/types';
  import Search from '@lucide/svelte/icons/search';
  import Star from '@lucide/svelte/icons/star';
  import Plug from '@lucide/svelte/icons/plug';
  let q = $state(''), results = $state<NodeInfo[]>([]), loading = $state(false), searched = $state(false);
  let timer: ReturnType<typeof setTimeout> | undefined, seq = 0;
  const op = $derived(canOperate());
  const favNodes = $derived(new Set(app.favorites.map((f) => f.node)));
  function oninput() {
    clearTimeout(timer);
    if (q.trim().length < 2) { results = []; searched = false; return; }
    loading = true;
    timer = setTimeout(async () => {
      const my = ++seq;
      try { const r = await api.search(q.trim(), 50); if (my === seq) { results = r; searched = true; } } catch (e) { toastError(e); }
      finally { if (my === seq) loading = false; }
    }, 200);
  }
  async function fav(i: NodeInfo) {
    try { await api.addFavorite({ node: i.node }); await refreshFavorites(); toast(`Added ${i.node} to favorites`); } catch (e) { toastError(e); }
  }
</script>
<div class="space-y-4">
  <h1 class="text-xl font-bold tracking-tight">Node lookup</h1>
  <div class="relative"><Search size={16} class="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-muted" />
    <input class="input pl-9" type="search" placeholder="Search by node number, callsign, description or location" aria-label="Search nodes" bind:value={q} {oninput} /></div>
  <section class="card" aria-live="polite">
    {#if loading && !results.length}<p class="p-6 text-center text-sm text-muted">Searching…</p>
    {:else if !searched}<p class="p-6 text-center text-sm text-muted">Type at least two characters.</p>
    {:else if results.length === 0}<p class="p-6 text-center text-sm text-muted">No nodes match “{q}”.</p>
    {:else}
      <ul class="divide-y divide-line">
        {#each results as r (r.node)}
          <li class="flex flex-wrap items-center gap-x-3 gap-y-2 px-4 py-2.5">
            <div class="min-w-0 flex-1 basis-56">
              <div><span class="font-mono font-semibold">{r.node}</span> <span class="font-medium">{r.callsign}</span></div>
              <div class="truncate text-xs text-muted">{[r.description, r.location].filter(Boolean).join(' · ')}</div>
            </div>
            <div class="flex gap-1.5">
              <button class="btn btn-sm" disabled={!op || favNodes.has(r.node)} onclick={() => fav(r)}><Star size={14} />{favNodes.has(r.node) ? 'Favorite' : 'Add to favorites'}</button>
              <button class="btn btn-sm btn-primary" disabled={!op || !app.selectedNode} onclick={() => connectTo(app.selectedNode, r.node, 'transceive', false, exclusive.value)}><Plug size={14} />Connect</button>
            </div>
          </li>
        {/each}
      </ul>
    {/if}
  </section>
  {#if app.nodes.length > 1}<p class="text-xs text-muted">Connect uses local node {app.selectedNode} (change on the dashboard).</p>{/if}
</div>
