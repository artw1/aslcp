<script lang="ts">
  import { api } from '../../lib/api';
  import { isAdmin } from '../../lib/store.svelte';
  import { toast, toastError } from '../../lib/toast.svelte';
  import type { NodeListStatus } from '../../lib/types';
  import Refresh from '@lucide/svelte/icons/refresh-cw';

  let nl = $state<NodeListStatus | null>(null), rbusy = $state(false);
  async function loadNl() { try { nl = await api.nodelist(); } catch (e) { toastError(e); } }
  async function refresh() {
    rbusy = true;
    try { await api.refreshNodelist(); toast('Node list refresh started', 'info'); setTimeout(loadNl, 3000); setTimeout(loadNl, 10000); } catch (e) { toastError(e); } finally { rbusy = false; }
  }
  $effect(() => { loadNl(); });
</script>

<section class="card space-y-3 p-4" aria-label="Node list">
  <h2 class="text-base font-semibold">Node list</h2>
  {#if nl}
    <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
      <dt class="text-muted">Nodes</dt><dd class="font-mono">{nl.count.toLocaleString()}</dd>
      <dt class="text-muted">Updated</dt><dd>{nl.updated ? new Date(nl.updated).toLocaleString() : 'never'}</dd>
      <dt class="text-muted">Source</dt><dd class="break-all">{nl.source}</dd>
      <dt class="text-muted">Status</dt><dd class={nl.error ? 'text-danger' : 'text-ok'}>{nl.error ?? 'OK'}</dd>
    </dl>
  {:else}<p class="text-sm text-muted">Loading…</p>{/if}
  {#if isAdmin()}<button class="btn" disabled={rbusy} onclick={refresh}><Refresh size={14} class={rbusy ? 'animate-spin' : ''} />Refresh now</button>{/if}
</section>
