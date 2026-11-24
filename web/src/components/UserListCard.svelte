<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../lib/api';
  import { toastError } from '../lib/toast.svelte';
  import type { UserListStatus } from '../lib/types';
  import Toggle from './Toggle.svelte';
  import Download from '@lucide/svelte/icons/download';

  // `compact`: one row for the Engine card (switch, label, summary, Download now, hint). Otherwise a card of its own.
  let { compact = false }: { compact?: boolean } = $props();
  let st = $state<UserListStatus | null>(null), err = $state(''), busy = $state(false);
  const fmt = (iso?: string) => (iso ? new Date(iso).toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' }) : 'never');
  const fmtShort = (iso: string) => new Date(iso).toLocaleString([], { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' });
  const summary = $derived(!st ? '' : !st.enabled ? 'Off' : st.busy ? 'Downloading…' : st.updated ? `${st.entries.toLocaleString()} ${st.entries === 1 ? 'entry' : 'entries'} · updated ${fmtShort(st.updated)}` : 'Not downloaded yet');
  const HINT = 'Looks up the name and place of a talker for the Now Calling strip. Downloaded from radioid.net and checked daily.';

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

{#if compact}
  <div class="space-y-1 border-t border-line pt-3" role="group" aria-label="radioid.net user list" data-testid="dv-userlist">
    <div class="flex flex-wrap items-center gap-x-4 gap-y-1">
      {#if st}<span class="inline-flex items-center gap-2 text-sm"><Toggle tap checked={st.enabled} disabled={busy} label={st.enabled ? 'Turn the user list off' : 'Turn the user list on'} onchange={toggle} />radioid.net user list</span>
      {:else}<span class="text-sm">radioid.net user list</span>{/if}
      {#if st}
        <span class="text-xs text-muted" data-testid="dv-userlist-summary">{summary}</span>
        <button class="btn btn-sm sm:ml-auto" disabled={busy || st.busy || !st.enabled} onclick={download}><Download size={14} />Download now</button>
      {:else}<span class="text-xs text-muted">{err || 'Loading…'}</span>{/if}
    </div>
    {#if st?.error}<p class="break-words text-xs text-danger" role="alert">Last error: {st.error}</p>{/if}
    <p class="text-xs text-muted">{HINT}</p>
  </div>
{:else}
<section class="card space-y-3 p-4" aria-label="radioid.net user list">
  <div class="flex items-center justify-between gap-2">
    <h2 class="text-base font-semibold">radioid.net user list</h2>
    {#if st}<Toggle tap checked={st.enabled} disabled={busy} label={st.enabled ? 'Turn the user list off' : 'Turn the user list on'} onchange={toggle} />{/if}
  </div>
  {#if !st}
    <p class="text-sm text-muted">{err || 'Loading…'}</p>
  {:else}
    <p class="text-xs text-muted">{HINT}</p>
    <dl class="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-sm">
      <dt class="text-muted">Status</dt><dd>{st.enabled ? (st.busy ? 'Downloading…' : 'On') : 'Off'}</dd>
      <dt class="text-muted">Entries</dt><dd class="tabular-nums">{st.entries.toLocaleString()}</dd>
      <dt class="text-muted">Last updated</dt><dd>{fmt(st.updated)}</dd>
      {#if st.error}<dt class="text-muted">Last error</dt><dd class="break-words text-danger">{st.error}</dd>{/if}
    </dl>
    <button class="btn min-h-8" disabled={busy || st.busy || !st.enabled} onclick={download}><Download size={15} />Download now</button>
  {/if}
</section>
{/if}
