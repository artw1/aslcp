<script lang="ts">
  import type { PubLink, PubNode } from '../../lib/types';
  import { clock } from '../../lib/store.svelte';
  import { pub } from '../../lib/publicLive.svelte';
  import { fmtDuration, infoLine, KIND_LABEL, MODE_LABEL } from '../../lib/util';
  import Mic from '@lucide/svelte/icons/mic';
  import Ear from '@lucide/svelte/icons/ear';
  import Audio from '@lucide/svelte/icons/audio-lines';
  import Minus from '@lucide/svelte/icons/minus';

  let { node }: { node: PubNode } = $props();
  const elapsed = $derived(Math.max(0, Math.floor((clock.now - (pub.nodeAt[node.node] ?? clock.now)) / 1000)));
  // Keyed links first, then newest connection first.
  const links = $derived([...node.links].sort((a, b) => Number(b.keyed) - Number(a.keyed) || a.connected - b.connected));
  const SHORT: Record<string, string> = { T: 'Xcvr', R: 'Mon', L: 'Local', C: '…' };
  const connTime = (l: PubLink) => fmtDuration(l.connected + elapsed);
  const received = (l: PubLink) => (l.keyed ? 'now' : l.sinceKey == null ? 'never' : fmtDuration(l.sinceKey + elapsed));
  const title = (l: PubLink) => l.info?.callsign || (l.info ? '' : KIND_LABEL[l.kind]);
  const nodeLine = $derived([node.info?.callsign, node.info?.description, node.info?.location].filter(Boolean).join(' '));
  const localState = $derived(node.txKeyed ? 'TX keyed' : node.rxKeyed ? 'RX keyed' : 'Idle');
  const localClass = $derived(node.stale ? '' : node.txKeyed ? 'row-tx' : node.rxKeyed ? 'row-rx' : '');
</script>

<table class="tbl table-fixed" aria-label="Connection status for node {node.node}">
  <colgroup>
    <col class="w-16 sm:w-20" /><col /><col class="w-24 max-md:w-0" /><col class="w-12 max-md:w-0" />
    <col class="w-[4.5rem] sm:w-24" /><col class="w-14 sm:w-28" />
  </colgroup>
  <thead>
    <tr class="title">
      <th colspan="6" class="!py-1.5">
        <div class="flex items-center gap-2">
          <span class="min-w-0 flex-1 truncate text-left"><span class="font-mono">{node.node}</span>{#if nodeLine}<span class="text-muted">&nbsp;·&nbsp;</span>{nodeLine}{/if}</span>
          <span class="shrink-0 text-xs font-normal text-muted">{node.links.length} link{node.links.length === 1 ? '' : 's'}</span>
        </div>
      </th>
    </tr>
    {#if links.length > 0}
      <tr>
        <th scope="col">Node</th><th scope="col">{pub.snap?.callsigns ? 'Node info' : 'Type'}</th>
        <th scope="col" class="max-md:w-0 max-md:overflow-hidden max-md:!p-0 max-md:!border-0 max-md:invisible">Received</th>
        <th scope="col" class="max-md:w-0 max-md:overflow-hidden max-md:!p-0 max-md:!border-0 max-md:invisible">Dir</th>
        <th scope="col"><span class="sm:hidden">Time</span><span class="max-sm:hidden">Connected</span></th><th scope="col">Mode</th>
      </tr>
    {/if}
  </thead>
  <tbody>
    <tr class={localClass}>
      <td class="num font-semibold">{node.node}</td>
      <td colspan="5">
        <div class="flex flex-wrap items-center gap-x-2">
          {#if node.stale}<span class="font-medium text-warn">Stale: no data from the node</span><span class="muted text-muted">· last state: {localState}</span>
          {:else}
            {#if node.txKeyed}<Mic size={14} class="shrink-0" aria-hidden="true" />{:else if node.rxKeyed}<Ear size={14} class="shrink-0" aria-hidden="true" />{:else}<Minus size={14} class="shrink-0 text-muted" aria-hidden="true" />{/if}
            <span class="font-semibold">{localState}</span>
            {#if node.txKeyed && node.rxKeyed}<span class="rounded border border-white/60 px-1 text-xs font-semibold"><Ear size={11} class="mr-0.5 inline" aria-hidden="true" />RX</span>{/if}
            {#if links.length === 0}<span class="muted text-muted">· no links</span>{/if}
          {/if}
        </div>
      </td>
    </tr>
    {#each links as l, i (l.node + '/' + i)}
      <tr class={l.keyed ? 'row-tx' : ''}>
        <td class="num font-semibold" title={KIND_LABEL[l.kind]}>{l.node}</td>
        <td class="max-w-0">
          <div class="flex items-center gap-1.5">
            {#if l.keyed}<span class="inline-flex shrink-0 items-center gap-1 rounded border border-white/60 px-1 text-xs font-bold" role="img" aria-label="Keyed: audio coming in"><Audio size={12} aria-hidden="true" /><span class="max-sm:hidden">KEY</span></span>{/if}
            <span class="truncate"><span class="font-semibold">{l.label || title(l)}</span> <span class="muted text-muted">{infoLine(l.info)}</span></span>
          </div>
          <div class="muted text-xs text-muted md:hidden">heard {received(l)} · {l.direction}{#if l.mode === 'C'} · connecting{/if}</div>
        </td>
        <td class="max-md:w-0 max-md:overflow-hidden max-md:!p-0 max-md:!border-0 max-md:invisible">{received(l)}</td>
        <td class="max-md:w-0 max-md:overflow-hidden max-md:!p-0 max-md:!border-0 max-md:invisible">{l.direction}</td>
        <td>{connTime(l)}</td>
        <td class="whitespace-nowrap" title={MODE_LABEL[l.mode]}><span class="sm:hidden">{SHORT[l.mode]}</span><span class="hidden sm:inline">{MODE_LABEL[l.mode]}</span></td>
      </tr>
    {/each}
  </tbody>
</table>
