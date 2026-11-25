<script lang="ts">
  import { api } from '../../lib/api';
  import { app } from '../../lib/store.svelte';
  import { toast, toastError } from '../../lib/toast.svelte';
  import type { User } from '../../lib/types';
  import Confirm from '../Confirm.svelte';
  import Dialog from '../Dialog.svelte';
  import Trash from '@lucide/svelte/icons/trash-2';

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
  $effect(() => { loadUsers(); });
</script>

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

<Confirm bind:open={delOpen} title="Delete user?" message="Delete {delUser?.username}? This cannot be undone." confirmLabel="Delete" onconfirm={doDelete} />
<Dialog bind:open={resetOpen} title="Reset password for {resetUser?.username}">
  <label class="label" for="rp">New password</label><input id="rp" class="input" type="password" bind:value={resetPw} autocomplete="new-password" />
  {#snippet footer()}<button class="btn" onclick={() => (resetOpen = false)}>Cancel</button><button class="btn btn-primary" onclick={doReset}>Reset</button>{/snippet}
</Dialog>
