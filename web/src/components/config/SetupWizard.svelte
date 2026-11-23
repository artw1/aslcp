<script lang="ts">
  // "Set up this node": the questions. Review, apply, health, registration check and rollback are WizardFlow's
  // (shared with the other wizards); the server decides the order (docs/API.md, "Node setup").
  import { api } from '../../lib/api';
  import type { Preset } from '../../lib/nodecfg';
  import { STEP_TITLES, WIZARD_STEPS, emptyDraft, nodeKind, presetsFor, setupBody, stepError, type WizardStep } from '../../lib/nodesetup';
  import Dialog from '../Dialog.svelte';
  import WizardFlow from './WizardFlow.svelte';
  import SetupQuestions from './SetupQuestions.svelte';

  let { oldNode, presets, onclose, ondone }: {
    /** The sample node being set up (the overview's placeholder, or the selected node when restarted). */
    oldNode: string; presets: Preset[]; onclose: () => void; ondone: () => void;
  } = $props();

  let open = $state(true), flow = $state(false), changed = false;
  let step = $state<WizardStep>('node');
  // svelte-ignore state_referenced_locally
  let d = $state(emptyDraft(oldNode));
  let amiToken = $state('');
  $effect(() => { if (!open && !flow) onclose(); });

  const kind = $derived(nodeKind(d.node));
  const pubPresets = $derived(presetsFor(presets, d.radio));
  const idx = $derived(WIZARD_STEPS.indexOf(step));
  const err = $derived(stepError(step, d, presets));
  const body = $derived(setupBody(oldNode, d));
  $effect(() => { if (!pubPresets.some((p) => p.name === d.preset) && pubPresets[0]) d.preset = pubPresets[0].name; });
  // The generated AMI secret belongs to one plan: any answer that changes the request starts a new one.
  $effect(() => { void JSON.stringify(body); amiToken = ''; });

  function next() {
    if (err) return;
    if (idx >= WIZARD_STEPS.length - 2) { flow = true; open = false; } else step = WIZARD_STEPS[idx + 1];
  }
  function back() { if (idx > 0) step = WIZARD_STEPS[idx - 1]; }
  const plan = async () => { const r = await api.nodeSetupPlan(body, amiToken || undefined); amiToken = r.amiToken ?? ''; return r.plan; };
  const apply = (base: Record<string, string>, message: string | undefined) => api.nodeSetupApply(body, { amiToken, base, message });
</script>

<Dialog bind:open title="Set up this node: {STEP_TITLES[step]}" xl>
    <p class="mb-3 text-xs text-muted" role="status">Step {idx + 1} of {WIZARD_STEPS.length}. Nothing is changed until you apply on the last page.</p>
    <SetupQuestions {step} bind:d {presets} />
    {#if err}<p class="mt-3 text-xs text-danger" role="alert">{err}</p>{/if}
  {#snippet footer()}
    {#if idx > 0}<button class="btn" onclick={back}>Back</button>{/if}
    <button class="btn" onclick={() => (open = false)}>Cancel</button>
    <button class="btn btn-primary" disabled={!!err} onclick={next}>{idx === WIZARD_STEPS.length - 2 ? 'Review' : 'Next'}</button>
  {/snippet}
</Dialog>

{#if flow}
  <WizardFlow title="Set up this node" doneTitle="Node set up" failTitle="Cannot set up" message="Setup wizard: node {d.node.trim()}" {plan} {apply}
    intro={(p) => `${p.renumber ? `Node ${p.oldNode} becomes node ${p.node} everywhere it appears.` : `Node ${p.node} keeps its number.`} A new AMI secret is generated and kept on the server; it is never shown.`}
    footnote="savenode.conf and Allmon3 are updated too when they exist on this system."
    rollbackNote="Not what you wanted? Restore the previous configuration with one click."
    rolledBackText="Everything is back as it was before the setup, including savenode.conf and the AMI secret."
    registrationNode={kind === 'public' ? d.node.trim() : ''}
    onback={() => { flow = false; open = true; }} onclose={() => { flow = false; if (!changed) open = true; else onclose(); }} ondone={() => { changed = true; ondone(); }} />
{/if}
