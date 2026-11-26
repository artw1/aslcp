<script lang="ts">
  // The settings of one mode: its fields as the server lists them, and the saved favorites count.
  import { api } from '../../lib/api';
  import { app, refreshModes } from '../../lib/store.svelte';
  import { toast, toastError } from '../../lib/toast.svelte';
  import { draftOf, missingField, savePayload, dirty, modeTitle } from '../../lib/modes';
  import ModeFields from '../ModeFields.svelte';

  let { mode }: { mode: string } = $props();
  const info = $derived(app.modes.find((m) => m.name === mode));
  let draft = $state<Record<string, string>>({});
  let ids = $state<{ dmrId?: number; nxdnId?: number } | undefined>(undefined);
  $effect(() => { api.station().then((s) => { ids = { dmrId: s.dmrId, nxdnId: s.nxdnId }; }).catch(() => { /* the node checks */ }); });
  let err = $state(''), busy = $state(false), seen = '';
  $effect(() => { if (info && seen !== mode) { seen = mode; draft = draftOf(info); err = ''; } });
  $effect(() => { if (!info) refreshModes().catch(toastError); });

  async function save(ev: Event) {
    ev.preventDefault();
    if (!info) return;
    const f = missingField(info, draft, ids);
    err = f ? `${f.label} is required.` : '';
    if (f) return;
    busy = true;
    try { await api.saveMode(mode, { values: savePayload(info.fields ?? [], draft) }); await refreshModes(); if (info) draft = draftOf(info); toast(`${modeTitle(mode, app.modes)} saved`); } catch (e) { toastError(e); } finally { busy = false; }
  }
  async function clear() {
    busy = true;
    try { await api.deleteMode(mode); await refreshModes(); if (info) draft = draftOf(info); toast('Settings cleared'); } catch (e) { toastError(e); } finally { busy = false; }
  }
</script>

<section class="card space-y-3 p-4" aria-label={modeTitle(mode, app.modes)}>
  <div class="flex items-center justify-between gap-2">
    <h2 class="text-base font-semibold">{modeTitle(mode, app.modes)}</h2>
    {#if info}<span class="badge">{info.active ? 'Active' : info.configured ? 'Set up' : 'Not set up'}</span>{/if}
  </div>
  {#if !info}
    <p class="text-sm text-muted">Loading…</p>
  {:else if !(info.fields ?? []).length}
    <p class="text-sm text-muted">This mode has no settings.</p>
  {:else}
    <form class="space-y-3" onsubmit={save}>
      <ModeFields mode={info} bind:draft idPrefix="mp" />
      {#if err}<p class="text-sm text-danger" role="alert">{err}</p>{/if}
      <div class="flex flex-wrap gap-2">
        <button class="btn btn-primary" type="submit" disabled={busy || !dirty(info, draft)}>{busy ? 'Saving…' : 'Save'}</button>
        {#if info.configured}<button class="btn btn-bad-outline" type="button" onclick={clear} disabled={busy || info.active}>Clear settings</button>{/if}
      </div>
    </form>
  {/if}
</section>
