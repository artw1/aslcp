<script lang="ts">
  // SimpleUSB device settings (simpleusb.conf, the node's device section). Audio levels are not here: Audio Tuning owns them.
  import { untrack } from 'svelte';
  import { fieldById, initialDraft, keysForForm, draftInvalid, type NodeModel, type SchemaDoc } from '../../lib/nodecfg';
  import { deviceRequest } from '../../lib/nodewizards';
  import FormShell from './FormShell.svelte';
  import FieldRow from './FieldRow.svelte';

  let { node, doc, onclose, ondone }: { node: NodeModel; doc: SchemaDoc; onclose: () => void; ondone: () => void } = $props();
  const keys = untrack(() => keysForForm(doc, 'radio').filter((k) => k.scope === 'device'));
  const fields = untrack(() => node.radio.deviceFields ?? []);
  const detect = keys.filter((k) => ['carrierfrom', 'ctcssfrom', 'rxondelay'].includes(k.name));
  const audio = keys.filter((k) => ['invertptt', 'rxboost', 'preemphasis', 'deemphasis', 'plfilter'].includes(k.name));
  const usb = keys.filter((k) => ['devstr', 'serial'].includes(k.name));
  let draft = $state(untrack(() => initialDraft(fields, keys)));
  const bad = $derived(draftInvalid(fields, keys, draft));
</script>

<FormShell title="Radio device, node {node.node}" canReview={!bad} build={() => deviceRequest(node, keys, draft)} {onclose} {ondone}
  intro="How the USB sound adapter reads the radio's squelch and CTCSS lines and treats the audio. These are written in [{node.radio.device}] of simpleusb.conf.">
  {#snippet children()}
    <p class="rounded border border-line bg-surface-2 p-2 text-sm">Receive and transmit audio levels are tuned under <a class="underline" href="#/tune">Audio Tuning</a>, which also saves them; they are not edited here.</p>
    <h3 class="text-sm font-semibold">Squelch and CTCSS detection</h3>
    {#each detect as k (k.id)}<FieldRow {k} f={fieldById(fields, k.id)} bind:value={draft[k.id]} />{/each}
    <h3 class="text-sm font-semibold">Audio path</h3>
    {#each audio as k (k.id)}<FieldRow {k} f={fieldById(fields, k.id)} bind:value={draft[k.id]} />{/each}
    <h3 class="text-sm font-semibold">Which adapter</h3>
    {#each usb as k (k.id)}<FieldRow {k} f={fieldById(fields, k.id)} bind:value={draft[k.id]} />{/each}
    <p class="text-xs text-muted">The device section belongs to this radio only, so there is no template to reset to. Other settings in the section are shown in Advanced and are never changed here.</p>
  {/snippet}
</FormShell>
