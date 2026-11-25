<script lang="ts">
  import { app } from '../lib/store.svelte';
  import { reconcilePending } from '../lib/actions.svelte';
  import StatusTable from './StatusTable.svelte';
  import ConnectBar from './ConnectBar.svelte';
  import NetworkList from './NetworkList.svelte';
  import SchedulesCard from './SchedulesCard.svelte';
  import Favorites from './Favorites.svelte';
  import NodeConnectDialog from './NodeConnectDialog.svelte';
  import Footer from './Footer.svelte';

  const node = $derived(app.nodes.find((n) => n.node === app.selectedNode));
  $effect(() => { app.nodes; reconcilePending(); });
</script>

<div class="space-y-3">
  {#if !app.haveNodes}
    <p class="py-10 text-center text-sm text-muted" role="status">Loading node status…</p>
  {:else if app.nodes.length === 0}
    <p class="rounded-md border border-line bg-surface px-3 py-8 text-center text-sm text-muted">No local nodes found. Is Asterisk running with app_rpt?</p>
  {:else}
    <SchedulesCard />
    <section class="space-y-2" aria-label="Connection status">
      {#each app.nodes as n (n.node)}
        <div class="rounded-md border border-line bg-surface"><StatusTable node={n} /></div>
      {/each}
    </section>
    <ConnectBar />
    <Favorites />
    {#if node}<NetworkList {node} showNode={app.nodes.length > 1} />{/if}
  {/if}
  <NodeConnectDialog />
  <Footer />
</div>
