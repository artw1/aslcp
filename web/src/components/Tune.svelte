<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import { api } from '../lib/api';
  import { app, clock, refreshTune } from '../lib/store.svelte';
  import { router, navigate } from '../lib/router.svelte';
  import { toast, toastError } from '../lib/toast.svelte';
  import type { TuneMeter, TuneParams, TunePatch, TuneState, Link } from '../lib/types';
  import Confirm from './Confirm.svelte';
  import TriangleAlert from '@lucide/svelte/icons/triangle-alert';
  import Save from '@lucide/svelte/icons/save';
  import Undo from '@lucide/svelte/icons/undo-2';
  import Unlink from '@lucide/svelte/icons/unlink';

  const SIGNALS: [string, string][] = [['no', 'Not used'], ['usb', 'USB (COS/CTCSS pin)'], ['usbinvert', 'USB, inverted'], ['pp', 'Parallel port'], ['ppinvert', 'Parallel port, inverted']];
  const FIELD_NAMES: Record<string, string> = {
    rxVoice: 'Rx level', txA: 'Tx A level', txB: 'Tx B level', echo: 'Echo', rxBoost: 'Rx boost', preEmphasis: 'Pre-emphasis', deEmphasis: 'De-emphasis',
    plFilter: 'PL filter', pttInvert: 'PTT mode', carrierFrom: 'Carrier from', ctcssFrom: 'CTCSS from', rxOnDelayMs: 'Rx on delay', txOffDelayMs: 'Tx off delay',
  };
  const fieldName = (f: string) => FIELD_NAMES[f] ?? f;

  // ---- device selection (the choice is part of the URL so the page can be bookmarked)
  const devices = $derived(app.tune.devices);
  const queryDev = $derived(new URLSearchParams(router.query).get('dev') ?? '');
  const dev = $derived(devices.find((d) => d.name === queryDev)?.name ?? devices[0]?.name ?? '');
  const device = $derived(devices.find((d) => d.name === dev));

  let st = $state<TuneState | null>(null);
  let loading = $state(false), loadErr = $state('');
  let busy = $state(0);
  let meter = $state<TuneMeter | null>(null), meterErr = $state('');
  let loadedFor = '';

  async function load(d: string) {
    loadedFor = d; st = null; meter = null; loadErr = ''; loading = true;
    try {
      const s = await api.tuneGet(d);
      if (loadedFor === d) st = s;
    } catch (e) { if (loadedFor === d) loadErr = e instanceof Error ? e.message : String(e); } finally { if (loadedFor === d) loading = false; }
  }
  $effect(() => { if (app.user && dev && dev !== loadedFor) void load(dev); });
  // The device list may be empty when the page is opened directly: look again.
  onMount(() => { if (!app.tune.loaded || !devices.length) void refreshTune(); });

  // ---- live receive meter, about once a second while the tab is visible
  let meterTimer: ReturnType<typeof setTimeout> | undefined, alive = true, meterBusy = false;
  async function pollMeter() {
    const d = dev;
    if (alive && d && st && !document.hidden && !meterBusy) {
      meterBusy = true;
      try { const m = await api.tuneMeter(d); if (d === dev) { meter = m; meterErr = ''; } }
      catch (e) { meterErr = e instanceof Error ? e.message : String(e); }
      finally { meterBusy = false; }
    }
    if (alive) meterTimer = setTimeout(pollMeter, 1000);
  }
  onMount(() => { meterTimer = setTimeout(pollMeter, 500); });

  const meterPct = $derived(meter ? Math.max(0, Math.min(100, ((meter.pk + 90) / 90) * 100)) : 0);
  const clipping = $derived(!!meter && (meter.clip > 0 || meter.pk >= -0.5));
  const meterColor = $derived(clipping ? 'bg-danger' : meter && meter.pk > -6 ? 'bg-warn' : 'bg-ok');

  // ---- links of this node (from the live node status the dashboard uses)
  const nodeStatus = $derived(app.nodes.find((n) => n.node === (st?.node ?? device?.node)));
  const links = $derived<Link[]>(nodeStatus?.links ?? []);
  const linkNames = $derived(links.map((l) => l.info?.callsign ? `${l.node} (${l.info.callsign})` : l.node));
  let discOpen = $state(false), discBusy = $state(false);
  async function disconnectAll() {
    const n = st?.node ?? device?.node;
    if (!n) return;
    discBusy = true;
    try { await api.disconnectAll(n); toast('Disconnect all sent'); } catch (e) { toastError(e); } finally { discBusy = false; }
  }

  // ---- applying settings (each control is applied to the radio as soon as it changes)
  async function apply(patch: TunePatch) {
    if (!st) return;
    busy++;
    try {
      const r = await api.tuneSet(dev, patch);
      for (const e of r.errors) toast(`${fieldName(e.field)}: ${e.message}`, 'error');
      st.params = r.params; st.dirty = r.dirty;
    } catch (e) { toastError(e); try { st = await api.tuneGet(dev); } catch { /* keep what is shown */ } } finally { busy--; }
  }
  function level(field: 'rxVoice' | 'txA' | 'txB', v: number) {
    if (!st || !Number.isFinite(v)) return;
    v = Math.max(0, Math.min(999, Math.round(v)));
    st.params[field] = v;
    void apply({ [field]: v });
  }
  function flag(field: 'rxBoost' | 'preEmphasis' | 'deEmphasis' | 'plFilter' | 'pttInvert', v: boolean) { if (st) { st.params[field] = v; void apply({ [field]: v }); } }
  function select(field: 'carrierFrom' | 'ctcssFrom', v: string) { if (st) { st.params[field] = v; void apply({ [field]: v }); } }
  function delay(field: 'rxOnDelayMs' | 'txOffDelayMs', v: number) {
    if (!st || !Number.isFinite(v)) return;
    v = Math.max(0, Math.min(60000, Math.round(v / 20) * 20));
    st.params[field] = v;
    void apply({ [field]: v });
  }
  const isDirty = (f: keyof TuneParams) => !!st?.dirty.includes(f);

  // ---- echo mode
  const echoOn = $derived(!!st?.echoState.on);
  const echoLeft = $derived.by(() => {
    const t = st?.echoState.expiresAt ? Date.parse(st.echoState.expiresAt) : 0;
    return t > 0 ? Math.max(0, Math.round((t - clock.now) / 1000)) : null;
  });
  const mmss = (s: number) => `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`;
  let echoConfirm = $state(false);
  async function setEcho(on: boolean) {
    if (!st) return;
    busy++;
    try { const e = await api.tuneEcho(dev, on); st.echoState = e; st.params.echo = e.on; } catch (e) { toastError(e); try { st = await api.tuneGet(dev); } catch { /* ignore */ } } finally { busy--; }
  }
  function echoClick() { if (echoOn) void setEcho(false); else if (links.length) echoConfirm = true; else void setEcho(true); }
  // The server also switches echo off when the page stops asking; this makes it immediate.
  function leaving() { if (st?.echoState.on) api.tuneEchoOffOnExit(dev); }
  onDestroy(() => { alive = false; clearTimeout(meterTimer); leaving(); });
  // Auto-off by the server: refresh when the countdown ends.
  $effect(() => { if (echoOn && echoLeft === 0) { const t = setTimeout(async () => { try { st = await api.tuneGet(dev); } catch { /* ignore */ } }, 1500); return () => clearTimeout(t); } });

  // ---- save and revert
  let saveOpen = $state(false), revertOpen = $state(false);
  async function save() {
    busy++;
    try {
      const r = await api.tuneSave(dev);
      st = await api.tuneGet(dev);
      toast(r.warning ? `Saved. ${r.warning}` : r.commit ? 'Saved and recorded in the config history' : 'Saved', r.warning ? 'info' : 'ok', r.warning ? 12000 : 4500);
    } catch (e) { toastError(e); try { st = await api.tuneGet(dev); } catch { /* ignore */ } } finally { busy--; }
  }
  async function revert() {
    busy++;
    try {
      const r = await api.tuneRevert(dev);
      for (const e of r.errors) toast(`${fieldName(e.field)}: ${e.message}`, 'error');
      if (st) { st.params = r.params; st.dirty = r.dirty; }
      if (!r.errors.length) toast('Reverted to the saved values');
    } catch (e) { toastError(e); } finally { busy--; }
  }
  const dirty = $derived((st?.dirty.length ?? 0) > 0);
  function beforeUnload(e: BeforeUnloadEvent) { if (dirty || echoOn) { e.preventDefault(); } }
</script>

<svelte:window onbeforeunload={beforeUnload} onpagehide={leaving} />

<div class="space-y-3 py-1">
  <div class="flex flex-wrap items-center gap-2">
    <h1 class="text-base font-semibold">Audio tuning</h1>
    {#if devices.length > 1}
      <label class="flex items-center gap-1.5 text-sm"><span class="text-muted">Device</span>
        <select class="input w-auto" value={dev} onchange={(e) => navigate('/tune?dev=' + encodeURIComponent(e.currentTarget.value))}>
          {#each devices as d}<option value={d.name}>{d.name}{d.node ? ` (node ${d.node})` : ''}</option>{/each}
        </select>
      </label>
    {:else if device}
      <span class="badge">{device.name}{device.node ? ` · node ${device.node}` : ''}</span>
    {/if}
    {#if dirty}<span class="badge text-warn" role="status">Unsaved changes</span>{/if}
  </div>

  {#if !app.tune.loaded}
    <p class="text-sm text-muted" role="status">Loading…</p>
  {:else if !devices.length}
    <div class="card p-4 text-sm">
      <p class="font-medium">No SimpleUSB audio device was found.</p>
      <p class="mt-1 text-muted">This page tunes nodes that use a SimpleUSB interface (<code>rxchannel = SimpleUSB/…</code> in rpt.conf). {app.tune.reason ? `Reason: ${app.tune.reason}.` : 'No node uses one on this system.'}</p>
      <button class="btn mt-3" onclick={() => refreshTune()}>Check again</button>
    </div>
  {:else if loading && !st}
    <p class="text-sm text-muted" role="status">Reading the interface…</p>
  {:else if loadErr}
    <div class="card border-danger/50 p-4 text-sm" role="alert">
      <p class="font-medium text-danger">Cannot read this device</p>
      <p class="mt-1">{loadErr}</p>
      <button class="btn mt-3" onclick={() => load(dev)}>Retry</button>
    </div>
  {:else if st}
    {#if echoOn}
      <div class="card flex flex-wrap items-center gap-2 border-danger bg-danger/10 p-3 text-sm" role="alert">
        <TriangleAlert size={18} class="shrink-0 text-danger" />
        <div class="min-w-0 flex-1">
          <p class="font-semibold text-danger">Echo mode is ON: this node retransmits what it hears.</p>
          <p>It switches itself off {echoLeft !== null ? `in ${mmss(echoLeft)}` : 'automatically'}, or when you leave this page. Echo should never be saved on.</p>
        </div>
        <button class="btn btn-primary" onclick={() => setEcho(false)}>Turn echo off</button>
      </div>
    {/if}

    <!-- links -->
    <section class="card p-3" aria-label="Links">
      <div class="flex flex-wrap items-center gap-2">
        <h2 class="text-sm font-semibold">Links</h2>
        {#if links.length}
          <span class="badge text-warn">{links.length} linked</span>
          <span class="text-sm">{linkNames.join(', ')}</span>
          <button class="btn btn-sm ml-auto" disabled={discBusy} onclick={() => (discOpen = true)}><Unlink size={14} />Disconnect all links</button>
        {:else}
          <span class="badge text-ok">No links</span>
          <span class="text-sm text-muted">Nothing is linked to node {st.node}.</span>
        {/if}
      </div>
      {#if links.length}
        <p class="mt-2 text-sm text-muted">
          While tuning, audio this node receives is sent to the linked nodes, and their traffic mixes into the node. Echo is the worst case.
          Disconnect the links first, or accept that linked stations will hear you. Permanent links and auto-reconnect links may come back on their own.
        </p>
      {/if}
    </section>

    <div class="grid gap-3 lg:grid-cols-2">
      <!-- receive -->
      <section class="card space-y-3 p-3" aria-label="Receive">
        <h2 class="text-sm font-semibold">Receive</h2>
        <div>
          <div class="flex items-baseline justify-between text-xs text-muted"><span>Receive level (dBFS, relative)</span>
            <span class="tabular-nums {clipping ? 'font-semibold text-danger' : ''}">{meter ? `Pk ${meter.pk.toFixed(1)}  Avg ${meter.avg}  Max ${meter.max}` : '—'}</span></div>
          <div class="relative mt-1 h-4 overflow-hidden rounded bg-surface-2" role="meter" aria-label="Receive peak level" aria-valuemin="-90" aria-valuemax="0" aria-valuenow={meter?.pk ?? -90}>
            <div class="h-full transition-[width] duration-300 {meterColor}" style="width:{meterPct}%"></div>
          </div>
          <div class="mt-0.5 flex justify-between text-[10px] text-muted tabular-nums"><span>-90</span><span>-60</span><span>-30</span><span>0</span></div>
          {#if clipping}<p class="mt-1 flex items-center gap-1 text-sm font-medium text-danger" role="alert"><TriangleAlert size={14} />Clipping: lower the Rx level.</p>{/if}
          {#if meter?.stale}<p class="mt-1 text-xs text-muted">The radio is busy; showing the last reading.</p>{/if}
          {#if meterErr}<p class="mt-1 text-xs text-warn">Meter: {meterErr}</p>{/if}
          {#if links.length}
            <p class="mt-1 text-xs text-muted">Links are connected. The meter reads only this radio's own receiver; audio from linked nodes is not included, so it will not show their level.</p>
          {/if}
          <p class="mt-1 text-xs text-muted">Relative level, not deviation. Peak hold is one second.</p>
        </div>
        {@render slider('Rx level', 'rxVoice', st.params.rxVoice, 'Voice level from the receiver')}
        <div class="grid gap-3 sm:grid-cols-2">
          <label class="block"><span class="label">Carrier from {isDirty('carrierFrom') ? '*' : ''}</span>
            <select class="input" value={st.params.carrierFrom} onchange={(e) => select('carrierFrom', e.currentTarget.value)}>
              {#if st.params.carrierOther}<option value={st.params.carrierFrom} disabled>Other (set in the file)</option>{/if}
              {#each SIGNALS as [v, l]}<option value={v}>{l}</option>{/each}
            </select></label>
          <label class="block"><span class="label">CTCSS from {isDirty('ctcssFrom') ? '*' : ''}</span>
            <select class="input" value={st.params.ctcssFrom} onchange={(e) => select('ctcssFrom', e.currentTarget.value)}>
              {#if st.params.ctcssOther}<option value={st.params.ctcssFrom} disabled>Other (set in the file)</option>{/if}
              {#each SIGNALS as [v, l]}<option value={v}>{l}</option>{/each}
            </select></label>
        </div>
        <div class="grid gap-1 sm:grid-cols-2">
          {@render toggle('Rx boost', 'rxBoost', st.params.rxBoost)}
          {@render toggle('De-emphasis', 'deEmphasis', st.params.deEmphasis)}
          {@render toggle('PL filter', 'plFilter', st.params.plFilter)}
        </div>
        {@render ms('Rx on delay', 'rxOnDelayMs', st.params.rxOnDelayMs)}
      </section>

      <!-- transmit -->
      <section class="card space-y-3 p-3" aria-label="Transmit">
        <h2 class="text-sm font-semibold">Transmit</h2>
        {@render slider('Tx A level', 'txA', st.params.txA, 'Transmit audio level, channel A')}
        {@render slider('Tx B level', 'txB', st.params.txB, 'Transmit audio level, channel B')}
        <div class="grid gap-1 sm:grid-cols-2">
          {@render toggle('Pre-emphasis', 'preEmphasis', st.params.preEmphasis)}
          <label class="flex items-center justify-between gap-2 rounded px-1 py-1 text-sm"><span>PTT mode {isDirty('pttInvert') ? '*' : ''}</span>
            <select class="input w-auto" value={st.params.pttInvert ? 'open' : 'ground'} onchange={(e) => flag('pttInvert', e.currentTarget.value === 'open')}>
              <option value="ground">Ground (normal)</option><option value="open">Open (inverted)</option>
            </select></label>
        </div>
        {@render ms('Tx off delay', 'txOffDelayMs', st.params.txOffDelayMs)}
        <div class="flex flex-wrap gap-2 border-t border-line pt-3">
          <button class="btn {echoOn ? 'btn-primary' : ''}" onclick={echoClick}>{echoOn ? 'Turn echo off' : 'Echo mode…'}</button>
        </div>
        <p class="text-xs text-muted">Echo mode plays back what the receiver hears, so you can listen to your own audio on a second radio.</p>
        <p class="text-xs text-muted">For a steady test tone (e.g. with a deviation meter), use <code>simpleusb-tune-menu</code> in a terminal.</p>
      </section>
    </div>

    <!-- save / revert -->
    <section class="card flex flex-wrap items-center gap-2 p-3" aria-label="Save">
      <button class="btn btn-primary" disabled={busy > 0} onclick={() => (saveOpen = true)}><Save size={15} />Save</button>
      <button class="btn" disabled={busy > 0 || !dirty} onclick={() => (revertOpen = true)}><Undo size={15} />Revert</button>
      <span class="text-sm {dirty ? 'text-warn' : 'text-muted'}">{dirty ? `Unsaved: ${st.dirty.map(fieldName).join(', ')}` : 'The radio matches the saved file.'}</span>
      <p class="w-full text-xs text-muted">Changes apply to the running radio at once but are lost when the module is reloaded or Asterisk restarts, unless you save them. Saving writes simpleusb.conf and records it in the config history.</p>
    </section>
  {/if}
</div>

{#snippet slider(label: string, field: 'rxVoice' | 'txA' | 'txB', value: number, hint: string)}
  <div>
    <div class="flex items-center justify-between gap-2">
      <label class="label mb-0" for="s-{field}">{label} {isDirty(field) ? '*' : ''}</label>
      <input class="input w-20 text-right tabular-nums" type="number" min="0" max="999" step="1" value={value} aria-label="{label} value"
        onchange={(e) => level(field, e.currentTarget.valueAsNumber)} />
    </div>
    <input id="s-{field}" class="mt-1 w-full accent-[var(--accent)]" type="range" min="0" max="999" step="1" value={value} title={hint}
      oninput={(e) => { if (st) st.params[field] = e.currentTarget.valueAsNumber; }} onchange={(e) => level(field, e.currentTarget.valueAsNumber)} />
  </div>
{/snippet}

{#snippet toggle(label: string, field: 'rxBoost' | 'preEmphasis' | 'deEmphasis' | 'plFilter', value: boolean)}
  <label class="flex items-center justify-between gap-2 rounded px-1 py-1 text-sm"><span>{label} {isDirty(field) ? '*' : ''}</span>
    <input type="checkbox" class="size-4 accent-[var(--accent)]" checked={value} onchange={(e) => flag(field, e.currentTarget.checked)} /></label>
{/snippet}

{#snippet ms(label: string, field: 'rxOnDelayMs' | 'txOffDelayMs', value: number)}
  <label class="flex items-center justify-between gap-2 text-sm"><span>{label} {isDirty(field) ? '*' : ''}</span>
    <span class="flex items-center gap-1"><input class="input w-24 text-right tabular-nums" type="number" min="0" max="60000" step="20" value={value}
      onchange={(e) => delay(field, e.currentTarget.valueAsNumber)} /><span class="text-muted">ms</span></span></label>
{/snippet}

<Confirm bind:open={discOpen} title="Disconnect all links" confirmLabel="Disconnect all"
  message={`This disconnects ${linkNames.join(', ')} from node ${st?.node ?? ''}, including permanent links. Permanent and auto-reconnect links may come back on their own.`}
  onconfirm={disconnectAll} />

<Confirm bind:open={echoConfirm} title="Turn echo mode on?" confirmLabel="Turn echo on"
  message={`Node ${st?.node ?? ''} will retransmit what it hears, and the linked nodes (${linkNames.join(', ')}) will hear it. Disconnect the links first if you do not want that. Echo switches itself off after a few minutes.`}
  onconfirm={() => setEcho(true)} />

<Confirm bind:open={saveOpen} title="Save tuning" confirmLabel="Save"
  message={`Writes the running values to simpleusb.conf and records the change in the config history. Echo mode is switched off first.`}
  onconfirm={save} />

<Confirm bind:open={revertOpen} title="Revert changes" confirmLabel="Revert"
  message="Sets the running radio back to the values in simpleusb.conf from when this page was opened or last saved." onconfirm={revert} />
