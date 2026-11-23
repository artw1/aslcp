<script lang="ts">
  // "Set up this node": answers -> combined diff -> apply through the server's fixed pipeline -> health,
  // registration check and one-click rollback. The server decides the order (docs/API.md, "Node setup").
  import { onDestroy } from 'svelte';
  import { api, ApiError } from '../../lib/api';
  import { advancedLink, errorParts, type Preset, type Refusal } from '../../lib/nodecfg';
  import { RUN_LABELS, STEP_TITLES, WIZARD_STEPS, emptyDraft, nodeKind, presetsFor, registrationDone, setupBody, stepError, type SetupApplyResult, type SetupPlan, type SetupRollback, type SetupStepResult, type WizardStep } from '../../lib/nodesetup';
  import type { ConfigApplyResult } from '../../lib/types';
  import Dialog from '../Dialog.svelte';
  import DiffView from './DiffView.svelte';
  import Check from '@lucide/svelte/icons/check';
  import X from '@lucide/svelte/icons/x';
  import Minus from '@lucide/svelte/icons/minus';
  import TriangleAlert from '@lucide/svelte/icons/triangle-alert';

  let { oldNode, presets, onclose, ondone }: {
    /** The sample node being set up (the overview's placeholder, or the selected node when restarted). */
    oldNode: string; presets: Preset[]; onclose: () => void; ondone: () => void;
  } = $props();

  type Phase = 'ask' | 'planning' | 'review' | 'applying' | 'result' | 'failed';
  let open = $state(true);
  let phase = $state<Phase>('ask');
  let step = $state<WizardStep>('node');
  // svelte-ignore state_referenced_locally
  let d = $state(emptyDraft(oldNode));
  let plan = $state<SetupPlan | null>(null), amiToken = $state(''), message = $state('');
  let steps = $state<SetupStepResult[]>([]), result = $state<ConfigApplyResult | null>(null), rollback = $state<SetupRollback | null>(null);
  let error = $state(''), refusal = $state<Refusal | null>(null), stale = $state(false), rolledBack = $state(false), applied = $state(false);
  let reg = $state<{ state: string; detail?: string } | null>(null), regTries = $state(0), regDone = $state(false);
  let timer: ReturnType<typeof setTimeout> | undefined;
  let changedOnce = false;
  onDestroy(() => clearTimeout(timer));
  $effect(() => { if (!open) { clearTimeout(timer); if (changedOnce) ondone(); onclose(); } });

  const kind = $derived(nodeKind(d.node));
  const pubPresets = $derived(presetsFor(presets, d.radio));
  const idx = $derived(WIZARD_STEPS.indexOf(step));
  const err = $derived(stepError(step, d, presets));
  const body = $derived(setupBody(oldNode, d));
  $effect(() => { if (!pubPresets.some((p) => p.name === d.preset) && pubPresets[0]) d.preset = pubPresets[0].name; });
  // The generated AMI secret belongs to one plan: any answer that changes the request starts a new one.
  $effect(() => { void JSON.stringify(body); amiToken = ''; });

  function fail(e: unknown) {
    const p = e instanceof ApiError ? errorParts(e.body) : {};
    refusal = p.refusal ?? null; stale = e instanceof ApiError && e.status === 409;
    error = e instanceof Error ? e.message : String(e);
    phase = 'failed';
  }
  function next() {
    if (err) return;
    if (idx >= WIZARD_STEPS.length - 2) void doPlan(); else step = WIZARD_STEPS[idx + 1];
  }
  function back() { if (phase === 'review') phase = 'ask'; else if (idx > 0) step = WIZARD_STEPS[idx - 1]; }
  async function doPlan() {
    phase = 'planning'; error = ''; refusal = null; stale = false;
    try {
      const r = await api.nodeSetupPlan(body, amiToken || undefined);
      plan = r.plan; amiToken = r.amiToken ?? ''; message = `Setup wizard: node ${d.node.trim()}`; phase = 'review';
    } catch (e) { fail(e); }
  }
  async function apply() {
    if (!plan) return;
    phase = 'applying'; error = ''; steps = []; rollback = null;
    try {
      const r: SetupApplyResult = await api.nodeSetupApply(body, { amiToken, base: plan.base, message: message.trim() || undefined });
      steps = r.steps; result = r.result ?? null; rollback = r.rollback ?? null; applied = !!r.result; changedOnce = applied;
      phase = 'result'; if (applied && kind === 'public') pollReg();
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
        const st = o.nodes.find((n) => n.node === d.node.trim())?.registration.status;
        if (st) reg = st;
        if (registrationDone(st?.state) || st?.state === 'failed' || regTries >= 12) { regDone = true; return; }
      } catch { if (regTries >= 12) { regDone = true; return; } }
      timer = setTimeout(tick, 5000);
    };
    timer = setTimeout(tick, 1500);
  }

  const heading = $derived(phase === 'result' ? (rolledBack ? 'Rolled back' : error ? 'Setup stopped' : 'Node set up') : phase === 'failed' ? 'Cannot set up' : phase === 'ask' ? `Set up this node: ${STEP_TITLES[step]}` : 'Set up this node');
  const canApply = $derived(phase === 'review' && !!plan && !plan.noop && !plan.errors.length && message.trim() !== '');
</script>

<Dialog bind:open title={heading} xl>
  {#if phase === 'ask'}
    <p class="mb-3 text-xs text-muted" role="status">Step {idx + 1} of {WIZARD_STEPS.length}. Nothing is changed until you apply on the last page.</p>
    {#if step === 'node'}
      <div class="grid gap-3 sm:grid-cols-2">
        <div class="min-w-0">
          <label class="label" for="sw-node">Node number</label>
          <input id="sw-node" class="input font-mono" inputmode="numeric" bind:value={d.node} autocomplete="off" placeholder="1999" aria-invalid={d.node && !kind ? 'true' : undefined} />
          <p class="mt-0.5 text-xs text-muted">
            {#if kind === 'private'}Private node (1 to 1999): not registered with AllStarLink, so it can link only to nodes you connect to by address.
            {:else if kind === 'public'}Public node (2000 and up): registered with AllStarLink so others can reach it. You get the number and password from your AllStarLink portal.
            {:else}1 to 1999 is a private node (no registration). 2000 and up is a public node.{/if}
          </p>
        </div>
        <div class="min-w-0">
          <label class="label" for="sw-pass">Node password</label>
          <input id="sw-pass" class="input font-mono" type="password" bind:value={d.password} autocomplete="new-password" spellcheck="false" />
          <p class="mt-0.5 text-xs text-muted">{kind === 'public' ? 'The password of this node on the AllStarLink portal; it is also used to register.' : 'Any password you like for this node.'} It is stored in savenode.conf and, for a public node, the registration line. It is never shown again.</p>
        </div>
      </div>
    {:else if step === 'identity'}
      <label class="label" for="sw-call">Callsign</label>
      <input id="sw-call" class="input max-w-xs uppercase" bind:value={d.callsign} autocomplete="off" spellcheck="false" placeholder="N0CALL" />
      <p class="mt-0.5 text-xs text-muted">Sent in Morse code to identify the node (<span class="font-mono">idrecording = |i{d.callsign.trim().toUpperCase() || 'CALL'}</span>).</p>
    {:else if step === 'radio'}
      <fieldset class="space-y-2">
        <legend class="label">How does this node connect to a radio?</legend>
        <label class="flex items-start gap-2 text-sm"><input type="radio" name="sw-radio" value="simpleusb" bind:group={d.radio} class="mt-1" /><span><span class="font-medium">USB sound interface (SimpleUSB)</span><br /><span class="text-xs text-muted">A CM1xx style USB sound card with a radio. Levels are tuned later under Audio Tuning.</span></span></label>
        <label class="flex items-start gap-2 text-sm"><input type="radio" name="sw-radio" value="hub" bind:group={d.radio} class="mt-1" /><span><span class="font-medium">Hub (no radio)</span><br /><span class="text-xs text-muted">A node that only links other nodes together.</span></span></label>
        <p class="text-xs text-muted">USBRadio is not supported by this wizard: edit it in Advanced.</p>
      </fieldset>
    {:else if step === 'duplex'}
      <fieldset class="space-y-2">
        <legend class="label">Duplex preset</legend>
        {#each pubPresets as p (p.name)}
          <label class="flex items-start gap-2 text-sm"><input type="radio" name="sw-preset" value={p.name} bind:group={d.preset} class="mt-1" /><span><span class="font-medium">{p.label}</span><br /><span class="text-xs text-muted">{p.desc}</span></span></label>
        {/each}
      </fieldset>
    {:else if step === 'registration'}
      {#if kind === 'public'}
        <fieldset class="space-y-2">
          <legend class="label">How should the node register with AllStarLink?</legend>
          <label class="flex items-start gap-2 text-sm"><input type="radio" name="sw-reg" value="http" bind:group={d.regMethod} class="mt-1" /><span><span class="font-medium">HTTP (recommended)</span><br /><span class="text-xs text-muted">Works behind most routers and firewalls.</span></span></label>
          <label class="flex items-start gap-2 text-sm"><input type="radio" name="sw-reg" value="iax" bind:group={d.regMethod} class="mt-1" /><span><span class="font-medium">IAX (optional)</span><br /><span class="text-xs text-muted">The older method; needs UDP port 4569 reachable from the internet.</span></span></label>
        </fieldset>
        <div class="mt-3 max-w-sm">
          <label class="label" for="sw-host">Registration server (optional)</label>
          <input id="sw-host" class="input font-mono" bind:value={d.regHost} autocomplete="off" spellcheck="false" placeholder="default server" />
        </div>
      {:else}
        <p class="text-sm">Node {d.node.trim() || '…'} is a private node (1 to 1999), so it is not registered. Go back and choose a number of 2000 or more to register it.</p>
      {/if}
    {:else if step === 'echolink'}
      <label class="flex items-center gap-2 text-sm"><input type="checkbox" bind:checked={d.echolink} />Set up EchoLink (optional)</label>
      {#if d.echolink}
        <p class="mt-1 text-xs text-muted">You need an EchoLink account validated for this callsign. The EchoLink module is loaded for you.</p>
        <div class="mt-3 grid gap-3 sm:grid-cols-2">
          <div class="min-w-0"><label class="label" for="sw-elcall">EchoLink call</label><input id="sw-elcall" class="input uppercase" bind:value={d.elCall} placeholder="N0CALL-L" autocomplete="off" spellcheck="false" /><p class="mt-0.5 text-xs text-muted">-L links nodes to EchoLink, -R makes the node an EchoLink repeater.</p></div>
          <div class="min-w-0"><label class="label" for="sw-elpass">EchoLink password</label><input id="sw-elpass" class="input" type="password" bind:value={d.elPassword} autocomplete="new-password" /></div>
          <div class="min-w-0"><label class="label" for="sw-elemail">E-mail</label><input id="sw-elemail" class="input" type="email" bind:value={d.elEmail} autocomplete="off" /></div>
          <div class="min-w-0"><label class="label" for="sw-elname">Name</label><input id="sw-elname" class="input" bind:value={d.elName} autocomplete="off" /></div>
          <div class="min-w-0"><label class="label" for="sw-elqth">Location (QTH)</label><input id="sw-elqth" class="input" bind:value={d.elQth} autocomplete="off" /></div>
        </div>
      {/if}
    {/if}
    {#if err}<p class="mt-3 text-xs text-danger" role="alert">{err}</p>{/if}
  {:else if phase === 'planning'}
    <p class="text-sm text-muted" role="status">Working out what would change…</p>
  {:else if phase === 'applying'}
    <p class="text-sm text-muted" role="status">Working through the setup. Restarting Asterisk can take up to a minute…</p>
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
      <p class="text-sm">Nothing would change: the node is already set up like this.</p>
    {:else}
      <section class="mb-3">
        <h3 class="label">What happens</h3>
        <ol class="list-inside list-decimal space-y-0.5 text-sm">
          {#each plan.steps as s}<li>{s.label}{#if s.skipped}<span class="text-muted"> (skipped: {s.skipped})</span>{/if}</li>{/each}
        </ol>
        <p class="mt-1 text-xs text-muted">{plan.renumber ? `Node ${plan.oldNode} becomes node ${plan.node} everywhere it appears.` : `Node ${plan.node} keeps its number.`} A new AMI secret is generated and kept on the server; it is never shown.</p>
      </section>
    {/if}
    {#if plan.errors.length}
      <div class="mb-3 rounded border border-danger/50 bg-danger/10 p-2" role="alert"><p class="mb-1 text-sm font-medium text-danger">This would break the configuration, so it cannot be applied:</p>
        <ul class="space-y-1">{#each plan.errors as f}<li class="flex items-start gap-1.5 text-sm"><TriangleAlert size={15} class="mt-0.5 shrink-0 text-danger" /><span>{f.message}</span></li>{/each}</ul></div>
    {/if}
    {#if plan.warnings.length}
      <div class="mb-3 rounded border border-warn/50 bg-warn/10 p-2"><p class="mb-1 text-sm font-medium">Worth reading</p>
        <ul class="space-y-1">{#each plan.warnings as f}<li class="flex items-start gap-1.5 text-sm"><TriangleAlert size={15} class="mt-0.5 shrink-0 text-warn" /><span>{f.message}</span></li>{/each}</ul></div>
    {/if}
    <div class="space-y-3">
      {#each plan.files.filter((f) => f.changed) as f}<section><h3 class="mb-1 font-mono text-xs font-semibold">{f.path}</h3><DiffView diff={f.diff} /></section>{/each}
    </div>
    {#if !plan.noop}
      <p class="mt-3 flex items-start gap-1.5 text-sm text-warn"><TriangleAlert size={16} class="mt-0.5 shrink-0" /><span>{plan.restart ? `Asterisk is restarted to apply this (${plan.restartReasons.join('; ')}). Links drop and come back.` : 'Asterisk is reloaded to apply this.'} savenode.conf and Allmon3 are updated too when they exist on this system.</span></p>
      <div class="mt-3"><label class="label" for="sw-msg">History message</label><input id="sw-msg" class="input" maxlength="200" bind:value={message} /></div>
    {/if}
  {:else if phase === 'result'}
    <ul class="space-y-1 text-sm" aria-label="Setup steps">
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
    {#if rolledBack}<p class="mt-3 text-sm" role="status">Everything is back as it was before the setup, including savenode.conf and the AMI secret.</p>{/if}
    {#if rollback && !rolledBack}
      <div class="mt-3 flex flex-wrap items-center gap-2 rounded border border-line bg-surface-2 p-2 text-sm">
        <span class="grow">Not what you wanted? Restore the previous configuration with one click.</span>
        <button class="btn" onclick={doRollback}>Roll back</button>
      </div>
    {/if}
  {/if}
  {#snippet footer()}
    {#if phase === 'result' || phase === 'failed'}
      <button class="btn btn-primary" onclick={() => (open = false)}>Close</button>
    {:else}
      {#if (phase === 'ask' && idx > 0) || phase === 'review'}<button class="btn" onclick={back}>Back</button>{/if}
      <button class="btn" onclick={() => (open = false)} disabled={phase === 'applying' || phase === 'planning'}>Cancel</button>
      {#if phase === 'ask'}<button class="btn btn-primary" disabled={!!err} onclick={next}>{idx === WIZARD_STEPS.length - 2 ? 'Review' : 'Next'}</button>
      {:else if phase === 'review' || phase === 'applying'}<button class="btn btn-primary" disabled={!canApply || phase === 'applying'} onclick={apply}>Apply</button>{/if}
    {/if}
  {/snippet}
</Dialog>
