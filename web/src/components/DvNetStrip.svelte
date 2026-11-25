<script lang="ts">
  // The DV tab's network status strip: one chip per network with a dot (green logged in, red login failed,
  // amber logging in, grey disabled). An admin taps a chip to open that network in Digital modes setup;
  // for everyone else the chips are plain status.
  import { isAdmin } from '../lib/store.svelte';
  import { modeButtonLabel, netChip, netSettingsHref, stripModes } from '../lib/dvpanel';
  import type { DVSnapshot } from '../lib/types';

  let { modes }: { modes: DVSnapshot['modes'] } = $props();
  const admin = $derived(isAdmin());
  const list = $derived(stripModes(modes));
  const DOT = { ok: 'bg-ok', err: 'bg-danger', warn: 'bg-warn', off: 'bg-muted' } as const;
  const TXT = { ok: 'text-ok', err: 'text-danger', warn: 'text-warn', off: 'text-muted' } as const;
  const chipCls = 'inline-flex items-center gap-1.5 rounded-full border border-line bg-surface-2 px-2.5 py-1 text-xs';
</script>

{#if list.length}
  <ul class="flex flex-wrap gap-1.5" aria-label="Networks" data-testid="dv-logins">
    {#each list as m (m.id)}
      {@const c = netChip(m)}
      <li>
        {#if admin}
          <a href={netSettingsHref(m.id)} class="{chipCls} hover:bg-surface" title={m.detail || "Open the settings"} data-testid="dv-chip-{m.id.toLowerCase()}">
            <span class="size-2 rounded-full {DOT[c.tone]}" aria-hidden="true"></span>
            <span class="font-medium">{modeButtonLabel(m)}</span><span class={TXT[c.tone]}>{c.text}</span>
          </a>
        {:else}
          <span class={chipCls} title={m.detail || undefined} data-testid="dv-chip-{m.id.toLowerCase()}">
            <span class="size-2 rounded-full {DOT[c.tone]}" aria-hidden="true"></span>
            <span class="font-medium">{modeButtonLabel(m)}</span><span class={TXT[c.tone]}>{c.text}</span>
          </span>
        {/if}
      </li>
    {/each}
  </ul>
{/if}
