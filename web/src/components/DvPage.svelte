<script lang="ts">
  // The DV tab: the digital modes card with the level meter, the network status strip, and Last heard as a card of its own.
  import { app, clock, isAdmin } from '../lib/store.svelte';
  import DvCard from './DvCard.svelte';
  import { agoText } from '../lib/nowcalling';
  import { dvAt } from '../lib/pins';
  import { heardLine } from '../lib/dvpanel';
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
    <section class="card space-y-1 p-4" aria-label="Last heard" data-testid="dv-heard">
      <h2 class="text-base font-semibold">Last heard</h2>
      {#if app.dv.heard?.length}
        <ul class="divide-y divide-line font-mono text-xs sm:text-sm">
          {#each app.dv.heard as h, i (i)}
            <li class="flex items-baseline gap-2 py-1" title={h.dmrId ? `DMR ID ${h.dmrId}` : undefined}>
              <span class="min-w-0 flex-1 truncate">{heardLine({ ...h, at: undefined })}</span>
              {#if dvAt(h)}<span class="shrink-0 font-sans text-xs text-muted">{agoText((clock.now - dvAt(h)) / 1000)}</span>{/if}
            </li>
          {/each}
        </ul>
      {:else}<p class="text-xs text-muted">Nothing heard yet.</p>{/if}
    </section>
  {/if}
  <Footer />
</div>
