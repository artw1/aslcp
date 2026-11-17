<script lang="ts">
  import { api } from '../../lib/api';
  import { toast, toastError } from '../../lib/toast.svelte';
  import Confirm from '../Confirm.svelte';
  import Power from '@lucide/svelte/icons/power';
  let { small = true, label = 'Restart Asterisk' }: { small?: boolean; label?: string } = $props();
  let open = $state(false), busy = $state(false);
  async function go() {
    busy = true;
    try { await api.restartAsteriskNow(); toast('Asterisk is restarting; links will drop and come back when it is up', 'info'); } catch (e) { toastError(e); } finally { busy = false; }
  }
</script>

<button type="button" class="btn {small ? 'btn-sm' : ''}" disabled={busy} onclick={() => (open = true)}><Power size={14} />{label}</button>
<Confirm bind:open title="Restart Asterisk?" confirmLabel="Restart" onconfirm={go}
  message="Asterisk restarts immediately. All radio links and active calls drop and come back once it is up again, which can take a minute." />
