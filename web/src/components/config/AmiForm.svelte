<script lang="ts">
  // Manager interface (manager.conf). The [admin] password can be replaced but is never shown; ASLCP's own login follows it.
  import { untrack } from 'svelte';
  import { fieldById, initialDraft, keysForForm, draftInvalid, type AMIInfo, type SchemaDoc } from '../../lib/nodecfg';
  import { amiRequest, bindWarning, randomSecret } from '../../lib/nodewizards';
  import FormShell from './FormShell.svelte';
  import FieldRow from './FieldRow.svelte';
  import TriangleAlert from '@lucide/svelte/icons/triangle-alert';

  let { node, ami, doc, onclose, ondone }: { node: string; ami: AMIInfo; doc: SchemaDoc; onclose: () => void; ondone: () => void } = $props();
  const keys = untrack(() => keysForForm(doc, 'ami'));
  const fields = untrack(() => ami.fields ?? []);
  const secretKey = keys.find((k) => k.secret)!;
  const rest = keys.filter((k) => !k.secret);
  const bindKey = keys.find((k) => k.name === 'bindaddr');
  let draft = $state(untrack(() => initialDraft(fields, keys)));
  let generated = $state(false);
  const bad = $derived(draftInvalid(fields, keys, draft));
  const bindNow = $derived(bindKey ? (draft[bindKey.id] ?? '') : '');
  const warn = $derived(bindWarning(bindNow));
  const off = $derived(draft['manager.conf:general:enabled'] === 'no');
  function generate() { draft[secretKey.id] = randomSecret(); generated = true; }
  function clear() { draft[secretKey.id] = ''; generated = false; }
</script>

<FormShell title="Manager interface (AMI)" canReview={!bad} build={() => amiRequest(node, fields, keys, draft)} {onclose} {ondone}
  intro="The login that ASLCP, Allmon3 and other programs use to talk to Asterisk. Changing it wrongly can lock them out, so every change is shown first.">
  {#snippet children()}
    {#if !ami.present}<p class="rounded border border-warn/50 bg-warn/10 p-2 text-sm">manager.conf does not exist on this system.</p>{/if}
    {#if off}<p class="flex items-start gap-1.5 text-sm text-danger" role="alert"><TriangleAlert size={15} class="mt-0.5 shrink-0" /><span>Turning the manager interface off stops ASLCP and the other tools. The review will refuse it.</span></p>{/if}
    {#each rest as k (k.id)}
      <div class="min-w-0">
        <FieldRow {k} f={fieldById(fields, k.id)} bind:value={draft[k.id]} />
        {#if k.name === 'bindaddr' && warn}<p class="mt-1 flex items-start gap-1.5 text-xs text-warn" role="status"><TriangleAlert size={14} class="mt-0.5 shrink-0" /><span>{warn}</span></p>{/if}
      </div>
    {/each}
    <div class="min-w-0">
      <FieldRow k={secretKey} f={fieldById(fields, secretKey.id)} bind:value={draft[secretKey.id]} />
      <div class="mt-1 flex flex-wrap items-center gap-2">
        <button type="button" class="btn btn-sm" onclick={generate}>Generate a random password</button>
        {#if draft[secretKey.id]}<button type="button" class="btn btn-sm" onclick={clear}>Keep the current one</button>{/if}
        {#if generated}<span class="text-xs text-muted">A new random password is ready. It is not shown anywhere.</span>{/if}
      </div>
      <p class="mt-1 text-xs text-muted">ASLCP follows the new password by itself when it reads its login from manager.conf. Allmon3 (allmon3.ini) and other programs keep the old one until you update them.</p>
    </div>
  {/snippet}
</FormShell>
