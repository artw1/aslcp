<script lang="ts">
  // The settings of one available network, built from the server's option specs (lib/dvnetform.ts):
  // Enable switch, master address (BrandMeister and DMR - Other: picked from the master list), port, password (write-only: "Set" or "Change"), options, login state.
  import { untrack } from 'svelte';
  import { api } from '../lib/api';
  import { toast, toastError } from '../lib/toast.svelte';
  import type { DVIdentity, DVListItem, DVNetView } from '../lib/types';
  import { buildPut, enableBlock, formError, formOf, hasChanges, loginText, loginTone, MASTER_OTHER, applyMaster, masterChoice, masterListMode, passwordUx, liveLogin, savedMessage, nxdnOffNote } from '../lib/dvnetform';
  import { app } from '../lib/store.svelte';
  import Toggle from './Toggle.svelte';

  let { v, identity, onsaved }: { v: DVNetView; identity: DVIdentity | null; onsaved: (n: DVNetView) => void } = $props();
  let f = $state(untrack(() => formOf(v))), busy = $state(false), editPw = $state(false), showAdv = $state(false);
  let seen = untrack(() => v);
  // a refreshed view (after a save, or another admin's change) resets the form
  $effect(() => { if (v !== seen) { seen = v; f = formOf(v); editPw = false; choice = masterChoice(f.address, untrack(() => masters) ?? []); } });

  // BrandMeister and DMR - Other: the master comes from a master list (server side, the DMR host list). null: not loaded yet.
  let masters = $state<DVListItem[] | null>(null), choice = $state('');
  const listMode = untrack(() => masterListMode(v.mode));
  const isDMR = untrack(() => v.mode === 'DMR');
  const hasPicker = listMode !== null;
  const listed = $derived(masters !== null && masters.length > 0);
  $effect(() => {
    if (!listMode) return;
    api.dvList(listMode).then((l) => { masters = l.items; choice = masterChoice(f.address, l.items); }).catch(() => { masters = []; });
  });
  function pick(c: string) {
    choice = c;
    if (c === MASTER_OTHER) { if ((masters ?? []).some((m) => m.value === f.address)) f.address = ''; return; }
    const m = (masters ?? []).find((x) => x.value === c);
    if (m) f = applyMaster(v, f, m); else f.address = c;
  }
  const showOther = $derived(hasPicker && masters !== null && (!listed || choice === MASTER_OTHER));

  const id = $derived(`dvn-${v.mode.toLowerCase()}`);
  const pw = $derived(passwordUx(v.passwordSet, editPw));
  const err = $derived(formError(v, f));
  const block = $derived(f.enabled ? enableBlock(v, f, identity) : '');
  const offNote = $derived(nxdnOffNote(v, f));
  const put = $derived(buildPut(v, f));
  const live = $derived(liveLogin(v, app.dv?.modes));
  const login = $derived(v.enabled ? loginText(live.login, live.detail) : '');
  const basic = $derived(v.options.filter((o) => !o.advanced));
  const adv = $derived(v.options.filter((o) => o.advanced));

  async function save() {
    busy = true;
    try {
      const sent = put, wasOn = v.enabled;
      const r = await api.dvNetworkPut(v.mode, sent);
      onsaved(r);
      toast(savedMessage(v.label, sent, r.applied === false ? r.applyError : undefined, wasOn && !r.enabled), 'info');
    } catch (e) { toastError(e); } finally { busy = false; }
  }
</script>
{#snippet opt(o: import('../lib/types').DVOptSpec)}
  <div>
    <label class="label" for="{id}-{o.key}">{o.label}</label>
    {#if o.kind === 'bool'}
      <select id="{id}-{o.key}" class="input" bind:value={f.options[o.key]}>
        <option value="">Default ({o.default === 'true' ? 'on' : 'off'})</option><option value="true">On</option><option value="false">Off</option>
      </select>
    {:else}
      <input id="{id}-{o.key}" class="input font-mono" bind:value={f.options[o.key]} placeholder={o.default} inputmode={o.kind === 'int' ? 'numeric' : undefined} maxlength={o.kind === 'text' ? (o.maxLen ?? 32) : undefined} autocomplete="off" />
    {/if}
    {#if o.help}<p class="mt-0.5 text-xs text-muted">{o.help}</p>{/if}
  </div>
{/snippet}
<div class="space-y-3" data-testid="{id}-form">
  <div class="flex flex-wrap items-center gap-2">
    <span class="inline-flex items-center gap-2 text-sm"><Toggle checked={f.enabled} disabled={busy} label="Enable {v.label}" onchange={(on) => (f.enabled = on)} />Enable</span>
    {#if login}<span class="badge {loginTone(live.login) === 'ok' ? 'border-ok text-ok' : loginTone(live.login) === 'err' ? 'border-danger text-danger' : ''}" role="status" data-testid="{id}-login">{login}</span>{/if}
  </div>
  {#if v.help}<p class="text-xs text-muted" data-testid="{id}-help">{v.help}</p>{/if}
  <div class="grid grid-cols-2 gap-3">
    {#if v.fields.address && hasPicker}
      <div class="col-span-2 space-y-2">
        {#if listed}
          <div><label class="label" for="{id}-master">Master</label>
            <select id="{id}-master" class="input" value={choice} onchange={(e) => pick(e.currentTarget.value)} data-testid="{id}-master">
              {#if choice === ''}<option value="">Choose a master...</option>{/if}
              {#each masters ?? [] as m (m.value)}<option value={m.value}>{m.name}</option>{/each}
              <option value={MASTER_OTHER}>Other...</option>
            </select></div>
        {:else if masters !== null}
          <p class="text-xs text-muted" data-testid="{id}-nolist">The {isDMR ? 'DMR' : 'BrandMeister'} master list has not been downloaded yet.</p>
        {/if}
        {#if showOther}
          <div><label class="label" for="{id}-address">Other address</label>
            <input id="{id}-address" class="input font-mono" bind:value={f.address} autocomplete="off" spellcheck="false" /></div>
        {/if}
      </div>
    {:else if v.fields.address}
      <div class="col-span-2 sm:col-span-1"><label class="label" for="{id}-address">Master address</label>
        <input id="{id}-address" class="input font-mono" bind:value={f.address} placeholder={v.defaultAddress ?? ''} autocomplete="off" spellcheck="false" /></div>
    {/if}
    {#if v.fields.port && (!isDMR || showOther)}
      <div class="col-span-2 sm:col-span-1"><label class="label" for="{id}-port">Port</label>
        <input id="{id}-port" class="input font-mono" bind:value={f.port} inputmode="numeric" maxlength="5" placeholder={v.defaultPort ? String(v.defaultPort) : ''} /></div>
    {/if}
    {#if v.fields.password}
      <div class="col-span-2">
        <label class="label" for="{id}-password">Password</label>
        <div class="flex flex-wrap items-center gap-2">
          {#if pw.show}
            <input id="{id}-password" type="password" class="input min-w-0 flex-1 font-mono" bind:value={f.password} autocomplete="new-password" maxlength="64" placeholder={v.passwordSet ? 'New password' : (v.defaultPassword ?? '')} />
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
  {#if err}<p class="text-sm text-danger" role="alert">{err}</p>{:else if block}<p class="text-sm text-warn" data-testid="{id}-block">{block}</p>{:else if offNote}<p class="text-sm text-warn" data-testid="{id}-offnote">{offNote}</p>{/if}
  <button type="button" class="btn btn-primary" disabled={busy || !!err || !!block || !hasChanges(put)} onclick={save} data-testid="{id}-save">Save</button>
</div>
