<script lang="ts">
  import { ApiError, api } from '../lib/api';
  import { app, refreshSchedules } from '../lib/store.svelte';
  import { exclusive, exclusiveMonitor } from '../lib/settings.svelte';
  import { toast, toastError } from '../lib/toast.svelte';
  import { DAY_NAMES, DAY_ORDER, conflictMessage, emptyInput, findConflicts, validateSchedule } from '../lib/schedules';
  import type { ConnectMode, Schedule, ScheduleInput } from '../lib/types';
  import Dialog from './Dialog.svelte';
  import NodePicker from './NodePicker.svelte';

  let { open = $bindable(false), schedule = null }: { open: boolean; schedule?: Schedule | null } = $props();
  let f = $state<ScheduleInput>(emptyInput('', true));
  let repeat = $state<'weekly' | 'once'>('weekly');
  let busy = $state(false), error = $state(''), touchedExcl = $state(false);
  const locals = $derived(app.nodes.map((n) => n.node));
  const connect = $derived(f.action === 'connect');
  const defExcl = (m: ConnectMode) => (m === 'transceive' ? exclusive.value : m === 'monitor' ? exclusiveMonitor.value : false);
  let wasOpen = false;
  $effect(() => {
    if (open && !wasOpen) {
      error = ''; busy = false;
      if (schedule) {
        const { id: _i, lastRun: _l, lastResult: _r, ...rest } = schedule;
        f = { ...rest, days: [...rest.days], mode: rest.mode || 'transceive' };
        repeat = schedule.date ? 'once' : 'weekly'; touchedExcl = true;
      } else {
        f = emptyInput(app.selectedNode || locals[0] || '', defExcl('transceive'));
        f.days = [2]; repeat = 'weekly'; touchedExcl = false;
      }
    }
    wasOpen = open;
  });
  function setMode(m: ConnectMode) { f.mode = m; if (!touchedExcl) f.exclusive = defExcl(m); }
  function toggleDay(d: number) { f.days = f.days.includes(d) ? f.days.filter((x) => x !== d) : [...f.days, d].sort((a, b) => a - b); }
  function body(): ScheduleInput {
    const c = f.action === 'connect';
    return {
      name: f.name.trim(), enabled: f.enabled, node: f.node, target: f.target.trim(), action: f.action,
      mode: c ? f.mode : 'transceive', permanent: f.permanent, exclusive: c && f.mode !== 'localmonitor' ? f.exclusive : false,
      days: repeat === 'weekly' ? [...f.days].sort((a, b) => a - b) : [], date: repeat === 'once' ? f.date : '',
      start: f.start, end: c ? f.end : '',
    };
  }
  /** Overlap check against the saved schedules (the server checks again and answers 409). */
  const clash = $derived.by(() => {
    if (!open) return '';
    const c = findConflicts(body(), app.schedules?.schedules ?? [], schedule?.id);
    return c.length ? conflictMessage(f.node, c) : '';
  });
  async function save(e: Event) {
    e.preventDefault();
    const b = body();
    error = validateSchedule(b, locals);
    if (error || clash) return;
    busy = true;
    try {
      if (schedule) await api.updateSchedule(schedule.id, b); else await api.addSchedule(b);
      await refreshSchedules();
      toast(schedule ? 'Schedule updated' : 'Schedule added');
      open = false;
    } catch (x) {
      if (x instanceof ApiError && x.code === 'schedule_conflict') { error = `This schedule ${x.message}.`; refreshSchedules().catch(() => {}); }
      else { error = x instanceof Error ? x.message : String(x); toastError(x); }
    } finally { busy = false; }
  }
  const chk = 'size-4 accent-[var(--accent)]';
</script>
<Dialog bind:open title={schedule ? 'Edit schedule' : 'Add schedule'} wide>
  <form id="schedform" class="space-y-3" onsubmit={save}>
    <div class="grid gap-3 sm:grid-cols-2">
      <div class="sm:col-span-2"><label class="label" for="sname">Name <span class="font-normal">(optional, for example the net's name)</span></label><input id="sname" class="input" maxlength="60" bind:value={f.name} placeholder="Tuesday net" /></div>
      {#if app.nodes.length > 1}
        <div><label class="label" for="snode">Local node</label>
          <select id="snode" class="input" bind:value={f.node}>{#each app.nodes as n (n.node)}<option value={n.node}>{n.node}{n.info?.callsign ? ` · ${n.info.callsign}` : ''}</option>{/each}</select></div>
      {/if}
      <div><label class="label" for="saction">Action</label>
        <select id="saction" class="input" bind:value={f.action}><option value="connect">Connect</option><option value="disconnect">Disconnect</option></select></div>
      <div class={app.nodes.length > 1 ? 'sm:col-span-2' : ''}><label class="label" for="starget">Target node</label><NodePicker id="starget" bind:value={f.target} /></div>
      {#if connect}
        <div><label class="label" for="smode">Mode</label>
          <select id="smode" class="input" value={f.mode} onchange={(e) => setMode(e.currentTarget.value as ConnectMode)}><option value="transceive">Transceive</option><option value="monitor">Monitor</option><option value="localmonitor">Local monitor</option></select></div>
      {/if}
    </div>
    <div class="flex flex-wrap gap-x-4 gap-y-1.5 text-sm">
      <label class="inline-flex cursor-pointer items-center gap-1.5"><input type="checkbox" class={chk} bind:checked={f.permanent} />{connect ? 'Permanent link' : 'Permanent disconnect'}</label>
      {#if connect && f.mode !== 'localmonitor'}
        <label class="inline-flex cursor-pointer items-center gap-1.5" title="Drops the other non-permanent links first so two networks are not bridged."><input type="checkbox" class={chk} checked={f.exclusive} onchange={(e) => { f.exclusive = e.currentTarget.checked; touchedExcl = true; }} />Disconnect others first</label>
      {/if}
    </div>
    <fieldset class="space-y-2 rounded border border-line p-2">
      <legend class="px-1 text-xs font-medium text-muted">Repeat</legend>
      <div class="flex gap-1.5" role="group" aria-label="Repeat">
        <button type="button" class="btn btn-sm {repeat === 'weekly' ? 'btn-primary' : ''}" aria-pressed={repeat === 'weekly'} onclick={() => (repeat = 'weekly')}>Weekly</button>
        <button type="button" class="btn btn-sm {repeat === 'once' ? 'btn-primary' : ''}" aria-pressed={repeat === 'once'} onclick={() => (repeat = 'once')}>Once</button>
      </div>
      {#if repeat === 'weekly'}
        <div class="flex flex-wrap gap-1" role="group" aria-label="Days of the week">
          {#each DAY_ORDER as d}
            <button type="button" class="btn btn-sm min-w-10 {f.days.includes(d) ? 'btn-primary' : ''}" aria-pressed={f.days.includes(d)} onclick={() => toggleDay(d)}>{DAY_NAMES[d]}</button>
          {/each}
        </div>
      {:else}
        <div class="max-w-48"><label class="label" for="sdate">Date</label><input id="sdate" type="date" class="input" bind:value={f.date} /></div>
      {/if}
      <div class="flex flex-wrap gap-3">
        <div><label class="label" for="sstart">{connect ? 'Start' : 'Time'}</label><input id="sstart" type="time" class="input w-32" bind:value={f.start} required /></div>
        {#if connect}
          <div><label class="label" for="send">End <span class="font-normal">(optional)</span></label>
            <div class="flex items-center gap-1"><input id="send" type="time" class="input w-32" bind:value={f.end} />{#if f.end}<button type="button" class="btn btn-sm" onclick={() => (f.end = '')}>Clear</button>{/if}</div></div>
        {/if}
      </div>
      <p class="text-xs text-muted">Times are node time{app.schedules?.timezone ? ` (${app.schedules.timezone})` : ''}.{#if connect}{' '}An end time disconnects the link then{f.end && f.end < f.start ? '; this window ends the next day' : ''}.{/if}</p>
    </fieldset>
    <label class="inline-flex cursor-pointer items-center gap-1.5 text-sm"><input type="checkbox" class={chk} bind:checked={f.enabled} />Enabled</label>
    {#if clash}<p class="text-sm text-warn" role="alert">This schedule {clash}. Two schedules where one disconnects others first must not overlap: change the time, the day or the "disconnect others" option.</p>{/if}
    {#if error}<p class="text-sm text-danger" role="alert">{error}</p>{/if}
  </form>
  {#snippet footer()}
    <button class="btn" onclick={() => (open = false)}>Cancel</button>
    <button class="btn btn-primary" type="submit" form="schedform" disabled={busy || !!clash}>{busy ? 'Saving…' : 'Save'}</button>
  {/snippet}
</Dialog>
