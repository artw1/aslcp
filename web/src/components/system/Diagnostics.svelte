<script lang="ts">
  import Download from '@lucide/svelte/icons/download';
  import { api } from '../../lib/api';
  import type { DiagPlan } from '../../lib/types';

  let plan = $state<DiagPlan | null>(null), err = $state(''), busy = $state(false), done = $state('');
  let mask = $state(true);

  $effect(() => {
    api.diagPlan().then((p) => { plan = p; mask = p.maskDefault; }).catch((e) => { err = e instanceof Error ? e.message : String(e); });
  });

  async function download() {
    busy = true; err = ''; done = '';
    try { done = await api.diagBundle(mask); } catch (e) { err = e instanceof Error ? e.message : String(e); } finally { busy = false; }
  }
</script>

<section class="card space-y-3 p-4">
  <p class="text-sm">
    Download one file with the information people need to help you with a problem: versions, recent logs, the network summary, disk and memory, service states and your configuration files.
    Passwords and other secrets are replaced by <code>&lt;redacted&gt;</code> before the file is built. Read the files before you post the bundle in a public place.
  </p>
  <label class="flex items-start gap-2 text-sm">
    <input type="checkbox" class="mt-1" bind:checked={mask} />
    <span>Hide my callsign and public IP address<br /><span class="text-muted">They are replaced by <code>&lt;callsign&gt;</code> and <code>&lt;own-ip&gt;</code>. Turn this off only if the people helping you need them.</span></span>
  </label>
  <div class="flex items-center gap-3">
    <button class="btn btn-primary" disabled={busy || !plan?.available} onclick={download}>
      <Download size={14} class={busy ? 'animate-pulse' : ''} />{busy ? 'Building…' : 'Download diagnostics'}
    </button>
    {#if plan && !plan.available}<span class="text-sm text-muted">Not available: this install has no privileged helper.</span>{/if}
    {#if busy}<span class="text-sm text-muted" role="status">This can take up to {plan?.timeLimit ?? 90} seconds.</span>{/if}
    {#if done}<span class="text-sm text-muted" role="status">Saved {done}</span>{/if}
  </div>
  {#if err}<p class="text-sm text-danger" role="alert">{err}</p>{/if}
</section>

{#if plan}
  <section class="card space-y-2 p-4" aria-label="What the bundle includes">
    <h3 class="text-sm font-semibold">What is included</h3>
    <ul class="space-y-1 text-sm">
      {#each plan.items as it (it.name)}
        <li><span class="font-mono text-xs">{it.name}</span><span class="text-muted"> — {it.desc}</span></li>
      {/each}
    </ul>
    <h3 class="pt-2 text-sm font-semibold">Hidden in every file</h3>
    <ul class="list-disc pl-5 text-sm">{#each plan.redacted as r (r)}<li>{r}</li>{/each}</ul>
    <h3 class="pt-2 text-sm font-semibold">Never included</h3>
    <ul class="list-disc pl-5 text-sm">{#each plan.excluded as r (r)}<li>{r}</li>{/each}</ul>
    <p class="text-xs text-muted">At most {Math.round(plan.maxBytes / 1048576)} MB before compression, and at most {plan.timeLimit} seconds to build. Each download is written to the audit log.</p>
  </section>
{/if}
