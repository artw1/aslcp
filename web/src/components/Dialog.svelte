<script lang="ts">
  import type { Snippet } from 'svelte';
  import X from '@lucide/svelte/icons/x';
  let { open = $bindable(false), title, children, footer, wide = false, xl = false }: {
    open: boolean; title: string; children: Snippet; footer?: Snippet; wide?: boolean; xl?: boolean;
  } = $props();
  let el: HTMLDialogElement | undefined = $state();
  $effect(() => {
    if (!el) return;
    if (open && !el.open) el.showModal();
    if (!open && el.open) el.close();
  });
</script>

<dialog bind:this={el} onclose={() => (open = false)}
  onclick={(e) => { if (e.target === el) open = false; }}
  aria-labelledby="dlg-title"
  class="m-auto w-[calc(100%-1.5rem)] {xl ? 'max-w-4xl' : wide ? 'max-w-2xl' : 'max-w-md'} rounded-xl border border-line bg-surface p-0 text-fg shadow-2xl backdrop:bg-black/50">
  {#if open}
    <div class="flex items-center justify-between border-b border-line px-4 py-3">
      <h2 id="dlg-title" class="text-base font-semibold">{title}</h2>
      <button class="btn btn-sm btn-icon border-transparent" aria-label="Close" onclick={() => (open = false)}><X size={16} /></button>
    </div>
    <div class="max-h-[70dvh] overflow-y-auto p-4">{@render children()}</div>
    {#if footer}<div class="flex flex-wrap justify-end gap-2 border-t border-line px-4 py-3">{@render footer()}</div>{/if}
  {/if}
</dialog>
