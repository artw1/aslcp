<script lang="ts">
  // The digital modes card of the dashboard (docs/DV.md "Dashboard card"). Everyone sees the state;
  // operators and admins get the controls. The snapshot comes from the `dv` SSE event.
  import { api } from '../lib/api';
  import { app, canOperate, isAdmin } from '../lib/store.svelte';
  import { toast, toastError } from '../lib/toast.svelte';
  import { dvErrorMessage } from '../lib/dvmsg';
  import { indexList, optionLabel, searchList } from '../lib/dvlist';
  import type { DVIndexed } from '../lib/dvlist';
    import { CONFIRM_MS, allOk, bridgeLinked, bridgeNodeName, nodeLinkClass, defaultFavName, favoriteBlock, favoritesFor, findFavorite, controlsLocked, pendingOutcome, shownMode, reflectorSilent, stateLine, stateTitle, stateTone, validFavName, validTune, withPrivate, PARROT_ID, PRIVATE_MARK, heardLine, masterLine, M17_MODULES, m17Tune, isDMRFamily, modeButtonLabel, modeLabel, dashboardModes, quietHint, QUIET_TICK_MS } from '../lib/dvpanel';
  import type { Pending } from '../lib/dvpanel';
  import { exclusiveHint, linkButton, linkExclusiveOnClick, linkLine, pausedHint } from '../lib/dvbridge';
  import type { DVCmdReply, DVFavorite, DVListItem, DVListMode, DVTuneMode } from '../lib/types';
  import NodePicker from './NodePicker.svelte';
  import Dialog from './Dialog.svelte';
  import Menu from './Menu.svelte';
  import RadioTower from '@lucide/svelte/icons/radio-tower';
  import Star from '@lucide/svelte/icons/star';
  import Ellipsis from '@lucide/svelte/icons/ellipsis';
  import Unlink from '@lucide/svelte/icons/unlink';

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
  // DMR private call: the toggle, or a trailing # typed in the field. Never offered for the other modes.
  let priv = $state(false);
  // M17: the reflector comes from the picker, the module from the dropdown ("M17-XXX" + " " + "C").
  let module = $state('A');
  const sendValue = $derived(isDMRFamily(mode) ? withPrivate(mode, tuneValue, priv || tuneValue.trim().endsWith(PRIVATE_MARK)) : mode === 'M17' ? m17Tune(tuneValue, module) : tuneValue.trim());
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
  // BM Parrot (9990) only answers a private call.
  const parrot = () => tune(PARROT_ID + PRIVATE_MARK);

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
  // A private call is saved with its #, so the chip calls it privately again.
  const favValue = $derived(target && isDMRFamily(mode) && b?.private ? target + PRIVATE_MARK : target);
  const starred = $derived(mode ? findFavorite(favs, mode, favValue) : undefined);
  const block = $derived(!mode ? 'Mode unknown' : target && target === unlinkValue ? 'Nothing is linked' : favoriteBlock(favs, mode, favValue));

  let dlg = $state({ open: false, id: 0, name: '' });
  function addFav() { if (!block && b) dlg = { open: true, id: 0, name: defaultFavName(b) }; }
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
      <span class="inline-flex items-center gap-1.5 font-medium"><RadioTower size={15} class="text-accent" aria-hidden="true" />Digital modes</span>
      <span class={tone === 'warn' ? 'text-warn' : tone === 'ok' ? 'text-ok' : ''} title={stateTitle(snap)} data-tone={tone}>{stateLine(b, reflectorSilent(snap))}</span>
      {#if master}<span class="min-w-0 max-w-full truncate text-xs text-muted" title={masterTitle} data-testid="dv-master">{master}</span>{/if}
      {#if mode === 'LOOP'}<a href="#/digital" class="text-xs text-accent underline hover:no-underline" data-testid="dv-echo-link">{admin ? 'Stop it in Digital modes setup' : 'Pick a mode to stop it'}</a>{/if}
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
    {#if bn?.present && bn.node && link}
      <p class="flex flex-wrap items-center gap-2" data-testid="dv-node-link">
        <span class={link.mode === 'permanent' && !link.linked ? 'text-warn' : ''} title={linked ? 'Bridge node linked' : undefined}>{linkLine(bn.node, link, nodeName)}</span>
        {#if op && nodeBtn}<button type="button" class="btn btn-sm {nodeLinkClass(nodeBtn.action)}" disabled={busy} title={nodeBtn.title} onclick={toggleLink}>{nodeBtn.label}</button>{/if}
        <label class="inline-flex cursor-pointer items-center gap-1.5 text-xs" title={exclusiveHint(link.mode, link.exclusive)} data-testid="dv-exclusive">
          <input type="checkbox" class="size-4 accent-[var(--accent)]" checked={link.exclusive} onchange={(e) => setExclusive(e.currentTarget.checked)} disabled={!op || busy} />Disconnect other links first</label>
      </p>
      {#if pausedHint(link)}<p class="text-xs text-muted">{pausedHint(link)}</p>{/if}
    {/if}
    {#if snap.running && !snap.bridgeNode.present}
      <p class="text-muted">No bridge node yet: audio through AllStar needs the bridge node setup{admin ? ' (Digital modes tab).' : '.'}</p>
    {/if}

    {#if op && live}
      <div class="flex flex-wrap items-center gap-1.5" role="group" aria-label="Mode">
        {#each dashboardModes(snap.modes) as m (m.id)}
          <button type="button" class="btn btn-sm {shown === m.id ? 'btn-primary' : ''}" aria-pressed={shown === m.id}
            disabled={!m.enabled || locked}
            title={m.enabled ? undefined : `${modeButtonLabel(m)} is disabled${admin ? ' (Digital modes tab)' : ''}`}
            onclick={() => setMode(m.id)}>{modeButtonLabel(m)}{#if pending?.mode === m.id && outcome === 'waiting'}…{/if}</button>
        {/each}
        {#if mode !== 'LOOP'}<button type="button" class="btn btn-sm ml-auto" disabled={locked || !mode || !unlinkValue} title={mode ? `Unlink the ${modeLabel(mode)} talkgroup or reflector: send ${unlinkValue} to ${modeLabel(mode)}. The bridge node link is not changed.` : 'Mode unknown'} onclick={unlink}><Unlink size={13} aria-hidden="true" />Unlink {mode ? modeLabel(mode) + ' target' : ''}</button>{/if}
      </div>


      {#if mode && mode !== 'LOOP'}
        <form class="flex flex-wrap items-center gap-1.5" onsubmit={(e) => { e.preventDefault(); void go(); }}>
          <div class="min-w-40 flex-1">
            {#if pickable}
              <NodePicker id="dvtune" bind:value={tuneValue} options={pickable.items} search={(t) => searchList(pickable.index, t)} label={(o) => optionLabel(mode.toLowerCase() as DVListMode, o)} placeholder="Search {mode} or type a value" inputmode="text" onenter={go} />
            {:else}
              <input id="dvtune" class="input font-mono" bind:value={tuneValue} placeholder={isDMRFamily(mode) ? 'Talkgroup number' : 'Target'} inputmode={isDMRFamily(mode) ? 'numeric' : 'text'} autocomplete="off" aria-label="Tune target" />
            {/if}
          </div>
          {#if mode === 'M17'}
            <select class="input w-auto" bind:value={module} disabled={locked} aria-label="Module" title="The module (room) of the reflector" data-testid="dv-m17-module">
              {#each M17_MODULES as x (x)}<option value={x}>Module {x}</option>{/each}
            </select>
          {/if}
          {#if isDMRFamily(mode)}
            <label class="inline-flex cursor-pointer items-center gap-1.5 text-xs" title="Call the number as a private call (a trailing # on the ID)" data-testid="dv-private">
              <input type="checkbox" class="size-4 accent-[var(--accent)]" bind:checked={priv} disabled={locked} />Private call</label>
          {/if}
          <button type="submit" class="btn btn-primary btn-sm" disabled={locked || !tuneOk}>Go</button>
          {#if isDMRFamily(mode)}
            <button type="button" class="btn btn-sm" disabled={locked} title="BM Parrot: call 9990 as a private call and hear yourself back" data-testid="dv-parrot" onclick={parrot}>Parrot</button>
          {/if}
          <button type="button" class="btn btn-sm btn-icon" disabled={locked || !!block} aria-pressed={!!starred}
            aria-label={starred ? 'Already a favourite' : 'Save the current target as a favourite'} title={block ?? 'Save the current target as a favourite'} onclick={addFav}>
            <Star size={14} class={starred ? 'fill-current text-accent' : ''} aria-hidden="true" /></button>
        </form>
      {/if}
    {/if}

    {#if snap.heard?.length}
      <details class="text-xs" data-testid="dv-heard">
        <summary class="cursor-pointer text-muted">Last heard</summary>
        <ul class="mt-1 space-y-0.5 font-mono">
          {#each snap.heard as h, i (i)}<li title={h.dmrId ? `DMR ID ${h.dmrId}` : undefined}>{heardLine(h)}</li>{/each}
        </ul>
      </details>
    {/if}

    {#if mode && chips.length}
      <div class="flex flex-wrap items-center gap-1.5" role="group" aria-label="Favourites for {mode}">
        {#each chips as f (f.id)}
          <span class="inline-flex items-center rounded border border-line {f.value === favValue ? 'bg-accent/10' : ''}">
            {#if op && live}
              <button type="button" class="px-2 py-0.5 text-xs hover:bg-surface-2 disabled:opacity-45" disabled={locked} title="Tune to {f.value}" onclick={() => tune(f.value)}>{f.name}</button>
              <Menu label="Favourite {f.name}" btnClass="px-1 py-0.5 text-muted hover:bg-surface-2" items={menu}>
                {#snippet trigger()}<Ellipsis size={13} aria-hidden="true" />{/snippet}
              </Menu>
              {#snippet menu(close: () => void)}
                <button type="button" role="menuitem" class="block w-full rounded px-2 py-1 text-left text-sm hover:bg-surface-2" onclick={() => { close(); renameFav(f); }}>Rename</button>
                <button type="button" role="menuitem" class="block w-full rounded px-2 py-1 text-left text-sm text-danger hover:bg-danger/10" onclick={() => { close(); void delFav(f); }}>Delete</button>
              {/snippet}
            {:else}
              <span class="px-2 py-0.5 text-xs" title={f.value}>{f.name}</span>
            {/if}
          </span>
        {/each}
      </div>
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
