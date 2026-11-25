<script lang="ts">
  // The Now Calling banner of the public page: the same red strip as the Dashboard's, read-only (no details sheet).
  import { clock } from '../../lib/store.svelte';
  import { mmss } from '../../lib/nowcalling';
  import type { PubBanner } from '../../lib/tiles';
  let { call }: { call: PubBanner } = $props();
  const secs = $derived(call.startMs ? Math.max(0, Math.floor((clock.now - call.startMs) / 1000)) : 0);
</script>

<div class="nc-tx rounded-md" role="status" aria-label="Now calling: {call.main}, {call.source}" data-testid="public-nowcalling">
  <div class="flex h-[52px] items-center gap-3 px-3 py-1.5">
    <span class="nc-dot shrink-0" aria-hidden="true"></span>
    <span class="min-w-0 flex-1">
      <span class="block truncate text-[11px] font-semibold uppercase tracking-wide opacity-90">Now calling · {call.source}</span>
      <span class="block truncate text-[15px] font-bold leading-tight">
        {#if call.qrz}<a class="underline decoration-dotted underline-offset-2" href={call.qrz} target="_blank" rel="noopener noreferrer">{call.main}</a>{:else}{call.main}{/if}{#if call.sub}<span class="font-normal opacity-90"> · {call.sub}</span>{/if}
      </span>
    </span>
    {#if call.startMs}<span class="shrink-0 font-mono text-sm font-semibold tabular-nums">{mmss(secs)}</span>{/if}
  </div>
</div>

<style>
  .nc-tx { background: var(--row-tx); color: var(--row-fg); }
  .nc-dot { width: 10px; height: 10px; border-radius: 9999px; background: #fff; }
  @media (prefers-reduced-motion: no-preference) {
    .nc-dot { animation: nc-pulse 1.4s ease-in-out infinite; }
  }
  @keyframes nc-pulse { 0%, 100% { opacity: 1; transform: scale(1); } 50% { opacity: .45; transform: scale(.7); } }
</style>
