<script lang="ts">
  import { api, lostConnection } from '../../lib/api';
  import { app, awaitRestart } from '../../lib/store.svelte';
  import { toast, toastError } from '../../lib/toast.svelte';
  import type { HostStatus } from '../../lib/types';
  import Confirm from '../Confirm.svelte';
  import Toggle from '../Toggle.svelte';
  import Power from '@lucide/svelte/icons/power';
  import RotateCw from '@lucide/svelte/icons/rotate-cw';

  let host = $state<HostStatus | null>(null), err = $state(''), zones = $state<string[]>([]);
  let name = $state(''), tz = $state(''), busy = $state(false);
  let askName = $state(false), askReboot = $state(false), askOff = $state(false), offDone = $state(false);
  const label = /^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/i;
  const nameOk = $derived(label.test(name.trim()));
  const nameChanged = $derived(!!host && name.trim().toLowerCase() !== host.hostname);
  const tzChanged = $derived(!!host && tz !== host.timezone);

  async function load() {
    try {
      host = await api.sysHost(); err = '';
      name = host.hostname; tz = host.timezone;
      if (host.timeAvailable && !zones.length) zones = (await api.sysTimezones()).timezones;
    } catch (e) { err = e instanceof Error ? e.message : String(e); }
  }
  $effect(() => { load(); });

  async function patch(p: Parameters<typeof api.setSysHost>[0], ok: string) {
    busy = true;
    try { host = await api.setSysHost(p); name = host.hostname; tz = host.timezone; toast(ok, 'info'); } catch (e) { toastError(e); } finally { busy = false; }
  }
  async function doReboot() {
    const before = app.instance;
    try { await api.reboot(); awaitRestart('reboot', before); } catch (e) { if (lostConnection(e)) awaitRestart('reboot', before); else toastError(e); }
  }
  async function doPoweroff() {
    try { await api.poweroff(); offDone = true; } catch (e) { if (lostConnection(e)) offDone = true; else toastError(e); }
  }
</script>

{#if err}
  <p class="text-sm text-danger" role="alert">{err}</p>
{:else if !host}
  <p class="text-sm text-muted" role="status">Loading…</p>
{:else}
  <div class="grid grid-cols-1 items-start gap-4 lg:grid-cols-2">
    <section class="card space-y-3 p-4" aria-label="Hostname">
      <h3 class="text-base font-semibold">Hostname</h3>
      {#if !host.hostnameAvailable}
        <p class="text-sm text-muted">
          {host.hostnamectlMissing ? 'Not available: hostnamectl is missing on this system.' : 'Not available: the hostname could not be read.'}
        </p>
      {:else}
        <div>
          <label class="label" for="sys-host">Hostname</label>
          {#if host.hostnamectlAvailable}
            <div class="flex gap-2">
              <input id="sys-host" class="input" bind:value={name} maxlength="63" autocomplete="off" spellcheck="false" aria-invalid={!nameOk} />
              <button class="btn btn-primary" disabled={busy || !nameOk || !nameChanged} onclick={() => (askName = true)}>Change</button>
            </div>
            <p class="mt-1 text-xs {nameOk ? 'text-muted' : 'text-danger'}">Letters, digits and hyphens; up to 63 characters; not starting or ending with a hyphen.</p>
          {:else}
            <input id="sys-host" class="input" value={host.hostname} readonly />
            <p class="mt-1 text-xs text-muted">
              {host.hostnamectlMissing ? 'Changing it is not available: hostnamectl is missing on this system.' : 'Changing it is not available: hostnamectl is not usable on this system.'}
            </p>
          {/if}
        </div>
        {#if host.prettyHostname}<p class="text-xs text-muted">{host.prettyHostname}</p>{/if}
        {#if host.os}<p class="text-xs text-muted">{host.os}</p>{/if}
      {/if}
    </section>

    <section class="card space-y-3 p-4" aria-label="Time">
      <h3 class="text-base font-semibold">Time</h3>
      {#if !host.timeAvailable}
        <p class="text-sm text-muted">Not available: timedatectl is missing on this system.</p>
      {:else}
        <p class="text-sm">Clock: <span class="font-mono tabular-nums">{host.clock || host.now}</span></p>
        <div>
          <label class="label" for="sys-tz">Time zone</label>
          <div class="flex gap-2">
            <input id="sys-tz" class="input" list="sys-tzlist" bind:value={tz} autocomplete="off" spellcheck="false" />
            <datalist id="sys-tzlist">{#each zones as z (z)}<option value={z}></option>{/each}</datalist>
            <button class="btn btn-primary" disabled={busy || !tzChanged || !zones.includes(tz)} onclick={() => patch({ timezone: tz }, `Time zone: ${tz}`)}>Set</button>
          </div>
        </div>
        <div class="flex items-center gap-3">
          <Toggle label="Network time (NTP)" checked={host.ntp} disabled={busy || !host.canNtp} onchange={(v) => patch({ ntp: v }, v ? 'NTP on' : 'NTP off')} />
          <span class="text-sm">Network time (NTP)</span>
          <span class="badge {host.ntp && host.ntpSynchronized ? 'text-ok' : 'text-warn'}">{!host.canNtp ? 'no NTP service' : host.ntp ? (host.ntpSynchronized ? 'synchronized' : 'not synchronized') : 'off'}</span>
        </div>
        {#if host.localRtc}<p class="text-xs text-muted">The hardware clock is kept in local time.</p>{/if}
      {/if}
    </section>

    <section class="card space-y-3 p-4 lg:col-span-2" aria-label="Power">
      <h3 class="text-base font-semibold">Power</h3>
      {#if offDone}
        <p class="text-sm" role="status">The node is shutting down. This page stops responding; power it back on by hand.</p>
      {:else}
        <p class="text-sm text-muted">Reboot restarts the whole node; shut down powers it off and it stays off until someone switches it on.</p>
        <div class="flex flex-wrap gap-2">
          <button class="btn btn-danger" disabled={!!app.restarting} onclick={() => (askReboot = true)}><RotateCw size={14} />Reboot</button>
          <button class="btn btn-danger" disabled={!!app.restarting} onclick={() => (askOff = true)}><Power size={14} />Shut down</button>
        </div>
      {/if}
    </section>
  </div>
{/if}

<Confirm bind:open={askName} title="Change the hostname?" message={`Change the hostname to ${name.trim().toLowerCase()}? Other machines and tools that use the old name will no longer find this node by it. A reboot is recommended afterwards.`} confirmLabel="Change hostname" onconfirm={() => patch({ hostname: name.trim().toLowerCase() }, `Hostname: ${name.trim().toLowerCase()}`)} />
<Confirm bind:open={askReboot} title="Reboot the node?" message="Reboot the node? The radio and the active link go down; the panel comes back after the system starts." confirmLabel="Reboot" onconfirm={doReboot} />
<Confirm bind:open={askOff} title="Shut down the node?" message="Shut down the node? The radio and the active link go down and the node stays off until someone powers it on by hand. You cannot restart it from this page." confirmLabel="Shut down" onconfirm={doPoweroff} />
