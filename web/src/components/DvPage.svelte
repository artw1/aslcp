<script lang="ts">
  // The DV tab: the digital modes card with the level meter, the mode buttons card (status for everyone, one mode's Enable
  // switch and settings opening in place for an admin) and Last heard as a table in a card of its own.
  import { onMount } from 'svelte';
  import { api } from '../lib/api';
  import type { DVIdentity, DVNetView } from '../lib/types';
  import { app, clock, isAdmin } from '../lib/store.svelte';
  import DvCard from './DvCard.svelte';
  import { agoText } from '../lib/nowcalling';
  import { dvAt } from '../lib/pins';
  import { heardRow } from '../lib/dvpanel';
  import DvModes from './DvModes.svelte';
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
    <DvModes admin={isAdmin()} {nets} {identity} modes={app.dv.modes} onsaved={netSaved} />
    <section class="card space-y-1 p-4" aria-label="Last heard" data-testid="dv-heard">
      <h2 class="text-base font-semibold">Last heard</h2>
      {#if app.dv.heard?.length}
        <div class="overflow-x-auto">
          <table class="w-full text-left text-xs sm:text-sm">
            <thead class="text-muted">
              <tr><th class="py-1 pr-3 font-medium">Time</th><th class="py-1 pr-3 font-medium">Callsign</th><th class="hidden py-1 pr-3 font-medium sm:table-cell">Mode</th><th class="py-1 font-medium">Target</th></tr>
            </thead>
            <tbody class="divide-y divide-line">
              {#each app.dv.heard as h, i (i)}
                {@const r = heardRow(h)}
                <tr title={h.dmrId ? `DMR ID ${h.dmrId}` : undefined}>
                  <td class="whitespace-nowrap py-1 pr-3 text-muted">{dvAt(h) ? agoText((clock.now - dvAt(h)) / 1000) : ''}</td>
                  <td class="whitespace-nowrap py-1 pr-3 font-mono">{r.call}</td>
                  <td class="hidden py-1 pr-3 sm:table-cell">{r.mode}</td>
                  <td class="py-1 font-mono">{[r.target, r.slot].filter(Boolean).join(' · ')}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {:else}<p class="text-xs text-muted">Nothing heard yet.</p>{/if}
    </section>
  {/if}
  <Footer />
</div>
