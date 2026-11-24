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
  const lowOrHigh = $derived(rows.some((r) => r.d?.last && dirShown(r.d) && (r.d.last.verdict === 'low' || r.d.last.verdict === 'high')));
  const tone = { ok: 'text-ok', warn: 'text-warn', danger: 'text-danger' } as const;
</script>
{#if running && m?.audio}
  <div class="space-y-1" data-testid="dv-meter">
    <div class="grid grid-cols-1 gap-x-4 gap-y-1 sm:grid-cols-2">
      {#each rows as r (r.key)}
        {@const d = r.d}
        {@const pk = d?.active ? d.peakDb : -90}
        {@const clipping = !!d?.active && d.peakDb >= -0.5}
        <div class="flex items-center gap-2 text-xs">
          <span class="w-9 shrink-0 font-medium {r.key === 'tx' ? 'text-tx' : 'text-rx'}" title={r.label}>{r.key === 'tx' ? 'TX' : 'RX'}</span>
          <div class="relative h-2 min-w-16 flex-1 overflow-hidden rounded bg-surface-2" role="meter" aria-label="{r.label} peak level" aria-valuemin="-90" aria-valuemax="0" aria-valuenow={pk}>
            <div class="h-full transition-[width] duration-300 {meterColor(pk, clipping)}" style="width:{meterPct(pk)}%"></div>
          </div>
          <span class="w-20 shrink-0 text-right tabular-nums text-muted">{d?.active ? `${dbText(d.rmsDb)} dBFS` : ''}</span>
        </div>
      {/each}
    </div>
    {#each rows as r (r.key)}
      {@const d = r.d}
      {#if dirShown(d) && d?.last}
        {@const s = d.last}
        {@const full = `${summaryLine(s, r.what)}, ${verdictLabel(s)}${agcLine(s) ? ' ' + agcLine(s) : ''}`}
        <p class="flex flex-wrap items-center gap-x-2 text-xs text-muted" data-testid="dv-meter-last-{r.key}">
          <span class="font-medium">{r.key === 'tx' ? 'TX' : 'RX'} last {r.what}</span>
          <span class="badge {tone[verdictTone(s)]}" title={full}>{verdictLabel(s)}</span>
          <span class="sr-only">{full}</span>
          <span class="tabular-nums" aria-hidden="true">{dbText(s.speechDb)} / peak {dbText(s.peakDb)}</span>
        </p>
      {/if}
    {/each}
    {#if lowOrHigh}<p class="text-xs text-muted">{METER_HINT}</p>{/if}
  </div>
{/if}
