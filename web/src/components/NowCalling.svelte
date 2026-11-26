<script lang="ts">
  import { app, clock } from '../lib/store.svelte';
  import { agoText, ariaLabel, durationSec, durText, idleLine, mainLine, mmss, placeFull, qrzUrl, radioidUrl, sinceSec, sourceDetail, sourceLabel, sourceShort, stripState, who } from '../lib/nowcalling';
  import type { SheetRow } from '../lib/nowcalling';
  import type { NowCall } from '../lib/types';
  import NowCallingStrip from './NowCallingStrip.svelte';
  import NowCallingSheet from './NowCallingSheet.svelte';

  const st = $derived(stripState(app.nowcalling));
  const now = $derived(clock.now);
  let open = $state(false);
  // The strip going away (no history) closes the sheet.
  $effect(() => { if (st.mode === 'none') open = false; });

  const recent = $derived((app.nowcalling?.recent ?? []).slice(0, 5));
  // The details show the talker, or the last one heard while idle.
  const shown = $derived(st.mode === 'none' ? null : st.call);
  const startedAt = (c: NowCall) => new Date(c.start).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
  const lenOf = (c: NowCall) => durText(durationSec(c, now));

  function rowsOf(c: NowCall): SheetRow[] {
    const r: SheetRow[] = [{ label: 'Source', value: sourceDetail(c) }, { label: 'Callsign', value: who(c), cls: 'font-semibold' }];
    if (c.kind === 'allstar') {
      if (c.desc) r.push({ label: 'Description', value: c.desc });
      if (c.location) r.push({ label: 'Place', value: c.location });
    } else {
      if (c.name) r.push({ label: 'Name', value: c.name, note: c.source ? `via ${c.source}` : '' });
      const place = placeFull(c) || c.location;
      if (place) r.push({ label: 'Place', value: place });
    }
    if (c.dmrId) r.push({ label: 'DMR ID', value: c.dmrId, cls: 'font-mono' });
    r.push({ label: 'Started', value: startedAt(c) }, { label: 'Duration', value: c.active ? mmss(durationSec(c, now) ?? 0) : lenOf(c) || 'unknown', cls: 'tabular-nums' });
    return r;
  }
  function linksOf(c: NowCall): { label: string; href: string }[] {
    const l: { label: string; href: string }[] = [];
    if (qrzUrl(c.call)) l.push({ label: 'QRZ lookup', href: qrzUrl(c.call) });
    if (radioidUrl(c)) l.push({ label: 'radioid.net', href: radioidUrl(c) });
    return l;
  }
</script>

{#if st.mode === 'talking'}
  <NowCallingStrip mode="talking" label={ariaLabel(st, now)} source={sourceLabel(st.call)} main={mainLine(st.call)} secs={mmss(durationSec(st.call, now) ?? 0)} onclick={() => (open = true)} />
{:else if st.mode === 'idle'}
  <NowCallingStrip mode="idle" label={ariaLabel(st, now)} idle={idleLine(st.call, now)} onclick={() => (open = true)} />
{/if}

<NowCallingSheet bind:open title={st.mode === 'talking' ? 'Now calling' : 'Last heard'} rows={shown ? rowsOf(shown) : []} links={shown ? linksOf(shown) : []}>
  <section aria-label="Recent">
    <h3 class="mb-1 text-xs font-semibold uppercase tracking-wide text-muted">Recent</h3>
    {#if recent.length === 0}
      <p class="text-muted">Nobody else heard yet.</p>
    {:else}
      <ul class="divide-y divide-line">
        {#each recent as r, i (r.id + i)}
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
</NowCallingSheet>
