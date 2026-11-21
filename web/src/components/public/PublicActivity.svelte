<script lang="ts">
  import type { PubActivity } from '../../lib/types';
  import { clock } from '../../lib/store.svelte';
  import { pub } from '../../lib/publicLive.svelte';
  import { fmtAgoLong, fmtDuration } from '../../lib/util';
  let { items, multi }: { items: PubActivity[]; multi: boolean } = $props();
  const ago = (a: PubActivity) => fmtAgoLong((clock.now + pub.skew) / 1000 - a.at);
</script>
<section aria-label="Recent activity">
  <h2 class="mb-1 text-sm font-semibold uppercase tracking-wide text-muted">Recent activity</h2>
  <div class="overflow-hidden rounded-md border border-line bg-surface">
    {#if items.length === 0}<p class="py-4 text-center text-sm text-muted">No transmissions heard yet.</p>
    {:else}
      <table class="tbl">
        <thead><tr><th scope="col" class="w-24">When</th><th scope="col">Station</th>{#if multi}<th scope="col" class="w-16">Node</th>{/if}<th scope="col" class="w-20 text-right">Duration</th></tr></thead>
        <tbody>
          {#each items as a, i (a.node + '/' + a.link + '/' + a.at + '/' + i)}
            <tr>
              <td class="whitespace-nowrap">{ago(a)}</td>
              <td class="max-w-0">
                <span class="block truncate">
                  {#if a.link === 'local'}<span class="font-semibold">Local receiver</span>
                  {:else}<span class="num font-semibold">{a.link}</span>{#if a.callsign}&nbsp;<span class="text-muted">{a.callsign}</span>{/if}{/if}
                  {#if a.kerchunk}<span class="badge ml-1" title="Very short transmission">kerchunk</span>{/if}
                </span>
              </td>
              {#if multi}<td class="num">{a.node}</td>{/if}
              <td class="num text-right">{fmtDuration(a.duration)}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    {/if}
  </div>
</section>
