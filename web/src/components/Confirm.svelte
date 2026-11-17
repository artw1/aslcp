<script lang="ts">
  import Dialog from './Dialog.svelte';
  let { open = $bindable(false), title, message, confirmLabel = 'Confirm', onconfirm }: {
    open: boolean; title: string; message: string; confirmLabel?: string; onconfirm: () => void;
  } = $props();
</script>
<Dialog bind:open {title}>
  <p class="text-sm">{message}</p>
  {#snippet footer()}
    <button class="btn" onclick={() => (open = false)}>Cancel</button>
    <button class="btn btn-primary" onclick={() => { open = false; onconfirm(); }}>{confirmLabel}</button>
  {/snippet}
</Dialog>
