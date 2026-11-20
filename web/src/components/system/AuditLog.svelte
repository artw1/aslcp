<script lang="ts">
  import { api } from '../../lib/api';
  import { auditSummary } from '../../lib/sysnav';
  import type { AuditEntry } from '../../lib/types';

  let rows = $state<AuditEntry[]>([]), next = $state<number | null>(null), err = $state(''), busy = $state(false), loaded = $state(false);
  let user = $state(''), action = $state('');

  async function load(more: boolean) {
    busy = true;
    try {
      const p = await api.audit({ before: more && next ? next : undefined, user: user.trim(), action: action.trim() });
      rows = more ? [...rows, ...p.entries] : p.entries;
      next = p.next; err = ''; loaded = true;
    } catch (e) { err = e instanceof Error ? e.message : String(e); } finally { busy = false; }
  }
  $effect(() => { load(false); });
  const when = (t: string) => new Date(t).toLocaleString();
</script>

<form class="flex flex-wrap items-end gap-3" onsubmit={(e) => { e.preventDefault(); load(false); }}>
  <label><span class="label">User</span><input class="input" bind:value={user} maxlength="64" autocomplete="off" /></label>
  <label><span class="label">Action starts with</span><input class="input" bind:value={action} maxlength="64" placeholder="system." autocomplete="off" /></label>
  <button class="btn btn-primary" type="submit" disabled={busy}>Filter</button>
</form>
{#if err}
  <p class="text-sm text-danger" role="alert">{err}</p>
{:else if !loaded}
  <p class="text-sm text-muted" role="status">Loading…</p>
{:else}
  <section class="card overflow-x-auto" aria-label="Audit log">
    <table class="tbl">
      <thead><tr><th>Time</th><th>User</th><th>Action</th><th>Detail</th></tr></thead>
      <tbody>
        {#each rows as r (r.id)}
          <tr><td class="whitespace-nowrap text-xs">{when(r.time)}</td><td>{r.user}</td><td class="font-mono text-xs">{r.action}</td><td class="break-all text-xs text-muted" title={r.detail}>{auditSummary(r.detail)}</td></tr>
        {:else}
          <tr><td colspan="4" class="text-muted">No entries.</td></tr>
        {/each}
      </tbody>
    </table>
  </section>
  {#if next}<button class="btn" disabled={busy} onclick={() => load(true)}>Load older</button>{/if}
{/if}
