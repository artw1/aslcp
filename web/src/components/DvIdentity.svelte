<script lang="ts">
  // The node's DMR identity: callsign, DMR ID and an optional ESSID (2 digits, 01-99). The repeater ID the
  // networks use is the DMR ID followed by the ESSID (DMR ID x 100 + ESSID), so the form shows only the ESSID.
  import { untrack } from 'svelte';
  import { api } from '../lib/api';
  import { toast, toastError } from '../lib/toast.svelte';
  import type { DVIdentity } from '../lib/types';
  import { essidToRepeaterId, identityError, repeaterIdToEssid } from '../lib/dvnetform';

  let { identity, onsaved }: { identity: DVIdentity; onsaved: (i: DVIdentity) => void } = $props();
  const fresh = (i: DVIdentity) => ({ callsign: i.callsign, dmrId: i.dmrId ? String(i.dmrId) : '', essid: repeaterIdToEssid(i.dmrId, i.repeaterId) });
  let f = $state(untrack(() => fresh(identity))), busy = $state(false), seen = untrack(() => identity);
  $effect(() => { if (identity !== seen) { seen = identity; f = fresh(identity); } });
  const err = $derived(identityError(f.callsign, f.dmrId, f.essid));
  const dirty = $derived(JSON.stringify(f) !== JSON.stringify(fresh(identity)));

  async function save() {
    busy = true;
    try {
      const rid = essidToRepeaterId(f.dmrId, f.essid);
      const r = await api.dvIdentityPut({ callsign: f.callsign.trim().toUpperCase(), dmrId: Number(f.dmrId), ...(rid !== undefined ? { repeaterId: rid } : {}) });
      onsaved(r);
      toast(r.applied === false && r.applyError ? `Identity saved. ${r.applyError}` : 'Identity saved', 'info');
    } catch (e) { toastError(e); } finally { busy = false; }
  }
</script>
<section class="card space-y-3 p-4 lg:col-span-2" aria-label="Identity" data-testid="dv-identity">
  <h2 class="text-base font-semibold">Identity</h2>
  <p class="text-xs text-muted">Your callsign and DMR ID, used when logging in to a DMR network.</p>
  <div class="grid grid-cols-1 gap-3 sm:grid-cols-3">
    <div><label class="label" for="dvi-call">Callsign</label><input id="dvi-call" class="input font-mono uppercase" bind:value={f.callsign} maxlength="8" autocomplete="off" /></div>
    <div><label class="label" for="dvi-id">DMR ID</label><input id="dvi-id" class="input font-mono" bind:value={f.dmrId} inputmode="numeric" maxlength="7" /></div>
    <div><label class="label" for="dvi-rid">ESSID (optional)</label><input id="dvi-rid" class="input font-mono" bind:value={f.essid} inputmode="numeric" maxlength="2" placeholder="e.g. 01" /></div>
  </div>
  {#if err && dirty}<p class="text-sm text-danger" role="alert">{err}</p>{/if}
  <button type="button" class="btn btn-primary" disabled={busy || !!err || !dirty} onclick={save} data-testid="dv-identity-save">Save identity</button>
</section>
