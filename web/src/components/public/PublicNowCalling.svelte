<script lang="ts">
  // The public page's Now Calling strip and sheet: the app's own shared components, fed from the public snapshot only.
  import { clock } from '../../lib/store.svelte';
  import { mmss } from '../../lib/nowcalling';
  import { pubAria, pubIdleLine, pubSheetRows } from '../../lib/pubnow';
  import type { PubNow } from '../../lib/pubnow';
  import NowCallingStrip from '../NowCallingStrip.svelte';
  import NowCallingSheet from '../NowCallingSheet.svelte';
  let { call }: { call: PubNow } = $props();
  const now = $derived(clock.now);
  let open = $state(false);
</script>

<div data-testid="public-nowcalling">
  {#if call.mode === 'talking'}
    <NowCallingStrip mode="talking" label={pubAria(call, now)} source={call.source} main={call.main || call.source} secs={mmss(call.startMs ? (now - call.startMs) / 1000 : 0)} onclick={() => (open = true)} />
  {:else}
    <NowCallingStrip mode="idle" label={pubAria(call, now)} idle={pubIdleLine(call, now)} onclick={() => (open = true)} />
  {/if}
  <NowCallingSheet bind:open title={call.mode === 'talking' ? 'Now calling' : 'Last heard'} rows={pubSheetRows(call, now)} links={call.qrz ? [{ label: 'QRZ lookup', href: call.qrz }] : []} />
</div>
