<script lang="ts">
  import { api } from '../lib/api';
  import { refreshFavorites } from '../lib/store.svelte';
  import { toast, toastError } from '../lib/toast.svelte';
  import type { ImportPreview } from '../lib/types';
  import Dialog from './Dialog.svelte';
  let { open = $bindable(false) }: { open: boolean } = $props();
  let preview = $state<ImportPreview | null>(null), loading = $state(false), busy = $state(false);
  let wasOpen = false;
  $effect(() => {
    if (open && !wasOpen) {
      preview = null; loading = true;
      api.importPreview().then((p) => (preview = p)).catch(toastError).finally(() => (loading = false));
    }
    wasOpen = open;
  });
  async function doImport() {
    busy = true;
    try {
      const r = await api.importAllscan(); await refreshFavorites();
      toast(`Imported ${r.imported}, skipped ${r.skipped} already present`); open = false;
    } catch (e) { toastError(e); } finally { busy = false; }
  }
</script>
<Dialog bind:open title="Import favorites from AllScan" wide>
  {#if loading}<p class="text-sm text-muted">Looking for an AllScan installation…</p>
  {:else if preview && !preview.found}<p class="text-sm">No AllScan installation was found on this node.</p>
  {:else if preview}
    <p class="mb-2 text-sm text-muted">Found <span class="font-mono">{preview.path}</span> with {preview.items.length} favorite{preview.items.length === 1 ? '' : 's'}. Nodes already in your favorites are skipped.</p>
    <ul class="divide-y divide-line rounded-md border border-line text-sm">
      {#each preview.items as it}<li class="flex gap-3 px-3 py-1.5"><span class="w-20 shrink-0 font-mono">{it.node}</span><span class="truncate">{it.label}</span></li>{/each}
    </ul>
  {/if}
  {#snippet footer()}
    <button class="btn" onclick={() => (open = false)}>Close</button>
    <button class="btn btn-primary" disabled={!preview?.found || !preview.items.length || busy} onclick={doImport}>{busy ? 'Importing…' : `Import ${preview?.items.length ?? ''}`}</button>
  {/snippet}
</Dialog>
