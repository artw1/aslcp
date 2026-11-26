<script lang="ts">
  import { api } from '../../lib/api';
  import { app, refreshNode } from '../../lib/store.svelte';
  import { toast, toastError } from '../../lib/toast.svelte';
  import { stationBody, validateStation } from '../../lib/station';
  import type { Station } from '../../lib/types';
  import StationFields from '../StationFields.svelte';

  let st = $state<Station | null>(null), err = $state(''), busy = $state(false);
  const radio = $derived(app.node?.radio.present ?? true);
  async function load() { try { const s = await api.station(); st = { ...s, radio: { ...s.radio }, id: { ...s.id } }; } catch (e) { toastError(e); } }
  $effect(() => { load(); });
  async function save(ev: Event) {
    ev.preventDefault();
    if (!st) return;
    err = validateStation(st, radio);
    if (err) return;
    busy = true;
    try { await api.saveStation(stationBody(st, radio)); await refreshNode(); toast('Station saved'); } catch (e) { toastError(e); } finally { busy = false; }
  }
</script>

<section class="card space-y-3 p-4" aria-label="Station">
  <h2 class="text-base font-semibold">Station</h2>
  {#if st}
    <form class="space-y-3" onsubmit={save}>
      <StationFields bind:st {radio} idPrefix="stn" />
      {#if err}<p class="text-sm text-danger" role="alert">{err}</p>{/if}
      <button class="btn btn-primary" type="submit" disabled={busy}>{busy ? 'Saving…' : 'Save'}</button>
    </form>
  {:else}<p class="text-sm text-muted">Loading…</p>{/if}
</section>
