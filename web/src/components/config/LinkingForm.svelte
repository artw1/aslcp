<script lang="ts">
  // Linking and telemetry: the rpt.conf keys of one node that shape links and the telemetry sounds.
  import { untrack } from 'svelte';
  import { fieldById, fieldActionRequest, initialDraft, draftInvalid, draftChanges, type FormRequest, type NodeModel, type SchemaDoc } from '../../lib/nodecfg';
  import { linkingKeys } from '../../lib/nodewizards';
  import FormShell from './FormShell.svelte';
  import FieldRow from './FieldRow.svelte';

  let { node, doc, onclose, ondone }: { node: NodeModel; doc: SchemaDoc; onclose: () => void; ondone: () => void } = $props();
  const keys = untrack(() => linkingKeys(doc.keys));
  const links = keys.filter((k) => ['context', 'startup_macro', 'linkmongain', 'erxgain', 'etxgain'].includes(k.name));
  const telem = keys.filter((k) => !links.includes(k));
  let draft = $state(untrack(() => initialDraft(node.fields, keys)));
  const bad = $derived(draftInvalid(node.fields, keys, draft));
  const build = (): FormRequest | null => { const changes = draftChanges(node.fields, keys, draft); return changes.length ? { node: node.node, mode: 'override', changes } : null; };
</script>

<FormShell title="Linking and telemetry, node {node.node}" canReview={!bad} {build} {onclose} {ondone}
  intro="How this node behaves when linked and how its telemetry sounds (courtesy tones, messages) are played. Changes are written on this node only, unless you use Change for all nodes.">
  {#snippet children(act)}
    <h3 class="text-sm font-semibold">Links</h3>
    {#each links as k (k.id)}
      <FieldRow {k} f={fieldById(node.fields, k.id)} bind:value={draft[k.id]}
        onreset={(f) => act(fieldActionRequest(node.node, 'reset', f.id, ''), `Reset ${k.label}`)}
        ontemplate={(f, v) => act(fieldActionRequest(node.node, 'template', f.id, v), `Change ${k.label} for all nodes`)} />
    {/each}
    <h3 class="text-sm font-semibold">Telemetry</h3>
    {#each telem as k (k.id)}
      <FieldRow {k} f={fieldById(node.fields, k.id)} bind:value={draft[k.id]}
        onreset={(f) => act(fieldActionRequest(node.node, 'reset', f.id, ''), `Reset ${k.label}`)}
        ontemplate={(f, v) => act(fieldActionRequest(node.node, 'template', f.id, v), `Change ${k.label} for all nodes`)} />
    {/each}
  {/snippet}
</FormShell>
