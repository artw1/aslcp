<script lang="ts">
  // Dry run -> diff review -> apply -> result for one form request (POST /api/config/form).
  import { untrack } from 'svelte';
  import { api, ApiError } from '../../lib/api';
  import { advancedLink, changeLine, errorParts, type FormRequest, type Plan, type Refusal } from '../../lib/nodecfg';
  import type { ConfigApplyResult } from '../../lib/types';
  import Dialog from '../Dialog.svelte';
  import DiffView from './DiffView.svelte';
  import ResultView from './ResultView.svelte';
  import RestartAsterisk from './RestartAsterisk.svelte';
  import TriangleAlert from '@lucide/svelte/icons/triangle-alert';

  let { request: reqProp, title, onback, onclose, ondone }: {
    request: FormRequest; title: string;
    /** Back to the form (not offered for one-click actions). */
    onback?: () => void; onclose: () => void;
    /** The files changed: the overview should reload. */
    ondone: () => void;
  } = $props();
  const request = untrack(() => reqProp);

  type Phase = 'planning' | 'review' | 'applying' | 'result' | 'failed';
  let open = $state(true);
  let phase = $state<Phase>('planning');
  let plan = $state<Plan | null>(null);
  let result = $state<ConfigApplyResult | null>(null);
  let refusal = $state<Refusal | null>(null);
  let error = $state(''), blocked = $state<Plan | null>(null), stale = $state(false);
  let message = $state(`Form: node ${request.node}`);
  let notices = $state<string[]>([]);
  let steps = $state<{ id: string; status: string; detail?: string }[]>([]);
  let restart = $state(false), restarted = $state(false), restartPending = $state(false), rolledBack = $state(false);
  let changedOnce = false;
  $effect(() => { if (!open) { if (changedOnce) ondone(); onclose(); } });

  function fail(e: unknown) {
    const parts = e instanceof ApiError ? errorParts(e.body) : {};
    refusal = parts.refusal ?? null;
    blocked = parts.plan ?? null;
    stale = e instanceof ApiError && e.status === 409;
    error = e instanceof Error ? e.message : String(e);
    phase = 'failed';
  }
  async function dryRun() {
    phase = 'planning'; error = ''; refusal = null; blocked = null; stale = false;
    try {
      const r = await api.nodeCfgForm(request, { dryRun: true });
      plan = r.plan; restart = false; phase = 'review';
    } catch (e) { fail(e); }
  }
  void dryRun();

  async function apply() {
    if (!plan) return;
    phase = 'applying'; error = '';
    try {
      const r = await api.nodeCfgForm(request, { dryRun: false, base: plan.base, restart: plan.restart && restart, message: message.trim() || undefined });
      plan = r.plan; result = r.result ?? null; restarted = r.restarted; restartPending = r.restartPending; notices = r.notices ?? []; steps = r.steps ?? [];
      changedOnce = !!r.result; phase = 'result';
    } catch (e) {
      // Files written but the restart failed: show what happened.
      if (e instanceof ApiError && e.body?.applied && e.body.result) { result = e.body.result as ConfigApplyResult; error = e.message; changedOnce = true; phase = 'result'; return; }
      fail(e);
    }
  }
  async function rollback() {
    if (!result) return;
    phase = 'applying';
    try { result = await api.configRestore(result.before, true); rolledBack = true; restartPending = false; changedOnce = true; phase = 'result'; }
    catch (e) { error = e instanceof Error ? e.message : String(e); phase = 'result'; }
  }
  const heading = $derived(phase === 'result' ? (rolledBack ? 'Rolled back' : result ? (result.health && !result.health.ok ? 'Saved, health check failed' : 'Saved') : 'Nothing to change') : phase === 'failed' ? 'Cannot apply' : title);
  const canApply = $derived(phase === 'review' && !!plan && !plan.noop && !plan.errors.length && message.trim() !== '');
</script>

{#snippet findings(list: { level: string; message: string; file?: string }[])}
  <ul class="space-y-1">
    {#each list as f}
      <li class="flex items-start gap-1.5 text-sm"><TriangleAlert size={15} class="mt-0.5 shrink-0 {f.level === 'error' ? 'text-danger' : 'text-warn'}" /><span>{f.message}{#if f.file} <a class="underline" href={'#' + advancedLink(f.file)}>Open {f.file} in Advanced</a>{/if}</span></li>
    {/each}
  </ul>
{/snippet}

<Dialog bind:open title={heading} xl>
  {#if phase === 'planning'}
    <p class="text-sm text-muted" role="status">Working out what would change…</p>
  {:else if phase === 'applying'}
    <p class="text-sm text-muted" role="status">Saving, reloading and checking health. This can take up to 15 seconds…</p>
  {:else if phase === 'failed'}
    <div class="space-y-2 text-sm" role="alert">
      {#if refusal}
        <p class="font-medium">Forms do not change this safely.</p>
        <p>{refusal.message}</p>
        {#if refusal.file}<p><a class="btn btn-sm" href={'#' + advancedLink(refusal.file, refusal.line)} onclick={() => (open = false)}>Edit {refusal.file} in Advanced</a></p>{/if}
      {:else if stale}
        <p>A file changed on disk since the preview was made. Nothing was saved.</p>
        <p><button class="btn btn-sm" onclick={dryRun}>Plan again</button></p>
      {:else}
        <p class="text-danger">{error}</p>
      {/if}
      {#if blocked?.errors.length}{@render findings(blocked.errors)}{/if}
    </div>
  {:else if phase === 'result'}
    {#if result}
      <ResultView {result} {rolledBack} />
    {:else}<p class="text-sm">Everything was already as asked. No file was written.</p>{/if}
    {#if error}<p class="mt-3 text-sm text-danger" role="alert">{error}</p>{/if}
    {#each notices as n}<p class="mt-3 text-sm" role="status">{n}</p>{/each}
    {#each steps.filter((x) => x.id === 'allmon3') as st}<p class="mt-1 text-sm text-muted" role="status">Allmon3: {st.status === 'done' ? 'updated' : st.status === 'skipped' ? 'not changed (' + (st.detail || 'not installed') + ')' : 'could not be updated: ' + (st.detail ?? '')}</p>{/each}
    {#if result?.health && !result.health.ok && !rolledBack}
      <div class="mt-3 flex flex-wrap items-center gap-2 rounded border border-danger/50 bg-danger/10 p-2 text-sm">
        <TriangleAlert size={16} class="shrink-0 text-danger" />
        <span class="grow">Asterisk or a node is not answering after the change. You can restore the previous state.</span>
        <button class="btn btn-primary" onclick={rollback}>Roll back</button>
      </div>
    {/if}
    {#if restarted}
      <p class="mt-3 text-sm text-ok" role="status">Asterisk was restarted to apply the change.</p>
    {:else if restartPending}
      <div class="mt-3 flex flex-wrap items-center gap-2 rounded border border-warn/50 bg-warn/10 p-2 text-sm">
        <TriangleAlert size={16} class="shrink-0 text-warn" />
        <span class="grow">The change is saved but only takes effect after an Asterisk restart.</span>
        <RestartAsterisk />
      </div>
    {/if}
  {:else if plan}
    {#if plan.noop}
      <p class="text-sm">Nothing would change: the settings are already as asked.</p>
    {:else}
      <section class="mb-3">
        <h3 class="label">What changes</h3>
        <ul class="list-inside list-disc space-y-0.5 text-sm">
          {#each plan.changes.filter((c) => c.action !== 'unchanged') as c}<li>{changeLine(c)}{#if c.note}<span class="text-muted"> · {c.note}</span>{/if}</li>{/each}
        </ul>
      </section>
    {/if}
    {#if plan.notes?.length}
      <section class="mb-3 rounded border border-line bg-surface-2 p-2" aria-label="What stays as it is">
        <h3 class="label">What stays as it is</h3>
        <ul class="list-inside list-disc space-y-0.5 text-sm">{#each plan.notes as n}<li>{n}</li>{/each}</ul>
      </section>
    {/if}
    {#if plan.errors.length}
      <div class="mb-3 rounded border border-danger/50 bg-danger/10 p-2" role="alert"><p class="mb-1 text-sm font-medium text-danger">This would break the configuration, so it cannot be applied:</p>{@render findings(plan.errors)}</div>
    {/if}
    {#if plan.warnings.length}
      <div class="mb-3 rounded border border-warn/50 bg-warn/10 p-2"><p class="mb-1 text-sm font-medium">Worth reading</p>{@render findings(plan.warnings)}</div>
    {/if}
    <div class="space-y-3">
      {#each plan.files.filter((f) => f.changed) as f}
        <section><h3 class="mb-1 font-mono text-xs font-semibold">{f.path}</h3><DiffView diff={f.diff} /></section>
      {/each}
    </div>
    {#if !plan.noop}
      <div class="mt-3 text-sm">
        {#if plan.restart}
          <p class="flex items-start gap-1.5 text-warn"><TriangleAlert size={16} class="mt-0.5 shrink-0" /><span>An Asterisk restart is needed for this to take effect ({plan.restartReasons.join('; ') || 'see the settings marked needs restart'}). Links drop and come back.</span></p>
          <label class="mt-1 flex items-center gap-2"><input type="checkbox" bind:checked={restart} />Restart Asterisk right after saving</label>
        {:else if plan.reload.length}
          <h3 class="label">Reload plan</h3>
          <ul class="space-y-0.5">{#each plan.reload as r}<li><code class="font-mono text-xs">{r.command}</code> <span class="text-muted">· {r.reason}</span></li>{/each}</ul>
        {:else}<p class="text-xs text-muted">No reload needed.</p>{/if}
        <div class="mt-3"><label class="label" for="plan-msg">History message</label><input id="plan-msg" class="input" maxlength="200" bind:value={message} /></div>
      </div>
    {/if}
  {/if}
  {#snippet footer()}
    {#if phase === 'result'}
      <button class="btn btn-primary" onclick={() => (open = false)}>Close</button>
    {:else}
      {#if onback && phase !== 'applying'}<button class="btn" onclick={onback}>Back</button>{/if}
      <button class="btn" onclick={() => (open = false)} disabled={phase === 'applying'}>{phase === 'failed' ? 'Close' : 'Cancel'}</button>
      {#if phase === 'review' || phase === 'applying'}<button class="btn btn-primary" disabled={!canApply || phase === 'applying'} onclick={apply}>Apply</button>{/if}
    {/if}
  {/snippet}
</Dialog>
