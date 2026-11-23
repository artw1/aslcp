<script lang="ts">
  // "Change node number": every place the planner owns, plus savenode.conf and allmon3.ini through the helper;
  // what it cannot change is listed on the review page (docs/CONFIG.md, "Change the node number").
  import { api } from '../../lib/api';
  import type { NodeModel } from '../../lib/nodecfg';
  import { nodeKind } from '../../lib/nodesetup';
  import { emptyRenumber, radioWizardBlock, renumberBody, renumberError, renumberKindNote, renumberNeedsPassword } from '../../lib/nodewizards';
  import Dialog from '../Dialog.svelte';
  import WizardFlow from './WizardFlow.svelte';

  // The overview cannot tell which node savenode.conf holds, so the operator says so (the checkbox).
  let { node, nodes, onclose, ondone }: { node: NodeModel; nodes: NodeModel[]; onclose: () => void; ondone: () => void } = $props();

  let open = $state(true), flow = $state(false), changed = false;
  let d = $state(emptyRenumber());
  $effect(() => { if (!open && !flow) onclose(); });

  const registered = $derived(node.registration.method !== 'none');
  const existing = $derived(nodes.map((n) => n.node));
  const err = $derived(renumberError(node.node, d, registered, existing));
  const body = $derived(renumberBody(node.node, d, registered));
  const note = $derived(renumberKindNote(node.node, d, registered));
  const blocked = $derived(radioWizardBlock(node));
  const kind = $derived(nodeKind(d.node));

  function next() { if (!err) { flow = true; open = false; } }
  const plan = async () => (await api.nodeRenumberPlan(body)).plan;
  const apply = (base: Record<string, string>, message: string | undefined) => api.nodeRenumberApply(body, { base, message });
</script>

<Dialog bind:open title="Change node number, node {node.node}" xl>
  {#if blocked}
    <p class="rounded border border-warn/50 bg-warn/10 p-2 text-sm" role="alert">This wizard does not change this node. {blocked}</p>
  {:else}
    <p class="mb-3 text-sm text-muted">Changes node {node.node} to a new number everywhere ASLCP can: the rpt.conf section and [nodes] line, the SimpleUSB device section, the registration, EchoLink and the dialplan, plus savenode.conf (when you say it is this node) and Allmon3. Asterisk is restarted. The review page lists what it cannot change.</p>
    <div class="grid gap-3 sm:grid-cols-2">
      <div class="min-w-0">
        <label class="label" for="rn-node">New node number</label>
        <input id="rn-node" class="input font-mono" inputmode="numeric" bind:value={d.node} autocomplete="off" aria-invalid={d.node && err ? 'true' : undefined} />
        <p class="mt-0.5 text-xs text-muted">{kind === 'private' ? 'Private node (1 to 1999): not registered with AllStarLink.' : kind === 'public' ? 'Public node (2000 and up): registered with AllStarLink.' : '1 to 1999 is private, 2000 and up is public.'}</p>
      </div>
      <div class="min-w-0">
        <label class="label" for="rn-pass">New node password{renumberNeedsPassword(registered, d) ? '' : ' (not needed)'}</label>
        <input id="rn-pass" class="input font-mono" type="password" bind:value={d.password} autocomplete="new-password" spellcheck="false" />
        <p class="mt-0.5 text-xs text-muted">Needed to register a public node under the new number and to write savenode.conf. Never shown again.</p>
      </div>
    </div>
    <label class="mt-3 flex items-start gap-2 text-sm"><input type="checkbox" bind:checked={d.savenode} class="mt-1" /><span><span class="font-medium">This is the node saved in savenode.conf</span><span class="block text-xs text-muted">The node that the AllStarLink setup tool (node-setup) manages. Updates NODE and PASSWORD there.</span></span></label>
    {#if note}<p class="mt-3 text-sm" role="status">{note}</p>{/if}
    {#if d.node && err}<p class="mt-3 text-xs text-danger" role="alert">{err}</p>{/if}
  {/if}
  {#snippet footer()}
    <button class="btn" onclick={() => (open = false)}>Cancel</button>
    {#if !blocked}<button class="btn btn-primary" disabled={!!err} onclick={next}>Review</button>{/if}
  {/snippet}
</Dialog>

{#if flow}
  <WizardFlow title="Change node number" doneTitle="Node number changed" failTitle="Cannot change the node number" message="Change node number {node.node} to {d.node.trim()}" {plan} {apply}
    intro={(p) => `Node ${p.oldNode} becomes node ${p.node} everywhere it appears in the files below.`}
    footnote="Allmon3 is updated too when it is installed."
    rollbackNote="Not what you wanted? Restore the previous configuration, savenode.conf and Allmon3 with one click."
    rolledBackText="Everything is back as it was before the change, including savenode.conf and Allmon3."
    registrationNode={kind === 'public' && registered ? d.node.trim() : ''}
    onback={() => { flow = false; open = true; }} onclose={() => { flow = false; if (!changed) open = true; else onclose(); }} ondone={() => { changed = true; ondone(); }} />
{/if}
