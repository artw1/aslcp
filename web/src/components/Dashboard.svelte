<script lang="ts">
  // Overview: node tiles, the digital modes tile, alerts, pinned favourites and one activity feed.
  // The full controls live on the AllStarLink (/allstar) and DV (/dv) tabs.
  import { api } from '../lib/api';
  import { app, canOperate, clock } from '../lib/store.svelte';
  import { navigate } from '../lib/router.svelte';
  import { connectTo } from '../lib/actions.svelte';
  import { exclusive } from '../lib/settings.svelte';
  import { toast, toastError } from '../lib/toast.svelte';
  import { dvErrorMessage } from '../lib/dvmsg';
  import { stateLine, reflectorSilent, stateTone, modeLabel } from '../lib/dvpanel';
  import { agoText } from '../lib/nowcalling';
  import { buildAlerts, buildPins, dvLastCaller, dvPinPlan, pinActive, pinLocalNode, tileLinks, tileNodes, tileTalker } from '../lib/pins';
  import type { PinItem } from '../lib/pins';
  import type { DVFavorite } from '../lib/types';
  import { mergeHeard } from '../lib/lastheard';
  import LastHeardTable from './LastHeardTable.svelte';
  import Footer from './Footer.svelte';
  import RadioTower from '@lucide/svelte/icons/radio-tower';
  import Pin from '@lucide/svelte/icons/pin';
  import TriangleAlert from '@lucide/svelte/icons/triangle-alert';

  const op = $derived(canOperate());
  const dv = $derived(app.dv);
  const dvOn = $derived(!!dv?.installed);
  const tiles = $derived(tileNodes(app.nodes, dv));
  const alerts = $derived(buildAlerts(app.nodes, app.haveNodes, dv));
  const feed = $derived(mergeHeard(app.nowcalling?.recent, dv?.heard));

  // DV favourites are not in the store: load them once the engine is known to be installed.
  let dvFavs = $state<DVFavorite[]>([]);
  let loaded = false;
  $effect(() => {
    if (!dvOn || loaded) return;
    loaded = true;
    api.dvFavorites().then((f) => { dvFavs = f; }).catch(() => { loaded = false; });
  });
  const pins = $derived(buildPins(app.favorites, dvFavs));
  const dvLast = $derived(dvLastCaller(dv));

  function open(node: string) { app.selectedNode = node; navigate('/allstar'); }
  const tileState = (n: (typeof app.nodes)[number]) => (n.stale ? 'Stale' : n.txKeyed ? 'TX keyed' : n.rxKeyed ? 'RX keyed' : 'Idle');
  const tileClass = (n: (typeof app.nodes)[number]) => (n.stale ? 'border-warn/50' : n.txKeyed ? 'border-danger' : n.rxKeyed ? 'border-rx' : 'border-line');
  const nodeName = (n: (typeof app.nodes)[number]) => n.info?.callsign || n.info?.description || '';

  let busyKey = $state('');
  async function tap(p: PinItem) {
    if (busyKey || !op) return;
    busyKey = p.key;
    try {
      if (p.kind === 'asl' && p.node) {
        const local = pinLocalNode(app.selectedNode, tiles);
        if (!local) { toast('No local node to connect from', 'error'); return; }
        await connectTo(local, p.node, 'transceive', false, exclusive.value);
      } else if (p.fav) {
        const plan = dvPinPlan(dv, p.fav);
        if (!plan) { toast('Digital modes are not running', 'error'); return; }
        if (plan.mode) await api.dvMode(plan.mode);
        const r = await api.dvTune({ mode: p.fav.mode, tune: plan.tune });
        app.dv = r.snapshot;
        if (plan.link) await api.connect(plan.link.local, plan.link.node, 'transceive', false, plan.link.exclusive);
        toast(r.confirmed ? `Tuned ${p.label}` : 'Sent, not confirmed yet', r.confirmed ? 'ok' : 'info');
        if (plan.link) app.dv = await api.dvSnapshot();
      }
    } catch (e) { if (p.kind === 'dv') toast(dvErrorMessage(e), 'error'); else toastError(e); }
    finally { busyKey = ''; }
  }
  const age = (at: number) => (at ? agoText((clock.now - at) / 1000) : '');
</script>

<div class="space-y-3">
  {#if !app.haveNodes}
    <p class="py-10 text-center text-sm text-muted" role="status">Loading node status…</p>
  {:else}
    {#if alerts.length}
      <section class="space-y-1" aria-label="Alerts" data-testid="alerts">
        {#each alerts as a (a.key)}
          <a href={'#' + (a.href ?? '/')} class="flex items-center gap-2 rounded-md border border-warn/50 bg-warn/10 px-3 py-1.5 text-sm text-warn hover:bg-warn/20">
            <TriangleAlert size={15} class="shrink-0" aria-hidden="true" /><span class="min-w-0">{a.text}</span>
          </a>
        {/each}
      </section>
    {/if}

    <section class="grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3" aria-label="Nodes" data-testid="tiles">
      {#each tiles as n (n.node)}
        <button type="button" class="flex flex-col justify-start rounded-md border bg-surface px-3 py-2 text-left hover:bg-surface-2 {tileClass(n)} {app.selectedNode === n.node && tiles.length > 1 ? 'ring-1 ring-accent' : ''}" onclick={() => open(n.node)} aria-label="Node {n.node}, open the AllStarLink tab" data-testid="tile-{n.node}">
          <div class="flex items-baseline gap-2">
            <span class="font-mono text-base font-semibold">{n.node}</span>
            <span class="min-w-0 flex-1 truncate text-xs text-muted">{nodeName(n)}</span>
          </div>
          <div class="mt-1 flex items-center gap-2 text-xs">
            <span class="font-medium {n.stale ? 'text-warn' : n.txKeyed ? 'text-danger' : n.rxKeyed ? 'text-rx' : 'text-muted'}">{tileState(n)}</span>
            <span class="text-muted">{n.links.length} link{n.links.length === 1 ? '' : 's'}</span>
          </div>
          {#if tileTalker(n, app.nowcalling?.current, dv)}
            <div class="mt-1 truncate text-xs font-medium {n.txKeyed ? 'text-danger' : 'text-rx'}" data-testid="tile-talker-{n.node}">{n.txKeyed ? 'Talking' : 'Heard'}: <span class="font-mono">{tileTalker(n, app.nowcalling?.current, dv)}</span></div>
          {/if}
          {#if n.links.length}
            {@const tl = tileLinks(n, dv)}
            <ul class="mt-1.5 space-y-0.5 border-t border-line pt-1.5 text-xs" aria-label="Links" data-testid="tile-links-{n.node}">
              {#each tl.shown as l (l.node)}
                <li class="flex gap-1.5"><span class="shrink-0 font-mono">{l.node}</span><span class="min-w-0 truncate text-muted">{l.label}</span></li>
              {/each}
              {#if tl.more}<li class="text-muted">+{tl.more} more</li>{/if}
            </ul>
          {/if}
        </button>
      {/each}
      {#if dvOn && dv}
        {@const tone = stateTone(dv)}
        <a href="#/dv" class="rounded-md border bg-surface px-3 py-2 hover:bg-surface-2 {tone === 'warn' ? 'border-warn/50' : tone === 'ok' ? 'border-ok/50' : 'border-line'}" aria-label="Digital modes, open the DV tab" data-testid="tile-dv">
          <div class="flex items-baseline gap-2">
            <span class="inline-flex items-center gap-1 text-base font-semibold"><RadioTower size={14} class="text-accent" aria-hidden="true" />DV</span>
            <span class="min-w-0 flex-1 truncate text-xs text-muted">{dv.bridge.source !== 'none' && dv.bridge.mode ? modeLabel(dv.bridge.mode) : 'Mode unknown'}</span>
          </div>
          <div class="mt-1 truncate text-xs text-muted" title={stateLine(dv.bridge, reflectorSilent(dv))}>
            {dv.bridge.mode === 'LOOP' ? 'Echo test' : dv.bridge.tune ? `Tuned to ${dv.bridge.targetName || dv.bridge.tune}` : 'Nothing tuned'}
          </div>
          {#if dvLast}
            <div class="mt-1 truncate text-xs" data-testid="tile-dv-last">Last: <span class="font-mono">{dvLast.who}</span>{#if dvLast.at}<span class="ml-1 text-muted">· {age(dvLast.at)}</span>{/if}</div>
          {/if}
          <div class="mt-1 text-xs font-medium {dv.link?.linked ? 'text-ok' : 'text-warn'}">{dv.bridgeNode?.present ? (dv.link?.linked ? 'Linked' : 'Not linked') : 'No bridge node'}</div>
        </a>
      {/if}
    </section>
    {#if app.nodes.length === 0 && !alerts.length}
      <p class="rounded-md border border-line bg-surface px-3 py-8 text-center text-sm text-muted">No local nodes found. Is Asterisk running with app_rpt?</p>
    {/if}

    <section aria-label="Pinned favorites">
      <h2 class="mb-1 text-sm font-semibold uppercase tracking-wide text-muted">Pinned</h2>
      {#if pins.length}
        <div class="flex flex-wrap gap-2" data-testid="pins">
          {#each pins as p (p.key)}
            <button type="button" class="btn !min-h-10 !px-3 {p.kind === 'dv' ? 'border-accent/50' : ''}" disabled={!op || !!busyKey} aria-label="{p.kind === 'asl' ? 'Connect to' : 'Tune'} {p.label}" title={p.kind === 'asl' ? `Connect ${pinLocalNode(app.selectedNode, tiles) || 'a local node'} to ${p.sub}` : `Switch to ${p.sub} and tune ${p.fav?.value}`} onclick={() => tap(p)}>
              {#if pinActive(p, app.nodes, dv)}<span class="inline-block size-2 shrink-0 rounded-full bg-ok" data-testid="pin-active" aria-hidden="true"></span><span class="sr-only">active</span>{/if}
              <span class="rounded bg-surface-2 px-1 text-[10px] font-semibold text-muted">{p.kind === 'asl' ? 'ASL' : 'DV'}</span>
              <span class="truncate">{p.label}</span>
              <span class="font-mono text-xs text-muted">{p.sub}</span>
            </button>
          {/each}
        </div>
      {:else}
        <p class="rounded-md border border-line bg-surface px-3 py-3 text-sm text-muted">Nothing pinned yet. Use the <Pin size={13} class="inline" aria-hidden="true" /> button on a favorite (AllStarLink and DV tabs) to put a one-tap button here.</p>
      {/if}
    </section>

    <section aria-label="Activity">
      <h2 class="mb-1 text-sm font-semibold uppercase tracking-wide text-muted">Last heard</h2>
      <div class="rounded-md border border-line bg-surface px-3 py-1">
        {#if feed.length === 0}
          <p class="py-3 text-center text-sm text-muted">Nothing heard yet.</p>
        {:else}
          <LastHeardTable rows={feed} testid="activity" />
        {/if}
      </div>
    </section>
  {/if}
  <Footer />
</div>
