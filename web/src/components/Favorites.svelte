<script lang="ts">
  import { app, canOperate, clock, refreshFavorites, setFavScan } from '../lib/store.svelte';
  import { api } from '../lib/api';
  import { connectTo } from '../lib/actions.svelte';
  import { form, pickNode } from '../lib/form.svelte';
  import { exclusive } from '../lib/settings.svelte';
  import { toastError } from '../lib/toast.svelte';
  import type { Favorite } from '../lib/types';
  import FavoriteDialog from './FavoriteDialog.svelte';
  import ImportDialog from './ImportDialog.svelte';
  import Confirm from './Confirm.svelte';
  import Menu from './Menu.svelte';
  import Pencil from '@lucide/svelte/icons/pencil';
  import Trash from '@lucide/svelte/icons/trash-2';
  import Up from '@lucide/svelte/icons/chevron-up';
  import Down from '@lucide/svelte/icons/chevron-down';
  import Grip from '@lucide/svelte/icons/grip-vertical';
  import Download from '@lucide/svelte/icons/download';
  import Settings2 from '@lucide/svelte/icons/settings-2';
  import Dots from '@lucide/svelte/icons/ellipsis';
  import Link2 from '@lucide/svelte/icons/link-2';

  const op = $derived(canOperate());
  let manage = $state(false);
  let editing = $state<Favorite | null>(null), dlg = $state(false), imp = $state(false);
  let delFav = $state<Favorite | null>(null), delOpen = $state(false);
  let dragId = $state<number | null>(null), overId = $state<number | null>(null);

  const node = $derived(app.nodes.find((n) => n.node === app.selectedNode));
  // Favorites currently linked to any of our local nodes (keyed if any link is keyed).
  const linked = $derived.by(() => {
    const m = new Map<string, { keyed: boolean }>();
    for (const n of app.nodes) for (const l of n.links) { const o = m.get(l.node); m.set(l.node, { keyed: l.keyed || !!o?.keyed }); }
    return m;
  });
  const scan = $derived(app.favstats.enabled);
  const localNodes = $derived(new Set(app.nodes.map((x) => x.node)));
  /** Node numbers the stats service lists: 2000..999999 (below are private, 7 digits are EchoLink). */
  const validNode = (n: string) => /^([2-9]\d{3}|[1-9]\d{4,5})$/.test(n);
  // The Rx% and LCnt columns need room: Description and Location drop out first on narrow screens.
  const descBp = $derived(scan ? 'lg:table-cell' : 'sm:table-cell'), locBp = $derived(scan ? 'xl:table-cell' : 'md:table-cell');
  const act = (n: string) => (scan ? app.favstats.stats[n] : undefined);
  let sortKey = $state<'rx' | 'lcnt' | null>(null), sortDesc = $state(true);
  function sortBy(k: 'rx' | 'lcnt') {
    if (sortKey === k) sortDesc = !sortDesc; else { sortKey = k; sortDesc = true; }
  }
  // Missing values always sort last.
  function sortVal(f: Favorite): number | null {
    const a = act(f.node);
    if (!a) return null;
    return sortKey === 'rx' ? a.rxPct : a.linkCnt;
  }
  function sorted(list: Favorite[]): Favorite[] {
    if (!sortKey || !scan) return list;
    return [...list].sort((a, b) => {
      const x = sortVal(a), y = sortVal(b);
      if (x == null || y == null) return x == null && y == null ? 0 : x == null ? 1 : -1;
      return sortDesc ? y - x : x - y;
    });
  }
  const ariaSort = (k: 'rx' | 'lcnt') => (sortKey === k ? (sortDesc ? 'descending' : 'ascending') : 'none');
  const retryIn = $derived(app.favstats.retryAt ? Math.ceil((app.favstats.retryAt - clock.now) / 1000) : 0);
  const nextIn = $derived(app.favstats.nextAt ? Math.max(0, Math.ceil((app.favstats.nextAt - clock.now) / 1000)) : null);
  const scanState = $derived.by(() => {
    const f = app.favstats;
    if (!scan) return '';
    if (f.backoff) {
      const why = f.backoffReason === 'rate-limit' ? 'rate limited' : f.backoffReason === 'service-error' ? 'stats service error' : f.backoffReason === 'network-error' ? 'paused after a network error' : 'paused';
      return retryIn > 0 ? `${why}, retrying in ${retryIn}s` : `${why}, retrying`;
    }
    if (f.total == null) return 'starting…';
    if (f.total === 0) return 'no favorites to check';
    return `checked ${f.checked} of ${f.total}` + (nextIn == null ? '' : ` · next in ${nextIn}s`);
  });
  // Rx% cell: values, "no data" for checked nodes without stats, tooltips with the age of the last check.
  const scanned = (n: string) => validNode(n) && !localNodes.has(n);
  const checkedAgo = (n: string) => {
    const t = app.favstats.checkedAt[n];
    return t == null ? null : Math.max(0, Math.round((clock.now - t) / 1000));
  };
  const agoText = (n: string) => { const s = checkedAgo(n); return s == null ? 'not checked yet' : `checked ${s}s ago`; };
  const rxTitle = (n: string) => {
    if (!validNode(n)) return 'Not scanned (EchoLink or private node)';
    if (localNodes.has(n)) return 'Not scanned (one of your nodes)';
    if (!act(n) && checkedAgo(n) != null) return `No data (offline) · ${agoText(n)}`;
    return `Lifetime TX time as % of uptime · ${agoText(n)}`;
  };
  const rxText = (n: string) => {
    const a = act(n);
    if (a) return a.rxPct == null ? '-' : a.rxPct + '%';
    return scanned(n) && checkedAgo(n) != null ? '—' : '';
  };
  let scanBusy = $state(false);
  async function toggleScan(on: boolean) {
    scanBusy = true;
    try { await setFavScan(on); } catch (e) { toastError(e); } finally { scanBusy = false; }
  }
  const cellClass = (n: string) => {
    const a = act(n);
    if (!a) return 'text-muted';
    if (a.keyed) return 'row-tx-cell';
    if (a.active) return a.webTx ? 'row-rx-cell act-web' : 'row-rx-cell';
    return 'text-muted';
  };
  const cellTitle = (n: string) => {
    const a = act(n);
    if (!a) return undefined;
    return [a.keyed ? 'Keyed' : '', a.active ? 'Active' : 'Not active', a.webTx ? 'WebTransceiver' : ''].filter(Boolean).join(', ');
  };
  // Named groups first, ungrouped last.
  const groups = $derived.by(() => {
    const m = new Map<string, Favorite[]>();
    for (const f of [...app.favorites].sort((a, b) => a.group.localeCompare(b.group) || a.position - b.position)) {
      if (!m.has(f.group)) m.set(f.group, []);
      m.get(f.group)!.push(f);
    }
    const named = [...m.entries()].filter(([g]) => g !== '');
    return m.has('') ? [...named, ['', m.get('')!] as [string, Favorite[]]] : named;
  });
  const flatIds = () => groups.flatMap(([, l]) => l.map((f) => f.id));
  const indexOf = (id: number) => flatIds().indexOf(id) + 1;
  const showHeads = $derived(groups.length > 1 || (groups.length === 1 && groups[0][0] !== ''));
  const next = $derived(scan ? app.favstats.next : '');
  const COLS = $derived((manage && op ? 6 : 5) + (scan ? 2 : 0));

  async function saveOrder(ids: number[]) {
    try { await api.reorder(ids); await refreshFavorites(); } catch (e) { toastError(e); }
  }
  function move(f: Favorite, dir: -1 | 1) {
    const list = groups.find(([g]) => g === f.group)![1].map((x) => x.id);
    const i = list.indexOf(f.id), j = i + dir;
    if (j < 0 || j >= list.length) return;
    [list[i], list[j]] = [list[j], list[i]];
    const out: number[] = [];
    for (const [g, l] of groups) out.push(...(g === f.group ? list : l.map((x) => x.id)));
    saveOrder(out);
  }
  async function drop(target: Favorite) {
    const id = dragId; dragId = overId = null;
    if (id == null || id === target.id) return;
    const src = app.favorites.find((x) => x.id === id)!;
    try {
      if (src.group !== target.group) await api.updateFavorite(id, { group: target.group });
    } catch (e) { toastError(e); return; }
    const ids = flatIds().filter((x) => x !== id);
    ids.splice(ids.indexOf(target.id), 0, id);
    await saveOrder(ids);
  }
  async function remove(f: Favorite) {
    try { await api.deleteFavorite(f.id); await refreshFavorites(); } catch (e) { toastError(e); }
  }
  function rowClick(f: Favorite) { if (!manage) pickNode(f.node); }
  function rowDbl(f: Favorite) {
    if (manage || !op || !node) return;
    pickNode(f.node);
    connectTo(node.node, f.node, 'transceive', form.permanent, exclusive.value);
  }
  const name = (f: Favorite) => f.label || f.info?.callsign || f.node;
</script>

<section aria-label="Favorites">
  <div class="mb-1 flex flex-wrap items-center gap-x-2 gap-y-1">
    <h2 class="text-sm font-semibold uppercase tracking-wide text-muted">Favorites <span class="font-normal">({app.favorites.length})</span></h2>
    {#if op}
      <button type="button" class="btn btn-sm {scan ? 'btn-primary' : 'border-accent text-accent'}" aria-pressed={scan} disabled={scanBusy}
        title="Show activity for favorite nodes from stats.allstarlink.org" onclick={() => toggleScan(!scan)}>
        {#if scan}<span class="scan-dot pulse" aria-hidden="true"></span>Scanning{:else}Start scan{/if}
      </button>
    {:else}
      <span class="rounded border border-line px-1.5 py-0.5 text-xs font-medium {scan ? 'text-fg' : 'text-muted'}" title="Show activity for favorite nodes from stats.allstarlink.org">Scan {scan ? 'on' : 'off'}</span>
    {/if}
    {#if scan}<span class="text-xs text-muted" aria-live="off">{scanState}</span>{/if}
    <span class="ml-auto hidden text-xs text-muted lg:inline">{manage ? 'Drag rows or use the arrows to reorder' : 'Click to select, double-click to connect'}</span>
    {#if op}
      <button class="btn btn-sm {manage ? 'btn-primary' : ''}" aria-pressed={manage} onclick={() => (manage = !manage)}><Settings2 size={13} />Manage</button>
      <Menu label="Favorites menu" btnClass="btn btn-sm btn-icon" align="right">
        {#snippet trigger()}<Dots size={15} />{/snippet}
        {#snippet items(close)}
          <button role="menuitem" class="btn btn-sm w-full justify-start border-transparent" onclick={() => { close(); imp = true; }}><Download size={14} />Import from AllScan</button>
        {/snippet}
      </Menu>
    {/if}
  </div>
  <div class="rounded-md border border-line bg-surface">
    <table class="tbl" aria-label="Favorites">
      <thead>
        <tr>
          {#if manage && op}<th class="w-24"><span class="sr-only">Reorder</span></th>{/if}
          <th class="w-8">#</th><th class="w-20 sm:w-24">Node</th><th>Name</th><th class="hidden {descBp}">Description</th><th class="hidden {locBp}">Location</th>
          {#if scan}
            <th class="w-14 text-right" aria-sort={ariaSort('rx')}><button type="button" class="hover:underline" title="Lifetime TX time as % of uptime" onclick={() => sortBy('rx')}>Rx%{sortKey === 'rx' ? (sortDesc ? ' ▼' : ' ▲') : ''}</button></th>
            <th class="w-14 text-right" aria-sort={ariaSort('lcnt')}><button type="button" class="hover:underline" title="Number of links the node has" onclick={() => sortBy('lcnt')}>LCnt{sortKey === 'lcnt' ? (sortDesc ? ' ▼' : ' ▲') : ''}</button></th>
          {/if}
        </tr>
      </thead>
      <tbody>
        {#if app.favorites.length === 0}
          <tr><td colspan={COLS + 1} class="py-6 text-center text-muted">{app.haveFavorites ? 'No favorites yet. Type a node above and press Add Favorite, or import from AllScan.' : 'Loading…'}</td></tr>
        {/if}
        {#each groups as [group, list] (group)}
          {#if showHeads}
            <tr class="title"><td colspan={COLS + 1} class="!py-0.5 text-xs font-semibold uppercase tracking-wide text-muted">{group || 'Ungrouped'}</td></tr>
          {/if}
          {#each sorted(list) as f, i (f.id)}
            {@const l = linked.get(f.node)}
            {@const a = act(f.node)}
            <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions -->
            <tr class="{manage ? '' : 'cursor-pointer'} hover:bg-surface-2 {overId === f.id ? 'bg-accent/15' : ''} {form.target.trim() === f.node ? 'bg-accent/10' : ''}"
              draggable={manage && op} ondragstart={() => (dragId = f.id)} ondragend={() => (dragId = overId = null)}
              ondragover={(e) => { if (dragId != null) { e.preventDefault(); overId = f.id; } }} ondrop={(e) => { e.preventDefault(); drop(f); }}
              onclick={() => rowClick(f)} ondblclick={() => rowDbl(f)}>
              {#if manage && op}
                <td class="whitespace-nowrap">
                  <span class="inline-flex items-center">
                    <span class="cursor-grab text-muted" aria-hidden="true"><Grip size={14} /></span>
                    <button class="rowbtn" aria-label="Move {name(f)} up" disabled={i === 0} onclick={() => move(f, -1)}><Up size={13} /></button>
                    <button class="rowbtn" aria-label="Move {name(f)} down" disabled={i === list.length - 1} onclick={() => move(f, 1)}><Down size={13} /></button>
                    <button class="rowbtn" aria-label="Edit {name(f)}" onclick={() => { editing = f; dlg = true; }}><Pencil size={13} /></button>
                    <button class="rowbtn text-danger" aria-label="Delete {name(f)}" onclick={() => { delFav = f; delOpen = true; }}><Trash size={13} /></button>
                  </span>
                </td>
              {/if}
              <td class="num text-center {cellClass(f.node)}" title={next === f.node ? 'Next to check' : cellTitle(f.node)} aria-label={next === f.node ? 'Next to check' : undefined}><span class="nx" class:nx-on={next === f.node}>{indexOf(f.id)}</span></td>
              <td class="num whitespace-nowrap font-semibold {l ? 'linked-cell' : ''}" title={l ? (l.keyed ? 'Linked to this node, keyed' : 'Linked to this node') : undefined}>
                {#if l}<Link2 size={12} class="mr-0.5 inline" aria-hidden="true" /><span class="sr-only">linked</span>{/if}<button type="button" class="font-mono font-semibold text-accent hover:underline" onclick={(e) => { e.stopPropagation(); rowClick(f); }} aria-label="Select node {f.node}{l ? ' (linked)' : ''}">{f.node}</button>
              </td>
              <td class="max-w-0">
                <div class="truncate font-medium">{name(f)}</div>
                <div class="truncate text-xs text-muted {scan ? 'lg:hidden' : 'sm:hidden'}">{[f.info?.description, f.info?.location].filter(Boolean).join(' · ')}</div>
              </td>
              <td class="hidden max-w-0 truncate {descBp}" title={f.info?.description}>{f.info?.description ?? ''}</td>
              <td class="hidden max-w-0 truncate {locBp}" title={f.info?.location}>{f.info?.location ?? ''}</td>
              {#if scan}
                <td class="num text-right" title={rxTitle(f.node)}>{rxText(f.node)}</td>
                <td class="num text-right">{a ? a.linkCnt : ''}</td>
              {/if}
            </tr>
          {/each}
        {/each}
      </tbody>
    </table>
  </div>
</section>

<FavoriteDialog bind:open={dlg} fav={editing} />
<ImportDialog bind:open={imp} />
<Confirm bind:open={delOpen} title="Delete favorite?" message="Remove {delFav?.label || delFav?.node} from your favorites?" confirmLabel="Delete" onconfirm={() => delFav && remove(delFav)} />

<style>
  /* Next to check: a blinking underscore under the # number (absolute, so the row never changes size). Static under reduced motion. */
  .nx { position: relative; display: inline-block; }
  .nx-on::after { content: ''; position: absolute; left: 0; right: 0; bottom: -3px; height: 2px; background: var(--accent); animation: nx-blink 1s steps(1, end) infinite; }
  :global(td.row-tx-cell) .nx-on::after, :global(td.row-rx-cell) .nx-on::after { background: currentColor; }
  @keyframes nx-blink { 0%, 55% { opacity: 1; } 56%, 100% { opacity: 0; } }
  @media (prefers-reduced-motion: reduce) { .nx-on::after { animation: none; } }
  .scan-dot { display: inline-block; width: 0.5rem; height: 0.5rem; border-radius: 9999px; background: currentColor; }
  :global(.tbl td.row-tx-cell) { background: var(--row-tx); color: var(--row-fg); font-weight: 600; }
  :global(.tbl td.row-rx-cell.act-web) { background: color-mix(in srgb, var(--row-rx) 82%, #4ade80); }
  :global(.tbl td.linked-cell) { background: color-mix(in srgb, var(--accent) 22%, transparent); box-shadow: inset 3px 0 0 var(--accent); }
  :global(.tbl td.row-rx-cell) { background: var(--row-rx); color: var(--row-fg); font-weight: 600; }
</style>
