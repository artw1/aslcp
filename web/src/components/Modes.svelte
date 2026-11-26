<script lang="ts">
  // Modes: the modes are equal choices. Pick a mode, pick a target (favorite, recent or typed), connect or disconnect.
  // One mode is active at a time; the radio is the fixed local end of whichever it is.
  import { api } from '../lib/api';
  import { app, canOperate, isAdmin, refreshFavorites, refreshModes } from '../lib/store.svelte';
  import { toast, toastError } from '../lib/toast.svelte';
  import { favoritesOf, modeTitle, needsVocoder, recentTargets, sortModes, stateText, linkStatsText, targetBody, targetLabel, targetText, vocoderBusy } from '../lib/modes';
  import type { Favorite, HostItem, HostList, Vocoder } from '../lib/types';
  import FavoriteDialog from './FavoriteDialog.svelte';
  import Confirm from './Confirm.svelte';
  import Footer from './Footer.svelte';
  import Plug from '@lucide/svelte/icons/plug';
  import Unplug from '@lucide/svelte/icons/unplug';
  import Pin from '@lucide/svelte/icons/pin';
  import Pencil from '@lucide/svelte/icons/pencil';
  import Trash from '@lucide/svelte/icons/trash-2';
  import Plus from '@lucide/svelte/icons/plus';
  import Settings from '@lucide/svelte/icons/settings';

  const op = $derived(canOperate());
  const list = $derived(sortModes(app.modes));
  const active = $derived(app.node?.active ?? '');
  // The mode being looked at; follows the active one until the user picks another card.
  let picked = $state('');
  const sel = $derived(picked && list.some((m) => m.name === picked) ? picked : active || list.find((m) => m.configured)?.name || list[0]?.name || '');
  const info = $derived(list.find((m) => m.name === sel));
  const isActive = $derived(!!sel && sel === active);
  const st = $derived(isActive ? app.node?.mode : info?.status);
  const connected = $derived(isActive && (st?.state === 'connected' || st?.state === 'connecting') && !!st?.target);

  let target = $state(''), busy = $state(false), q = $state('');
  let hostItem = $state<HostItem | null>(null);
  $effect(() => { void sel; target = ''; hostItem = null; q = ''; hosts = null; });

  const favs = $derived(favoritesOf(app.favorites, sel));
  const recent = $derived(recentTargets(app.heard, app.nowcalling?.recent ?? [], sel).filter((t) => !favs.some((f) => f.target === t)));

  // Reflectors and masters (YSF, M17, DMR, ...) come from a downloaded list.
  let hosts = $state<HostList | null>(null);
  $effect(() => {
    if (!info?.needsHost || hosts) return;
    const m = sel;
    api.hostLists(m).then((h) => { if (m === sel) hosts = h; }).catch(() => { if (m === sel) hosts = { mode: m, items: [], etag: '' }; });
  });
  const matches = $derived.by(() => {
    const t = (q || '').trim().toLowerCase();
    if (!hosts || !t) return [];
    return hosts.items.filter((i) => `${i.value} ${i.name} ${i.detail ?? ''}`.toLowerCase().includes(t)).slice(0, 8);
  });

  function pick(i: HostItem) { hostItem = i; target = i.value; q = ''; }
  function useFav(f: Favorite) { target = f.target; hostItem = null; }
  function body() {
    const t = target.trim();
    const h = hostItem && hostItem.value === t ? hostItem : hosts?.items.find((i) => i.value === t);
    return targetBody(t, h ? { host: h.address, port: h.port, name: h.name } : {});
  }

  async function run<T>(fn: () => Promise<T>, ok: string) {
    if (busy) return;
    busy = true;
    try { await fn(); toast(ok); } catch (e) { toastError(e); } finally { busy = false; }
  }
  const connect = () => {
    if (!target.trim()) { toast('Enter a target', 'error'); return; }
    return run(async () => { await api.select(sel, body()); }, `Connecting ${modeTitle(sel, app.modes)} to ${target.trim()}`);
  };
  const makeActive = () => run(async () => { await api.select(sel); }, `${modeTitle(sel, app.modes)} is active`);
  const disconnect = () => run(async () => { await api.disconnect(); }, 'Disconnected');
  const goIdle = () => run(async () => { await api.select(''); }, 'No mode is active');
  async function tap(f: Favorite) { useFav(f); await connect(); }

  let dlg = $state(false), editing = $state<Favorite | null>(null), delFav = $state<Favorite | null>(null), delOpen = $state(false);
  async function pin(f: Favorite) {
    try { await api.updateFavorite(f.id, { pinned: !f.pinned }); await refreshFavorites(); } catch (e) { toastError(e); }
  }
  async function remove() {
    const f = delFav; if (!f) return;
    try { await api.deleteFavorite(f.id); await refreshFavorites(); toast('Favorite removed'); } catch (e) { toastError(e); }
  }
  $effect(() => { if (!app.modes.length) refreshModes().catch(() => {}); });

  // The AMBE vocoder (md380-emu): admins can install it when the picked mode needs it. Installing is a helper job; poll until it ends.
  let voc = $state<Vocoder | null>(null), vocBusy = $state(false);
  const vocNeeded = $derived(isAdmin() && needsVocoder(info, voc));
  async function loadVocoder() {
    try { voc = await api.vocoder(); } catch { /* the notice stays hidden when the status is unknown */ }
  }
  $effect(() => { if (isAdmin()) void loadVocoder(); });
  $effect(() => {
    if (!vocoderBusy(voc)) return;
    const t = setInterval(async () => {
      await loadVocoder();
      if (voc?.job === 'done') { toast('AMBE vocoder installed'); void refreshModes().catch(() => {}); }
      else if (voc?.job === 'failed') toast(voc.jobLog ? `Vocoder install failed: ${voc.jobLog.trim().split('\n').pop()}` : 'Vocoder install failed', 'error');
    }, 2000);
    return () => clearInterval(t);
  });
  async function installVocoder() {
    if (vocBusy) return;
    vocBusy = true;
    try { voc = await api.installVocoder(); if (voc.job === 'running') toast('Installing the AMBE vocoder…'); else await loadVocoder(); } catch (e) { toastError(e); } finally { vocBusy = false; }
  }
</script>

<div class="space-y-3">
  <section class="rounded-md border px-3 py-2 {active ? 'border-accent/60 bg-accent/5' : 'border-line bg-surface'}" aria-label="Active mode" data-testid="active-mode">
    <div class="flex flex-wrap items-center gap-x-3 gap-y-1">
      <span class="text-xs font-semibold uppercase tracking-wide text-muted">Active mode</span>
      {#if active}
        <span class="text-base font-semibold" data-testid="active-name">{modeTitle(active, app.modes)}</span>
        <span class="badge {app.node?.mode?.state === 'connected' ? 'text-ok' : app.node?.mode?.state === 'error' ? 'text-danger' : 'text-warn'}">{stateText(app.node?.mode?.state)}</span>
        {#if app.node?.mode?.target}<span class="min-w-0 truncate font-mono text-sm">{targetText(app.node.mode.target)}</span>{/if}
        {#if op}<button class="btn btn-sm ml-auto" disabled={busy} onclick={goIdle}><Unplug size={13} aria-hidden="true" />Stop {modeTitle(active, app.modes)}</button>{/if}
      {:else}
        <span class="text-sm text-muted">None. The radio is idle.</span>
      {/if}
    </div>
    {#if app.node?.mode?.error}<p class="mt-1 text-xs text-danger" role="alert">{app.node.mode.error}</p>{/if}
    {#if app.node?.mode?.link}<p class="mt-1 font-mono text-xs text-muted" data-testid="link-stats" title="Link counters: voice full+mini frames received, voice frames sent, VNAKs sent/received, retransmits">{linkStatsText(app.node.mode.link)}</p>{/if}
  </section>

  {#if !app.node}
    <p class="py-10 text-center text-sm text-muted" role="status">Loading…</p>
  {:else}
    <section aria-label="Modes">
      <h2 class="mb-1 text-sm font-semibold uppercase tracking-wide text-muted">Mode</h2>
      <div class="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-5" role="group" aria-label="Choose a mode" data-testid="mode-cards">
        {#each list as m (m.name)}
          <button type="button" class="rounded-md border px-3 py-2 text-left transition-colors hover:bg-surface-2 {m.name === sel ? 'border-accent bg-accent/10' : 'border-line bg-surface'} {m.configured ? '' : 'opacity-70'}"
            aria-pressed={m.name === sel} data-mode={m.name} onclick={() => (picked = m.name)}>
            <span class="flex items-center gap-1.5"><span class="font-semibold">{m.title || modeTitle(m.name)}</span>{#if m.name === active}<span class="badge border-accent text-accent">Active</span>{/if}</span>
            <span class="mt-0.5 block truncate text-xs text-muted">{#if m.name === active}{stateText(app.node?.mode?.state, app.node?.mode?.detail)}{:else if m.configured}Ready to use{:else}Not set up{/if}</span>
          </button>
        {/each}
      </div>
    </section>

    {#if info}
      <section class="card space-y-3 p-3" aria-label="{info.title} target" data-testid="target-panel">
        <div class="flex flex-wrap items-center gap-2">
          <h2 class="text-base font-semibold">{info.title || modeTitle(info.name)}</h2>
          {#if isActive}<span class="badge border-accent text-accent">Active</span>{/if}
          {#if isAdmin()}<a class="btn btn-sm ml-auto" href={'#/settings/modes/' + info.name}><Settings size={13} aria-hidden="true" />Settings</a>{/if}
        </div>
        {#if vocNeeded}
          <div class="flex flex-wrap items-center gap-2 rounded border border-warn/60 bg-warn/5 px-3 py-2 text-sm" role="status" data-testid="vocoder-notice">
            <span class="min-w-0 flex-1">Needs the AMBE vocoder (md380-emu){#if voc && !voc.supported && voc.reason}. {voc.reason}{/if}</span>
            {#if voc?.supported !== false}
              <button type="button" class="btn btn-primary btn-sm" disabled={vocBusy || vocoderBusy(voc)} onclick={installVocoder}>{vocBusy || vocoderBusy(voc) ? 'Installing…' : 'Install'}</button>
            {/if}
          </div>
        {/if}
        {#if !info.configured}
          <p class="text-sm text-muted">{info.title} is not set up yet.{#if isAdmin()} Add its login in <a class="underline" href={'#/settings/modes/' + info.name}>Settings</a>.{:else} Ask an administrator to set it up.{/if}</p>
        {:else}
          {#if connected}
            <p class="text-sm">Connected to <b class="font-mono">{targetText(st?.target)}</b>{#if st?.talker} · <span class="text-muted">{st.talker.callsign || 'someone'} is talking</span>{/if}</p>
          {/if}
          {#if op}
            <form class="space-y-2" onsubmit={(e) => { e.preventDefault(); void connect(); }}>
              <label class="label" for="mtarget">{targetLabel(info)}</label>
              <div class="flex flex-wrap gap-2">
                <input id="mtarget" class="input min-w-40 flex-1 font-mono" bind:value={target} autocomplete="off" placeholder={info.targetHelp ?? ''} />
                <button class="btn btn-primary" disabled={busy || !target.trim()}><Plug size={14} aria-hidden="true" />Connect</button>
                {#if connected}<button type="button" class="btn btn-bad-outline" disabled={busy} onclick={disconnect}><Unplug size={14} aria-hidden="true" />Disconnect</button>{/if}
                {#if !isActive}<button type="button" class="btn" disabled={busy} onclick={makeActive} title="Make {info.title} the active mode without connecting">Make active</button>{/if}
              </div>
              {#if info.targetHelp}<p class="text-xs text-muted">{info.targetHelp}</p>{/if}
              {#if info.needsHost}
                <div>
                  <label class="label" for="mfind">Find in the list</label>
                  <input id="mfind" class="input" bind:value={q} autocomplete="off" placeholder="Name or number" disabled={!hosts?.items.length} />
                  {#if hosts && hosts.items.length === 0}<p class="mt-1 text-xs text-muted">No list downloaded yet. Type the target above.</p>{/if}
                  {#if matches.length}
                    <ul class="mt-1 divide-y divide-line rounded border border-line" data-testid="host-matches">
                      {#each matches as i (i.value + i.address)}
                        <li><button type="button" class="flex min-h-9 w-full items-center gap-2 px-2 text-left text-sm hover:bg-surface-2" onclick={() => pick(i)}><span class="font-mono font-semibold">{i.value}</span><span class="min-w-0 flex-1 truncate text-muted">{i.name}{i.detail ? ` · ${i.detail}` : ''}</span></button></li>
                      {/each}
                    </ul>
                  {/if}
                </div>
              {/if}
            </form>
          {/if}

          <div>
            <div class="mb-1 flex items-center gap-2">
              <h3 class="text-xs font-semibold uppercase tracking-wide text-muted">Favorites</h3>
              {#if op}<button class="btn btn-sm ml-auto" onclick={() => { editing = null; dlg = true; }}><Plus size={13} aria-hidden="true" />Add</button>{/if}
            </div>
            {#if favs.length === 0}
              <p class="text-sm text-muted">No favorites for {info.title} yet.</p>
            {:else}
              <ul class="divide-y divide-line rounded border border-line" data-testid="favorites">
                {#each favs as f (f.id)}
                  <li class="flex items-center gap-1 px-2 py-1">
                    <button type="button" class="min-h-9 min-w-0 flex-1 text-left" disabled={!op || busy} onclick={() => tap(f)} aria-label="Connect {info.title} to {f.label || f.target}">
                      <span class="block truncate text-sm font-medium">{f.label || f.target}</span>
                      <span class="block truncate font-mono text-xs text-muted">{f.target}{f.group ? ` · ${f.group}` : ''}</span>
                    </button>
                    {#if op}
                      <button class="btn btn-sm btn-icon {f.pinned ? 'text-accent' : ''}" aria-pressed={f.pinned} aria-label="{f.pinned ? 'Unpin' : 'Pin'} {f.label || f.target}" onclick={() => pin(f)}><Pin size={13} /></button>
                      <button class="btn btn-sm btn-icon" aria-label="Edit {f.label || f.target}" onclick={() => { editing = f; dlg = true; }}><Pencil size={13} /></button>
                      <button class="btn btn-sm btn-icon btn-danger" aria-label="Remove {f.label || f.target}" onclick={() => { delFav = f; delOpen = true; }}><Trash size={13} /></button>
                    {/if}
                  </li>
                {/each}
              </ul>
            {/if}
          </div>

          {#if recent.length}
            <div>
              <h3 class="mb-1 text-xs font-semibold uppercase tracking-wide text-muted">Recent</h3>
              <div class="flex flex-wrap gap-1.5" data-testid="recent">
                {#each recent as t (t)}<button type="button" class="btn btn-sm font-mono" onclick={() => (target = t)}>{t}</button>{/each}
              </div>
            </div>
          {/if}
        {/if}
      </section>
    {/if}
  {/if}
  <Footer />
</div>

<FavoriteDialog bind:open={dlg} fav={editing} initialMode={sel} initialTarget={target} />
<Confirm bind:open={delOpen} title="Remove favorite" message="Remove {delFav?.label || delFav?.target || ''} from the favorites?" confirmLabel="Remove" onconfirm={remove} />
