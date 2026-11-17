<script lang="ts">
  import type { ConfigApplyResult } from '../../lib/types';
  import Check from '@lucide/svelte/icons/check';
  import X from '@lucide/svelte/icons/x';
  let { result, rolledBack = false }: { result: ConfigApplyResult; rolledBack?: boolean } = $props();
  const reload = $derived(result.reload ?? []);
  const checks = $derived(result.health?.checks ?? []);
  const short = (c: string) => c.slice(0, 7);
</script>

<div class="space-y-3 text-sm">
  <p>{rolledBack ? 'Rolled back' : 'Saved'} as snapshot <code class="font-mono text-xs">{short(result.commit)}</code>{#if result.before}; previous state <code class="font-mono text-xs">{short(result.before)}</code>{/if}.</p>
  {#if result.skipped?.length}
    <p class="text-xs text-muted">Left alone (did not exist in that snapshot): {result.skipped.join(', ')}</p>
  {/if}
  <section>
    <h3 class="label">Reload</h3>
    {#if reload.length}
      <ul class="space-y-1">
        {#each reload as r}
          <li class="rounded border border-line px-2 py-1">
            <div class="flex items-center gap-1.5">
              {#if r.ok}<Check size={14} class="shrink-0 text-ok" aria-label="OK" />{:else}<X size={14} class="shrink-0 text-danger" aria-label="Failed" />{/if}
              <code class="font-mono text-xs">{r.command}</code>
            </div>
            {#if r.output}<pre class="mt-1 whitespace-pre-wrap font-mono text-xs text-muted">{r.output}</pre>{/if}
          </li>
        {/each}
      </ul>
    {:else}<p class="text-xs text-muted">No reload was run. Changes take effect after the next reload or restart.</p>{/if}
  </section>
  <section>
    <h3 class="label">Health check</h3>
    {#if result.health}
      <p class="mb-1 font-medium {result.health.ok ? 'text-ok' : 'text-danger'}" role="status">{result.health.ok ? 'Healthy' : 'Health check failed'}</p>
      <ul class="space-y-0.5">
        {#each checks as c}
          <li class="flex items-start gap-1.5">
            {#if c.ok}<Check size={14} class="mt-0.5 shrink-0 text-ok" aria-label="OK" />{:else}<X size={14} class="mt-0.5 shrink-0 text-danger" aria-label="Failed" />{/if}
            <span><span class="font-medium">{c.name}</span>{#if c.detail}<span class="text-muted">&nbsp;· {c.detail}</span>{/if}</span>
          </li>
        {/each}
      </ul>
    {:else}<p class="text-xs text-muted">Not run (no reload).</p>{/if}
  </section>
</div>
