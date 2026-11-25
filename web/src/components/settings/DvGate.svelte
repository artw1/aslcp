<script lang="ts">
  // Wraps a Digital Modes section that only means something once the engine is enabled (and its enable job is done).
  // Otherwise it shows a short note with a link to the Engine section.
  import { onMount, type Snippet } from 'svelte';
  import { api } from '../../lib/api';
  import type { DVStatus } from '../../lib/types';

  let { children }: { children: Snippet } = $props();
  let st = $state<DVStatus | null>(null), err = $state('');
  onMount(async () => { try { st = await api.dvStatus(); } catch (e) { err = e instanceof Error ? e.message : String(e); } });
  const running = $derived(st?.job === 'running');
</script>

{#if !st}
  <p class="text-sm text-muted">{err || 'Loading…'}</p>
{:else if st.enabled && !running}
  {@render children()}
{:else}
  <p class="card p-4 text-sm text-muted" role="status" data-testid="dv-gate">
    {running ? 'The Digital Modes engine is being enabled.' : 'The Digital Modes engine is off.'} Turn it on in <a href="#/settings/dv-engine" class="text-accent underline hover:no-underline">Engine</a> first.
  </p>
{/if}
