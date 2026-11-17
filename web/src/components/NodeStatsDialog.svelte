<script lang="ts">
  import { app } from '../lib/store.svelte';
  import { api } from '../lib/api';
  import type { NodeStats } from '../lib/types';
  import { humanizeKey } from '../lib/util';
  import Dialog from './Dialog.svelte';

  let { open = $bindable(false) }: { open: boolean } = $props();
  type Res = { node: string; stats?: NodeStats['stats']; error?: string };
  let results = $state<Res[]>([]), loading = $state(false);
  let wasOpen = false;
  $effect(() => {
    if (open && !wasOpen) load();
    wasOpen = open;
  });
  async function load() {
    loading = true; results = [];
    const nodes = app.nodes.map((n) => n.node);
    results = await Promise.all(nodes.map(async (node): Promise<Res> => {
      try { return { node, stats: (await api.nodeStats(node)).stats }; }
      catch (e) { return { node, error: e instanceof Error ? e.message : String(e) }; }
    }));
    loading = false;
  }
</script>
<Dialog bind:open title="Node stats" wide>
  {#if loading}<p class="text-sm text-muted" role="status">Loading…</p>{/if}
  <div class="space-y-4">
    {#each results as r (r.node)}
      <section aria-label="Node {r.node}">
        <h3 class="mb-1 border-b border-line pb-1 text-sm font-semibold"><span class="font-mono">{r.node}</span></h3>
        {#if r.error}<p class="text-sm text-danger">{r.error}</p>
        {:else if r.stats}
          <dl class="grid grid-cols-1 gap-x-6 text-[13px] sm:grid-cols-2">
            {#each r.stats as f (f.key)}
              <div class="flex items-baseline justify-between gap-3 border-b border-line/50 py-0.5"><dt class="text-muted">{humanizeKey(f.key)}</dt><dd class="text-right font-mono tabular-nums">{f.value}</dd></div>
            {/each}
          </dl>
        {/if}
      </section>
    {/each}
  </div>
  {#snippet footer()}<button class="btn" onclick={load} disabled={loading}>Refresh</button><button class="btn btn-primary" onclick={() => (open = false)}>Close</button>{/snippet}
</Dialog>
