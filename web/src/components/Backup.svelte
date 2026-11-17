<script lang="ts">
  import { api } from '../lib/api';
  import { toast, toastError } from '../lib/toast.svelte';
  import type { BackupInfo } from '../lib/types';
  import RestoreFlow from './RestoreFlow.svelte';
  import Download from '@lucide/svelte/icons/download';

  // Backup
  let binfo = $state<BackupInfo | null>(null), bsel = $state({ history: true, allmon3: true, sounds: true }), bbusy = $state(false), restoring = $state(false);
  const sizeText = (n: number) => (n >= 1048576 ? `${(n / 1048576).toFixed(1)} MB` : `${Math.max(1, Math.round(n / 1024))} KB`);
  const PART_LABEL = { history: 'Config history', allmon3: 'Allmon3 configuration', sounds: 'Custom sounds' } as const;
  async function loadBackup() { try { binfo = await api.backupInfo(); } catch (e) { toastError(e); } }
  async function downloadBackup() {
    bbusy = true;
    try {
      const on = (id: 'history' | 'allmon3' | 'sounds') => !!binfo?.parts.find((p) => p.id === id)?.present && bsel[id];
      const name = await api.backup({ history: on('history'), allmon3: on('allmon3'), sounds: on('sounds') });
      toast(`Saved ${name}. Store it like a password.`, 'info');
    } catch (e) { toastError(e); } finally { bbusy = false; }
  }
  $effect(() => { loadBackup(); });
</script>
<div class="space-y-4">
  <h1 class="text-xl font-bold tracking-tight">Backup</h1>
    <section class="card space-y-3 p-4" aria-label="Backup">
      <h2 class="text-base font-semibold">Backup</h2>
      <p class="text-sm">Download one file with this node's whole configuration: the same Asterisk setup, ASLCP users, favorites and settings. Restore it onto a freshly installed node to get the same node back.</p>
      <p class="text-sm text-muted">Always included: /etc/asterisk, AstDB, the ASLCP database and config{#if binfo?.sa818}, and the SA818 radio module settings{/if}.</p>
      {#if binfo}
        {@const present = binfo.parts.filter((p) => p.present)}
        {#if present.length}
          <fieldset class="space-y-1.5">
            <legend class="label">Optional parts</legend>
            {#each present as p (p.id)}
              <label class="flex cursor-pointer items-center gap-2 text-sm"><input type="checkbox" class="size-4 accent-[var(--accent)]" bind:checked={bsel[p.id]} disabled={bbusy} />{PART_LABEL[p.id]}<span class="text-xs text-muted">({sizeText(p.size)})</span></label>
            {/each}
          </fieldset>
        {/if}
      {/if}
      <div class="rounded border border-warn bg-warn/10 p-3 text-sm" role="note">
        <strong class="text-warn">The backup file is NOT encrypted.</strong> It contains every secret of this node: node passwords, IAX and AMI secrets, the EchoLink password and the ASLCP password hashes. Store it like a password and never share it.
      </div>
      <div class="flex flex-wrap gap-2">
        <button class="btn btn-primary" disabled={bbusy || !binfo} onclick={downloadBackup}><Download size={14} class={bbusy ? 'animate-pulse' : ''} />{bbusy ? 'Preparing…' : 'Download backup'}</button>
      </div>
      <div class="border-t border-line pt-3">
        <h3 class="text-sm font-semibold">Restore from a backup</h3>
        {#if restoring}
          <div class="mt-2"><RestoreFlow mode="admin" oncancel={() => (restoring = false)} /></div>
        {:else}
          <p class="mt-1 text-sm text-muted">Replace this node's configuration with a backup. You will see a preview first.</p>
          <button class="btn mt-2" onclick={() => (restoring = true)}>Restore from a backup…</button>
        {/if}
      </div>
    </section>
</div>
