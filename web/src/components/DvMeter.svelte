<script lang="ts">
  // Level meters of the bridge audio (to and from the network) and the last-over summary. Polls about once a second while visible.
  import { onMount } from 'svelte';
  import { api } from '../lib/api';
  import type { DVAudioDir, DVMeter } from '../lib/types';
  import { agcLine, dbText, dirShown, meterColor, meterPct, METER_HINT, summaryLine, verdictLabel, verdictTone } from '../lib/dvaudio';

  let { running }: { running: boolean } = $props();
  let m = $state<DVMeter | null>(null), busy = false, alive = true, timer: ReturnType<typeof setTimeout> | undefined;
  async function poll() {
    if (alive && running && !document.hidden && !busy) {
      busy = true;
      try { m = await api.dvMeter(); } catch { /* keep the last reading */ } finally { busy = false; }
    }
    if (alive) timer = setTimeout(poll, 1000);
  }
  onMount(() => { timer = setTimeout(poll, 300); return () => { alive = false; clearTimeout(timer); }; });

  const rows = $derived<{ key: string; label: string; what: string; d: DVAudioDir | undefined }[]>([
    { key: 'tx', label: 'To network', what: 'over', d: m?.audio?.tx },
    { key: 'rx', label: 'From network', what: 'over', d: m?.audio?.rx },
  ]);
  const tone = { ok: 'text-ok', warn: 'text-warn', danger: 'text-danger' } as const;
</script>
{#if running && m?.audio}
  <div class="space-y-1.5" data-testid="dv-meter">
    {#each rows as r (r.key)}
      {@const d = r.d}
      {@const pk = d?.active ? d.peakDb : -90}
      {@const clipping = !!d?.active && d.peakDb >= -0.5}
      <div>
        <div class="flex items-baseline justify-between gap-2 text-xs text-muted">
          <span>{r.label}</span><span class="tabular-nums">{d?.active ? `${dbText(d.rmsDb)} dBFS` : ''}</span>
        </div>
        <div class="relative mt-0.5 h-2 overflow-hidden rounded bg-surface-2" role="meter" aria-label="{r.label} peak level" aria-valuemin="-90" aria-valuemax="0" aria-valuenow={pk}>
          <div class="h-full transition-[width] duration-300 {meterColor(pk, clipping)}" style="width:{meterPct(pk)}%"></div>
        </div>
        {#if dirShown(d) && d?.last}
          {@const s = d.last}
          <p class="mt-0.5 text-xs" data-testid="dv-meter-last-{r.key}">
            {summaryLine(s, r.what)}, <span class="font-medium {tone[verdictTone(s)]}">{verdictLabel(s)}</span>{#if agcLine(s)}<span class="text-muted"> {agcLine(s)}</span>{/if}
          </p>
        {/if}
      </div>
    {/each}
    <p class="text-xs text-muted">{METER_HINT}</p>
  </div>
{/if}
