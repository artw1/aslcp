<script lang="ts">
  import { app, canOperate, clock, refreshSchedules, setSchedulesEnabled } from '../lib/store.svelte';
  import { api } from '../lib/api';
  import { toastError } from '../lib/toast.svelte';
  import { fmtAgo } from '../lib/util';
  import { MODE_NAMES, activeFor, countdown, crossesMidnight, evLabel, fmtAt, fmtWhen, nodeParts, resultClass, tzDiffers } from '../lib/schedules';
  import type { Schedule } from '../lib/types';
  import ScheduleDialog from './ScheduleDialog.svelte';
  import Confirm from './Confirm.svelte';
  import Menu from './Menu.svelte';
  import Toggle from './Toggle.svelte';
  import Plus from '@lucide/svelte/icons/plus';
  import Pencil from '@lucide/svelte/icons/pencil';
  import Trash from '@lucide/svelte/icons/trash-2';
  import Dots from '@lucide/svelte/icons/ellipsis';

  const op = $derived(canOperate());
  const snap = $derived(app.schedules);
  const multi = $derived(app.nodes.length > 1);
  const list = $derived(snap?.schedules ?? []);
  const paused = $derived(!!snap && !snap.enabled);
  let editing = $state<Schedule | null>(null), dlg = $state(false);
  let del = $state<Schedule | null>(null), delOpen = $state(false);
  let busy = $state(false);

  async function toggleAll(on: boolean) {
    busy = true;
    try { await setSchedulesEnabled(on); } catch (e) { toastError(e); } finally { busy = false; }
  }
  async function toggleOne(s: Schedule, enabled: boolean) {
    const { id: _i, lastRun: _l, lastResult: _r, ...rest } = s;
    try { await api.updateSchedule(s.id, { ...rest, enabled }); await refreshSchedules(); } catch (e) { toastError(e); }
  }
  async function remove(s: Schedule) {
    try { await api.deleteSchedule(s.id); await refreshSchedules(); } catch (e) { toastError(e); }
  }
  const title = (s: Schedule) => s.name || `${s.action === 'connect' ? 'Connect' : 'Disconnect'} ${s.target}`;
  const ago = (s: Schedule) => (s.lastRun ? fmtAgo((clock.now - Date.parse(s.lastRun)) / 1000) : '');
  const byId = (id: number) => list.find((s) => s.id === id);
  const modeText = (s: Schedule) => (s.action === 'connect' ? MODE_NAMES[s.mode] ?? s.mode : 'Disconnect');
  const nextEv = $derived(snap?.upcoming[0]);
</script>

{#snippet badges(s: Schedule)}
  {#if activeFor(snap, s.id)}<span class="badge border-ok text-ok" title="The window is open now">Running now</span>{/if}
  {#if s.permanent}<span class="badge">permanent</span>{/if}
  {#if s.action === 'connect' && s.exclusive && s.mode !== 'localmonitor'}<span class="badge" title="Disconnects other non-permanent links first">disconnect others</span>{/if}
{/snippet}
{#snippet menu(s: Schedule)}
  {#if op}
    <Menu label="Actions for {title(s)}" btnClass="btn btn-sm btn-icon" align="right">
      {#snippet trigger()}<Dots size={15} />{/snippet}
      {#snippet items(close)}
        <button role="menuitem" class="btn btn-sm w-full justify-start border-transparent" onclick={() => { close(); editing = s; dlg = true; }}><Pencil size={14} />Edit</button>
        <button role="menuitem" class="btn btn-sm w-full justify-start border-transparent text-danger" onclick={() => { close(); del = s; delOpen = true; }}><Trash size={14} />Delete</button>
      {/snippet}
    </Menu>
  {/if}
{/snippet}
{#snippet result(s: Schedule)}
  {#if s.lastResult}<span class={resultClass(s.lastResult)} title={s.lastResult}>{s.lastResult}</span>{#if s.lastRun}<span class="text-xs text-muted"> · {ago(s)}</span>{/if}{:else}<span class="text-muted">—</span>{/if}
{/snippet}

<div class="space-y-3">
  <section class="rounded-md border border-line bg-surface p-2" aria-label="Schedules status">
    <div class="flex flex-wrap items-center gap-x-3 gap-y-1">
      <h1 class="text-sm font-semibold uppercase tracking-wide text-muted">Schedules</h1>
      {#if snap}
        {#if op}
          <span class="inline-flex items-center gap-1.5"><Toggle checked={snap.enabled} disabled={busy} label={snap.enabled ? 'Pause all schedules' : 'Resume schedules'} onchange={toggleAll} /><b class="text-sm font-semibold {snap.enabled ? 'text-ok' : 'text-warn'}">{snap.enabled ? 'Active' : 'Paused'}</b></span>
        {:else}
          <b class="text-sm font-semibold {snap.enabled ? 'text-ok' : 'text-warn'}">{snap.enabled ? 'Active' : 'Paused'}</b>
        {/if}
        {#if paused && snap.pausedReason}<span class="text-sm text-warn">Paused: {snap.pausedReason}{snap.pausedAt ? `, ${nodeParts(snap.pausedAt).time}` : ''}</span>{/if}
      {/if}
      {#if op}<button class="btn btn-sm btn-primary ml-auto" onclick={() => { editing = null; dlg = true; }} disabled={app.nodes.length === 0}><Plus size={13} />Add schedule</button>{/if}
    </div>
    <p class="mt-1 text-xs text-muted">Any manual connect or disconnect pauses all schedules. Only turning the switch back on resumes them.</p>
    {#if snap}
      <p class="mt-1 text-sm {paused ? 'text-muted' : ''}">
        {#if nextEv}Next: <b class="font-semibold">{evLabel(nextEv, multi)}</b>{nextEv.name ? ` (${nextEv.name})` : ''}, <span class="tabular-nums">{fmtAt(nextEv.at, snap.now)}</span>, <span class="tabular-nums text-muted">{countdown(nextEv.at, clock.now)}</span>
        {:else}<span class="text-muted">Nothing scheduled in the next days.</span>{/if}
      </p>
      <p class="mt-0.5 text-xs text-muted">Times are node time{snap.timezone ? ` (${snap.timezone})` : ''}{tzDiffers(snap.now) ? '; your browser is in a different time zone' : ''}. Node time now: <span class="tabular-nums">{nodeParts(snap.now).time}</span>.</p>
    {/if}
  </section>

  <section aria-label="Schedule list">
    {#if !snap}
      <p class="py-6 text-center text-sm text-muted" role="status">Loading…</p>
    {:else if list.length === 0}
      <p class="rounded-md border border-line bg-surface px-3 py-8 text-center text-sm text-muted">No schedules yet.{op ? ' Use Add schedule to connect a link at a set time, for example a weekly net.' : ''}</p>
    {:else}
      <div class="hidden rounded-md border border-line bg-surface sm:block">
        <table class="tbl" aria-label="Schedules">
          <thead><tr><th>Name</th><th>When</th><th>{multi ? 'Node → Target' : 'Target'}</th><th>Mode</th><th class="w-12">On</th><th>Last result</th>{#if op}<th class="w-10"><span class="sr-only">Actions</span></th>{/if}</tr></thead>
          <tbody>
            {#each list as s (s.id)}
              <tr class={s.enabled && !paused ? '' : 'opacity-60'}>
                <td class="max-w-48 font-medium"><div class="truncate" title={s.name}>{s.name || '—'}</div></td>
                <td class="whitespace-nowrap">{fmtWhen(s)}{#if crossesMidnight(s)}<span class="ml-1 text-xs text-muted">(+1 day)</span>{/if}</td>
                <td class="whitespace-nowrap font-mono">{multi ? `${s.node} → ` : ''}{s.target}</td>
                <td><div class="flex flex-wrap items-center gap-1"><span>{modeText(s)}</span>{@render badges(s)}</div></td>
                <td><Toggle checked={s.enabled} disabled={!op} label="{s.enabled ? 'Disable' : 'Enable'} {title(s)}" onchange={(v) => toggleOne(s, v)} /></td>
                <td class="max-w-56"><div class="truncate">{@render result(s)}</div></td>
                {#if op}<td>{@render menu(s)}</td>{/if}
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
      <ul class="space-y-2 sm:hidden">
        {#each list as s (s.id)}
          <li class="rounded-md border border-line bg-surface p-2 {s.enabled && !paused ? '' : 'opacity-70'}">
            <div class="flex items-center gap-2">
              <div class="min-w-0 flex-1 truncate font-medium">{title(s)}</div>
              <Toggle checked={s.enabled} disabled={!op} label="{s.enabled ? 'Disable' : 'Enable'} {title(s)}" onchange={(v) => toggleOne(s, v)} />
              {@render menu(s)}
            </div>
            <div class="mt-0.5 text-sm tabular-nums">{fmtWhen(s)}{#if crossesMidnight(s)}<span class="ml-1 text-xs text-muted">(+1 day)</span>{/if}</div>
            <div class="mt-0.5 flex flex-wrap items-center gap-1 text-sm"><span class="font-mono">{multi ? `${s.node} → ` : ''}{s.target}</span><span class="text-muted">· {modeText(s)}</span>{@render badges(s)}</div>
            <div class="mt-0.5 text-xs">{@render result(s)}</div>
          </li>
        {/each}
      </ul>
    {/if}
  </section>

  {#if snap && snap.upcoming.length}
    <section aria-label="Upcoming">
      <h2 class="mb-1 text-sm font-semibold uppercase tracking-wide text-muted">Upcoming{#if paused}<span class="ml-1 font-normal normal-case">(paused, nothing will run)</span>{/if}</h2>
      <ul class="divide-y divide-line rounded-md border border-line bg-surface text-sm {paused ? 'opacity-60' : ''}">
        {#each snap.upcoming as e (e.at + e.id + e.action)}
          <li class="grid grid-cols-[1fr_auto] gap-x-3 px-2 py-1 sm:grid-cols-[10rem_1fr_auto]">
            <span class="tabular-nums">{fmtAt(e.at, snap.now)}</span>
            <span class="tabular-nums text-muted sm:order-last">{countdown(e.at, clock.now)}</span>
            <span class="col-span-2 min-w-0 sm:col-span-1">{evLabel(e, multi)}{#if e.name || byId(e.id)?.name}<span class="ml-1 text-muted">({e.name || byId(e.id)?.name})</span>{/if}</span>
          </li>
        {/each}
      </ul>
    </section>
  {/if}
</div>

<ScheduleDialog bind:open={dlg} schedule={editing} />
<Confirm bind:open={delOpen} title="Delete schedule?" message="Delete {del ? title(del) : ''}? A running window is forgotten without touching the link." confirmLabel="Delete" onconfirm={() => del && remove(del)} />
