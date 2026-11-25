<script lang="ts">
  // A node tile of the Dashboard and of the public page. With `onopen` the head is a button that opens the node; with `onunlink`
  // each linked node gets an Unlink button. Without both (the public page) it is a plain read-only card.
  import Unlink from '@lucide/svelte/icons/unlink';
  import { tileBorder, tileState } from '../lib/tiles';
  import type { TileView } from '../lib/tiles';

  let { tile, selected = false, busy = false, onopen = undefined, onunlink = undefined }: {
    tile: TileView; selected?: boolean; busy?: boolean; onopen?: () => void; onunlink?: (target: string) => void;
  } = $props();
</script>

{#snippet head()}
  <div class="flex items-baseline gap-2">
    <span class="font-mono text-base font-semibold">{tile.node}</span>
    <span class="min-w-0 flex-1 truncate text-xs text-muted">{tile.name}</span>
  </div>
  <div class="mt-1 flex items-center gap-2 text-xs">
    <span class="font-medium {tile.stale ? 'text-warn' : tile.txKeyed ? 'text-danger' : tile.rxKeyed ? 'text-rx' : 'text-muted'}">{tileState(tile)}</span>
    <span class="text-muted">{tile.linkCount} link{tile.linkCount === 1 ? '' : 's'}</span>
  </div>
  {#if tile.talker}
    <div class="mt-1 truncate text-xs font-medium {tile.txKeyed ? 'text-danger' : 'text-rx'}" data-testid="tile-talker-{tile.node}">{tile.txKeyed ? 'Talking' : 'Heard'}: <span class="font-mono">{tile.talker}</span></div>
  {/if}
{/snippet}

<div class="flex flex-col justify-start rounded-md border bg-surface {tileBorder(tile)} {selected ? 'ring-1 ring-accent' : ''}" data-testid="tile-{tile.node}">
  {#if onopen}
    <button type="button" class="w-full rounded-t-md px-3 pb-1 pt-2 text-left hover:bg-surface-2" onclick={onopen} aria-label="Node {tile.node}, open the AllStarLink tab" data-testid="tile-open-{tile.node}">{@render head()}</button>
  {:else}
    <div class="px-3 pb-1 pt-2">{@render head()}</div>
  {/if}
  {#if tile.shown.length}
    <ul class="mx-3 mb-2 mt-0.5 space-y-0.5 border-t border-line pt-1.5 text-xs" aria-label="Links" data-testid="tile-links-{tile.node}">
      {#each tile.shown as l, i (l.node + '/' + i)}
        <li class="flex items-center gap-1.5">
          <span class="shrink-0 font-mono">{l.node}</span>
          <span class="min-w-0 flex-1 truncate text-muted">{l.label}</span>
          {#if onunlink}
            <button type="button" class="btn btn-sm btn-bad-outline shrink-0 max-sm:min-h-11 pointer-coarse:min-h-11" disabled={busy} title="Unlink {l.node}" aria-label="Unlink {l.node}" data-testid="unlink-{tile.node}-{l.node}" onclick={() => onunlink(l.node)}><Unlink size={12} aria-hidden="true" />Unlink</button>
          {/if}
        </li>
      {/each}
      {#if tile.more}<li class="text-muted">+{tile.more} more</li>{/if}
    </ul>
  {/if}
</div>
