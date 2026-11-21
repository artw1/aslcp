<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../lib/api';
  import { toastError } from '../lib/toast.svelte';
  import type { UserListStatus } from '../lib/types';
  import Toggle from './Toggle.svelte';
  import Download from '@lucide/svelte/icons/download';

  let st = $state<UserListStatus | null>(null), err = $state(''), busy = $state(false);
  const fmt = (iso?: string) => (iso ? new Date(iso).toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' }) : 'never');

  async function load() { try { st = await api.userList(); err = ''; } catch (e) { err = e instanceof Error ? e.message : String(e); } }
  onMount(load);
  // While a download runs, poll until it ends.
  $effect(() => {
    if (!st?.busy) return;
    const t = setInterval(load, 1500);
    return () => clearInterval(t);
  });

  async function toggle(on: boolean) {
    busy = true;
    try { st = await api.setUserList(on); } catch (e) { toastError(e); } finally { busy = false; }
  }
  async function download() {
    busy = true;
    try { st = await api.downloadUserList(); } catch (e) { toastError(e); } finally { busy = false; }
  }
</script>

<section class="card space-y-3 p-4" aria-label="radioid.net user list">
  <div class="flex items-center justify-between gap-2">
    <h2 class="text-base font-semibold">radioid.net user list</h2>
    {#if st}<Toggle checked={st.enabled} disabled={busy} label={st.enabled ? 'Turn the user list off' : 'Turn the user list on'} onchange={toggle} />{/if}
  </div>
  {#if !st}
    <p class="text-sm text-muted">{err || 'Loading…'}</p>
  {:else}
    <p class="text-xs text-muted">Looks up the name and place of a talker for the Now Calling strip. The list is downloaded from radioid.net and refreshed weekly.</p>
    <dl class="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-sm">
      <dt class="text-muted">Status</dt><dd>{st.enabled ? (st.busy ? 'Downloading…' : 'On') : 'Off'}</dd>
      <dt class="text-muted">Entries</dt><dd class="tabular-nums">{st.entries.toLocaleString()}</dd>
      <dt class="text-muted">Last updated</dt><dd>{fmt(st.updated)}</dd>
      {#if st.error}<dt class="text-muted">Last error</dt><dd class="break-words text-danger">{st.error}</dd>{/if}
    </dl>
    <button class="btn" disabled={busy || st.busy || !st.enabled} onclick={download}><Download size={15} />Download now</button>
  {/if}
</section>
