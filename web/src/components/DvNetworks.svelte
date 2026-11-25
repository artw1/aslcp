<script lang="ts">
  // The Networks card of the digital modes setup: a stack of cards, one per network. Each card has the name, the
  // state badge, a one-line summary, the Enable switch (saved at once) and a chevron that opens the settings (DvNet).
  // Only one row is open at a time. The DV tab shows the same component as a card of its own (`card`), without the
  // `?net=` deep link (`deepLink={false}`), and read-only for a viewer (`readonly`: the rows show state, nothing opens or switches).
  import { api } from '../lib/api';
  import { toast, toastError } from '../lib/toast.svelte';
  import type { DVIdentity, DVNetView, DVSnapshot, DVTuneMode } from '../lib/types';
  import { cardState, MODE_NAME, UNAVAILABLE_TEXT } from '../lib/dvmodes';
  import { enableBlock, formOf, liveLogin, loginText, loginTone } from '../lib/dvnetform';
  import { enableToast, netSummary } from '../lib/dvsummary';
  import { NETWORK_MODES } from '../lib/dvnetworks';
  import { router } from '../lib/router.svelte';
  import { tick } from 'svelte';
  import Toggle from './Toggle.svelte';
  import DvNet from './DvNet.svelte';
  import ChevronDown from '@lucide/svelte/icons/chevron-down';

  let { nets, identity, modes, onsaved, card = false, deepLink = true, readonly = false }: { nets: DVNetView[]; identity: DVIdentity | null; modes: DVSnapshot['modes'] | undefined; onsaved: (n: DVNetView) => void; card?: boolean; deepLink?: boolean; readonly?: boolean } = $props();
  let open = $state<DVTuneMode | ''>(''), busy = $state<DVTuneMode | ''>(''), block = $state<{ id: DVTuneMode; text: string } | null>(null);
  const netOf = (id: DVTuneMode) => nets.find((n) => n.mode === id);

  // `#/digital?net=ysf` (a link into the setup) opens that network's settings and scrolls to it, once.
  let deepLinked = '';
  $effect(() => {
    if (!deepLink) return;
    const id = NETWORK_MODES.find((m) => m.toLowerCase() === new URLSearchParams(router.query).get('net')?.toLowerCase());
    if (!id || deepLinked === id) return;
    deepLinked = id;
    open = id;
    void tick().then(() => document.getElementById(`dv-row-${id.toLowerCase()}`)?.scrollIntoView({ block: 'start' }));
  });

  function toggleRow(id: DVTuneMode) { if (readonly) return; open = open === id ? '' : id; if (block?.id !== open) block = null; }

  // The row's switch saves the enabled state at once (the same PUT as Save). Enabling is checked with the
  // same rules as the form; when something is missing the row opens, says what, and the switch stays off.
  async function setEnabled(id: DVTuneMode, on: boolean) {
    const v = netOf(id);
    if (!v || readonly) return;
    if (on) {
      const text = enableBlock(v, formOf(v), identity);
      if (text) { block = { id, text }; open = id; return; }
    }
    block = null; busy = id;
    try {
      const r = await api.dvNetworkPut(id, { enabled: on });
      onsaved(r);
      toast(enableToast(v.label, r.enabled, r.applied === false ? r.applyError : undefined), 'info');
    } catch (e) { toastError(e); } finally { busy = ''; }
  }
</script>

<section aria-label="Networks" class={card ? 'card p-4' : ''} data-testid="dv-networks">
  <h2 class="text-base font-semibold">Networks</h2>
  <p class="mt-0.5 text-xs text-muted">{readonly ? 'The state of each network.' : 'Switch a network on here. Open a network for its settings.'}</p>
  <ul class="mt-3 space-y-2">
    {#each NETWORK_MODES as id (id)}
      {@const c = cardState(modes, id)}
      {@const v = netOf(id)}
      {@const sum = netSummary(v, c.available, c.enabled)}
      {@const isOpen = open === id}
      {@const m = modes?.find((x) => x.id === id)}
      {@const live = v ? liveLogin(v, modes) : m ? { login: m.login, detail: m.detail } : null}
      {@const login = c.enabled && live ? loginText(live.login, live.detail) : ''}
      {@const tone = loginTone(live?.login)}
      <li id="dv-row-{id.toLowerCase()}" class="{card ? 'rounded-md border bg-surface-2' : 'card'} scroll-mt-16 p-3 {isOpen ? 'border-accent' : card ? 'border-line' : ''}" data-testid="dv-row-{id.toLowerCase()}">
        <div class="flex items-center gap-2">
          <div class="min-w-0 flex-1">
            <div class="flex flex-wrap items-center gap-x-2 gap-y-0.5">
              <span class="text-sm font-medium">{MODE_NAME[id]}</span>
              <span class="badge {c.enabled ? 'border-ok text-ok' : ''}" role="status" data-testid="dv-{id.toLowerCase()}-state">{c.available ? (c.enabled ? 'Enabled' : 'Disabled') : 'Unavailable'}</span>
              {#if login}<span class="badge {tone === 'ok' ? 'border-ok text-ok' : tone === 'err' ? 'border-danger text-danger' : ''}" role="status" data-testid="dvn-{id.toLowerCase()}-login">{login}</span>{/if}
            </div>
            {#if sum}<p class="truncate text-xs text-muted">{sum}</p>{/if}
          </div>
          <span class="px-1.5"><Toggle tap checked={v ? v.enabled : c.enabled} disabled={readonly || !c.available || !v || busy !== ''} label="Enable {MODE_NAME[id]}" onchange={(on) => setEnabled(id, on)} /></span>
          {#if !readonly}<button type="button" class="btn btn-icon size-8 shrink-0" aria-expanded={isOpen} aria-controls="dv-row-{id.toLowerCase()}-body" aria-label="{MODE_NAME[id]} settings" onclick={() => toggleRow(id)}>
            <ChevronDown size={16} class="transition-transform {isOpen ? 'rotate-180' : ''}" />
          </button>{/if}
        </div>
        {#if isOpen && !readonly}
          <div id="dv-row-{id.toLowerCase()}-body" class="mt-3 border-t border-line pt-3">
            {#if !c.available}
              <p class="text-xs text-muted" data-testid="dv-{id.toLowerCase()}-unavailable">{UNAVAILABLE_TEXT}</p>
            {:else if v}
              {#if block?.id === id}<p class="mb-2 text-sm text-warn" role="alert" data-testid="dvn-{id.toLowerCase()}-block">{block.text}</p>{/if}
              <DvNet {v} {identity} onsaved={onsaved} />
            {:else}
              <p class="text-xs text-muted">The settings need an admin login.</p>
            {/if}
          </div>
        {/if}
      </li>
    {/each}
  </ul>
</section>
