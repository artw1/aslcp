<script lang="ts">
  import type { Snippet } from 'svelte';
  /** Small popup menu. `items` receives a close() callback. */
  let { label, trigger, items, disabled = false, btnClass = 'rowbtn', align = 'right' }: {
    label: string; trigger: Snippet; items: Snippet<[() => void]>; disabled?: boolean; btnClass?: string; align?: 'left' | 'right';
  } = $props();
  let open = $state(false);
  let root: HTMLElement | undefined = $state();
  const close = () => (open = false);
</script>

<svelte:window onclick={(e) => { if (open && root && !root.contains(e.target as Node)) open = false; }} onkeydown={(e) => { if (open && e.key === 'Escape') open = false; }} />

<span class="relative inline-flex" bind:this={root}>
  <button type="button" class={btnClass} {disabled} aria-haspopup="menu" aria-expanded={open} aria-label={label} title={label} onclick={() => (open = !open)}>{@render trigger()}</button>
  {#if open}
    <div role="menu" class="absolute top-full z-30 mt-1 min-w-48 rounded border border-line bg-surface p-1 text-left text-fg shadow-md {align === 'right' ? 'right-0' : 'left-0'}">
      {@render items(close)}
    </div>
  {/if}
</span>
