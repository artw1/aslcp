<script lang="ts">
  import { toasts, dismiss } from '../lib/toast.svelte';
  import Check from '@lucide/svelte/icons/circle-check';
  import Alert from '@lucide/svelte/icons/circle-alert';
  import Info from '@lucide/svelte/icons/info';
</script>
<div class="pointer-events-none fixed inset-x-0 bottom-3 z-[60] flex flex-col items-center gap-2 px-3" aria-live="polite" role="status">
  {#each toasts.list as t (t.id)}
    <button onclick={() => dismiss(t.id)} class="pointer-events-auto flex max-w-md items-start gap-2 rounded-lg border border-line bg-surface px-3 py-2 text-left text-sm shadow-lg">
      {#if t.kind === 'ok'}<Check size={16} class="mt-0.5 shrink-0 text-ok" />{:else if t.kind === 'error'}<Alert size={16} class="mt-0.5 shrink-0 text-danger" />{:else}<Info size={16} class="mt-0.5 shrink-0 text-accent" />{/if}
      <span class="sr-only">{t.kind === 'error' ? 'Error:' : ''}</span>{t.text}
    </button>
  {/each}
</div>
