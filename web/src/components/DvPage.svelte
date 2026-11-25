<script lang="ts">
  // The DV tab: the digital modes card with the level meter, the mode buttons card (status for everyone, one mode's Enable
  // switch and settings opening in place for an admin) and Last heard as a table (callsigns link to QRZ and radioid.net) in a card of its own.
  import { onMount } from 'svelte';
  import { api } from '../lib/api';
  import type { DVIdentity, DVNetView } from '../lib/types';
  import { app, isAdmin } from '../lib/store.svelte';
  import DvCard from './DvCard.svelte';
  import LastHeardTable from './LastHeardTable.svelte';
  import { dvHeardItems } from '../lib/lastheard';
  import DvModes from './DvModes.svelte';
  import SchedulesCard from './SchedulesCard.svelte';
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
      The digital modes engine is off.{#if isAdmin()} Turn it on in <a href="#/settings/digital-modes" class="text-accent underline hover:no-underline">Digital Modes settings</a>.{/if}
    </p>
  {:else}
    <SchedulesCard />
    <DvCard page />
    <DvModes admin={isAdmin()} {nets} {identity} modes={app.dv.modes} bridge={app.dv.bridge} onsaved={netSaved} />
    <section class="card space-y-1 p-4" aria-label="Last heard" data-testid="dv-heard">
      <h2 class="text-base font-semibold">Last heard</h2>
      {#if app.dv.heard?.length}
        <LastHeardTable rows={dvHeardItems(app.dv.heard)} />
      {:else}<p class="text-xs text-muted">Nothing heard yet.</p>{/if}
    </section>
  {/if}
  <Footer />
</div>
