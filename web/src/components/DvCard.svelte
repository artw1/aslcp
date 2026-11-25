<script lang="ts">
  // The digital modes card of the dashboard (docs/DV.md "Dashboard card"). Everyone sees the state;
  // operators and admins get the controls. The snapshot comes from the `dv` SSE event.
  import { api } from '../lib/api';
  import { app, canOperate, isAdmin } from '../lib/store.svelte';
  import { toast, toastError } from '../lib/toast.svelte';
  import { dvErrorMessage } from '../lib/dvmsg';
  import { indexList, optionLabel, searchList } from '../lib/dvlist';
  import type { DVIndexed } from '../lib/dvlist';
    import { CONFIRM_MS, allOk, bridgeLinked, isTargetLinked, bridgeNodeName, nodeLinkClass, defaultFavName, favoriteBlock, favoritesFor, findFavorite, controlsLocked, pendingOutcome, shownMode, reflectorSilent, stateLine, stateTitle, stateTone, validFavName, validTune, withPrivate, PRIVATE_MARK, heardLine, m17Display, masterLine, M17_MODULES, m17Tune, tunePlaceholder, isDMRFamily, modeButtonLabel, modeLabel, dashboardModes, quietHint, QUIET_TICK_MS } from '../lib/dvpanel';
  import type { Pending } from '../lib/dvpanel';
  import { ECHO_RUNNING } from '../lib/dvecho';
  import { exclusiveHint, linkButton, linkExclusiveOnClick, linkBoxText, linkLine, pausedHint } from '../lib/dvbridge';
  import type { DVCmdReply, DVFavorite, DVListItem, DVListMode, DVTuneMode } from '../lib/types';
  import NodePicker from './NodePicker.svelte';
  import Dialog from './Dialog.svelte';
  import DvMeter from './DvMeter.svelte';
  import Menu from './Menu.svelte';
  import PinFace from './PinFace.svelte';
  import RadioTower from '@lucide/svelte/icons/radio-tower';
  import Star from '@lucide/svelte/icons/star';
  import Ellipsis from '@lucide/svelte/icons/ellipsis';
  import Check from '@lucide/svelte/icons/check';
  import Search from '@lucide/svelte/icons/search';
  import Unlink from '@lucide/svelte/icons/unlink';
  import Pin from '@lucide/svelte/icons/pin';
  import LastHeardTable from './LastHeardTable.svelte';
  import { dvHeardItems } from '../lib/lastheard';

  // `page`: the DV tab shows the network status strip; its Last heard list is a card of its own (DvPage).
  let { page = false }: { page?: boolean } = $props();
  const snap = $derived(app.dv);
  const op = $derived(canOperate());
  const admin = $derived(isAdmin());
  const b = $derived(snap?.bridge);
  const mode = $derived((b && b.source !== 'none' ? b.mode : '') as DVTuneMode | '');
  const live = $derived(!!snap?.configured);
  const tone = $derived(stateTone(snap));
  const master = $derived(masterLine(snap, mode));
  const masterTitle = $derived(snap?.modes?.find((x) => x.id === mode)?.master ?? '');
  const ok = $derived(allOk(snap));
  const linked = $derived(bridgeLinked(snap));
  const nodeName = $derived(bridgeNodeName(snap));
  // What keys the bridge from the digital side: the bridge node's link is keyed (live from the node status), mode and target from ABInfo.
  let busy = $state(false);

  // Mode switch: highlighted at once, confirmed by the next snapshot, "Not confirmed" after 5 s.
  let pending = $state<Pending | null>(null);
  let now = $state(Date.now());
  let timer: ReturnType<typeof setTimeout> | undefined;
  const outcome = $derived(pendingOutcome(pending, b?.mode, now));
  const shown = $derived(shownMode(pending, outcome, mode));
  const locked = $derived(controlsLocked(busy, outcome));
  $effect(() => { if (outcome === 'confirmed') { pending = null; clearTimeout(timer); } });
  $effect(() => () => clearTimeout(timer));

  function apply(r: DVCmdReply) {
    app.dv = r.snapshot;
  }
  async function setMode(m: DVTuneMode) {
    if (locked || shown === m) return;
    clearTimeout(timer);
    now = Date.now();
    pending = { mode: m, since: now };
    timer = setTimeout(() => { now = Date.now(); }, CONFIRM_MS + 50);
    busy = true;
    try { apply(await api.dvMode(m)); }
    catch (e) { pending = null; clearTimeout(timer); toastError(e); }
    finally { busy = false; }
  }

  // Tune: a list picker for YSF, P25, NXDN and M17, a plain number for BM and DMR (the DMR list holds masters, not talkgroups).
  let lists = $state<Partial<Record<DVTuneMode, { items: DVListItem[]; index: DVIndexed[] }>>>({});
  const tried = new Set<string>();
  let tuneValue = $state('');
  const pickable = $derived(mode && !isDMRFamily(mode) ? lists[mode] : undefined);
  $effect(() => {
    const m = mode;
    tuneValue = '';
    if (!op || !live || !m || isDMRFamily(m) || tried.has(m)) return;
    tried.add(m);
    api.dvList(m.toLowerCase() as DVListMode)
      .then((l) => { lists[m] = { items: l.items, index: indexList(l.items) }; })
      .catch(() => { /* no list: free text */ });
  });
  // M17: the reflector comes from the picker, the module from the dropdown ("M17-XXX" + " " + "C").
  let module = $state('A');
  const sendValue = $derived(isDMRFamily(mode) ? withPrivate(mode, tuneValue, tuneValue.trim().endsWith(PRIVATE_MARK)) : mode === 'M17' ? m17Tune(tuneValue, module) : tuneValue.trim());
  const tuneOk = $derived(!!mode && validTune(mode, sendValue));
  async function tune(value: string) {
    if (locked || !mode) return;
    busy = true;
    try {
      const r = await api.dvTune({ mode, tune: value.trim() });
      apply(r);
      toast(r.confirmed ? 'Tuned' : 'Sent, not confirmed yet', r.confirmed ? 'ok' : 'info');
      return true;
    } catch (e) { toast(dvErrorMessage(e), 'error'); } finally { busy = false; }
  }
  // The DMR - Other "no traffic" hint: the server sends when the talkgroup was last used, the clock decides when it is 15 minutes old.
  let nowMs = $state(Date.now());
  $effect(() => { const t = setInterval(() => { nowMs = Date.now(); }, QUIET_TICK_MS); return () => clearInterval(t); });
  const quiet = $derived(quietHint(snap, nowMs));

  async function go() { if (tuneOk && (await tune(sendValue))) tuneValue = ''; }

  const unlinkValue = $derived(snap?.modes.find((x) => x.id === mode)?.unlink ?? '');
  async function unlink() {
    if (locked || !mode) return;
    busy = true;
    try { const r = await api.dvUnlink(); apply(r); toast(r.confirmed ? 'Unlinked' : 'Sent, not confirmed yet', r.confirmed ? 'ok' : 'info'); }
    catch (e) { toastError(e); } finally { busy = false; }
  }

  // The bridge node's link to a local node. This is the node link, not the talkgroup unlink above.
  const bn = $derived(snap?.bridgeNode);
  const link = $derived(snap?.link);
  const nodeBtn = $derived(bn?.present && bn.node && link && link.mode === 'ondemand' ? linkButton(link, bn.node) : null);
  async function toggleLink() {
    if (busy || !nodeBtn || !link || !bn?.node) return;
    busy = true;
    try {
      if (nodeBtn.action === 'connect') await api.connect(link.localNode, bn.node, 'transceive', false, linkExclusiveOnClick(link));
      else await api.disconnect(link.localNode, bn.node, false);
      toast(nodeBtn.action === 'connect' ? 'Linking the bridge node' : 'Bridge node unlinked', 'info');
      app.dv = await api.dvSnapshot();
    } catch (e) { toastError(e); } finally { busy = false; }
  }

  // "Disconnect other links first": a settings write (no config apply, no restart).
  async function setExclusive(on: boolean) {
    if (busy || !op) return;
    busy = true;
    try { await api.dvSetLinkExclusive(on); app.dv = await api.dvSnapshot(); }
    catch (e) { toastError(e); app.dv = await api.dvSnapshot().catch(() => app.dv); } finally { busy = false; }
  }

  // Favourites: shared by the whole node, chips for the current mode.
  let favs = $state<DVFavorite[]>([]);
  let favsLoaded = false;
  async function loadFavs() { try { favs = await api.dvFavorites(); } catch { /* keep the old list */ } }
  $effect(() => { if (snap?.installed && !favsLoaded) { favsLoaded = true; void loadFavs(); } });
  const chips = $derived(mode ? favoritesFor(favs, mode) : []);
  const target = $derived(b?.tune ?? '');
  const targetLinked = $derived(isTargetLinked(target, unlinkValue));
  // A private call is saved with its #, so the chip calls it privately again.
  const favValue = $derived(target && isDMRFamily(mode) && b?.private ? target + PRIVATE_MARK : target);
  const starred = $derived(mode ? findFavorite(favs, mode, favValue) : undefined);
  const nodeRow = $derived(!!(bn?.present && bn.node && link));
  const showNodeBox = $derived(nodeRow);
  const showTargetBox = $derived(!!mode);
  const targetUp = $derived(mode === 'LOOP' || targetLinked);
  const targetMain = $derived(mode === 'M17' ? m17Display(target) : b?.targetName || target);
  const targetSub = $derived(b?.targetName ? target : '');
  const targetFull = $derived(b?.targetName ? `${b.targetName} (${target})` : target);
  // The short status pill: "Linked" when the node link and the target are both up, else why not.
  const pill = $derived.by(() => {
    if (linked && targetLinked) return { text: 'Linked', cls: '!border-ok/50 !text-ok' };
    const text = !ok ? 'Service down' : !mode ? 'Mode unknown' : reflectorSilent(snap) ? 'Reflector not answering' : mode === 'LOOP' ? 'Echo test' : !linked && !targetLinked ? 'Not linked' : !linked ? 'Node not linked' : 'No target';
    return { text, cls: tone === 'warn' ? '!border-warn/50 !text-warn' : '' };
  });
  const block = $derived(!mode ? 'Mode unknown' : target && target === unlinkValue ? 'Nothing is linked' : favoriteBlock(favs, mode, favValue));

  let dlg = $state({ open: false, id: 0, name: '' });
  function addFav() { if (!block && b) dlg = { open: true, id: 0, name: defaultFavName(b) }; }
  async function togglePin(f: DVFavorite) {
    try { await api.dvUpdateFavorite(f.id, { pinned: !f.pinned }); await loadFavs(); } catch (e) { toastError(e); }
  }
  function renameFav(f: DVFavorite) { dlg = { open: true, id: f.id, name: f.name }; }
  async function saveFav(e: Event) {
    e.preventDefault();
    const name = validFavName(dlg.name);
    if (!name || !mode) return;
    busy = true;
    try {
      if (dlg.id) await api.dvUpdateFavorite(dlg.id, { name });
      else await api.dvAddFavorite({ mode, value: favValue, name });
      await loadFavs();
      toast(dlg.id ? 'Favourite renamed' : 'Favourite saved');
      dlg.open = false;
    } catch (x) { toastError(x); } finally { busy = false; }
  }
  async function delFav(f: DVFavorite) {
    try { await api.dvDeleteFavorite(f.id); await loadFavs(); toast('Favourite deleted'); } catch (e) { toastError(e); }
  }
  const nameOk = $derived(validFavName(dlg.name) !== null);
</script>

{#if snap?.installed}
  <section class="space-y-2 rounded-md border px-2 py-1.5 text-sm {linked ? 'border-ok/50 bg-ok/10' : 'border-line bg-surface'}" aria-label="Digital modes" data-linked={linked}>
    <div class="flex flex-wrap items-center gap-x-3 gap-y-1">
      <span class="inline-flex items-center gap-1.5 text-base font-semibold"><RadioTower size={16} class="text-accent" aria-hidden="true" />Digital modes</span>
      <span class="badge {pill.cls}" title={stateTitle(snap)} data-tone={tone} data-testid="dv-status">{pill.text}</span>
      <span class="sr-only">{stateLine(b, reflectorSilent(snap))}</span>
      {#if master}<span class="min-w-0 max-w-full truncate text-xs text-muted" title={masterTitle} data-testid="dv-master">{master}</span>{/if}
      <span class="sr-only">{ok ? 'All services running' : 'A service is down or the state is unknown'}</span>
      {#if linked}<span class="sr-only">Bridge node linked</span>{/if}
      {#if admin}<a href="#/digital" class="ml-auto text-xs text-accent underline hover:no-underline">Setup</a>{/if}
    </div>

    {#if !snap.running}
      <p class="text-warn">The digital modes engine is not running.{admin ? ' Start it from the Digital modes tab.' : ''}</p>
    {/if}
    {#if quiet}
      <p class="flex flex-wrap items-center gap-2 text-warn" role="status" data-testid="dv-quiet">{quiet.text}
        <button type="button" class="btn btn-sm" disabled={locked || busy} onclick={() => tune(quiet.tune)}>Retune</button></p>
    {/if}
    {#if outcome === 'unconfirmed' && pending}
      <p class="flex flex-wrap items-center gap-2 text-warn" role="status">Not confirmed: the bridge still reports {b?.mode || 'no mode'}, not {pending.mode}.
        <button type="button" class="text-xs underline hover:no-underline" onclick={() => { pending = null; }}>Dismiss</button></p>
    {/if}

    {#if showTargetBox || showNodeBox}
      <div class="grid grid-cols-1 gap-2 sm:grid-cols-2">
        {#if showTargetBox}
          <div class="rounded border bg-surface-2 px-3 py-2 {targetUp ? 'border-ok/40' : 'border-warn/40'}" data-testid="dv-target-box">
            <div class="flex items-start gap-2">
              <div class="min-w-0 flex-1">
                <div class="text-[11px] uppercase tracking-wide text-muted">{mode === 'LOOP' ? 'Echo test' : `${modeLabel(mode)} ${isDMRFamily(mode) ? 'talkgroup' : 'reflector'}`}</div>
                {#if mode === 'LOOP'}
                  <div class="truncate font-medium text-ok">{ECHO_RUNNING}</div>
                  <a href="#/digital" class="text-xs text-accent underline hover:no-underline" data-testid="dv-echo-link">{admin ? 'Stop it in Digital modes setup' : 'Pick a mode to stop it'}</a>
                {:else if targetLinked}
                  <div class="truncate font-medium {reflectorSilent(snap) ? 'text-warn' : 'text-ok'}" title={targetFull}>{targetMain}</div>
                  {#if targetSub}<div class="truncate font-mono text-xs text-muted" title={targetFull}>{targetSub}</div>{/if}
                {:else}
                  <div class="text-muted">Not linked</div>
                {/if}
              </div>
              {#if op && live && mode !== 'LOOP'}
                <div class="flex shrink-0 flex-col items-end gap-1">
                  <button type="button" class="btn btn-sm btn-bad-outline" disabled={locked || !mode || !unlinkValue || !targetLinked} title={!targetLinked ? 'Nothing is linked' : mode ? `Unlink the ${modeLabel(mode)} talkgroup or reflector: send ${unlinkValue} to ${modeLabel(mode)}. The bridge node link is not changed.` : 'Mode unknown'} data-testid="dv-unlink" onclick={unlink}><Unlink size={12} aria-hidden="true" />Unlink {mode ? modeLabel(mode) : ''}</button>
                </div>
              {/if}
            </div>
          </div>
        {/if}
        {#if showNodeBox && bn?.node && link}
          {@const lb = linkBoxText(bn.node, link)}
          <div class="rounded border bg-surface-2 px-3 py-2 {linked ? 'border-ok/40' : 'border-warn/40'}" data-testid="dv-node-link">
            <div class="flex items-start gap-2">
              <div class="min-w-0 flex-1">
                <div class="text-[11px] uppercase tracking-wide text-muted">AllStar link</div>
                <div class="truncate font-medium {link.mode === 'permanent' && !link.linked ? 'text-warn' : linked ? 'text-ok' : ''}" title={linkLine(bn.node, link, nodeName)}>{lb.main}</div>
                <div class="text-xs text-muted">{lb.sub}</div>
                {#if pausedHint(link)}<div class="text-xs text-muted">{pausedHint(link)}</div>{/if}
              </div>
              <div class="flex shrink-0 flex-col items-end gap-1">
                {#if op && nodeBtn}<button type="button" class="btn btn-sm {nodeBtn.action === 'connect' ? nodeLinkClass(nodeBtn.action) : 'btn-bad-outline'}" disabled={busy} title={nodeBtn.title} onclick={toggleLink}>{#if nodeBtn.action !== 'connect'}<Unlink size={12} aria-hidden="true" />{/if}{nodeBtn.label}</button>{/if}
                <label class="inline-flex cursor-pointer items-center gap-1 text-xs" title={exclusiveHint(link.mode, link.exclusive)} data-testid="dv-exclusive">
                  <input type="checkbox" class="size-3.5 accent-[var(--accent)]" checked={link.exclusive} onchange={(e) => setExclusive(e.currentTarget.checked)} disabled={!op || busy} />Disconnect other links first</label>
              </div>
            </div>
          </div>
        {/if}
      </div>
    {/if}
    {#if snap.running && !snap.bridgeNode.present}
      <p class="text-muted">No bridge node yet: audio through AllStar needs the bridge node setup{admin ? ' (Digital modes tab).' : '.'}</p>
    {/if}

    {#if op && live}
      {@const modes = dashboardModes(snap.modes)}
      <form class="space-y-1.5" data-testid="dv-controls" onsubmit={(e) => { e.preventDefault(); void go(); }}>
        <div class="flex flex-wrap items-stretch gap-1.5 md:flex-nowrap">
          <div class="flex w-full flex-wrap gap-1.5 md:w-auto md:flex-nowrap md:shrink-0" role="group" aria-label="Mode" data-testid="dv-modes">
            {#each modes as m (m.id)}
              {@const on = shown === m.id}
              <button type="button" class="btn !min-h-10 !px-3 !text-sm shrink-0 grow md:min-w-20 md:flex-none md:grow-0 {on ? 'btn-primary' : ''} {!m.enabled ? '!border-dashed !bg-surface-2 !text-muted' : ''}" aria-pressed={on}
                disabled={!m.enabled || locked}
                title={m.enabled ? undefined : `${modeButtonLabel(m)}: enable in setup${admin ? ' (Digital modes tab)' : ''}`}
                aria-label={m.enabled ? undefined : `${modeButtonLabel(m)}, not enabled. Enable in setup`}
                onclick={() => setMode(m.id)}>{#if on}<Check size={16} strokeWidth={3} aria-hidden="true" />{/if}{modeButtonLabel(m)}{#if pending?.mode === m.id && outcome === 'waiting'}…{/if}</button>
            {/each}
          </div>
          {#if mode && mode !== 'LOOP'}
            <div class="flex min-w-0 basis-full items-stretch gap-1.5 md:min-w-56 md:flex-1 md:basis-0">
              <div class="relative min-w-0 flex-1">
                <Search size={16} class="pointer-events-none absolute left-2.5 top-1/2 z-10 -translate-y-1/2 text-muted" aria-hidden="true" />
                {#if pickable}
                  <NodePicker id="dvtune" bind:value={tuneValue} options={pickable.items} search={(t) => searchList(pickable.index, t)} label={(o) => optionLabel(mode.toLowerCase() as DVListMode, o)} placeholder={tunePlaceholder(mode)} inputmode="text" onenter={go} inputClass="!min-h-10 !pl-9 focus:!border-accent focus:outline-none focus:ring-1 focus:ring-accent/40" />
                {:else}
                  <input id="dvtune" class="input font-mono !min-h-10 !pl-9 focus:!border-accent focus:outline-none focus:ring-1 focus:ring-accent/40" bind:value={tuneValue} placeholder={tunePlaceholder(mode)} inputmode={isDMRFamily(mode) ? 'numeric' : 'text'} autocomplete="off" aria-label="Tune target" />
                {/if}
              </div>
              {#if mode === 'M17'}
                <select class="input !min-h-10 !w-auto" bind:value={module} disabled={locked} aria-label="Module" title="The module (room) of the reflector" data-testid="dv-m17-module">
                  {#each M17_MODULES as x (x)}<option value={x}>Module {x}</option>{/each}
                </select>
              {/if}
              <button type="submit" class="btn btn-primary !min-h-10 !px-3 !text-sm" disabled={locked || !tuneOk}>Tune</button>
              <button type="button" class="btn btn-icon {starred ? 'border-accent' : ''} !min-h-10 !w-10 !px-0" disabled={locked || !!block} aria-pressed={!!starred} data-testid="dv-star"
                aria-label={starred ? 'Already a favourite' : 'Save the current target as a favourite'} title={block ?? 'Save the current target as a favourite'} onclick={addFav}>
                <Star size={16} class={starred ? 'fill-accent text-accent' : 'text-muted'} aria-hidden="true" /></button>
            </div>
          {/if}
        </div>
      </form>
    {/if}

    {#if mode && chips.length}
      <div class="flex flex-wrap gap-2" role="group" aria-label="Favourites for {mode}" data-testid="dv-favs">
        {#each chips as f (f.id)}
          {@const tuned = f.value === favValue}
          <span class="inline-flex items-stretch" data-tuned={tuned}>
            {#if op && live}
              <button type="button" class="btn btn-pin border-accent/50 {tuned ? 'bg-accent/10' : ''} rounded-r-none" disabled={locked} title="Tune to {f.value}" onclick={() => tune(f.value)}>
                <PinFace active={tuned} label={f.name} sub={mode === 'YSF' ? '' : f.value} dotId="fav-active" /></button>
              <button type="button" class="btn btn-pin !px-2 -ml-px rounded-none border-accent/50" aria-pressed={f.pinned} aria-label="{f.pinned ? 'Unpin' : 'Pin'} {f.name} on the dashboard" title="Pin to the dashboard" onclick={() => togglePin(f)}>
                <Pin size={14} class={f.pinned ? 'fill-accent text-accent' : 'text-muted'} aria-hidden="true" /></button>
              <Menu label="Favourite {f.name}" btnClass="btn btn-pin !px-2 -ml-px rounded-l-none border-accent/50 text-muted" items={menu}>
                {#snippet trigger()}<Ellipsis size={14} aria-hidden="true" />{/snippet}
              </Menu>
              {#snippet menu(close: () => void)}
                <button type="button" role="menuitem" class="block w-full rounded px-2 py-1 text-left text-sm hover:bg-surface-2" onclick={() => { close(); renameFav(f); }}>Rename</button>
                <button type="button" role="menuitem" class="block w-full rounded px-2 py-1 text-left text-sm text-danger hover:bg-danger/10" onclick={() => { close(); void delFav(f); }}>Delete</button>
              {/snippet}
            {:else}
              <span class="btn btn-pin cursor-default border-accent/50 hover:bg-surface" title={f.value}><PinFace active={tuned} label={f.name} sub={mode === 'YSF' ? '' : f.value} dotId="fav-active" /></span>
            {/if}
          </span>
        {/each}
      </div>
    {/if}

    <DvMeter running={snap.running} />

    {#if !page && snap.heard?.length}
      <details class="text-xs" data-testid="dv-heard">
        <summary class="cursor-pointer text-muted">Last heard <span class="ml-1 font-mono">{heardLine(snap.heard[0])}</span></summary>
        <LastHeardTable rows={dvHeardItems(snap.heard)} />
      </details>
    {/if}
  </section>

  <Dialog bind:open={dlg.open} title={dlg.id ? 'Rename favourite' : 'Save favourite'}>
    <form id="dvfavform" class="space-y-2" onsubmit={saveFav}>
      <div>
        <label class="label" for="dvfavname">Name <span class="font-normal">(1 to 40 characters)</span></label>
        <input id="dvfavname" class="input" bind:value={dlg.name} maxlength="40" autocomplete="off" />
      </div>
      {#if !dlg.id}<p class="text-xs text-muted">{mode} · <span class="font-mono">{favValue}</span></p>{/if}
    </form>
    {#snippet footer()}
      <button class="btn" onclick={() => (dlg.open = false)}>Cancel</button>
      <button class="btn btn-primary" type="submit" form="dvfavform" disabled={busy || !nameOk}>{busy ? 'Saving…' : 'Save'}</button>
    {/snippet}
  </Dialog>
{/if}
