<script lang="ts">
  import type { Link, NodeStatus } from '../lib/types';
  import { app, clock, canOperate } from '../lib/store.svelte';
  import { openNode } from '../lib/form.svelte';
  import { disconnectFrom, disconnectAll } from '../lib/actions.svelte';
  import { fmtDuration, infoLine, KIND_LABEL, MODE_LABEL } from '../lib/util';
  import { nodeLabel, bridgeRxTarget } from '../lib/dvpanel';
  import Menu from './Menu.svelte';
  import Confirm from './Confirm.svelte';
  import Unplug from '@lucide/svelte/icons/unplug';
  import Chevron from '@lucide/svelte/icons/chevron-down';
  import Dots from '@lucide/svelte/icons/ellipsis';
  import Mic from '@lucide/svelte/icons/mic';
  import Ear from '@lucide/svelte/icons/ear';
  import Audio from '@lucide/svelte/icons/audio-lines';
  import Minus from '@lucide/svelte/icons/minus';

  let { node }: { node: NodeStatus } = $props();
  const op = $derived(canOperate());
  let confirmAll = $state(false);
  const elapsed = $derived(Math.max(0, Math.floor((clock.now - app.stateAt) / 1000)));
  // Keyed links first, then newest connection first.
  const links = $derived([...node.links].sort((a, b) => Number(b.keyed) - Number(a.keyed) || a.connected - b.connected));
  const selected = $derived(app.nodes.length > 1 && app.selectedNode === node.node);

  const SHORT: Record<string, string> = { T: 'Xcvr', R: 'Mon', L: 'Local', C: '…' };
  const connTime = (l: Link) => fmtDuration(l.connected + elapsed);
  const received = (l: Link) => (l.keyed ? 'now' : l.sinceKey == null ? 'never' : fmtDuration(l.sinceKey + elapsed));
  const title = (l: Link) => l.info?.callsign || nodeLabel(app.dvswitch, l.node) || (l.kind === 'echolink' ? 'EchoLink' : '');
  const bridgeName = $derived(nodeLabel(app.dvswitch, node.node));
  const nodeLine = $derived(bridgeName || [node.info?.callsign, node.info?.description, node.info?.location].filter(Boolean).join(' '));
  const rxTarget = $derived(bridgeName && node.rxKeyed ? bridgeRxTarget(app.dvswitch) : null);
  const localTx = $derived(node.txKeyed);
  const localState = $derived(localTx ? 'TX keyed' : node.rxKeyed ? 'RX keyed' : 'Idle');
  const localClass = $derived(node.stale ? '' : localTx ? 'row-tx' : node.rxKeyed ? 'row-rx' : '');
  const staleText = $derived.by(() => {
    const r = (node.staleReason ?? '').trim();
    if (r === '' || r.startsWith('AMI disconnected')) return 'AMI disconnected';
    return r.length > 80 ? r.slice(0, 79) + '…' : r;
  });
</script>

<table class="tbl table-fixed" aria-label="Connection status for node {node.node}">
  <colgroup>
    <col class="w-16 sm:w-20" /><col /><col class="w-24 max-md:w-0" /><col class="w-12 max-md:w-0" />
    <col class="w-[4.5rem] sm:w-24" /><col class="w-14 sm:w-28" /><col class="w-[3.25rem]" />
  </colgroup>
  <thead>
    <tr class="title">
      <th colspan="7" class="!py-1.5">
        <div class="flex items-center gap-2">
          <button type="button" class="min-w-0 flex-1 truncate text-left {selected ? 'text-accent' : ''}" onclick={() => (app.selectedNode = node.node)} title="Use this node for the connect bar" aria-pressed={app.nodes.length > 1 ? selected : undefined}>
            <span class="font-mono">{node.node}</span>{#if nodeLine}<span class="text-muted">&nbsp;·&nbsp;</span>{nodeLine}{/if}
          </button>
          <span class="shrink-0 text-xs font-normal text-muted">{node.links.length} link{node.links.length === 1 ? '' : 's'}</span>
          <Menu label="Node {node.node} actions" disabled={!op}>
            {#snippet trigger()}<Dots size={16} />{/snippet}
            {#snippet items(close)}
              <button role="menuitem" class="btn btn-sm w-full justify-start border-transparent text-danger" disabled={node.links.length === 0} onclick={() => { close(); confirmAll = true; }}><Unplug size={14} />Disconnect all…</button>
            {/snippet}
          </Menu>
        </div>
      </th>
    </tr>
    {#if links.length > 0}
      <tr>
        <th>Node</th><th>Node info</th><th class="max-md:w-0 max-md:overflow-hidden max-md:!p-0 max-md:!border-0 max-md:invisible">Received</th><th class="max-md:w-0 max-md:overflow-hidden max-md:!p-0 max-md:!border-0 max-md:invisible">Dir</th>
        <th><span class="sm:hidden">Time</span><span class="max-sm:hidden">Connected</span></th><th>Mode</th><th><span class="sr-only">Actions</span></th>
      </tr>
    {/if}
  </thead>
  <tbody>
    <tr class={localClass}>
      <td class="num font-semibold">{node.node}</td>
      <td colspan="6">
        <div class="flex flex-wrap items-center gap-x-2">
          {#if node.stale}<span class="font-medium text-warn" title={node.staleReason}>Stale — {staleText}</span><span class="muted text-muted">· last state: {localState}</span>
          {:else}
            {#if localTx}<Mic size={14} class="shrink-0" aria-hidden="true" />{:else if node.rxKeyed}<Ear size={14} class="shrink-0" aria-hidden="true" />{:else}<Minus size={14} class="shrink-0 text-muted" aria-hidden="true" />{/if}
            <span class="font-semibold">{localState}</span>{#if rxTarget && !localTx}<span class="muted text-muted">·</span><span class="font-semibold" data-testid="dv-rx">{rxTarget}</span>{/if}
            {#if localTx && node.rxKeyed}<span class="rounded border border-white/60 px-1 text-xs font-semibold"><Ear size={11} class="mr-0.5 inline" aria-hidden="true" />RX</span>{/if}
            {#if links.length === 0}<span class="muted text-muted">· no links</span>{/if}
          {/if}
        </div>
      </td>
    </tr>
    {#each links as l (l.node)}
      <tr class={l.keyed ? 'row-tx' : ''}>
        <td class="num font-semibold" title={l.ip ? `${KIND_LABEL[l.kind]} · ${l.ip}` : KIND_LABEL[l.kind]}><button type="button" class="font-mono font-semibold underline decoration-dotted underline-offset-2 hover:decoration-solid" onclick={() => openNode(l.node, node.node)} aria-label="Connect options for node {l.node}">{l.node}</button></td>
        <td class="max-w-0">
          <div class="flex items-center gap-1.5">
            {#if l.keyed}<span class="inline-flex shrink-0 items-center gap-1 rounded border border-white/60 px-1 text-xs font-bold" role="img" aria-label="Keyed: audio coming in"><Audio size={12} aria-hidden="true" /><span class="max-sm:hidden">KEY</span></span>{/if}
            <span class="truncate"><span class="font-semibold">{title(l)}</span> <span class="muted text-muted">{infoLine(l.info)}</span></span>
          </div>
          <div class="muted text-xs text-muted md:hidden">heard {received(l)} · {l.direction}{#if l.state === 'CONNECTING'} · connecting{/if}</div>
        </td>
        <td class="max-md:w-0 max-md:overflow-hidden max-md:!p-0 max-md:!border-0 max-md:invisible">{received(l)}</td>
        <td class="max-md:w-0 max-md:overflow-hidden max-md:!p-0 max-md:!border-0 max-md:invisible">{l.direction}</td>
        <td>{connTime(l)}</td>
        <td class="whitespace-nowrap" title={MODE_LABEL[l.mode]}><span class="sm:hidden">{SHORT[l.mode]}</span><span class="hidden sm:inline">{MODE_LABEL[l.mode]}</span></td>
        <td class="whitespace-nowrap text-right">
          <span class="inline-flex items-center">
            <button class="rowbtn" disabled={!op} onclick={() => disconnectFrom(node.node, l.node)} aria-label="Disconnect {l.node}" title="Disconnect {l.node}"><Unplug size={14} /></button>
            <Menu label="More actions for {l.node}" disabled={!op}>
              {#snippet trigger()}<Chevron size={13} />{/snippet}
              {#snippet items(close)}
                <button role="menuitem" class="btn btn-sm w-full justify-start border-transparent text-danger" onclick={() => { close(); disconnectFrom(node.node, l.node, true); }}>Disconnect permanently</button>
              {/snippet}
            </Menu>
          </span>
        </td>
      </tr>
    {/each}
  </tbody>
</table>

<Confirm bind:open={confirmAll} title="Disconnect all links?" message="Node {node.node} will drop all {node.links.length} link{node.links.length === 1 ? '' : 's'}, permanent ones included." confirmLabel="Disconnect all" onconfirm={() => disconnectAll(node.node)} />
