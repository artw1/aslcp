<script lang="ts">
  import { app, canOperate } from '../lib/store.svelte';
  import { connectTo, disconnectFrom, isPending } from '../lib/actions.svelte';
  import { form } from '../lib/form.svelte';
  import type { ConnectMode, Favorite } from '../lib/types';
  import { isNodeNumber } from '../lib/util';
  import { exclusive, exclusiveMonitor, setExclusive, setExclusiveMonitor } from '../lib/settings.svelte';
  import { api } from '../lib/api';
  import { refreshFavorites } from '../lib/store.svelte';
  import { toastError } from '../lib/toast.svelte';
  import NodePicker from './NodePicker.svelte';
  import Confirm from './Confirm.svelte';
  import FavoriteDialog from './FavoriteDialog.svelte';
  import Plug from '@lucide/svelte/icons/plug';
  import Unplug from '@lucide/svelte/icons/unplug';
  import Ear from '@lucide/svelte/icons/ear';
  import Headphones from '@lucide/svelte/icons/headphones';
  import Star from '@lucide/svelte/icons/star';
  import Pencil from '@lucide/svelte/icons/pencil';
  import Trash from '@lucide/svelte/icons/trash-2';
  import Loader from '@lucide/svelte/icons/loader-circle';

  let { target = $bindable(''), idp = 'cb', showFavorites = true, onDone }: {
    target?: string; idp?: string; showFavorites?: boolean; onDone?: () => void;
  } = $props();
  const local = $derived(app.selectedNode);
  const op = $derived(canOperate());
  const t = $derived(target.trim());
  const valid = $derived(isNodeNumber(t));
  const busy = $derived(!!local && isPending(local, t));
  let last = $state<ConnectMode>('transceive');
  let favOpen = $state(false), editFav = $state<Favorite | null>(null);
  let delOpen = $state(false);
  const fav = $derived(app.favorites.find((f) => f.node === t));

  async function go(mode: ConnectMode) {
    if (!valid || !local) return;
    last = mode;
    // Local monitor never disconnects others; Connect and Monitor have their own checkbox.
    const excl = mode === 'transceive' ? exclusive.value : mode === 'monitor' ? exclusiveMonitor.value : false;
    if (await connectTo(local, t, mode, form.permanent, excl)) onDone?.();
  }
  async function doDisconnect() { if (t && local && await disconnectFrom(local, t, form.permanent)) onDone?.(); }
  async function removeFav() {
    if (!fav) return;
    try { await api.deleteFavorite(fav.id); await refreshFavorites(); } catch (e) { toastError(e); }
  }
  const cantConnect = $derived(!op || !valid || busy || !local);
</script>

<div>
  <div class="flex flex-wrap items-center gap-x-2 gap-y-1.5">
    <div class="flex items-center gap-2 max-sm:w-full">
      <label class="text-sm text-muted" for="{idp}-target">Node#</label>
      <div class="w-36 max-sm:flex-1"><NodePicker id="{idp}-target" bind:value={target} onenter={() => go('transceive')} placeholder="Node or search" /></div>
    </div>
    <div class="flex gap-1.5 max-sm:w-full [&>button]:max-sm:flex-1 [&>button]:max-sm:px-1">
      <button class="btn btn-primary" disabled={cantConnect} onclick={() => go('transceive')}>{#if busy && last === 'transceive'}<Loader size={14} class="animate-spin" />{:else}<Plug size={14} class="max-sm:hidden" />{/if}Connect</button>
      <button class="btn" disabled={!op || !t || !local} onclick={doDisconnect} title="Disconnect {t || 'the node in the box'}{form.permanent ? ' form.permanently' : ''}"><Unplug size={14} class="max-sm:hidden" />Disconnect</button>
      <button class="btn" disabled={cantConnect} onclick={() => go('monitor')}>{#if busy && last === 'monitor'}<Loader size={14} class="animate-spin" />{:else}<Ear size={14} class="max-sm:hidden" />{/if}Monitor</button>
      <button class="btn" disabled={cantConnect} onclick={() => go('localmonitor')}>{#if busy && last === 'localmonitor'}<Loader size={14} class="animate-spin" />{:else}<Headphones size={14} class="max-sm:hidden" />{/if}Local Mon</button>
    </div>
    {#if showFavorites}
    <span class="mx-1 hidden h-5 w-px bg-line sm:block" aria-hidden="true"></span>
    <div class="flex gap-1.5 max-sm:w-full [&>button]:max-sm:flex-1 [&>button]:max-sm:px-1">
      <button class="btn" disabled={!op || !valid || !!fav} onclick={() => { editFav = null; favOpen = true; }} title={fav ? 'Already a favorite' : 'Add the node in the box to favorites'}><Star size={14} class="max-sm:hidden" />Add Favorite</button>
      <button class="btn" disabled={!op || !fav} onclick={() => { editFav = fav ?? null; favOpen = true; }} title={fav ? 'Edit this favorite' : 'Type or pick a favorite node first'}><Pencil size={14} class="max-sm:hidden" />Edit Favorite</button>
      <button class="btn btn-danger" disabled={!op || !fav} onclick={() => (delOpen = true)} title={fav ? 'Delete this favorite' : 'Type or pick a favorite node first'}><Trash size={14} class="max-sm:hidden" />Delete Favorite</button>
    </div>
    {/if}
  </div>
  <div class="mt-1.5 flex flex-wrap items-center gap-x-4 gap-y-1 text-sm">
    <label class="inline-flex cursor-pointer items-center gap-1.5"><input type="checkbox" class="size-4 accent-[var(--accent)]" bind:checked={form.permanent} disabled={!op} />Permanent</label>
    <label class="inline-flex cursor-pointer items-center gap-1.5" title="Disconnects the current links first so two networks are not bridged. Permanent links stay."><input type="checkbox" class="size-4 accent-[var(--accent)]" checked={exclusive.value} onchange={(e) => setExclusive(e.currentTarget.checked)} disabled={!op} />Disconnect before Connect</label>
    <label class="inline-flex cursor-pointer items-center gap-1.5" title="Same, for Monitor. Local Mon never disconnects other links."><input type="checkbox" class="size-4 accent-[var(--accent)]" checked={exclusiveMonitor.value} onchange={(e) => setExclusiveMonitor(e.currentTarget.checked)} disabled={!op} />Disconnect before Monitor</label>
    {#if app.nodes.length > 1}
    <label class="inline-flex items-center gap-1.5" for="{idp}-local-node">Local node
      <select id="{idp}-local-node" class="input !w-auto font-mono" bind:value={app.selectedNode}>{#each app.nodes as n}<option value={n.node}>{n.node}</option>{/each}</select>
    </label>
    {/if}
    {#if !op}<span class="text-xs text-muted">Read-only account</span>{/if}
  </div>
</div>

<FavoriteDialog bind:open={favOpen} fav={editFav} initialNode={t} />
<Confirm bind:open={delOpen} title="Delete favorite?" message="Remove {fav?.label || fav?.node} from your favorites?" confirmLabel="Delete" onconfirm={removeFav} />
