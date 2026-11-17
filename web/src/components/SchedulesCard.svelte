<script lang="ts">
  import { app, canOperate, clock, setSchedulesEnabled } from '../lib/store.svelte';
  import { toastError } from '../lib/toast.svelte';
  import { cancelText, countdown, evLabel, fmtAt, nodeParts } from '../lib/schedules';
  import Toggle from './Toggle.svelte';
  import CalendarClock from '@lucide/svelte/icons/calendar-clock';

  const op = $derived(canOperate());
  const snap = $derived(app.schedules);
  const multi = $derived(app.nodes.length > 1);
  const next = $derived(snap?.upcoming[0]);
  const running = $derived.by(() => {
    if (!snap) return [];
    return snap.active.map((a) => ({ a, s: snap.schedules.find((x) => x.id === a.id) })).filter((r) => r.s);
  });
  const label = (n: { name?: string }) => (n.name ? ` (${n.name})` : '');
  let busy = $state(false);
  async function toggle(on: boolean) {
    busy = true;
    try { await setSchedulesEnabled(on); } catch (e) { toastError(e); } finally { busy = false; }
  }
</script>

{#if snap}
  <section class="flex flex-wrap items-center gap-x-3 gap-y-1 rounded-md border border-line bg-surface px-2 py-1.5 text-sm" aria-label="Schedules">
    <span class="inline-flex items-center gap-1.5 font-medium"><CalendarClock size={15} class="text-accent" aria-hidden="true" />Schedules:</span>
    {#if snap.schedules.length === 0}
      <span class="text-muted">No schedules</span>
    {:else}
      {#if op}
        <span class="inline-flex items-center gap-1.5"><Toggle checked={snap.enabled} disabled={busy} label={snap.enabled ? 'Pause all schedules' : 'Resume schedules'} onchange={toggle} /><span class={snap.enabled ? 'text-ok' : 'text-warn'}>{snap.enabled ? 'Active' : 'Paused'}</span></span>
      {:else}
        <span class="font-medium {snap.enabled ? 'text-ok' : 'text-warn'}">{snap.enabled ? 'Active' : 'Paused'}</span>
      {/if}
      {#if !snap.enabled && snap.resumeAt}
        <span class="text-warn">Paused until midnight{snap.pausedReason ? ` (${snap.pausedReason})` : ''}</span>
      {:else if !snap.enabled && snap.pausedReason}
        <span class="text-warn">Paused: {snap.pausedReason}{snap.pausedAt ? `, ${nodeParts(snap.pausedAt).time}` : ''}</span>
      {/if}
      {#if snap.lastCancel}<span class="text-warn">{cancelText(snap.lastCancel)}</span>{/if}
      {#if running.length}
        <span>Running: {running[0].s!.name || evLabel({ ...running[0].s!, action: 'connect' }, multi)} until <b class="font-semibold tabular-nums">{nodeParts(running[0].a.end).time}</b>{running.length > 1 ? ` (+${running.length - 1} more)` : ''}</span>
      {/if}
      {#if next}
        <span class="{snap.enabled ? '' : 'text-muted'}">Next: {evLabel(next, multi)}{label(next)}, <span class="tabular-nums">{fmtAt(next.at, snap.now)}</span>, <span class="tabular-nums text-muted">{countdown(next.at, clock.now)}</span></span>
      {:else if !running.length}
        <span class="text-muted">Nothing upcoming</span>
      {/if}
    {/if}
    <a href="#/schedules" class="ml-auto text-xs text-accent underline hover:no-underline">{snap.schedules.length ? 'Manage' : 'Add one'}</a>
  </section>
{/if}
