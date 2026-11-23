<script lang="ts">
  // Config page: the node overview (cards and forms) and, under Advanced, the raw file editor and history.
  import { drafts } from '../lib/configDrafts.svelte';
  import { navigate, router } from '../lib/router.svelte';
  import { parseConfigQuery } from '../lib/nodecfg';
  import RestartAsterisk from './config/RestartAsterisk.svelte';
  import NodeOverview from './config/NodeOverview.svelte';
  import Advanced from './config/Advanced.svelte';

  const route = $derived(parseConfigQuery(router.query));
  const dirty = $derived(Object.keys(drafts.files).length);
  const tabs = [['overview', 'Overview', '/config'], ['advanced', 'Advanced', '/config?tab=advanced']] as const;
</script>

<svelte:window onbeforeunload={(e) => { if (dirty) e.preventDefault(); }} />

<div class="flex flex-wrap items-center gap-2 pb-2">
  <h1 class="mr-2 text-base font-semibold">Config</h1>
  <div class="flex gap-0.5" role="tablist" aria-label="Config pages">
    {#each tabs as [id, label, path]}
      <button role="tab" aria-selected={route.tab === id} class="rounded px-2 py-1 text-[13px] font-medium {route.tab === id ? 'bg-surface-2 text-fg' : 'text-muted hover:text-fg'}" onclick={() => navigate(path)}>{label}{#if id === 'advanced' && dirty}<span class="ml-1 text-accent" title="{dirty} modified">●</span>{/if}</button>
    {/each}
  </div>
  <div class="ml-auto"><RestartAsterisk /></div>
</div>

{#if route.tab === 'advanced'}
  <Advanced file={route.file} line={route.line} history={route.history} />
{:else}
  <NodeOverview />
{/if}
