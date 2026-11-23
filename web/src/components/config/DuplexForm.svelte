<script lang="ts">
  import { untrack } from 'svelte';
  import { duplexRequest, fieldById, fieldActionRequest, initialDraft, keysForForm, presetPreview, draftInvalid, type NodeModel, type Preset, type SchemaDoc } from '../../lib/nodecfg';
  import FormShell from './FormShell.svelte';
  import FieldRow from './FieldRow.svelte';

  let { node, doc, onclose, ondone }: { node: NodeModel; doc: SchemaDoc; onclose: () => void; ondone: () => void } = $props();
  const keys = untrack(() => keysForForm(doc, 'duplex', 'timers'));
  const dKeys = keys.filter((k) => k.form === 'duplex'), tKeys = keys.filter((k) => k.form === 'timers');
  let draft = $state(untrack(() => initialDraft(node.fields, keys)));
  let presetName = $state('');
  const preset = $derived<Preset | null>(doc.presets.find((p) => p.name === presetName) ?? null);
  const radioOk = $derived(['simpleusb', 'hub', 'none'].includes(node.radio.type));
  const rows = $derived(preset ? presetPreview(preset, doc, node.fields) : []);
  const bad = $derived(draftInvalid(node.fields, keys, draft));

  function choose(name: string) {
    presetName = name;
    const p = doc.presets.find((x) => x.name === name);
    // Fill the inputs with what the preset sets, so the form shows the result and can still be adjusted.
    if (p) for (const [id, v] of Object.entries(p.values)) draft[id] = v;
  }
</script>

<FormShell title="Duplex and timers, node {node.node}" canReview={!bad} build={() => duplexRequest(node.node, node.fields, keys, draft, preset)} {onclose} {ondone}
  intro="How the radio is used and how long it holds the channel. Changes are written on this node only, unless you use Change for all nodes.">
  {#snippet children(act)}
    <section class="rounded border border-line p-2">
      <label class="label" for="dx-preset">Start from a preset</label>
      <select id="dx-preset" class="input" value={presetName} onchange={(e) => choose(e.currentTarget.value)}>
        <option value="">No preset (set each value below)</option>
        {#each doc.presets as p}<option value={p.name} disabled={p.hub && !radioOk}>{p.label}{p.hub && !radioOk ? ' (this node\'s radio is edited in Advanced)' : ''}</option>{/each}
      </select>
      {#if preset}
        <p class="mt-1 text-xs text-muted">{preset.desc}</p>
        <ul class="mt-1 space-y-0.5 text-xs" aria-label="What the preset sets">
          {#each rows as r}<li><span class="font-medium">{r.label}:</span> {r.same ? `${r.to} (already)` : r.from ? `${r.from} to ${r.to}` : r.to}</li>{/each}
        </ul>
      {/if}
    </section>
    <h3 class="text-sm font-semibold">Duplex and telemetry</h3>
    {#each dKeys as k (k.id)}
      <FieldRow {k} f={fieldById(node.fields, k.id)} bind:value={draft[k.id]}
        onreset={(f) => act(fieldActionRequest(node.node, 'reset', f.id, ''), `Reset ${k.label}`)}
        ontemplate={(f, v) => act(fieldActionRequest(node.node, 'template', f.id, v), `Change ${k.label} for all nodes`)} />
    {/each}
    <h3 class="text-sm font-semibold">Timers</h3>
    {#each tKeys as k (k.id)}
      <FieldRow {k} f={fieldById(node.fields, k.id)} bind:value={draft[k.id]}
        onreset={(f) => act(fieldActionRequest(node.node, 'reset', f.id, ''), `Reset ${k.label}`)}
        ontemplate={(f, v) => act(fieldActionRequest(node.node, 'template', f.id, v), `Change ${k.label} for all nodes`)} />
    {/each}
  {/snippet}
</FormShell>
