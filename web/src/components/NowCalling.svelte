<script lang="ts">
  import { app, clock } from '../lib/store.svelte';
  import { agoText, ariaLabel, durationSec, durText, idleLine, mainLine, mmss, placeFull, qrzUrl, radioidUrl, sinceSec, sourceDetail, sourceLabel, sourceShort, stripState, who } from '../lib/nowcalling';
  import type { NowCall } from '../lib/types';
  import ChevronRight from '@lucide/svelte/icons/chevron-right';
  import Clock from '@lucide/svelte/icons/clock';
  import X from '@lucide/svelte/icons/x';
  import ExternalLink from '@lucide/svelte/icons/external-link';

  const st = $derived(stripState(app.nowcalling));
  const now = $derived(clock.now);
  let open = $state(false);
  let dlg: HTMLDialogElement | undefined = $state();
  $effect(() => {
    if (!dlg) return;
    if (open && !dlg.open) dlg.showModal();
    if (!open && dlg.open) dlg.close();
  });
  // The strip going away (no history) closes the sheet.
  $effect(() => { if (st.mode === 'none') open = false; });

  const recent = $derived((app.nowcalling?.recent ?? []).slice(0, 5));
  // The details show the talker, or the last one heard while idle.
  const shown = $derived(st.mode === 'none' ? null : st.call);
  const startedAt = (c: NowCall) => new Date(c.start).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
  const lenOf = (c: NowCall) => durText(durationSec(c, now));
</script>

{#if st.mode !== 'none'}
  <button type="button" onclick={() => (open = true)} aria-label={ariaLabel(st, now)} aria-haspopup="dialog"
    class="nc-strip block w-full text-left {st.mode === 'talking' ? 'nc-tx' : 'border-t border-line bg-surface text-muted hover:text-fg'}">
    <span class="mx-auto flex max-w-[1100px] items-center gap-3 px-3 sm:px-4 {st.mode === 'talking' ? 'min-h-[52px] py-1.5' : 'min-h-[44px]'}">
      {#if st.mode === 'talking'}
        <span class="nc-dot shrink-0" aria-hidden="true"></span>
        <span class="min-w-0 flex-1">
          <span class="block truncate text-[11px] font-semibold uppercase tracking-wide opacity-90">Now calling · {sourceLabel(st.call)}</span>
          <span class="block truncate text-[15px] font-bold leading-tight">{mainLine(st.call)}</span>
        </span>
        <span class="shrink-0 font-mono text-sm font-semibold tabular-nums">{mmss(durationSec(st.call, now) ?? 0)}</span>
        <ChevronRight size={18} class="shrink-0" aria-hidden="true" />
      {:else}
        <Clock size={15} class="shrink-0" aria-hidden="true" />
        <span class="min-w-0 flex-1 truncate text-[13px]">{idleLine(st.call, now)}</span>
        <ChevronRight size={16} class="shrink-0" aria-hidden="true" />
      {/if}
    </span>
  </button>
{/if}

<dialog bind:this={dlg} onclose={() => (open = false)} onclick={(e) => { if (e.target === dlg) open = false; }}
  aria-labelledby="nc-title"
  class="nc-sheet m-auto w-[calc(100%-1.5rem)] max-w-md rounded-xl border border-line bg-surface p-0 text-fg shadow-2xl backdrop:bg-black/50 max-sm:mb-0 max-sm:mt-auto max-sm:w-full max-sm:max-w-none max-sm:rounded-b-none">
  {#if open}
    <div class="flex items-center justify-between border-b border-line px-4 py-2">
      <h2 id="nc-title" class="text-base font-semibold">{st.mode === 'talking' ? 'Now calling' : 'Last heard'}</h2>
      <button class="btn btn-icon border-transparent min-h-11 min-w-11" aria-label="Close" onclick={() => (open = false)}><X size={18} /></button>
    </div>
    <div class="max-h-[75dvh] space-y-4 overflow-y-auto p-4 text-sm">
      {#if shown}
        {@const c = shown}
        <dl class="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 gap-y-1.5">
          <dt class="text-muted">Source</dt>
          <dd class="break-words">{sourceDetail(c)}</dd>
          <dt class="text-muted">Callsign</dt><dd class="font-semibold">{who(c)}</dd>
          {#if c.name}<dt class="text-muted">Name</dt><dd>{c.name}</dd>{/if}
          {#if placeFull(c) || c.location}<dt class="text-muted">Place</dt><dd>{placeFull(c) || c.location}</dd>{/if}
          {#if c.dmrId}<dt class="text-muted">DMR ID</dt><dd class="font-mono">{c.dmrId}</dd>{/if}
          <dt class="text-muted">Started</dt><dd>{startedAt(c)}</dd>
          <dt class="text-muted">Duration</dt><dd class="tabular-nums">{c.active ? mmss(durationSec(c, now) ?? 0) : lenOf(c) || 'unknown'}</dd>
        </dl>
        {#if qrzUrl(c.call) || radioidUrl(c)}
          <div class="flex flex-wrap gap-2">
            {#if qrzUrl(c.call)}<a class="btn min-h-11" href={qrzUrl(c.call)} target="_blank" rel="noopener noreferrer">QRZ lookup<ExternalLink size={14} /></a>{/if}
            {#if radioidUrl(c)}<a class="btn min-h-11" href={radioidUrl(c)} target="_blank" rel="noopener noreferrer">radioid.net<ExternalLink size={14} /></a>{/if}
          </div>
        {/if}
      {/if}
      <section aria-label="Recent">
        <h3 class="mb-1 text-xs font-semibold uppercase tracking-wide text-muted">Recent</h3>
        {#if recent.length === 0}
          <p class="text-muted">Nobody else heard yet.</p>
        {:else}
          <ul class="divide-y divide-line">
            {#each recent as r (r.id)}
              <li class="flex min-h-11 items-center gap-2 py-1">
                <span class="min-w-0 flex-1">
                  <span class="block truncate font-medium">{who(r)}</span>
                  <span class="block truncate text-xs text-muted">{sourceShort(r)}</span>
                </span>
                <span class="shrink-0 text-right text-xs text-muted"><span class="block">{agoText(sinceSec(r, now))}</span>{#if lenOf(r)}<span class="block">{lenOf(r)}</span>{/if}</span>
              </li>
            {/each}
          </ul>
        {/if}
      </section>
    </div>
  {/if}
</dialog>

<style>
  .nc-tx { background: var(--row-tx); color: var(--row-fg); }
  .nc-strip:focus-visible { outline: 2px solid var(--accent); outline-offset: -3px; }
  .nc-tx:focus-visible { outline-color: #fff; }
  .nc-dot { width: 10px; height: 10px; border-radius: 9999px; background: #fff; }
  @media (prefers-reduced-motion: no-preference) {
    .nc-dot { animation: nc-pulse 1.4s ease-in-out infinite; }
  }
  @keyframes nc-pulse { 0%, 100% { opacity: 1; transform: scale(1); } 50% { opacity: .45; transform: scale(.7); } }
</style>
