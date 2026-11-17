<script lang="ts">
  import { app, isAdmin, awaitRestart } from '../lib/store.svelte';
  import { api, lostConnection } from '../lib/api';
  import { toast, toastError } from '../lib/toast.svelte';
  import { cToF, fmtUptime, bugReportUrl } from '../lib/util';
  import Confirm from './Confirm.svelte';
  import NodeStatsDialog from './NodeStatsDialog.svelte';
  const s = $derived(app.system);
  let stats = $state(false), restart = $state(false), restartAslcp = $state(false), reboot = $state(false);
  async function doRestart() {
    try { await api.restartAsterisk(); toast('Asterisk is restarting; links will drop and come back when it is up', 'info'); } catch (e) { toastError(e); }
  }
  async function doRestartAslcp() {
    const before = app.instance;
    try { await api.restartAslcp(); awaitRestart('aslcp', before); } catch (e) { if (lostConnection(e)) awaitRestart('aslcp', before); else toastError(e); }
  }
  async function doReboot() {
    const before = app.instance;
    try { await api.reboot(); awaitRestart('reboot', before); } catch (e) { if (lostConnection(e)) awaitRestart('reboot', before); else toastError(e); }
  }
</script>
<footer class="flex flex-wrap items-center gap-x-4 gap-y-1.5 border-t border-line pt-2 text-sm" aria-label="System status">
  {#if s}
    <span title="CPU temperature">CPU Temp: <b class="font-semibold tabular-nums">{#if s.cpuTemp != null}{Math.round(cToF(s.cpuTemp))}°F / {Math.round(s.cpuTemp)}°C{:else}n/a{/if}</b></span>
    <span title="Load average 1/5/15 min">Load: <span class="tabular-nums">{s.load.map((l) => l.toFixed(2)).join(' ')}</span></span>
    <span title="Host uptime">Up: <span class="tabular-nums">{fmtUptime(s.uptime)}</span></span>
    <span class={s.asterisk.connected ? '' : 'text-danger'} title={s.asterisk.version}>Asterisk: {s.asterisk.connected ? 'connected' : 'down'}{#if s.asterisk.connected && s.asterisk.version}<span class="text-muted">&nbsp;{s.asterisk.version.split('+')[0]}</span>{/if}</span>
  {:else}<span class="text-muted">Loading system status…</span>{/if}
  <a class="text-xs text-muted underline hover:text-fg" href={bugReportUrl({ version: app.version })} target="_blank" rel="noopener noreferrer">Report a bug</a>
  <span class="ml-auto flex gap-2">
    <button class="btn btn-sm" onclick={() => (stats = true)} disabled={app.nodes.length === 0}>Node Stats</button>
    {#if isAdmin()}
      <button class="btn btn-sm btn-danger" disabled={!!app.restarting} onclick={() => (restartAslcp = true)}>Restart ASLCP</button>
      <button class="btn btn-sm btn-danger" onclick={() => (restart = true)}>Restart Asterisk</button>
      <button class="btn btn-sm btn-danger" disabled={!!app.restarting} onclick={() => (reboot = true)}>Reboot</button>
    {/if}
  </span>
</footer>
<NodeStatsDialog bind:open={stats} />
<Confirm bind:open={restart} title="Restart Asterisk?" message="Asterisk will restart gracefully once active calls end. All links will drop and the node is offline for a minute or so; permanent links reconnect afterwards. This page reconnects automatically." confirmLabel="Restart Asterisk" onconfirm={doRestart} />
<Confirm bind:open={restartAslcp} title="Restart ASLCP?" message="Restart ASLCP? The panel will be unavailable for a few seconds." confirmLabel="Restart ASLCP" onconfirm={doRestartAslcp} />
<Confirm bind:open={reboot} title="Reboot the node?" message="Reboot the node? Asterisk and all links go down; the panel comes back after the system starts." confirmLabel="Reboot" onconfirm={doReboot} />
