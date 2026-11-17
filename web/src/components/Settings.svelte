<script lang="ts">
  import { api } from '../lib/api';
  import { app, isAdmin } from '../lib/store.svelte';
  import { toast, toastError } from '../lib/toast.svelte';
  import type { About, NodeListStatus, User } from '../lib/types';
  import { bugReportUrl, featureRequestUrl, releasesUrl } from '../lib/util';
  import Confirm from './Confirm.svelte';
  import Dialog from './Dialog.svelte';
  import PublicSettings from './PublicSettings.svelte';
  import Trash from '@lucide/svelte/icons/trash-2';
  import Key from '@lucide/svelte/icons/key-round';
  import Refresh from '@lucide/svelte/icons/refresh-cw';
  import Download from '@lucide/svelte/icons/download';

  // Password
  let cur = $state(''), nw = $state(''), nw2 = $state(''), pbusy = $state(false), perr = $state('');
  async function changePw(e: Event) {
    e.preventDefault(); perr = '';
    if (nw.length < 8) { perr = 'New password must be at least 8 characters.'; return; }
    if (nw !== nw2) { perr = 'Passwords do not match.'; return; }
    pbusy = true;
    try { await api.password(cur, nw); toast('Password changed'); cur = nw = nw2 = ''; } catch (x) { perr = x instanceof Error ? x.message : String(x); } finally { pbusy = false; }
  }

  // Users
  let users = $state<User[]>([]);
  let nu = $state(''), np = $state(''), nr = $state('operator'), ubusy = $state(false);
  let delUser = $state<User | null>(null), delOpen = $state(false);
  let resetUser = $state<User | null>(null), resetOpen = $state(false), resetPw = $state('');
  async function loadUsers() { try { users = await api.users(); } catch (e) { toastError(e); } }
  async function addUser(e: Event) {
    e.preventDefault(); ubusy = true;
    try { await api.addUser(nu.trim(), np, nr); toast(`User ${nu} created`); nu = np = ''; await loadUsers(); } catch (x) { toastError(x); } finally { ubusy = false; }
  }
  async function setRole(u: User, role: string) {
    try { await api.updateUser(u.id, { role }); toast(`${u.username} is now ${role}`); } catch (x) { toastError(x); } finally { await loadUsers(); }
  }
  async function doReset() {
    if (!resetUser) return;
    if (resetPw.length < 8) { toast('Password must be at least 8 characters', 'error'); return; }
    try { await api.updateUser(resetUser.id, { password: resetPw }); toast(`Password reset for ${resetUser.username}`); resetOpen = false; resetPw = ''; } catch (x) { toastError(x); }
  }
  async function doDelete() { if (!delUser) return; try { await api.deleteUser(delUser.id); toast('User deleted'); } catch (x) { toastError(x); } await loadUsers(); }


  // Node list
  let nl = $state<NodeListStatus | null>(null), rbusy = $state(false);
  async function loadNl() { try { nl = await api.nodelist(); } catch (e) { toastError(e); } }
  async function refresh() {
    rbusy = true;
    try { await api.refreshNodelist(); toast('Node list refresh started', 'info'); setTimeout(loadNl, 3000); setTimeout(loadNl, 10000); } catch (e) { toastError(e); } finally { rbusy = false; }
  }

  // About
  let about = $state<About | null>(null);
  async function loadAbout() { try { about = await api.about(); } catch (e) { toastError(e); } }
  const platformText = $derived(about ? [about.platform.model, `${about.platform.os}/${about.platform.arch}`].filter(Boolean).join(', ') : '');
  const reportUrl = $derived(bugReportUrl({ version: about?.version || app.version, asl: about?.asterisk, hardware: about?.platform.hardware, os: about?.platform.distro }));
  $effect(() => { loadNl(); loadAbout(); if (isAdmin()) loadUsers(); });
</script>
<div class="space-y-4">
  <h1 class="text-xl font-bold tracking-tight">Settings</h1>
  <div class="grid grid-cols-1 items-start gap-4 lg:grid-cols-2">
    <section class="card space-y-3 p-4" aria-label="About">
      <h2 class="text-base font-semibold">About</h2>
      <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
        <dt class="text-muted">ASLCP</dt><dd class="font-mono">{about?.version || app.version || '…'}</dd>
        <dt class="text-muted">Asterisk</dt><dd class="font-mono break-all">{about ? about.asterisk || 'n/a' : '…'}</dd>
        <dt class="text-muted">Platform</dt><dd class="break-words">{about ? platformText : '…'}</dd>
        <dt class="text-muted">OS</dt><dd class="break-words">{about ? about.platform.distro || 'n/a' : '…'}</dd>
      </dl>
      <div class="flex flex-wrap gap-2">
        <a class="btn" href={reportUrl} target="_blank" rel="noopener noreferrer">Report a bug</a>
        <a class="btn" href={featureRequestUrl} target="_blank" rel="noopener noreferrer">Request a feature</a>
        <a class="btn" href={releasesUrl} target="_blank" rel="noopener noreferrer">Release notes</a>
        {#if isAdmin()}<a class="btn" href="/api/diagnostics" download><Download size={14} />Download diagnostics</a>{/if}
      </div>
      {#if isAdmin()}<p class="text-xs text-muted">The diagnostics file has IP addresses, passwords, email addresses and other stations' callsigns removed. Check it before you attach it to a bug report.</p>{/if}
    </section>

    <section class="card space-y-3 p-4" aria-label="Node list">
      <h2 class="text-base font-semibold">Node list</h2>
      {#if nl}
        <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
          <dt class="text-muted">Nodes</dt><dd class="font-mono">{nl.count.toLocaleString()}</dd>
          <dt class="text-muted">Updated</dt><dd>{nl.updated ? new Date(nl.updated).toLocaleString() : 'never'}</dd>
          <dt class="text-muted">Source</dt><dd class="break-all">{nl.source}</dd>
          <dt class="text-muted">Status</dt><dd class={nl.error ? 'text-danger' : 'text-ok'}>{nl.error ?? 'OK'}</dd>
        </dl>
      {:else}<p class="text-sm text-muted">Loading…</p>{/if}
      {#if isAdmin()}<button class="btn" disabled={rbusy} onclick={refresh}><Refresh size={14} class={rbusy ? 'animate-spin' : ''} />Refresh now</button>{/if}
    </section>
  </div>
  {#if isAdmin()}<PublicSettings />{/if}
  <div class="grid grid-cols-1 items-start gap-4 lg:grid-cols-2">
    <form class="card space-y-3 p-4" onsubmit={changePw}>
      <h2 class="flex items-center gap-2 text-base font-semibold"><Key size={16} />Change password</h2>
      <div><label class="label" for="cur">Current password</label><input id="cur" class="input" type="password" autocomplete="current-password" bind:value={cur} required /></div>
      <div><label class="label" for="nw">New password</label><input id="nw" class="input" type="password" autocomplete="new-password" bind:value={nw} required /></div>
      <div><label class="label" for="nw2">Confirm new password</label><input id="nw2" class="input" type="password" autocomplete="new-password" bind:value={nw2} required /></div>
      {#if perr}<p class="text-sm text-danger" role="alert">{perr}</p>{/if}
      <button class="btn btn-primary" disabled={pbusy}>Change password</button>
    </form>


  {#if isAdmin()}
    <section class="card" aria-label="Users">
      <h2 class="border-b border-line px-4 py-3 text-base font-semibold">Users</h2>
      <ul class="divide-y divide-line">
        {#each users as u (u.id)}
          <li class="flex flex-wrap items-center gap-x-3 gap-y-2 px-4 py-2.5">
            <span class="min-w-0 flex-1 basis-32 truncate font-medium">{u.username}{#if u.id === app.user?.id}<span class="badge ml-2">you</span>{/if}</span>
            <select class="input w-auto" aria-label="Role for {u.username}" value={u.role} onchange={(e) => setRole(u, e.currentTarget.value)}>
              <option value="admin">admin</option><option value="operator">operator</option><option value="viewer">viewer</option>
            </select>
            <button class="btn btn-sm" onclick={() => { resetUser = u; resetPw = ''; resetOpen = true; }}>Reset password</button>
            <button class="btn btn-sm btn-icon btn-danger" aria-label="Delete {u.username}" disabled={u.id === app.user?.id} onclick={() => { delUser = u; delOpen = true; }}><Trash size={14} /></button>
          </li>
        {/each}
      </ul>
      <form class="flex flex-wrap items-end gap-2 border-t border-line bg-surface-2/40 p-4" onsubmit={addUser}>
        <div class="min-w-32 flex-1"><label class="label" for="nu">Username</label><input id="nu" class="input" bind:value={nu} autocomplete="off" required /></div>
        <div class="min-w-32 flex-1"><label class="label" for="np">Password</label><input id="np" class="input" type="password" bind:value={np} autocomplete="new-password" required /></div>
        <div><label class="label" for="nr">Role</label><select id="nr" class="input" bind:value={nr}><option value="admin">admin</option><option value="operator">operator</option><option value="viewer">viewer</option></select></div>
        <button class="btn btn-primary" disabled={ubusy}>Add user</button>
      </form>
    </section>
  {/if}
  </div>
</div>

<Confirm bind:open={delOpen} title="Delete user?" message="Delete {delUser?.username}? This cannot be undone." confirmLabel="Delete" onconfirm={doDelete} />
<Dialog bind:open={resetOpen} title="Reset password for {resetUser?.username}">
  <label class="label" for="rp">New password</label><input id="rp" class="input" type="password" bind:value={resetPw} autocomplete="new-password" />
  {#snippet footer()}<button class="btn" onclick={() => (resetOpen = false)}>Cancel</button><button class="btn btn-primary" onclick={doReset}>Reset</button>{/snippet}
</Dialog>
