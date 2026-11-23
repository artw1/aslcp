<script lang="ts">
  // Review -> apply -> step results -> health, registration check and one-click rollback for a wizard plan.
  // Shared by every wizard (setup, add node, change node number): the server runs the fixed step order
  // (docs/API.md, "Node setup" and "Node wizards"); the wizard only supplies how to plan and apply.
  import { onDestroy, untrack } from 'svelte';
  import { api, ApiError } from '../../lib/api';
  import { advancedLink, errorParts, type Refusal } from '../../lib/nodecfg';
  import { RUN_LABELS, registrationDone, type SetupApplyResult, type SetupPlan, type SetupRollback, type SetupStepResult } from '../../lib/nodesetup';
  import type { ConfigApplyResult } from '../../lib/types';
  import Dialog from '../Dialog.svelte';
  import DiffView from './DiffView.svelte';
  import Check from '@lucide/svelte/icons/check';
  import X from '@lucide/svelte/icons/x';
  import Minus from '@lucide/svelte/icons/minus';
  import TriangleAlert from '@lucide/svelte/icons/triangle-alert';

  let { title, doneTitle, failTitle, message: msgProp, plan: planFn, apply: applyFn, intro, footnote = '', rollbackNote, rolledBackText, registrationNode = '', onback, onclose, ondone }: {
    title: string; doneTitle: string; failTitle: string;
    /** Default history message. */
    message: string;
    plan: () => Promise<SetupPlan>;
    apply: (base: Record<string, string>, message: string | undefined) => Promise<SetupApplyResult>;
    /** Sentence under the step list (what happens to the node number, the AMI secret...). */
    intro: (p: SetupPlan) => string;
    footnote?: string; rollbackNote: string; rolledBackText: string;
    /** Poll the registration of this node after applying (public nodes). */
    registrationNode?: string;
    /** Back to the questions. */
    onback: () => void; onclose: () => void;
    /** The files changed: the overview should reload. */
    ondone: () => void;
  } = $props();

  type Phase = 'planning' | 'review' | 'applying' | 'result' | 'failed';
  let open = $state(true);
  let phase = $state<Phase>('planning');
  let plan = $state<SetupPlan | null>(null);
  let message = $state(untrack(() => msgProp));
  let steps = $state<SetupStepResult[]>([]), result = $state<ConfigApplyResult | null>(null), rollback = $state<SetupRollback | null>(null);
  let error = $state(''), refusal = $state<Refusal | null>(null), stale = $state(false), rolledBack = $state(false), applied = $state(false);
  let reg = $state<{ state: string; detail?: string } | null>(null), regTries = $state(0), regDone = $state(false);
  let timer: ReturnType<typeof setTimeout> | undefined;
  let changedOnce = false;
  onDestroy(() => clearTimeout(timer));
  $effect(() => { if (!open) { clearTimeout(timer); if (changedOnce) ondone(); onclose(); } });

  function fail(e: unknown) {
    const p = e instanceof ApiError ? errorParts(e.body) : {};
    refusal = p.refusal ?? null; stale = e instanceof ApiError && e.status === 409;
    error = e instanceof Error ? e.message : String(e);
    phase = 'failed';
  }
  async function doPlan() {
    phase = 'planning'; error = ''; refusal = null; stale = false;
    try { plan = await planFn(); phase = 'review'; } catch (e) { fail(e); }
  }
  void doPlan();

  async function doApply() {
    if (!plan) return;
    phase = 'applying'; error = ''; steps = []; rollback = null;
    try {
      const r = await applyFn(plan.base, message.trim() || undefined);
      steps = r.steps; result = r.result ?? null; rollback = r.rollback ?? null; applied = !!r.result; changedOnce = applied;
      phase = 'result'; if (applied && registrationNode) pollReg();
    } catch (e) {
      if (e instanceof ApiError && e.body?.applied) {
        steps = (e.body.steps as SetupStepResult[]) ?? []; result = (e.body.result as ConfigApplyResult) ?? null; rollback = (e.body.rollback as SetupRollback) ?? null;
        error = e.message; applied = true; changedOnce = true; phase = 'result'; return;
      }
      fail(e);
    }
  }
  async function doRollback() {
    if (!rollback) return;
    phase = 'applying'; clearTimeout(timer);
    try {
      const r = await api.nodeSetupRollback(rollback);
      steps = r.steps; result = r.result ?? result; rolledBack = true; error = ''; changedOnce = true; reg = null; regDone = true;
    } catch (e) {
      if (e instanceof ApiError && Array.isArray(e.body?.steps)) steps = e.body.steps as SetupStepResult[];
      error = e instanceof Error ? e.message : String(e);
    }
    phase = 'result';
  }
  // Registration check: GET /api/config/nodes?status=1 until the node says registered (about a minute at most).
  function pollReg() {
    regTries = 0; regDone = false; reg = { state: 'unknown' };
    const tick = async () => {
      regTries++;
      try {
        const o = await api.nodeCfgOverview(true);
        const st = o.nodes.find((n) => n.node === registrationNode)?.registration.status;
        if (st) reg = st;
        if (registrationDone(st?.state) || st?.state === 'failed' || regTries >= 12) { regDone = true; return; }
      } catch { if (regTries >= 12) { regDone = true; return; } }
      timer = setTimeout(tick, 5000);
    };
    timer = setTimeout(tick, 1500);
  }

  const heading = $derived(phase === 'result' ? (rolledBack ? 'Rolled back' : error ? 'Stopped part way' : doneTitle) : phase === 'failed' ? failTitle : title);
  const canApply = $derived(phase === 'review' && !!plan && !plan.noop && !plan.errors.length && message.trim() !== '');
</script>

{#snippet list(items: { level: string; message: string }[])}
  <ul class="space-y-1">{#each items as f}<li class="flex items-start gap-1.5 text-sm"><TriangleAlert size={15} class="mt-0.5 shrink-0 {f.level === 'error' ? 'text-danger' : 'text-warn'}" /><span>{f.message}</span></li>{/each}</ul>
{/snippet}

<Dialog bind:open title={heading} xl>
  {#if phase === 'planning'}
    <p class="text-sm text-muted" role="status">Working out what would change…</p>
  {:else if phase === 'applying'}
    <p class="text-sm text-muted" role="status">Working through the steps. Restarting Asterisk can take up to a minute…</p>
  {:else if phase === 'failed'}
    <div class="space-y-2 text-sm" role="alert">
      {#if refusal}
        <p class="font-medium">The wizard does not change this safely.</p><p>{refusal.message}</p>
        {#if refusal.file}<p><a class="btn btn-sm" href={'#' + advancedLink(refusal.file, refusal.line)} onclick={() => (open = false)}>Edit {refusal.file} in Advanced</a></p>{/if}
      {:else if stale}
        <p>A file changed on disk since the preview was made. Nothing was saved.</p><p><button class="btn btn-sm" onclick={doPlan}>Plan again</button></p>
      {:else}<p class="text-danger">{error}</p>{/if}
    </div>
  {:else if phase === 'review' && plan}
    {#if plan.noop}
      <p class="text-sm">Nothing would change: everything is already like this.</p>
    {:else}
      <section class="mb-3">
        <h3 class="label">What happens</h3>
        <ol class="list-inside list-decimal space-y-0.5 text-sm">
          {#each plan.steps as s}<li>{s.label}{#if s.skipped}<span class="text-muted"> (skipped: {s.skipped})</span>{/if}</li>{/each}
        </ol>
        <p class="mt-1 text-xs text-muted">{intro(plan)}</p>
      </section>
    {/if}
    {#if plan.errors.length}
      <div class="mb-3 rounded border border-danger/50 bg-danger/10 p-2" role="alert"><p class="mb-1 text-sm font-medium text-danger">This would break the configuration, so it cannot be applied:</p>{@render list(plan.errors)}</div>
    {/if}
    {#if plan.notes?.length}
      <section class="mb-3 rounded border border-line bg-surface-2 p-2" aria-label="What stays as it is">
        <h3 class="label">What stays as it is</h3>
        <ul class="list-inside list-disc space-y-0.5 text-sm">{#each plan.notes as n}<li>{n}</li>{/each}</ul>
      </section>
    {/if}
    {#if plan.warnings.length}
      <div class="mb-3 rounded border border-warn/50 bg-warn/10 p-2"><p class="mb-1 text-sm font-medium">Worth reading</p>{@render list(plan.warnings)}</div>
    {/if}
    {#if plan.manual?.length}
      <section class="mb-3 rounded border border-line p-2" aria-label="Not changed by this wizard">
        <h3 class="label">Not changed by this wizard</h3>
        <ul class="list-inside list-disc space-y-0.5 text-sm">{#each plan.manual as m}<li>{m}</li>{/each}</ul>
        <p class="mt-1 text-xs text-muted">Asterisk files can be edited in <a class="underline" href={'#' + advancedLink('voter.conf')}>Advanced</a>; the other items are outside /etc/asterisk or are other computers' settings.</p>
      </section>
    {/if}
    <div class="space-y-3">
      {#each plan.files.filter((f) => f.changed) as f}<section><h3 class="mb-1 font-mono text-xs font-semibold">{f.path}</h3><DiffView diff={f.diff} /></section>{/each}
    </div>
    {#if !plan.noop}
      <p class="mt-3 flex items-start gap-1.5 text-sm text-warn"><TriangleAlert size={16} class="mt-0.5 shrink-0" /><span>{plan.restart ? `Asterisk is restarted to apply this (${plan.restartReasons.join('; ')}). Links drop and come back.` : 'Asterisk is reloaded to apply this.'}{footnote ? ' ' + footnote : ''}</span></p>
      <div class="mt-3"><label class="label" for="wf-msg">History message</label><input id="wf-msg" class="input" maxlength="200" bind:value={message} /></div>
    {/if}
  {:else if phase === 'result'}
    <ul class="space-y-1 text-sm" aria-label="Steps">
      {#each steps as s}
        <li class="flex items-start gap-1.5">
          {#if s.status === 'done'}<Check size={15} class="mt-0.5 shrink-0 text-ok" aria-label="Done" />{:else if s.status === 'failed'}<X size={15} class="mt-0.5 shrink-0 text-danger" aria-label="Failed" />{:else}<Minus size={15} class="mt-0.5 shrink-0 text-muted" aria-label="Skipped" />{/if}
          <span><span class="font-medium">{RUN_LABELS[s.id] ?? s.id}</span>{#if s.detail}<span class="text-muted"> · {s.detail}</span>{/if}</span>
        </li>
      {/each}
    </ul>
    {#if error}<p class="mt-3 text-sm text-danger" role="alert">{error}</p>{/if}
    {#if result?.health && !rolledBack}
      <section class="mt-3"><h3 class="label">Health check</h3>
        <p class="mb-1 text-sm font-medium {result.health.ok ? 'text-ok' : 'text-danger'}" role="status">{result.health.ok ? 'Healthy' : 'Health check failed'}</p>
        <ul class="space-y-0.5 text-sm">{#each result.health.checks as c}<li class="flex items-start gap-1.5">{#if c.ok}<Check size={14} class="mt-0.5 shrink-0 text-ok" aria-label="OK" />{:else}<X size={14} class="mt-0.5 shrink-0 text-danger" aria-label="Failed" />{/if}<span><span class="font-medium">{c.name}</span>{#if c.detail}<span class="text-muted"> · {c.detail}</span>{/if}</span></li>{/each}</ul>
      </section>
    {/if}
    {#if reg && !rolledBack}
      <section class="mt-3"><h3 class="label">Registration check</h3>
        {#if reg.state === 'registered'}<p class="text-sm font-medium text-ok" role="status">Registered with AllStarLink{reg.detail ? ` · ${reg.detail}` : ''}.</p>
        {:else if reg.state === 'failed'}<p class="text-sm text-danger" role="alert">Registration is failing{reg.detail ? `: ${reg.detail}` : ''}. Check the node number and password on the AllStarLink portal.</p>
        {:else if regDone}<p class="text-sm text-warn" role="status">Not registered yet. It can take a few minutes; check again on the overview.</p>
        {:else}<p class="text-sm text-muted" role="status">Waiting for the registration to come up… ({regTries}/12)</p>{/if}
      </section>
    {/if}
    {#if rolledBack}<p class="mt-3 text-sm" role="status">{rolledBackText}</p>{/if}
    {#if plan?.manual?.length && !rolledBack}
      <section class="mt-3"><h3 class="label">Still to do by hand</h3><ul class="list-inside list-disc space-y-0.5 text-sm">{#each plan.manual as m}<li>{m}</li>{/each}</ul></section>
    {/if}
    {#if rollback && !rolledBack}
      <div class="mt-3 flex flex-wrap items-center gap-2 rounded border border-line bg-surface-2 p-2 text-sm">
        <span class="grow">{rollbackNote}</span>
        <button class="btn" onclick={doRollback}>Roll back</button>
      </div>
    {/if}
  {/if}
  {#snippet footer()}
    {#if phase === 'result' || phase === 'failed'}
      <button class="btn btn-primary" onclick={() => (open = false)}>Close</button>
    {:else}
      {#if phase === 'review'}<button class="btn" onclick={onback}>Back</button>{/if}
      <button class="btn" onclick={() => (open = false)} disabled={phase === 'applying' || phase === 'planning'}>Cancel</button>
      {#if phase === 'review' || phase === 'applying'}<button class="btn btn-primary" disabled={!canApply || phase === 'applying'} onclick={doApply}>Apply</button>{/if}
    {/if}
  {/snippet}
</Dialog>
