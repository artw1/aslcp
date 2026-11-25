<script lang="ts">
  // The DV tab: the digital modes card with the level meter, the Networks card (the setup's component: state and Enable switch
  // for everyone viewing, settings opening in place for an admin) and Last heard as a card of its own.
  import { onMount } from 'svelte';
  import { api } from '../lib/api';
  import type { DVIdentity, DVNetView } from '../lib/types';
  import { app, clock, isAdmin } from '../lib/store.svelte';
  import DvCard from './DvCard.svelte';
  import { agoText } from '../lib/nowcalling';
  import { dvAt } from '../lib/pins';
  import { heardLine } from '../lib/dvpanel';
  import DvNetworks from './DvNetworks.svelte';
  import Footer from './Footer.svelte';

  // The network settings and identity are admin only (an operator gets 403): a viewer gets the state rows from the snapshot.
  let nets = $state<DVNetView[]>([]), identity = $state<DVIdentity | null>(null);
  async function loadNets() {
    if (!isAdmin()) return;
    try { const r = await api.dvNetworks(); nets = r.modes; identity = r.identity; } catch { nets = []; identity = null; }
  }
  onMount(loadNets);
  function netSaved(n: DVNetView) { nets = nets.map((x) => (x.mode === n.mode ? n : x)); void loadNets(); }
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
    <DvNetworks card deepLink={false} readonly={!isAdmin()} {nets} {identity} modes={app.dv.modes} onsaved={netSaved} />
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
