<script lang="ts">
  // Allow and deny lists of one node. They live in the Asterisk database (not in a file), so each add or remove is
  // applied at once through Asterisk's own database API; the dialplan reads them on every incoming connection.
  import { api } from '../../lib/api';
  import { accessAddError, accessModeText, accessWarning, type AccessLists } from '../../lib/access';
  import Dialog from '../Dialog.svelte';

  let { node, initial, onclose, ondone }: { node: string; initial: AccessLists | null; onclose: () => void; ondone: () => void } = $props();
  let open = $state(true);
  $effect(() => { if (!open) onclose(); });
  let lists = $state<AccessLists | null>(null);
  let err = $state(''), busy = $state(false);
  let family = $state<'allowlist' | 'denylist'>('denylist'), remote = $state(''), note = $state('');
  let pendingRemove = $state<{ family: 'allowlist' | 'denylist'; remote: string } | null>(null);
  $effect(() => { lists = initial; if (!initial) void load(); });

  async function load() {
    try { lists = await api.accessLists(node); err = ''; } catch (e) { err = e instanceof Error ? e.message : String(e); }
  }
  const addErr = $derived(lists ? accessAddError(lists, family, remote, note) : '');
  const warn = $derived(lists ? accessWarning(lists, 'add', family) : '');
  async function change(op: 'add' | 'remove', fam: 'allowlist' | 'denylist', r: string, n = '') {
    busy = true; err = '';
    try {
      lists = await api.accessChange({ node, op, family: fam, remote: r, note: n.trim() || undefined });
      if (op === 'add') { remote = ''; note = ''; }
      pendingRemove = null;
      ondone();
    } catch (e) { err = e instanceof Error ? e.message : String(e); } finally { busy = false; }
  }
</script>

{#snippet list(title: string, fam: 'allowlist' | 'denylist', items: AccessLists['allow'])}
  <section aria-label={title}>
    <h4 class="text-sm font-semibold">{title} ({items.length})</h4>
    {#if items.length === 0}<p class="text-xs text-muted">No entries.</p>{:else}
      <ul class="mt-1 divide-y divide-line rounded border border-line text-sm">
        {#each items as it (it.remote)}
          <li class="flex flex-wrap items-center gap-2 px-2 py-1">
            <span class="font-mono">{it.remote}</span>
            {#if it.web}<span class="badge" title="A web transceiver entry (a callsign). Change it with the Asterisk command line.">web</span>{/if}
            {#if it.note}<span class="min-w-0 grow break-words text-xs text-muted">{it.note}</span>{:else}<span class="grow"></span>{/if}
            {#if !it.web}
              {#if pendingRemove && pendingRemove.family === fam && pendingRemove.remote === it.remote}
                <span class="text-xs">{lists ? accessWarning(lists, 'remove', fam) : ''}</span>
                <button class="btn btn-sm btn-primary" disabled={busy} onclick={() => change('remove', fam, it.remote)}>Remove</button>
                <button class="btn btn-sm" onclick={() => (pendingRemove = null)}>Keep</button>
              {:else}
                <button class="btn btn-sm" disabled={busy} aria-label="Remove {it.remote} from the {title.toLowerCase()}" onclick={() => (pendingRemove = { family: fam, remote: it.remote })}>Remove</button>
              {/if}
            {/if}
          </li>
        {/each}
      </ul>
    {/if}
  </section>
{/snippet}

<Dialog bind:open title="Access lists, node {node}" wide>
  <p class="mb-2 text-sm text-muted">Controls which nodes may connect to node {node}. The lists are kept in Asterisk's database, so there is no file to review: each change takes effect at once and applies to the next connection.</p>
  {#if err}<p class="mb-2 text-sm text-danger" role="alert">{err}</p>{/if}
  {#if !lists}
    {#if !err}<p class="text-sm text-muted" role="status">Loading…</p>{/if}
  {:else}
    {#if !lists.enforced}
      <p class="mb-2 rounded border border-warn/50 bg-warn/10 p-2 text-sm" role="alert">extensions.conf does not check these lists, so entries have no effect here. <a class="underline" href="#/config?tab=advanced&file=extensions.conf">Open extensions.conf in Advanced</a></p>
    {/if}
    <p class="mb-3 rounded border border-line bg-surface-2 p-2 text-sm" role="status"><span class="font-medium">Now:</span> {accessModeText(lists.mode)}</p>
    <div class="grid gap-3 sm:grid-cols-2">
      {@render list('Allow list', 'allowlist', lists.allow)}
      {@render list('Deny list', 'denylist', lists.deny)}
    </div>
    <form class="mt-4 grid gap-2 rounded border border-line p-2" onsubmit={(e) => { e.preventDefault(); if (!addErr && remote.trim()) void change('add', family, remote.trim(), note); }}>
      <h4 class="text-sm font-semibold">Add a node</h4>
      <div class="grid gap-2 sm:grid-cols-[auto_8rem_1fr]">
        <label class="min-w-0"><span class="label">List</span>
          <select class="input" bind:value={family}><option value="denylist">Deny list</option><option value="allowlist">Allow list</option></select></label>
        <label class="min-w-0"><span class="label">Node number</span>
          <input class="input" inputmode="numeric" bind:value={remote} aria-invalid={addErr ? 'true' : undefined} /></label>
        <label class="min-w-0"><span class="label">Note (optional)</span>
          <input class="input" bind:value={note} maxlength={lists.noteMax} /></label>
      </div>
      {#if addErr}<p class="text-xs text-danger" role="alert">{addErr}</p>{/if}
      {#if warn}<p class="text-xs text-warn" role="status">{warn}</p>{/if}
      <div><button class="btn btn-primary" type="submit" disabled={busy || !remote.trim() || !!addErr}>Add</button></div>
    </form>
    <p class="mt-3 text-xs text-muted">With an allow list, only the nodes on it can connect and the deny list is not looked at. Without one, nodes on the deny list are refused. Connections from this computer itself are never blocked.</p>
  {/if}
  {#snippet footer()}<button class="btn btn-primary" onclick={() => (open = false)}>Close</button>{/snippet}
</Dialog>
