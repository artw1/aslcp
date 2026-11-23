<script lang="ts">
  import { untrack } from 'svelte';
  import { advancedLink, echolinkRequest, fieldById, initialDraft, keysForForm, draftInvalid, type NodeModel, type SchemaDoc } from '../../lib/nodecfg';
  import FormShell from './FormShell.svelte';
  import FieldRow from './FieldRow.svelte';

  let { node, doc, onclose, ondone }: { node: NodeModel; doc: SchemaDoc; onclose: () => void; ondone: () => void } = $props();
  const all = untrack(() => keysForForm(doc, 'echolink').filter((k) => k.name !== 'astnode'));
  const main = all.filter((k) => ['call', 'pwd', 'name', 'qth', 'email', 'maxstns'].includes(k.name));
  const more = all.filter((k) => !main.includes(k));
  const fields = untrack(() => node.echolink.fields ?? []);
  let draft = $state(untrack(() => initialDraft(fields, all)));
  let enable = $state(untrack(() => node.echolink.enabled || (node.echolink.linked && node.echolink.configured)));
  const bad = $derived(draftInvalid(fields, all, draft));
  const el = $derived(node.echolink);
  const moduleOn = $derived(/^(explicit|auto)$/.test(el.moduleState));
</script>

<FormShell title="EchoLink, node {node.node}" canReview={!bad} build={() => echolinkRequest(node, all, draft, enable)} {onclose} {ondone}
  intro="Lets EchoLink stations connect to this node. Needs an EchoLink account for your callsign.">
  {#snippet children()}
    {#if !el.present}<p class="rounded border border-warn/50 bg-warn/10 p-2 text-sm">echolink.conf does not exist on this system. <a class="underline" href={'#' + advancedLink('echolink.conf')}>Open it in Advanced</a></p>{/if}
    <label class="flex items-start gap-2 text-sm"><input type="checkbox" bind:checked={enable} class="mt-1" />
      <span><span class="font-medium">Use EchoLink on node {node.node}</span>
        <span class="block text-xs text-muted">Points echolink.conf at this node{#if !moduleOn} and turns on the EchoLink driver (chan_echolink.so in modules.conf){/if}. {el.linked ? 'It already points here.' : 'It does not point here yet.'}</span></span></label>
    {#each main as k (k.id)}
      <FieldRow {k} f={fieldById(fields, k.id)} bind:value={draft[k.id]} />
    {/each}
    <details class="rounded border border-line p-2">
      <summary class="cursor-pointer text-sm font-medium">More details shown on the EchoLink directory</summary>
      <div class="mt-2 grid gap-3">
        {#each more as k (k.id)}<FieldRow {k} f={fieldById(fields, k.id)} bind:value={draft[k.id]} />{/each}
      </div>
    </details>
    <p class="text-xs text-muted">These settings are shared by every node on this system (one echolink.conf), so there is no per-node override.</p>
  {/snippet}
</FormShell>
