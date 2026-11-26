<script lang="ts">
  // The station's two optional digital IDs, shared by the first-run setup and Settings. Modes use them when their own
  // ID is empty. Leave a field empty for none.
  import type { Station } from '../lib/types';
  import { MAX_DMR_ID, MAX_NXDN_ID } from '../lib/station';
  let { st = $bindable(), idPrefix = 'st' }: { st: Station; idPrefix?: string } = $props();
  const id = (k: string) => `${idPrefix}-${k}`;
</script>

<div class="grid gap-3 sm:grid-cols-2">
  <div>
    <label class="label" for={id('dmrid')}>DMR ID <span class="font-normal text-muted">(optional)</span></label>
    <input id={id('dmrid')} class="input tabular-nums" type="number" inputmode="numeric" min="1" max={MAX_DMR_ID} step="1" bind:value={st.dmrId} placeholder="1234567" autocomplete="off" />
    <p class="mt-1 text-xs text-muted">Used by the DMR, BM and TGIF networks.</p>
  </div>
  <div>
    <label class="label" for={id('nxdnid')}>NXDN ID <span class="font-normal text-muted">(optional)</span></label>
    <input id={id('nxdnid')} class="input tabular-nums" type="number" inputmode="numeric" min="1" max={MAX_NXDN_ID} step="1" bind:value={st.nxdnId} placeholder="12345" autocomplete="off" />
    <p class="mt-1 text-xs text-muted">Used by NXDN.</p>
  </div>
</div>
