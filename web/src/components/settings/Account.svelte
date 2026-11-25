<script lang="ts">
  import { api } from '../../lib/api';
  import { toast } from '../../lib/toast.svelte';
  import Key from '@lucide/svelte/icons/key-round';

  let cur = $state(''), nw = $state(''), nw2 = $state(''), pbusy = $state(false), perr = $state('');
  async function changePw(e: Event) {
    e.preventDefault(); perr = '';
    if (nw.length < 8) { perr = 'New password must be at least 8 characters.'; return; }
    if (nw !== nw2) { perr = 'Passwords do not match.'; return; }
    pbusy = true;
    try { await api.password(cur, nw); toast('Password changed'); cur = nw = nw2 = ''; } catch (x) { perr = x instanceof Error ? x.message : String(x); } finally { pbusy = false; }
  }
</script>

<form class="card max-w-md space-y-3 p-4" onsubmit={changePw}>
  <h2 class="flex items-center gap-2 text-base font-semibold"><Key size={16} />Change password</h2>
  <div><label class="label" for="cur">Current password</label><input id="cur" class="input" type="password" autocomplete="current-password" bind:value={cur} required /></div>
  <div><label class="label" for="nw">New password</label><input id="nw" class="input" type="password" autocomplete="new-password" bind:value={nw} required /></div>
  <div><label class="label" for="nw2">Confirm new password</label><input id="nw2" class="input" type="password" autocomplete="new-password" bind:value={nw2} required /></div>
  {#if perr}<p class="text-sm text-danger" role="alert">{perr}</p>{/if}
  <button class="btn btn-primary" disabled={pbusy}>Change password</button>
</form>
