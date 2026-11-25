<script lang="ts">
  // The DV tab: the digital modes card with the level meter, the full Last heard list and the network logins.
  import { app, isAdmin } from '../lib/store.svelte';
  import DvCard from './DvCard.svelte';
  import Footer from './Footer.svelte';
</script>

<div class="space-y-3">
  {#if !app.dv}
    <p class="py-10 text-center text-sm text-muted" role="status">Loading digital modes…</p>
  {:else if !app.dv.installed}
    <p class="rounded-md border border-line bg-surface px-3 py-8 text-center text-sm text-muted" data-testid="dv-off">
      The digital modes engine is off.{#if isAdmin()} Set it up in <a href="#/digital" class="text-accent underline hover:no-underline">Digital modes</a>.{/if}
    </p>
  {:else}
    <DvCard page />
  {/if}
  <Footer />
</div>
