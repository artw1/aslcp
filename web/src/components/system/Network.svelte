<script lang="ts">
  import { api } from '../../lib/api';
  import type { SysNetwork } from '../../lib/types';
  import Refresh from '@lucide/svelte/icons/refresh-cw';

  let net = $state<SysNetwork | null>(null), err = $state(''), busy = $state(false);
  async function load() {
    busy = true;
    try { net = await api.sysNetwork(); err = ''; } catch (e) { err = e instanceof Error ? e.message : String(e); } finally { busy = false; }
  }
  $effect(() => { load(); });
</script>

<div class="flex items-center justify-between">
  <p class="text-sm text-muted">Read-only status of the network interfaces.</p>
  <button class="btn btn-sm" disabled={busy} onclick={load}><Refresh size={13} />Refresh</button>
</div>
{#if err}
  <p class="text-sm text-danger" role="alert">{err}</p>
{:else if !net}
  <p class="text-sm text-muted" role="status">Loading…</p>
{:else if !net.available}
  <p class="text-sm text-muted">Not available: the ip tool is missing on this system.</p>
{:else}
  <section class="card overflow-x-auto" aria-label="Interfaces">
    <table class="tbl">
      <thead><tr><th>Interface</th><th>State</th><th>Addresses</th><th>MAC</th><th>MTU</th></tr></thead>
      <tbody>
        {#each net.interfaces as i (i.name)}
          <tr>
            <td class="font-medium">{i.name}</td>
            <td><span class="badge {i.state === 'UP' ? 'text-ok' : ''}">{i.state}</span></td>
            <td class="font-mono text-xs">{#each i.addrs as a}<div>{a.addr}/{a.prefix}{#if a.dynamic} <span class="text-muted">dynamic</span>{/if}</div>{:else}<span class="text-muted">none</span>{/each}</td>
            <td class="font-mono text-xs">{i.mac || ''}</td>
            <td>{i.mtu || ''}</td>
          </tr>
        {/each}
      </tbody>
    </table>
  </section>
  <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
    <section class="card space-y-1 p-4" aria-label="Gateways">
      <h3 class="text-base font-semibold">Default gateway</h3>
      {#each net.gateways as g}<p class="font-mono text-sm">{g.gateway} <span class="text-muted">via {g.dev}{#if g.metric} (metric {g.metric}){/if}</span></p>{:else}<p class="text-sm text-muted">None</p>{/each}
    </section>
    <section class="card space-y-1 p-4" aria-label="DNS">
      <h3 class="text-base font-semibold">DNS servers</h3>
      {#each net.dns as d}<p class="font-mono text-sm">{d}</p>{:else}<p class="text-sm text-muted">None found</p>{/each}
      {#if net.dnsSource}<p class="text-xs text-muted">from {net.dnsSource}</p>{/if}
    </section>
  </div>
{/if}
