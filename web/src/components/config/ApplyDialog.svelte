<script lang="ts" module>
  export type Mode = { kind: 'apply'; files: ConfigDraft[] } | { kind: 'restore'; commit: string; summary: string; files: string[] };
  import type { ConfigDraft } from '../../lib/types';
</script>

<script lang="ts">
  // Review -> apply -> result flow. mode 'apply': preview the drafts first. mode 'restore': confirm, then restore a snapshot.
  import { untrack } from 'svelte';
  import { api, ApiError } from '../../lib/api';
  import type { ConfigPreview, ConfigApplyResult } from '../../lib/types';
  import Dialog from '../Dialog.svelte';
  import DiffView from './DiffView.svelte';
  import ResultView from './ResultView.svelte';
  import RestartAsterisk from './RestartAsterisk.svelte';
  import TriangleAlert from '@lucide/svelte/icons/triangle-alert';

  let { mode: modeProp, onclose, onchanged, onconflict }: {
    mode: Mode; onclose: () => void;
    onchanged: (paths: string[] | 'all') => void;
    onconflict: (paths: string[], message: string) => void;
  } = $props();

  // The parent mounts a fresh dialog per use, so the mode never changes during its lifetime.
  const mode = untrack(() => modeProp);
  let open = $state(true);
  $effect(() => { if (!open) onclose(); });

  type Phase = 'loading' | 'review' | 'applying' | 'result';
  let phase = $state<Phase>(mode.kind === 'apply' ? 'loading' : 'review');
  let preview = $state<ConfigPreview | null>(null);
  let result = $state<ConfigApplyResult | null>(null);
  let rolledBack = $state(false);
  let error = $state(''), working = $state('Applying');
  let reload = $state(true);
  const paths = $derived(mode.kind === 'apply' ? mode.files.map((f) => f.path) : mode.files);
  let message = $state(mode.kind === 'apply' ? defaultMessage(mode.files.map((f) => f.path)) : '');
  function defaultMessage(p: string[]) { return p.length <= 2 ? `Edit ${p.join(', ')}` : `Edit ${p.length} files`; }

  const syntaxErrors = $derived(preview?.files.filter((f) => !f.ok) ?? []);
  const changedCount = $derived(preview?.files.filter((f) => f.changed).length ?? 0);
  const canApply = $derived(mode.kind === 'restore' || (phase === 'review' && !!preview && !syntaxErrors.length && changedCount > 0 && message.trim() !== ''));

  if (mode.kind === 'apply') {
    api.configPreview(mode.files).then((p) => {
      preview = p; phase = 'review';
      // The file changed on disk since it was loaded: same handling as a 409 on apply.
      const stale = p.files.filter((f) => f.conflict).map((f) => f.path);
      if (stale.length) { open = false; onconflict(stale, 'files changed on disk since they were loaded'); }
    }, (e) => { error = e instanceof Error ? e.message : String(e); phase = 'review'; });
  }

  async function apply() {
    error = ''; phase = 'applying'; working = mode.kind === 'restore' ? 'Restoring' : 'Applying';
    try {
      result = mode.kind === 'apply' ? await api.configApply(mode.files, message.trim(), reload) : await api.configRestore(mode.commit, reload);
      phase = 'result';
      onchanged(mode.kind === 'apply' ? paths : 'all');
    } catch (e) {
      phase = 'review';
      if (e instanceof ApiError && e.status === 409 && e.code === 'conflict' && mode.kind === 'apply') {
        const b = e.body as { files?: unknown; paths?: unknown } | undefined;
        const list = [b?.files, b?.paths].find(Array.isArray) as unknown[] | undefined;
        let names = (list ?? []).filter((x): x is string => typeof x === 'string');
        if (!names.length) names = paths.filter((p) => e.message.includes(p));
        open = false;
        onconflict(names.length ? names : paths, e.message);
        return;
      }
      error = e instanceof Error ? e.message : String(e);
    }
  }
  async function rollback() {
    if (!result) return;
    error = ''; phase = 'applying'; working = 'Rolling back';
    try {
      result = await api.configRestore(result.before, true);
      rolledBack = true; phase = 'result';
      onchanged('all');
    } catch (e) { error = e instanceof Error ? e.message : String(e); phase = 'result'; }
  }
  const title = $derived(phase === 'result' ? (rolledBack ? 'Rolled back' : result?.health && !result.health.ok ? 'Applied, health check failed' : mode.kind === 'restore' ? 'Restored' : 'Applied') : mode.kind === 'restore' ? 'Restore snapshot' : 'Review changes');
</script>

<Dialog bind:open {title} xl>
  {#if phase === 'loading'}
    <p class="text-sm text-muted" role="status">Checking syntax and computing the diff…</p>
  {:else if phase === 'applying'}
    <p class="text-sm text-muted" role="status">{working}, reloading and checking health. This can take up to 15 seconds…</p>
  {:else if phase === 'result' && result}
    <ResultView {result} {rolledBack} />
    {#if error}<p class="mt-3 text-sm text-danger" role="alert">{error}</p>{/if}
    {#if result.health && !result.health.ok && !rolledBack}
      <div class="mt-3 flex flex-wrap items-center gap-2 rounded border border-danger/50 bg-danger/10 p-2 text-sm">
        <TriangleAlert size={16} class="shrink-0 text-danger" />
        <span class="grow">Asterisk or a node is not answering after the change. You can restore the previous state.</span>
        <button class="btn btn-primary" onclick={rollback}>Roll back</button>
      </div>
    {/if}
    {#if result.restart}
      <div class="mt-3 flex flex-wrap items-center gap-2 rounded border border-warn/50 bg-warn/10 p-2 text-sm">
        <TriangleAlert size={16} class="shrink-0 text-warn" />
        <span class="grow">Some changed files only take effect after an Asterisk restart.</span>
        <RestartAsterisk />
      </div>
    {/if}
  {:else if mode.kind === 'restore'}
    <p class="text-sm">Restore the files of snapshot <code class="font-mono text-xs">{mode.commit.slice(0, 7)}</code> ({mode.summary})? Only files that differ are rewritten, and the current state is snapshotted first, so this can be undone.</p>
    <ul class="mt-2 list-inside list-disc font-mono text-xs">{#each mode.files as f}<li>{f}</li>{/each}</ul>
    <label class="mt-3 flex items-center gap-2 text-sm"><input type="checkbox" bind:checked={reload} />Reload after restore</label>
    {#if error}<p class="mt-3 text-sm text-danger" role="alert">{error}</p>{/if}
  {:else}
    {#if error}<p class="mb-3 text-sm text-danger" role="alert">{error}</p>{/if}
    {#if preview}
      {#if syntaxErrors.length}
        <div class="mb-3 rounded border border-danger/50 bg-danger/10 p-2 text-sm" role="alert">
          <p class="font-medium text-danger">Syntax errors block applying:</p>
          <ul class="mt-1 list-inside list-disc">{#each syntaxErrors as f}<li><span class="font-mono text-xs">{f.path}</span>{#if f.error}{f.error.line > 0 ? `, line ${f.error.line}` : ''}: {f.error.message}{/if}</li>{/each}</ul>
        </div>
      {/if}
      <div class="space-y-3">
        {#each preview.files as f}
          <section>
            <h3 class="mb-1 flex items-center gap-2 font-mono text-xs font-semibold">{f.path}{#if f.ok && !f.changed}<span class="badge">unchanged</span>{/if}</h3>
            <DiffView diff={f.diff} />
          </section>
        {/each}
      </div>
      <div class="mt-3 text-sm">
        {#if preview.restart}
          <p class="flex items-start gap-1.5 text-warn"><TriangleAlert size={16} class="mt-0.5 shrink-0" /><span>These changes need an Asterisk restart; a reload will not pick them up. Restart from the result screen after applying.</span></p>
        {:else if preview.reload?.length}
          <h3 class="label">Reload plan</h3>
          <ul class="space-y-0.5">{#each preview.reload ?? [] as r}<li><code class="font-mono text-xs">{r.command}</code> <span class="text-muted">· {r.reason}</span></li>{/each}</ul>
        {:else}<p class="text-xs text-muted">No reload needed.</p>{/if}
      </div>
      <div class="mt-3 grid gap-2">
        <div><label class="label" for="cfg-msg">Commit message</label><input id="cfg-msg" class="input" maxlength="200" bind:value={message} /></div>
        <label class="flex items-center gap-2 text-sm"><input type="checkbox" bind:checked={reload} />Reload after apply</label>
      </div>
    {/if}
  {/if}
  {#snippet footer()}
    {#if phase === 'result'}
      <button class="btn btn-primary" onclick={() => (open = false)}>Close</button>
    {:else}
      <button class="btn" onclick={() => (open = false)} disabled={phase === 'applying'}>Cancel</button>
      <button class="btn btn-primary" disabled={!canApply || phase === 'applying'} onclick={apply}>{mode.kind === 'restore' ? 'Restore' : 'Apply'}</button>
    {/if}
  {/snippet}
</Dialog>
