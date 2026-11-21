<script lang="ts">
  import type { PubNode } from '../../lib/types';
  import Search from '@lucide/svelte/icons/search';
  let { node, showNode = false }: { node: PubNode; showNode?: boolean } = $props();
  let q = $state('');
  const all = $derived(node.network ?? []);
  const shown = $derived.by(() => {
    const t = q.trim().toLowerCase();
    if (!t) return all;
    return all.filter((n) => n.node.includes(t) || n.callsign?.toLowerCase().includes(t));
  });
  const id = $derived(`net-filter-${node.node}`);
</script>
<section aria-label="Linked network{showNode ? ' of node ' + node.node : ''}">
  <div class="mb-1 flex items-center gap-2">
    <h2 class="text-sm font-semibold uppercase tracking-wide text-muted">Network <span class="font-normal">({all.length}{#if showNode}, via {node.node}{/if})</span></h2>
    <div class="relative ml-auto w-44 sm:w-56"><Search size={13} class="pointer-events-none absolute left-2 top-1/2 -translate-y-1/2 text-muted" />
      <input {id} class="input !min-h-7 !py-0.5 pl-7 text-[13px]" type="search" placeholder="Filter" aria-label="Filter network" bind:value={q} /></div>
  </div>
  <div class="max-h-56 overflow-y-auto rounded-md border border-line bg-surface px-2 py-1">
    {#if shown.length === 0}<p class="py-3 text-center text-sm text-muted">{all.length === 0 ? 'No linked network.' : 'No matching nodes.'}</p>
    {:else}
      <ul class="grid grid-cols-2 gap-x-3 sm:grid-cols-3 lg:grid-cols-4">
        {#each shown as n, i (n.node + '/' + i)}
          <li class="flex min-w-0 items-baseline gap-1.5 px-0.5 py-px text-[13px]">
            <span class="shrink-0 font-mono font-semibold">{n.node}</span>
            <span class="min-w-0 truncate text-muted">{n.callsign ?? ''}</span>
          </li>
        {/each}
      </ul>
    {/if}
  </div>
</section>
