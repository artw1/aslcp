<script lang="ts">
  import { untrack } from 'svelte';
  import { advancedLink, customIdRecording, fieldById, fieldActionRequest, identityRequest, initialDraft, keysForForm, draftInvalid, validCallsign, type NodeModel, type SchemaDoc } from '../../lib/nodecfg';
  import FormShell from './FormShell.svelte';
  import FieldRow from './FieldRow.svelte';

  let { node, doc, onclose, ondone }: { node: NodeModel; doc: SchemaDoc; onclose: () => void; ondone: () => void } = $props();
  const keys = untrack(() => keysForForm(doc, 'identity').filter((k) => k.name !== 'idrecording'));
  let draft = $state(untrack(() => initialDraft(node.fields, keys)));
  let callsign = $state(untrack(() => node.callsign));
  const idrec = $derived(fieldById(node.fields, 'rpt.conf:node:idrecording'));
  const custom = $derived(customIdRecording(idrec));
  const csErr = $derived(callsign.trim() && !validCallsign(callsign) ? 'Letters and digits, for example N0CALL' : '');
  const bad = $derived(!!csErr || draftInvalid(node.fields, keys, draft));
</script>

<FormShell title="Identity, node {node.node}" canReview={!bad} build={() => identityRequest(node, keys, draft, custom ? node.callsign : callsign)} {onclose} {ondone}
  intro="How the node identifies itself on the air. The callsign is sent in Morse code from the identification setting.">
  {#snippet children(act)}
    <div class="min-w-0">
      <label class="label" for="id-callsign">Callsign</label>
      <input id="id-callsign" class="input uppercase" bind:value={callsign} disabled={custom} autocomplete="off" spellcheck="false" placeholder="N0CALL" aria-invalid={csErr ? 'true' : undefined} />
      {#if csErr}<p class="mt-0.5 text-xs text-danger" role="alert">{csErr}</p>{/if}
      {#if custom}
        <p class="mt-0.5 text-xs text-warn">This node's identification names a sound recording ({idrec?.value}), not a Morse callsign, so it is not changed here. <a class="underline" href={'#' + advancedLink('rpt.conf', idrec?.source.line)}>Edit in Advanced</a></p>
      {:else}
        <p class="mt-0.5 text-xs text-muted">Written as <span class="font-mono">idrecording = |i{callsign.trim().toUpperCase() || 'CALL'}</span> on this node{#if idrec?.source.kind === 'template'}, replacing the template's value for this node only{/if}.</p>
      {/if}
    </div>
    {#each keys as k (k.id)}
      <FieldRow {k} f={fieldById(node.fields, k.id)} bind:value={draft[k.id]}
        onreset={(f) => act(fieldActionRequest(node.node, 'reset', f.id, ''), `Reset ${k.label}`)}
        ontemplate={(f, v) => act(fieldActionRequest(node.node, 'template', f.id, v), `Change ${k.label} for all nodes`)} />
    {/each}
  {/snippet}
</FormShell>
