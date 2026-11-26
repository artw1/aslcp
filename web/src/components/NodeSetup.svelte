<script lang="ts">
  // First-run setup of the station, shown to an admin until the node has a callsign: the callsign, the radio channel,
  // whether to ID and two optional digital IDs. The modes are set up later on the Modes page; everything else is in Settings.
  import { api } from '../lib/api';
  import { app, refreshNode } from '../lib/store.svelte';
  import { toast, toastError } from '../lib/toast.svelte';
  import { stationBody, validateStation } from '../lib/station';
  import { detectedLabel, hasSA818, isShari, pinError, radioBody, resolveInterface } from '../lib/radio';
  import type { RadioInfo, Station } from '../lib/types';
  import StationFields from './StationFields.svelte';
  import RadioPicker from './RadioPicker.svelte';
  import DigitalIdFields from './DigitalIdFields.svelte';
  import Radio from '@lucide/svelte/icons/radio';

  let st = $state<Station | null>(null), info = $state<RadioInfo | null>(null);
  let err = $state(''), busy = $state(false), loaded = $state(false);
  // The channel fields show only when the chosen interface programs an SA818; an old node without info keeps the old rule.
  const iface = $derived(st && info ? resolveInterface(st.radio, info.presets, isShari(info)) : undefined);
  const radio = $derived(info ? hasSA818(iface) : (app.node?.radio.present ?? true));

  async function load() {
    try {
      const s = await api.station();
      st = { ...s, radio: { ...s.radio }, id: { ...s.id } };
      try { info = await api.radioInfo(); } catch { /* an old node has no info */ }
      loaded = true;
    } catch (e) { err = e instanceof Error ? e.message : String(e); }
  }
  $effect(() => { load(); });

  async function finish(ev: Event) {
    ev.preventDefault();
    if (!st) return;
    err = validateStation(st, radio) || (info ? pinError(iface) : '');
    if (err) return;
    busy = true;
    try {
      const body = stationBody(st, radio);
      if (info) body.radio = radioBody(body.radio, info.presets, isShari(info));
      await api.saveStation(body);
      await refreshNode();
      toast('Station saved');
    } catch (e) { toastError(e); } finally { busy = false; }
  }
</script>

<main class="mx-auto w-full max-w-2xl space-y-4 px-4 py-6">
  <header class="flex items-center gap-3">
    <Radio class="size-7 text-accent" aria-hidden="true" />
    <div><h1 class="text-xl font-semibold">Set up your station</h1><p class="text-sm text-muted">The callsign is required. You set up the modes later on the Modes page. Everything here can be changed later in Settings.</p></div>
  </header>
  {#if !loaded && !err}
    <p class="text-sm text-muted">Loading…</p>
  {:else if st}
    <form class="space-y-4" onsubmit={finish}>
      <section class="card space-y-3 p-4" aria-label="Station">
        <h2 class="text-base font-semibold">Station</h2>
        <StationFields bind:st {radio} idPrefix="setup" />
      </section>
      <section class="card space-y-3 p-4" aria-label="Digital IDs">
        <div>
          <h2 class="text-base font-semibold">Digital IDs</h2>
          <p class="text-sm text-muted">Optional. You can change both later in Settings.</p>
        </div>
        <DigitalIdFields bind:st idPrefix="setup" />
      </section>
      {#if info}
        <section class="card space-y-3 p-4" aria-label="Radio interface">
          <h2 class="text-base font-semibold">Radio interface</h2>
          <RadioPicker bind:st {info} idPrefix="setup-rp" />
        </section>
      {/if}
      <section class="card space-y-1 p-4" aria-label="Hardware">
        <h2 class="text-base font-semibold">Detected hardware</h2>
        <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
          <dt class="text-muted">Radio</dt><dd>{info ? (detectedLabel(info) || 'No USB radio interface found') : (app.node?.radio.present ?? true) ? 'Present' : 'None (the node runs without a radio)'}</dd>
          {#if radio && st.radio.sa818Port}<dt class="text-muted">SA818 port</dt><dd class="font-mono">{st.radio.sa818Port}</dd>{/if}
        </dl>
      </section>
      {#if err}<p class="text-sm text-danger" role="alert">{err}</p>{/if}
      <div class="flex justify-end"><button class="btn btn-primary" type="submit" disabled={busy}>{busy ? 'Saving…' : 'Finish setup'}</button></div>
    </form>
  {:else}
    <p class="text-sm text-danger" role="alert">{err}</p>
  {/if}
</main>
