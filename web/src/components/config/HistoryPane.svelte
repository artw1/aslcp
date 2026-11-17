<script lang="ts">
  import { api } from '../../lib/api';
  import { toastError } from '../../lib/toast.svelte';
  import type { ConfigCommit, ConfigCommitDetail } from '../../lib/types';
  import DiffView from './DiffView.svelte';
  import RotateCcw from '@lucide/svelte/icons/rotate-ccw';
  import Eye from '@lucide/svelte/icons/eye';
  import EyeOff from '@lucide/svelte/icons/eye-off';

  let { onrestore, rev = 0 }: { onrestore: (c: ConfigCommitDetail) => void; rev?: number } = $props();
  const PAGE = 30;
  let commits = $state<ConfigCommit[]>([]);
  let more = $state(false), loading = $state(true), error = $state('');
  let sel = $state<string | null>(null);
  let detail = $state<ConfigCommitDetail | null>(null), revealed = $state(false), dbusy = $state(false);

  async function load(reset: boolean) {
    loading = true; error = '';
    try {
      const off = reset ? 0 : commits.length;
      const page = await api.configHistory(PAGE, off);
      commits = reset ? page : [...commits, ...page];
      more = page.length === PAGE;
    } catch (e) { error = e instanceof Error ? e.message : String(e); } finally { loading = false; }
  }
  $effect(() => { rev; load(true); });

  async function open(c: ConfigCommit) {
    if (sel === c.commit) { sel = null; detail = null; return; }
    sel = c.commit; revealed = false; await fetchDetail(false);
  }
  async function fetchDetail(reveal: boolean) {
    const id = sel; if (!id) return;
    dbusy = true;
    try { const d = await api.configCommit(id, reveal); if (sel === id) { detail = d; revealed = reveal; } } catch (e) { toastError(e); } finally { dbusy = false; }
  }
  const fmt = (d: string) => { const t = new Date(d); return isNaN(+t) ? d : t.toLocaleString([], { dateStyle: 'medium', timeStyle: 'short' }); };
</script>

<div class="card overflow-hidden">
  {#if error}<p class="p-3 text-sm text-danger" role="alert">{error}</p>{/if}
  <ul>
    {#each commits as c (c.commit)}
      <li class="border-b border-line last:border-b-0">
        <button type="button" class="grid w-full grid-cols-[1fr_auto] gap-x-3 px-3 py-1.5 text-left hover:bg-surface-2 {sel === c.commit ? 'bg-surface-2' : ''}" aria-expanded={sel === c.commit} onclick={() => open(c)}>
          <span class="min-w-0 truncate text-sm font-medium">{c.message}</span>
          <span class="text-xs text-muted tabular-nums">{fmt(c.date)}</span>
          <span class="min-w-0 truncate font-mono text-xs text-muted">{c.files.join(', ') || 'no files'}</span>
          <span class="text-xs text-muted">{c.author} · <span class="font-mono">{c.commit.slice(0, 7)}</span></span>
        </button>
        {#if sel === c.commit}
          <div class="border-t border-line bg-bg/40 p-3">
            {#if !detail}<p class="text-xs text-muted" role="status">Loading…</p>
            {:else}
              <div class="mb-2 flex flex-wrap items-center gap-2">
                <button class="btn btn-sm" disabled={dbusy} onclick={() => fetchDetail(!revealed)}>{#if revealed}<EyeOff size={14} />Hide secrets{:else}<Eye size={14} />Reveal secrets{/if}</button>
                <button class="btn btn-sm" onclick={() => detail && onrestore(detail)}><RotateCcw size={14} />Restore this snapshot</button>
                {#if !revealed}<span class="text-xs text-muted">Secret values are masked.</span>{:else}<span class="text-xs text-warn">Secrets are visible.</span>{/if}
              </div>
              <div class="space-y-2">
                {#each detail.files as f}
                  <section>
                    <h3 class="mb-1 flex items-center gap-2 font-mono text-xs font-semibold">{f.path}<span class="badge">{f.status}</span></h3>
                    <DiffView diff={f.diff} />
                  </section>
                {:else}<p class="text-xs text-muted">No file changes.</p>{/each}
              </div>
            {/if}
          </div>
        {/if}
      </li>
    {:else}
      {#if !loading && !error}<li class="p-3 text-sm text-muted">No history yet. Snapshots are created on the first apply.</li>{/if}
    {/each}
  </ul>
  {#if loading}<p class="p-3 text-xs text-muted" role="status">Loading…</p>{/if}
  {#if more && !loading}<div class="border-t border-line p-2 text-center"><button class="btn btn-sm" onclick={() => load(false)}>Load more</button></div>{/if}
</div>
