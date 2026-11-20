<script lang="ts">
  import { api } from '../../lib/api';
  import { toast, toastError } from '../../lib/toast.svelte';
  import type { WifiAP, WifiState } from '../../lib/types';
  import { ssidError, pskError, moveItem, signalLabel, delayLabel } from '../../lib/wifi';
  import Confirm from '../Confirm.svelte';
  import Toggle from '../Toggle.svelte';
  import Refresh from '@lucide/svelte/icons/refresh-cw';
  import Up from '@lucide/svelte/icons/arrow-up';
  import Down from '@lucide/svelte/icons/arrow-down';
  import Trash from '@lucide/svelte/icons/trash-2';
  import Lock from '@lucide/svelte/icons/lock';

  let st = $state<WifiState | null>(null), err = $state(''), busy = $state(false);
  let scan = $state<WifiAP[] | null>(null), scanNote = $state(''), scanning = $state(false);
  let ssid = $state(''), psk = $state(''), hidden = $state(false), showPsk = $state(false);
  let askForget = $state(false), forgetId = $state(''), forgetName = $state('');
  let askAdd = $state(false), askHotspot = $state(false), delay = $state(180);

  const ssidMsg = $derived(ssid === '' ? '' : ssidError(ssid));
  const pskMsg = $derived(pskError(psk));
  const canAdd = $derived(!busy && ssid !== '' && !ssidMsg && !pskMsg);
  const wifiOk = $derived(!!st && st.available && st.hasWifi);
  // A change that may cut the connection this page is using gets a confirmation.
  const risky = $derived(!!st?.viaWifi);

  function apply(s: WifiState) { st = s; delay = s.hotspot.delaySeconds || 180; }
  async function load() {
    try { apply(await api.wifi()); err = ''; } catch (e) { err = e instanceof Error ? e.message : String(e); }
  }
  $effect(() => { load(); });

  async function doScan() {
    scanning = true;
    try { const r = await api.wifiScan(); scan = r.networks; scanNote = r.note ?? ''; } catch (e) { toastError(e); } finally { scanning = false; }
  }
  function pick(a: WifiAP) { ssid = a.ssid; hidden = false; if (!a.secured) psk = ''; document.getElementById('wifi-psk')?.focus(); }

  async function add() {
    busy = true;
    try {
      const r = await api.wifiAdd(ssid, psk, hidden);
      apply(r.wifi); toast(`Connected to ${ssid}`, 'info');
      ssid = ''; psk = ''; hidden = false; showPsk = false;
      if (scan) void doScan();
    } catch (e) { toastError(e); void load(); } finally { busy = false; }
  }
  function startAdd() { if (risky) askAdd = true; else void add(); }

  async function move(i: number, to: number) {
    if (!st) return;
    busy = true;
    try { apply(await api.wifiOrder(moveItem(st.networks, i, to).map((n) => n.id))); } catch (e) { toastError(e); void load(); } finally { busy = false; }
  }
  function askRemove(id: string, name: string) { forgetId = id; forgetName = name; askForget = true; }
  async function forget() {
    busy = true;
    try { apply(await api.wifiForget(forgetId)); toast(`Removed ${forgetName}`, 'info'); } catch (e) { toastError(e); void load(); } finally { busy = false; }
  }
  async function hotspot(on: boolean, d = 0) {
    busy = true;
    try { apply(await api.wifiHotspot(on, d)); toast(on ? 'Setup hotspot on' : 'Setup hotspot off', 'info'); } catch (e) { toastError(e); } finally { busy = false; }
  }
  const delayOk = $derived(Number.isInteger(delay) && delay >= 30 && delay <= 3600);
</script>

{#if err}
  <p class="text-sm text-danger" role="alert">{err}</p>
{:else if !st}
  <p class="text-sm text-muted" role="status">Loading…</p>
{:else if !wifiOk}
  <p class="text-sm text-muted">Not available: {st.reason || 'no WiFi device found'}.</p>
{:else}
  <div class="flex items-center justify-between">
    <p class="text-sm text-muted">
      {#if st.hotspot.active}The setup hotspot is on.
      {:else if st.active}Connected to <strong class="text-fg">{st.active}</strong>.
      {:else if st.ethernet}Using Ethernet; WiFi is not connected.
      {:else}Not connected to any network.{/if}
      {#if st.radio === 'disabled'} The WiFi radio is switched off.{/if}
    </p>
    <button class="btn btn-sm" disabled={busy} onclick={load}><Refresh size={13} />Refresh</button>
  </div>

  {#if st.viaHotspot}
    <p class="rounded border border-warn bg-warn/10 p-3 text-sm" role="note">You are using the setup hotspot. Once the node joins a network, this connection ends; reconnect to your normal network and open the node's new address.</p>
  {:else if st.viaWifi}
    <p class="rounded border border-warn/50 bg-warn/10 p-3 text-sm" role="note">You reached this page over WiFi. Changing the networks can disconnect you.</p>
  {/if}

  <div class="grid grid-cols-1 items-start gap-4 lg:grid-cols-2">
    <section class="card space-y-3 p-4" aria-label="Saved networks">
      <h3 class="text-base font-semibold">Saved networks</h3>
      <p class="text-xs text-muted">The node joins the first one in range, top to bottom.</p>
      {#if st.networks.length === 0}
        <p class="text-sm text-muted">No saved networks.</p>
      {:else}
        <ol class="divide-y divide-line rounded border border-line">
          {#each st.networks as n, i (n.id)}
            <li class="flex items-center gap-2 px-2 py-1.5">
              <span class="min-w-0 flex-1 truncate text-sm"><span class="font-medium">{n.ssid}</span>
                {#if n.active}<span class="badge text-ok">connected</span>{/if}
                {#if n.hidden}<span class="badge">hidden</span>{/if}
                {#if !n.secured}<span class="badge">open</span>{/if}
              </span>
              <button class="btn btn-sm" aria-label="Move {n.ssid} up" disabled={busy || i === 0} onclick={() => move(i, i - 1)}><Up size={13} /></button>
              <button class="btn btn-sm" aria-label="Move {n.ssid} down" disabled={busy || i === st.networks.length - 1} onclick={() => move(i, i + 1)}><Down size={13} /></button>
              <button class="btn btn-sm btn-danger" aria-label="Remove {n.ssid}" disabled={busy} onclick={() => askRemove(n.id, n.ssid)}><Trash size={13} /></button>
            </li>
          {/each}
        </ol>
      {/if}
    </section>

    <section class="card space-y-3 p-4" aria-label="Add a network">
      <h3 class="text-base font-semibold">Add a network</h3>
      <form class="space-y-3" onsubmit={(e) => { e.preventDefault(); if (canAdd) startAdd(); }}>
        <div>
          <label class="label" for="wifi-ssid">Network name (SSID)</label>
          <input id="wifi-ssid" class="input" bind:value={ssid} autocomplete="off" spellcheck="false" aria-invalid={!!ssidMsg} />
          {#if ssidMsg}<p class="mt-1 text-xs text-danger">{ssidMsg}</p>{/if}
        </div>
        <div>
          <label class="label" for="wifi-psk">Password (leave empty for an open network)</label>
          <div class="flex gap-2">
            <input id="wifi-psk" class="input" type={showPsk ? 'text' : 'password'} bind:value={psk} autocomplete="new-password" spellcheck="false" aria-invalid={!!pskMsg} />
            <button type="button" class="btn" onclick={() => (showPsk = !showPsk)}>{showPsk ? 'Hide' : 'Show'}</button>
          </div>
          {#if pskMsg}<p class="mt-1 text-xs text-danger">{pskMsg}</p>{/if}
        </div>
        <label class="flex items-center gap-2 text-sm"><input type="checkbox" bind:checked={hidden} />Hidden network (does not announce its name)</label>
        <button class="btn btn-primary" type="submit" disabled={!canAdd}>{busy ? 'Connecting…' : 'Save and connect'}</button>
        <p class="text-xs text-muted">The node connects right away. If it cannot, the network is not saved. The password is stored on the node for NetworkManager and is never shown again.</p>
      </form>
    </section>
  </div>

  <section class="card space-y-3 p-4" aria-label="Available networks">
    <div class="flex items-center justify-between">
      <h3 class="text-base font-semibold">Networks in range</h3>
      <button class="btn btn-sm" disabled={scanning || st.hotspot.active} onclick={doScan}><Refresh size={13} class={scanning ? 'animate-spin' : ''} />{scan ? 'Scan again' : 'Scan'}</button>
    </div>
    {#if st.hotspot.active}
      <p class="text-sm text-muted">The setup hotspot uses the radio, so the node cannot scan while it is on. Type the network name instead.</p>
    {:else if scan === null}
      <p class="text-sm text-muted">Scanning takes a few seconds and may briefly interrupt WiFi.</p>
    {:else if scan.length === 0}
      <p class="text-sm text-muted">No networks found.{scanNote ? ' ' + scanNote : ''}</p>
    {:else}
      {#if scanNote}<p class="text-xs text-muted">{scanNote}</p>{/if}
      <ul class="divide-y divide-line rounded border border-line">
        {#each scan as a (a.ssid)}
          <li class="flex items-center gap-2 px-2 py-1.5 text-sm">
            <span class="min-w-0 flex-1 truncate">{a.ssid}
              {#if a.inUse}<span class="badge text-ok">connected</span>{:else if a.saved}<span class="badge">saved</span>{/if}
              {#if !a.supported}<span class="badge">needs a login (not supported)</span>{/if}
            </span>
            {#if a.secured}<Lock size={13} class="text-muted" aria-label="secured" />{:else}<span class="text-xs text-muted">open</span>{/if}
            <span class="w-20 text-right text-xs text-muted">{signalLabel(a.signal)} ({a.signal})</span>
            <button class="btn btn-sm" disabled={!a.supported} onclick={() => pick(a)}>Use</button>
          </li>
        {/each}
      </ul>
    {/if}
  </section>

  <section class="card space-y-3 p-4" aria-label="Setup hotspot">
    <h3 class="text-base font-semibold">Setup hotspot</h3>
    <div class="flex items-center gap-3">
      <Toggle label="Setup hotspot" checked={st.hotspot.enabled} disabled={busy} onchange={(v) => (v ? hotspot(true) : (askHotspot = true))} />
      <span class="text-sm">Start a WiFi network when the node has no connection</span>
      <span class="badge {st.hotspot.active ? 'text-warn' : ''}">{st.hotspot.active ? 'on now' : st.hotspot.enabled ? 'standby' : 'off'}</span>
    </div>
    <p class="text-sm text-muted">If the node has no Ethernet and no saved WiFi for {delayLabel(st.hotspot.delaySeconds)}, it opens a WiFi network named <span class="font-mono">{st.hotspot.ssid}</span> so you can reach this page and add your WiFi. It stops as soon as a network connects.</p>
    <p class="rounded border border-warn/50 bg-warn/10 p-3 text-sm" role="note">The hotspot is open (no WiFi password) and this page is plain HTTP: anyone in range can read a login typed over it. The node does not route traffic from the hotspot to any other network{st.hotspot.firewall === 'firewalld' ? '; the firewall zone allows only this page, DHCP and DNS' : '; no firewall zone is set up because firewalld is not running'}.</p>
    {#if st.hotspot.enabled}
      <div class="flex flex-wrap items-end gap-2">
        <div>
          <label class="label" for="wifi-delay">Start after (seconds)</label>
          <input id="wifi-delay" class="input w-32" type="number" min="30" max="3600" step="30" bind:value={delay} />
        </div>
        <button class="btn" disabled={busy || !delayOk || delay === st.hotspot.delaySeconds} onclick={() => hotspot(true, delay)}>Set</button>
      </div>
    {/if}
  </section>
{/if}

<Confirm bind:open={askAdd} title="Change WiFi?" message="You reached this page over WiFi. Connecting to {ssid} may disconnect you; reconnect and open the node's address on the new network." confirmLabel="Save and connect" onconfirm={add} />
<Confirm bind:open={askForget} title="Remove the network?" message={`Remove ${forgetName}? The node will no longer join it automatically.${risky ? ' You reached this page over WiFi, so this may disconnect you.' : ''}`} confirmLabel="Remove" onconfirm={forget} />
<Confirm bind:open={askHotspot} title="Turn the setup hotspot off?" message="Without the hotspot you can only reach a node that has lost its network from a cable or from the console. Turn it off?" confirmLabel="Turn off" onconfirm={() => hotspot(false)} />
