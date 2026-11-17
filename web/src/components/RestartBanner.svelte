<script lang="ts">
  import { app } from '../lib/store.svelte';
  import Refresh from '@lucide/svelte/icons/refresh-cw';
</script>
{#if app.restarting}
  <div class="fixed inset-x-0 top-0 z-50 flex items-center justify-center gap-2 border-b border-line bg-surface px-3 py-2 text-sm shadow-md" role="status" aria-live="polite">
    <Refresh size={14} class="animate-spin" />
    {app.restarting.kind === 'reboot' ? 'Rebooting… waiting for the node' : 'Restarting…'}
  </div>
{/if}
