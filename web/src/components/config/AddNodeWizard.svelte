<script lang="ts">
  // "Add node": a second local node that inherits the template. The questions are the setup wizard's (SetupQuestions);
  // review, apply, health and rollback are WizardFlow's (docs/CONFIG.md, "Add a node").
  import { api } from '../../lib/api';
  import type { NodeModel, Preset } from '../../lib/nodecfg';
  import { STEP_TITLES, emptyDraft, nodeKind, presetsFor } from '../../lib/nodesetup';
  import { ADD_STEPS, addNodeBody, addStepError, type AddStep } from '../../lib/nodewizards';
  import Dialog from '../Dialog.svelte';
  import WizardFlow from './WizardFlow.svelte';
  import SetupQuestions from './SetupQuestions.svelte';

  let { nodes, from, presets, onclose, ondone }: {
    /** The local nodes today; the new one inherits the template of `from`. */
    nodes: NodeModel[]; from: string; presets: Preset[]; onclose: () => void; ondone: () => void;
  } = $props();

  let open = $state(true), flow = $state(false), changed = false;
  let step = $state<AddStep>('node');
  let d = $state(emptyDraft(''));
  $effect(() => { if (!open && !flow) onclose(); });

  const existing = $derived(nodes.map((n) => n.node));
  const pubPresets = $derived(presetsFor(presets, d.radio));
  const idx = $derived(ADD_STEPS.indexOf(step));
  const err = $derived(addStepError(step, d, presets, existing));
  const body = $derived(addNodeBody(d, from));
  const template = $derived(nodes.find((n) => n.node === from)?.stanza?.template ?? '');
  $effect(() => { if (!pubPresets.some((p) => p.name === d.preset) && pubPresets[0]) d.preset = pubPresets[0].name; });

  function next() {
    if (err) return;
    if (idx >= ADD_STEPS.length - 2) { flow = true; open = false; } else step = ADD_STEPS[idx + 1];
  }
  function back() { if (idx > 0) step = ADD_STEPS[idx - 1]; }
  const plan = async () => (await api.nodeAddPlan(body)).plan;
  const apply = (base: Record<string, string>, message: string | undefined) => api.nodeAddApply(body, { base, message });
</script>

<Dialog bind:open title="Add a node: {STEP_TITLES[step]}" xl>
  <p class="mb-3 text-xs text-muted" role="status">Step {idx + 1} of {ADD_STEPS.length}. Nothing is changed until you apply on the last page.</p>
  {#if step === 'node'}
    <p class="mb-3 text-sm text-muted">Adds a second node to this system, with its own number{template ? `, using the settings of template ${template} like node ${from}` : `, starting from a copy of node ${from}'s basics`}. Two radio nodes need two USB adapters.</p>
  {/if}
  <SetupQuestions {step} bind:d {presets} variant="add" />
  {#if err}<p class="mt-3 text-xs text-danger" role="alert">{err}</p>{/if}
  {#snippet footer()}
    {#if idx > 0}<button class="btn" onclick={back}>Back</button>{/if}
    <button class="btn" onclick={() => (open = false)}>Cancel</button>
    <button class="btn btn-primary" disabled={!!err} onclick={next}>{idx === ADD_STEPS.length - 2 ? 'Review' : 'Next'}</button>
  {/snippet}
</Dialog>

{#if flow}
  <WizardFlow title="Add a node" doneTitle="Node added" failTitle="Cannot add the node" message="Add node {d.node.trim()}" {plan} {apply}
    intro={(p) => `Node ${p.node} is added; the existing nodes keep their settings. Asterisk is restarted so the new node starts.`}
    footnote="Nothing else (savenode.conf, Allmon3) is touched."
    rollbackNote="Not what you wanted? Remove the new node again with one click (restores the previous configuration)."
    rolledBackText="Everything is back as it was before the new node was added."
    registrationNode={nodeKind(d.node) === 'public' ? d.node.trim() : ''}
    onback={() => { flow = false; open = true; }} onclose={() => { flow = false; if (!changed) open = true; else onclose(); }} ondone={() => { changed = true; ondone(); }} />
{/if}
