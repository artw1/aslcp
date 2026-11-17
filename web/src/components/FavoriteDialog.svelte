<script lang="ts">
  import { api } from '../lib/api';
  import { app, refreshFavorites } from '../lib/store.svelte';
  import { toast, toastError } from '../lib/toast.svelte';
  import type { Favorite } from '../lib/types';
  import { isNodeNumber } from '../lib/util';
  import Dialog from './Dialog.svelte';
  import NodePicker from './NodePicker.svelte';

  let { open = $bindable(false), fav = null, initialNode = '' }: { open: boolean; fav?: Favorite | null; initialNode?: string } = $props();
  let node = $state(''), label = $state(''), group = $state(''), busy = $state(false), hint = $state('');
  const groups = $derived([...new Set(app.favorites.map((f) => f.group).filter(Boolean))]);
  let wasOpen = false;
  $effect(() => {
    if (open && !wasOpen) { node = fav?.node ?? initialNode; label = fav?.label ?? ''; group = fav?.group ?? ''; hint = ''; if (!fav && node) lookupHint(); }
    wasOpen = open;
  });
  async function lookupHint() {
    hint = '';
    if (!isNodeNumber(node)) return;
    try { const i = await api.lookup(node.trim()); hint = [i.callsign, i.description, i.location].filter(Boolean).join(' · '); } catch { hint = 'Not found in the node list'; }
  }
  async function save(e: Event) {
    e.preventDefault();
    if (!isNodeNumber(node)) { toast('Enter a valid node number', 'error'); return; }
    busy = true;
    try {
      const body = { node: node.trim(), label: label.trim() || undefined, group: group.trim() };
      if (fav) await api.updateFavorite(fav.id, { ...body, label: label.trim() }); else await api.addFavorite(body);
      await refreshFavorites();
      toast(fav ? 'Favorite updated' : 'Favorite added');
      open = false;
    } catch (x) { toastError(x); } finally { busy = false; }
  }
</script>
<Dialog bind:open title={fav ? 'Edit favorite' : 'Add favorite'}>
  <form id="favform" class="space-y-3" onsubmit={save}>
    <div>
      <label class="label" for="fnode">Node</label>
      <NodePicker id="fnode" bind:value={node} onpick={(i) => { if (!label) label = [i.callsign, i.description].filter(Boolean).join(' - '); hint = [i.callsign, i.description, i.location].filter(Boolean).join(' · '); }} />
      {#if hint}<p class="mt-1 text-xs text-muted">{hint}</p>{/if}
    </div>
    <div><label class="label" for="flabel">Label <span class="font-normal">(optional, defaults to node list name)</span></label><input id="flabel" class="input" bind:value={label} /></div>
    <div>
      <label class="label" for="fgroup">Group</label>
      <input id="fgroup" class="input" list="groups" bind:value={group} placeholder="No group" />
      <datalist id="groups">{#each groups as g}<option value={g}></option>{/each}</datalist>
    </div>
  </form>
  {#snippet footer()}
    <button class="btn" onclick={() => (open = false)}>Cancel</button>
    <button class="btn btn-primary" type="submit" form="favform" disabled={busy}>{busy ? 'Saving…' : 'Save'}</button>
  {/snippet}
</Dialog>
