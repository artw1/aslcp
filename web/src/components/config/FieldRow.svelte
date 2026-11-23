<script lang="ts">
  // One setting of a node form: label, control by type, provenance badge and the explicit reset / change-for-all actions.
  import { advancedLink, provenance, validateValue, valueLabel, type Field, type SchemaKey } from '../../lib/nodecfg';

  let { k, f, value = $bindable(''), onreset, ontemplate, hint = '' }: {
    k: SchemaKey; f: Field | undefined; value: string;
    /** "Reset to template" and "Change for all nodes" ask the parent to run that one change. */
    onreset?: (f: Field) => void; ontemplate?: (f: Field, value: string) => void; hint?: string;
  } = $props();
  const id = $derived('fld-' + k.id.replace(/[^\w]/g, '-'));
  const err = $derived(validateValue(k, value, !!f?.set));
  const prov = $derived(f ? provenance(f) : null);
  const editable = $derived(!!f && f.canSet);
  const yes = $derived(k.type === 'yesno' ? ['yes', 'no'] : ['1', '0']);
  const secretSet = $derived(!!k.secret && !!f?.set);
</script>

<div class="min-w-0">
  <div class="flex flex-wrap items-center gap-x-2 gap-y-0.5">
    <label class="label !mb-0" for={id}>{k.label}</label>
    {#if prov}<span class="badge {prov.tone === 'node' ? '!text-accent' : ''}" title={f?.source.file ? `${f.source.file}${f.source.line ? ':' + f.source.line : ''}` : ''}>{prov.label}</span>{/if}
    {#if k.reload === 'restart'}<span class="badge !text-warn" title="Changing this needs an Asterisk restart">needs restart</span>{/if}
  </div>
  {#if k.type === 'enum'}
    <select {id} class="input mt-1" bind:value disabled={!editable}>
      {#each k.values ?? [] as v, i}<option value={v}>{k.labels?.[i] ? `${k.labels[i]} (${v})` : v}</option>{/each}
    </select>
  {:else if k.type === 'yesno' || k.type === 'flag'}
    <select {id} class="input mt-1" bind:value disabled={!editable}>
      <option value={yes[0]}>Yes</option><option value={yes[1]}>No</option>
    </select>
  {:else if k.secret}
    <input {id} class="input mt-1" type="password" autocomplete="new-password" bind:value disabled={!editable} placeholder={secretSet ? 'Set. Type to change' : 'Not set'} />
  {:else}
    <div class="mt-1 flex items-center gap-1.5">
      <input {id} class="input" type="text" inputmode={k.type === 'int' ? 'numeric' : undefined} bind:value disabled={!editable} placeholder={k.optional && !f?.set ? 'Not set' : ''} aria-invalid={err ? 'true' : undefined} />
      {#if k.unit}<span class="shrink-0 text-xs text-muted">{k.unit}</span>{/if}
    </div>
  {/if}
  {#if err}<p class="mt-0.5 text-xs text-danger" role="alert">{err}</p>{/if}
  <p class="mt-0.5 text-xs text-muted">{k.desc}{hint ? ' ' + hint : ''}</p>
  {#if f?.overrides}
    <p class="mt-0.5 text-xs text-muted">This node's value hides the template's: <span class="font-mono">{k.secret ? 'hidden' : valueLabel(k, f.overrides.value)}</span> in [{f.overrides.section}].</p>
  {/if}
  {#if f?.locked}
    <p class="mt-0.5 text-xs text-warn" role="status">{f.locked}. <a class="underline" href={'#' + advancedLink(f.source.file || k.file, f.source.line)}>Edit in Advanced</a></p>
  {:else if f && (f.canReset || f.canTemplate) && (onreset || ontemplate)}
    <div class="mt-1 flex flex-wrap gap-1.5">
      {#if f.canReset && onreset}<button type="button" class="btn btn-sm" onclick={() => onreset?.(f)} title="Remove this node's own line so the template value applies">{f.overrides ? 'Reset to template' : 'Remove from this node'}</button>{/if}
      {#if f.canTemplate && ontemplate}<button type="button" class="btn btn-sm" disabled={!!err || value.trim() === ''} onclick={() => ontemplate?.(f, value)} title="Write the value above into the template, so every node that inherits it changes">Change for all nodes</button>{/if}
    </div>
  {/if}
</div>
