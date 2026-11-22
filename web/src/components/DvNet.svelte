<script lang="ts">
  // The settings of one available network, built from the server's option specs (lib/dvnetform.ts):
  // Enable switch, master address, port, password (write-only: "Set" or "Change"), options, login state.
  import { untrack } from 'svelte';
  import { api } from '../lib/api';
  import { toast, toastError } from '../lib/toast.svelte';
  import type { DVIdentity, DVNetView } from '../lib/types';
  import { buildPut, enableBlock, formError, formOf, hasChanges, loginText, loginTone, passwordUx } from '../lib/dvnetform';
  import Toggle from './Toggle.svelte';

  let { v, identity, onsaved }: { v: DVNetView; identity: DVIdentity | null; onsaved: (n: DVNetView) => void } = $props();
  let f = $state(untrack(() => formOf(v))), busy = $state(false), editPw = $state(false), showAdv = $state(false);
  let seen = untrack(() => v);
  // a refreshed view (after a save, or another admin's change) resets the form
  $effect(() => { if (v !== seen) { seen = v; f = formOf(v); editPw = false; } });

  const id = $derived(`dvn-${v.mode.toLowerCase()}`);
  const pw = $derived(passwordUx(v.passwordSet, editPw));
  const err = $derived(formError(v, f));
  const block = $derived(f.enabled ? enableBlock(v, f, identity) : '');
  const put = $derived(buildPut(v, f));
  const login = $derived(v.enabled ? loginText(v.login, v.detail) : '');
  const basic = $derived(v.options.filter((o) => !o.advanced));
  const adv = $derived(v.options.filter((o) => o.advanced));

  async function save() {
    busy = true;
    try {
      const r = await api.dvNetworkPut(v.mode, put);
      onsaved(r);
      toast(r.applied === false && r.applyError ? `${v.label} saved. ${r.applyError}` : `${v.label} settings saved`, 'info');
    } catch (e) { toastError(e); } finally { busy = false; }
  }
</script>
{#snippet opt(o: import('../lib/types').DVOptSpec)}
  <div>
    <label class="label" for="{id}-{o.key}">{o.label}</label>
    {#if o.kind === 'bool'}
      <select id="{id}-{o.key}" class="input" bind:value={f.options[o.key]}>
        <option value="">Default ({o.default === '1' ? 'on' : 'off'})</option><option value="1">On</option><option value="0">Off</option>
      </select>
    {:else}
      <input id="{id}-{o.key}" class="input font-mono" bind:value={f.options[o.key]} placeholder={o.default} inputmode={o.kind === 'int' ? 'numeric' : undefined} autocomplete="off" />
    {/if}
    {#if o.help}<p class="mt-0.5 text-xs text-muted">{o.help}</p>{/if}
  </div>
{/snippet}
<div class="space-y-3" data-testid="{id}-form">
  <div class="flex flex-wrap items-center gap-2">
    <span class="inline-flex items-center gap-2 text-sm"><Toggle checked={f.enabled} disabled={busy} label="Enable {v.label}" onchange={(on) => (f.enabled = on)} />Enable</span>
    {#if login}<span class="badge {loginTone(v.login) === 'ok' ? 'border-ok text-ok' : loginTone(v.login) === 'err' ? 'border-err text-err' : ''}" role="status" data-testid="{id}-login">{login}</span>{/if}
  </div>
  {#if v.help}<p class="text-xs text-muted" data-testid="{id}-help">{v.help}</p>{/if}
  <div class="grid grid-cols-2 gap-3">
    {#if v.fields.address}
      <div class="col-span-2 sm:col-span-1"><label class="label" for="{id}-address">Master address</label>
        <input id="{id}-address" class="input font-mono" bind:value={f.address} autocomplete="off" spellcheck="false" /></div>
    {/if}
    {#if v.fields.port}
      <div class="col-span-2 sm:col-span-1"><label class="label" for="{id}-port">Port</label>
        <input id="{id}-port" class="input font-mono" bind:value={f.port} inputmode="numeric" maxlength="5" placeholder={v.defaultPort ? String(v.defaultPort) : ''} /></div>
    {/if}
    {#if v.fields.password}
      <div class="col-span-2">
        <label class="label" for="{id}-password">Password</label>
        <div class="flex flex-wrap items-center gap-2">
          {#if pw.show}
            <input id="{id}-password" type="password" class="input min-w-0 flex-1 font-mono" bind:value={f.password} autocomplete="new-password" maxlength="64" placeholder={v.passwordSet ? 'New password' : ''} />
          {:else}
            <span class="text-sm text-muted" id="{id}-password" data-testid="{id}-pw-state">{pw.status}</span>
          {/if}
          {#if v.passwordSet}
            <button type="button" class="btn btn-sm" disabled={busy} onclick={() => { editPw = !editPw; f.password = ''; }}>{pw.action}</button>
            <label class="inline-flex items-center gap-1.5 text-xs"><input type="checkbox" class="size-4" bind:checked={f.clearPassword} disabled={busy} />Remove</label>
          {/if}
        </div>
      </div>
    {/if}
    {#each basic as o (o.key)}<div class="col-span-2 sm:col-span-1">{@render opt(o)}</div>{/each}
  </div>
  {#if adv.length}
    <button type="button" class="btn btn-sm" aria-expanded={showAdv} onclick={() => (showAdv = !showAdv)}>{showAdv ? 'Hide' : 'Show'} advanced options</button>
    {#if showAdv}<div class="grid grid-cols-2 gap-3">{#each adv as o (o.key)}<div class="col-span-2 sm:col-span-1">{@render opt(o)}</div>{/each}</div>{/if}
  {/if}
  {#if err}<p class="text-sm text-err" role="alert">{err}</p>{:else if block}<p class="text-sm text-warn" data-testid="{id}-block">{block}</p>{/if}
  <button type="button" class="btn btn-primary" disabled={busy || !!err || !!block || !hasChanges(put)} onclick={save} data-testid="{id}-save">Save</button>
</div>
