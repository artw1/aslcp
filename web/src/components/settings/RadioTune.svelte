<script lang="ts">
  // The radio: the sound chip's receive and transmit levels, and the SA818 channel. Whatever mode is active, audio goes to this radio.
  import { api } from '../../lib/api';
  import { app, refreshNode } from '../../lib/store.svelte';
  import { toast, toastError } from '../../lib/toast.svelte';
  import { stationBody, validateStation } from '../../lib/station';
  import { hasSA818, isShari, pinError, radioBody, resolveInterface } from '../../lib/radio';
  import type { RadioInfo, RadioLevels, Station } from '../../lib/types';
  import StationFields from '../StationFields.svelte';
  import RadioPicker from '../RadioPicker.svelte';

  let lv = $state<RadioLevels | null>(null), st = $state<Station | null>(null), info = $state<RadioInfo | null>(null);
  let err = $state(''), busy = $state(false), prog = $state(false);
  const present = $derived(app.node?.radio.present ?? false);
  const iface = $derived(st && info ? resolveInterface(st.radio, info.presets, isShari(info)) : undefined);
  // Without the info (an old node) the SA818 port decides, as before presets existed.
  const sa818 = $derived(info ? hasSA818(iface) : !!st?.radio.sa818Port);
  const pct = (n: number) => (n < 0 ? 'n/a' : `${Math.round(n)}%`);
  const db = (n: number) => (Number.isFinite(n) ? `${n.toFixed(0)} dB` : '–');

  async function loadLevels() { try { lv = await api.radio(); } catch { /* the radio may be absent */ } }
  async function load() {
    try { const s = await api.station(); st = { ...s, radio: { ...s.radio }, id: { ...s.id } }; } catch (e) { toastError(e); }
    try { info = await api.radioInfo(); } catch { /* an old node has no info */ }
    await loadLevels();
  }
  $effect(() => { load(); const t = setInterval(loadLevels, 2000); return () => clearInterval(t); });

  async function setLevel(p: { rxVolume?: number; txVolume?: number }) { try { lv = await api.setRadio(p); } catch (e) { toastError(e); } }
  async function save(ev: Event) {
    ev.preventDefault();
    if (!st) return;
    err = validateStation(st, sa818) || (info ? pinError(iface) : '');
    if (err) return;
    busy = true;
    try { const body = stationBody(st, sa818);
      if (info) body.radio = radioBody(body.radio, info.presets, isShari(info));
      await api.saveStation(body); await refreshNode(); await load(); toast('Radio settings saved'); } catch (e) { toastError(e); } finally { busy = false; }
  }
  async function program() {
    prog = true;
    try { await api.radioProgram(); toast('Radio programmed'); } catch (e) { toastError(e); } finally { prog = false; }
  }
</script>

<section class="card space-y-3 p-4" aria-label="Levels">
  <h2 class="text-base font-semibold">Levels</h2>
  {#if !present}
    <p class="text-sm text-muted">This node has no radio audio.</p>
  {:else if lv}
    <div class="grid gap-4 sm:grid-cols-2">
      <div>
        <label class="label" for="rt-rx">Receive volume (from the radio): {pct(lv.CaptureVolume)}</label>
        <input id="rt-rx" class="w-full accent-[var(--accent)]" type="range" min="0" max="100" disabled={lv.CaptureVolume < 0} value={Math.max(lv.CaptureVolume, 0)} onchange={(e) => setLevel({ rxVolume: +e.currentTarget.value })} />
        <p class="mt-1 text-xs text-muted tabular-nums">Level {db(lv.Rx.RMSDb)}, peak {db(lv.Rx.PeakDb)}{#if lv.Rx.Clipped} , clipped {lv.Rx.Clipped}{/if}</p>
      </div>
      <div>
        <label class="label" for="rt-tx">Transmit volume (to the radio): {pct(lv.PlaybackVolume)}</label>
        <input id="rt-tx" class="w-full accent-[var(--accent)]" type="range" min="0" max="100" disabled={lv.PlaybackVolume < 0} value={Math.max(lv.PlaybackVolume, 0)} onchange={(e) => setLevel({ txVolume: +e.currentTarget.value })} />
        <p class="mt-1 text-xs text-muted tabular-nums">Level {db(lv.Tx.RMSDb)}, peak {db(lv.Tx.PeakDb)}{#if lv.Tx.Clipped} , clipped {lv.Tx.Clipped}{/if}</p>
      </div>
    </div>
    {#if lv.DroppedRx || lv.DroppedTx || lv.Xruns}<p class="text-xs text-muted">Dropped frames: receive {lv.DroppedRx}, transmit {lv.DroppedTx}. Audio underruns: {lv.Xruns}.</p>{/if}
  {:else}<p class="text-sm text-muted">Loading…</p>{/if}
</section>

{#if st && info}
  <section class="card mt-4 space-y-3 p-4" aria-label="Interface">
    <h2 class="text-base font-semibold">Interface</h2>
    <RadioPicker bind:st {info} idPrefix="rt" />
    {#if info.restartRequired}<p class="text-sm text-muted">The node restarts to use the saved interface settings.</p>{/if}
  </section>
{/if}

{#if st && (sa818 || info)}
  <section class="card mt-4 space-y-3 p-4" aria-label="Channel">
    <h2 class="text-base font-semibold">Channel</h2>
    <form class="space-y-3" onsubmit={save}>
      {#if sa818}
        <StationFields bind:st radio idPrefix="rt" />
      {:else}
        <p class="text-sm text-muted">The frequency and tone are set on the radio itself. Save to keep the interface settings above.</p>
      {/if}
      {#if err}<p class="text-sm text-danger" role="alert">{err}</p>{/if}
      <div class="flex flex-wrap gap-2">
        <button class="btn btn-primary" type="submit" disabled={busy}>{busy ? 'Saving…' : 'Save'}</button>
        {#if sa818 && st.radio.sa818Port}<button class="btn" type="button" onclick={program} disabled={prog}>{prog ? 'Programming…' : 'Program the SA818'}</button>{/if}
      </div>
      {#if sa818 && st.radio.sa818Port}<p class="text-xs text-muted">SA818 on <span class="font-mono">{st.radio.sa818Port}</span>. Saving programs the channel when the radio is present.</p>{/if}
    </form>
  </section>
{/if}
