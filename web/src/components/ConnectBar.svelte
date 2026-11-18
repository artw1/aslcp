<script lang="ts">
  import { form, registerFocus } from '../lib/form.svelte';
  import ConnectControls from './ConnectControls.svelte';

  let wrap: HTMLElement | undefined = $state();
  $effect(() => {
    registerFocus(() => { const i = wrap?.querySelector('input'); i?.focus(); i?.select(); });
    return () => registerFocus(null);
  });
</script>

<section class="rounded-md border border-line bg-surface p-2" aria-label="Connect" bind:this={wrap}>
  <ConnectControls bind:target={form.target} />
</section>
