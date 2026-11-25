<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../lib/api';
  import { toastError } from '../lib/toast.svelte';
  import type { HamDBStatus } from '../lib/types';
  import Toggle from './Toggle.svelte';

  // One row next to the radioid.net user list: the switch, a summary and the hint. Off by default.
  let st = $state<HamDBStatus | null>(null), err = $state(''), busy = $state(false);
  const summary = $derived(!st ? '' : !st.enabled ? 'Off' : `${st.cached.toLocaleString()} cached · ${st.today} of ${st.dailyMax} requests today${st.pausedUntil ? ' · paused' : ''}`);
  const HINT = 'For YSF, P25, NXDN and M17 callers the radioid.net list does not know, asks hamdb.org for the name and place. This sends the callsigns it hears to hamdb.org (one at a time, at most 100 a day, answers kept on this node for 30 days).';

  async function load() { try { st = await api.hamdb(); err = ''; } catch (e) { err = e instanceof Error ? e.message : String(e); } }
  onMount(load);
  async function toggle(on: boolean) {
    busy = true;
    try { st = await api.setHamDB(on); } catch (e) { toastError(e); } finally { busy = false; }
  }
</script>

<div class="space-y-1 border-t border-line pt-3" role="group" aria-label="Look up callsigns on hamdb.org" data-testid="dv-hamdb">
  <div class="flex flex-wrap items-center gap-x-4 gap-y-1">
    {#if st}<span class="inline-flex items-center gap-2 text-sm"><Toggle tap checked={st.enabled} disabled={busy} label={st.enabled ? 'Stop looking up callsigns on hamdb.org' : 'Look up callsigns on hamdb.org'} onchange={toggle} />Look up callsigns on hamdb.org</span>
    {:else}<span class="text-sm">Look up callsigns on hamdb.org</span>{/if}
    <span class="text-xs text-muted" data-testid="dv-hamdb-summary">{st ? summary : err || 'Loading…'}</span>
  </div>
  <p class="text-xs text-muted">{HINT}</p>
</div>
