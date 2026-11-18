<script lang="ts">
  import type { NodeStatus } from '../lib/types';
  import { KIND_LABEL, infoLine } from '../lib/util';
  import { openNode } from '../lib/form.svelte';
  import Search from '@lucide/svelte/icons/search';
  let { node, showNode = false }: { node: NodeStatus; showNode?: boolean } = $props();
  let q = $state('');
  const shown = $derived.by(() => {
    const t = q.trim().toLowerCase();
    const l = node.network;
    if (!t) return l;
    return l.filter((n) => [n.node, n.info?.callsign, n.info?.description, n.info?.location].some((s) => s?.toLowerCase().includes(t)));
  });
</script>
<section aria-label="Linked network">
  <div class="mb-1 flex items-center gap-2">
    <h2 class="text-sm font-semibold uppercase tracking-wide text-muted">Network <span class="font-normal">({node.network.length}{#if showNode}, via {node.node}{/if})</span></h2>
    <div class="relative ml-auto w-44 sm:w-56"><Search size={13} class="pointer-events-none absolute left-2 top-1/2 -translate-y-1/2 text-muted" />
      <input class="input !min-h-7 !py-0.5 pl-7 text-[13px]" type="search" placeholder="Filter" aria-label="Filter network" bind:value={q} /></div>
  </div>
  <div class="max-h-56 overflow-y-auto rounded-md border border-line bg-surface px-2 py-1">
    {#if shown.length === 0}<p class="py-3 text-center text-sm text-muted">{node.network.length === 0 ? 'No linked network.' : 'No matching nodes.'}</p>
    {:else}
      <ul class="grid grid-cols-2 gap-x-3 sm:grid-cols-3 lg:grid-cols-4">
        {#each shown as n (n.node)}
          <li class="min-w-0">
            <button type="button" class="flex w-full min-w-0 items-baseline gap-1.5 rounded-sm px-0.5 py-px text-left text-[13px] hover:bg-surface-2" title="{n.node} {infoLine(n.info)} ({KIND_LABEL[n.kind]}, {n.mode})" onclick={() => openNode(n.node, node.node)}>
              <span class="shrink-0 font-mono font-semibold">{n.node}</span>
              <span class="min-w-0 truncate text-muted">{n.info?.callsign ?? ''}</span>
            </button>
          </li>
        {/each}
      </ul>
    {/if}
  </div>
</section>
