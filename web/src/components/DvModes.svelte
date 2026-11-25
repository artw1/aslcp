<script lang="ts">
  // The DV tab's mode buttons card: one button per network with its state and login status (dot and text). An admin
  // clicks a button to open that one mode's panel in place, below the buttons: the Enable switch (saved at once, with the
  // same checks as the setup) and the mode's settings (DvNet, the setup's per-network component). One panel at a time;
  // clicking the open button closes it. A viewer sees the status only.
  import { api } from '../lib/api';
  import { toast, toastError } from '../lib/toast.svelte';
  import type { DVIdentity, DVNetView, DVSnapshot, DVTuneMode } from '../lib/types';
  import { cardState, MODE_NAME, UNAVAILABLE_TEXT } from '../lib/dvmodes';
  import { enableBlock, formOf } from '../lib/dvnetform';
  import { enableToast } from '../lib/dvsummary';
  import { modeButtonLabel, netChip, stripModes } from '../lib/dvpanel';
  import Toggle from './Toggle.svelte';
  import DvNet from './DvNet.svelte';

  let { nets, identity, modes, admin, onsaved }: { nets: DVNetView[]; identity: DVIdentity | null; modes: DVSnapshot['modes']; admin: boolean; onsaved: (n: DVNetView) => void } = $props();
  let open = $state<DVTuneMode | ''>(''), busy = $state(false), block = $state<string>('');
  const list = $derived(stripModes(modes));
  const openNet = $derived(nets.find((n) => n.mode === open));
  const DOT = { ok: 'bg-ok', err: 'bg-danger', warn: 'bg-warn', off: 'bg-muted' } as const;
  const TXT = { ok: 'text-ok', err: 'text-danger', warn: 'text-warn', off: 'text-muted' } as const;

  function toggle(id: DVTuneMode) { open = open === id ? '' : id; block = ''; }

  // The same rules as the setup's row switch: enabling is checked like the form; when something is missing the panel says what and the switch stays off.
  async function setEnabled(v: DVNetView, on: boolean) {
    if (on) {
      const text = enableBlock(v, formOf(v), identity);
      if (text) { block = text; return; }
    }
    block = ''; busy = true;
    try {
      const r = await api.dvNetworkPut(v.mode, { enabled: on });
      onsaved(r);
      toast(enableToast(v.label, r.enabled, r.applied === false ? r.applyError : undefined), 'info');
    } catch (e) { toastError(e); } finally { busy = false; }
  }
</script>

<section class="card p-4" aria-label="Modes" data-testid="dv-modes">
  <h2 class="text-base font-semibold">Modes</h2>
  <p class="mt-0.5 text-xs text-muted">{admin ? 'The state of each network. Open one for its switch and settings.' : 'The state of each network.'}</p>
  <ul class="mt-3 flex flex-wrap gap-2" data-testid="dv-logins">
    {#each list as m (m.id)}
      {@const c = netChip(m)}
      {@const isOpen = open === m.id}
      <li>
        {#snippet face()}
          <span class="size-2 shrink-0 rounded-full {DOT[c.tone]}" aria-hidden="true"></span>
          <span class="font-medium">{modeButtonLabel(m)}</span><span class={TXT[c.tone]}>{c.text}</span>
        {/snippet}
        {#if admin}
          <button type="button" class="btn {isOpen ? 'border-accent bg-surface-2 ring-1 ring-accent' : ''}" aria-pressed={isOpen} aria-controls="dv-mode-panel" title={m.detail || undefined} data-testid="dv-chip-{m.id.toLowerCase()}" onclick={() => toggle(m.id)}>{@render face()}</button>
        {:else}
          <span class="btn cursor-default hover:bg-surface" title={m.detail || undefined} data-testid="dv-chip-{m.id.toLowerCase()}">{@render face()}</span>
        {/if}
      </li>
    {/each}
  </ul>
  {#if admin && open}
    {@const c = cardState(modes, open)}
    <div id="dv-mode-panel" class="mt-3 rounded-md border border-line bg-surface-2 p-3" data-testid="dv-mode-panel" data-mode={open}>
      <div class="mb-2 flex items-center justify-between gap-2">
        <h3 class="text-sm font-medium">{MODE_NAME[open]}</h3>
        {#if openNet && c.available}<Toggle tap checked={openNet.enabled} disabled={busy} label="Enable {MODE_NAME[open]}" onchange={(on) => setEnabled(openNet, on)} />{/if}
      </div>
      {#if !c.available}
        <p class="text-xs text-muted">{UNAVAILABLE_TEXT}</p>
      {:else if openNet}
        {#if block}<p class="mb-2 text-sm text-warn" role="alert" data-testid="dvn-{open.toLowerCase()}-block">{block}</p>{/if}
        {#key open}<DvNet v={openNet} {identity} {onsaved} />{/key}
      {:else}
        <p class="text-xs text-muted">The settings are not loaded.</p>
      {/if}
    </div>
  {/if}
</section>
