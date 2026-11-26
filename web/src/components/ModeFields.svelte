<script lang="ts">
  // The settings form of one mode, drawn from the fields the server lists. A password field stays empty and shows "set"
  // when a value is saved; leave it empty to keep it.
  import type { ModeInfo } from '../lib/types';
  let { mode, draft = $bindable(), idPrefix = 'mf' }: { mode: ModeInfo; draft: Record<string, string>; idPrefix?: string } = $props();
  const fields = $derived(mode.fields ?? []);
  const basic = $derived(fields.filter((f) => !f.advanced));
  const adv = $derived(fields.filter((f) => f.advanced));
  const id = (k: string) => `${idPrefix}-${mode.name}-${k}`;
  const chk = 'size-4 accent-[var(--accent)]';
</script>

{#snippet field(f: (typeof fields)[number])}
  {#if f.kind === 'bool'}
    <label class="flex cursor-pointer items-start gap-2 text-sm sm:col-span-2"><input type="checkbox" class="mt-0.5 {chk}" checked={draft[f.key] === 'true'} onchange={(e) => (draft[f.key] = e.currentTarget.checked ? 'true' : 'false')} /><span>{f.label}{#if f.help}<span class="block text-xs text-muted">{f.help}</span>{/if}</span></label>
  {:else}
    <div>
      <label class="label" for={id(f.key)}>{f.label}{#if f.required}<span class="text-danger" aria-hidden="true"> *</span>{/if}</label>
      {#if f.kind === 'select'}
        <select id={id(f.key)} class="input" bind:value={draft[f.key]}>{#each f.options ?? [] as o}<option value={o}>{o}</option>{/each}</select>
      {:else if (f.kind === 'password' || f.secret)}
        <input id={id(f.key)} class="input" type="password" autocomplete="new-password" bind:value={draft[f.key]} placeholder={mode.secretsSet?.[f.key] ? 'set' : ''} required={f.required && !mode.secretsSet?.[f.key]} />
      {:else}
        <input id={id(f.key)} class="input {f.kind === 'number' ? 'tabular-nums' : ''}" type={f.kind === 'number' ? 'number' : 'text'} autocomplete="off" bind:value={draft[f.key]} placeholder={f.default ?? ''} required={f.required} />
      {/if}
      {#if f.help}<p class="mt-1 text-xs text-muted">{f.help}</p>{/if}
      {#if (f.kind === 'password' || f.secret) && mode.secretsSet?.[f.key]}<p class="mt-1 text-xs text-muted">Saved. Leave empty to keep it.</p>{/if}
    </div>
  {/if}
{/snippet}

<div class="grid gap-3 sm:grid-cols-2">
  {#each basic as f (f.key)}{@render field(f)}{/each}
</div>
{#if adv.length}
  <details class="mt-3 rounded border border-line">
    <summary class="cursor-pointer px-2 py-1 text-sm font-medium">Advanced</summary>
    <div class="grid gap-3 p-2 sm:grid-cols-2">{#each adv as f (f.key)}{@render field(f)}{/each}</div>
  </details>
{/if}
