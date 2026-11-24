<script lang="ts">
  // Audio settings shared by all digital modes: the AGC of each direction and the static gains (docs/DV.md "Audio level and AGC").
  // Renders inside the Audio card of Digital modes setup, under the echo test (DvSetup.svelte).
  // The AGC switches are always shown; the levels and gains are advanced: collapsed until asked for. Saved at once to the engine (hot reload, no restart).
  import { untrack } from 'svelte';
  import { api } from '../lib/api';
  import { toast, toastError } from '../lib/toast.svelte';
  import type { DVAudio } from '../lib/types';
  import { audioError } from '../lib/dvaudio';
  import Toggle from './Toggle.svelte';

  let { audio, onsaved }: { audio: DVAudio; onsaved: (a: DVAudio) => void } = $props();
  const fresh = (a: DVAudio) => ({ agcTx: a.agcTx, agcRx: a.agcRx, agcTargetDb: String(a.agcTargetDb), agcMaxGainDb: String(a.agcMaxGainDb), gainRxDb: String(a.gainRxDb), gainTxDb: String(a.gainTxDb) });
  let f = $state(untrack(() => fresh(audio))), busy = $state(false), seen = untrack(() => audio), show = $state(false);
  $effect(() => { if (audio !== seen) { seen = audio; f = fresh(audio); } });
  const err = $derived(audioError(f));
  const dirty = $derived(JSON.stringify(f) !== JSON.stringify(fresh(audio)));

  async function save() {
    busy = true;
    try {
      const r = await api.dvAudioPut({ agcTx: f.agcTx, agcRx: f.agcRx, agcTargetDb: Number(f.agcTargetDb), agcMaxGainDb: Number(f.agcMaxGainDb), gainRxDb: Number(f.gainRxDb), gainTxDb: Number(f.gainTxDb) });
      onsaved(r);
      toast(r.applied === false && r.applyError ? `Audio settings saved. ${r.applyError}` : 'Audio settings saved', 'info');
    } catch (e) { toastError(e); } finally { busy = false; }
  }
</script>
<div class="space-y-3 border-t border-line pt-3" data-testid="dv-audio">
  <p class="text-xs text-muted">Level control for the audio between your node and the digital networks. The defaults leave the audio as it is. The Digital modes card shows the levels.</p>
  <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
    <div>
      <span class="inline-flex items-center gap-2 text-sm"><Toggle checked={f.agcTx} label="Node audio AGC (to network)" onchange={(v) => (f.agcTx = v)} />Node audio AGC (to network)</span>
      <p class="mt-1 text-xs text-muted">Evens out the level of your voice before it goes to the network. Silence and noise are not boosted.</p>
    </div>
    <div>
      <span class="inline-flex items-center gap-2 text-sm"><Toggle checked={f.agcRx} label="Network audio AGC (to node)" onchange={(v) => (f.agcRx = v)} />Network audio AGC (to node)</span>
      <p class="mt-1 text-xs text-muted">Evens out the level of stations you hear from the network before it goes to your node. On by default.</p>
    </div>
  </div>
  <div>
    <button type="button" class="btn btn-sm" aria-expanded={show} onclick={() => (show = !show)}>{show ? 'Hide' : 'Show'} advanced audio settings</button>
  </div>
  {#if show}
    <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
      <div><label class="label" for="dva-target">AGC target level (dBFS)</label><input id="dva-target" class="input font-mono" bind:value={f.agcTargetDb} inputmode="numeric" maxlength="3" />
        <p class="mt-1 text-xs text-muted">The speech level the AGC aims for, -40 to -6. Default -22.</p></div>
      <div><label class="label" for="dva-max">AGC maximum gain (dB)</label><input id="dva-max" class="input font-mono" bind:value={f.agcMaxGainDb} inputmode="numeric" maxlength="2" />
        <p class="mt-1 text-xs text-muted">The most the AGC may raise a quiet voice, 0 to 30. Default 18.</p></div>
      <div><label class="label" for="dva-grx">Node audio gain (dB)</label><input id="dva-grx" class="input font-mono" bind:value={f.gainRxDb} inputmode="numeric" maxlength="3" />
        <p class="mt-1 text-xs text-muted">Fixed gain on the audio from your node to the network, -20 to 20. Default 0. Applied before the AGC.</p></div>
      <div><label class="label" for="dva-gtx">Network audio gain (dB)</label><input id="dva-gtx" class="input font-mono" bind:value={f.gainTxDb} inputmode="numeric" maxlength="3" />
        <p class="mt-1 text-xs text-muted">Fixed gain on the audio from the network to your node, -20 to 20. Default 0. Applied before the AGC.</p></div>
    </div>
  {/if}
  {#if dirty || show}
    {#if err && dirty}<p class="text-sm text-danger" role="alert">{err}</p>{/if}
    <button type="button" class="btn btn-primary" disabled={busy || !!err || !dirty} onclick={save} data-testid="dv-audio-save">Save audio settings</button>
  {/if}
</div>
