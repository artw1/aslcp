<script lang="ts">
  // Status reporting: whether this node posts its status to a statistics page (statpost_url, statpost_time).
  import { untrack } from 'svelte';
  import { statpostError, statusRequest, statusState, STATPOST_DEFAULT_URL, type FormRequest, type NodeModel } from '../../lib/nodecfg';
  import FormShell from './FormShell.svelte';

  let { node, onclose, ondone }: { node: NodeModel; onclose: () => void; ondone: () => void } = $props();
  const cur = untrack(() => statusState(node.fields));
  let on = $state(cur.on), url = $state(cur.url), time = $state(cur.time);
  const urlErr = $derived(on ? statpostError(url.trim()) : '');
  const n = $derived(Number(time));
  const timeErr = $derived(on && !(/^\d+$/.test(time.trim()) && n >= 30 && n <= 600) ? 'A whole number of seconds from 30 to 600' : '');
  const build = (): FormRequest | null => statusRequest(node.node, node.fields, on, url, time);
</script>

<FormShell title="Status reporting, node {node.node}" canReview={!urlErr && !timeErr} {build} {onclose} {ondone}
  intro="A node can post its status (is it up, who is connected) to a statistics page. This is optional. Changes are written on this node only.">
  {#snippet children()}
    <label class="flex items-center gap-2 text-sm"><input type="checkbox" bind:checked={on} /> Report the status of this node</label>
    {#if on}
      <label class="min-w-0"><span class="label">Address to report to</span>
        <input class="input font-mono" bind:value={url} aria-invalid={urlErr ? 'true' : undefined} /></label>
      {#if urlErr}<p class="text-xs text-danger" role="alert">{urlErr}</p>{/if}
      {#if url.trim() !== STATPOST_DEFAULT_URL}<button type="button" class="btn btn-sm justify-self-start" onclick={() => (url = STATPOST_DEFAULT_URL)}>Use the AllStarLink statistics page</button>{/if}
      <label class="min-w-0"><span class="label">Seconds between reports</span>
        <input class="input w-32" inputmode="numeric" bind:value={time} aria-invalid={timeErr ? 'true' : undefined} /></label>
      {#if timeErr}<p class="text-xs text-danger" role="alert">{timeErr}</p>{/if}
    {/if}
    {#if cur.fromTemplate}
      <p class="rounded border border-line bg-surface-2 p-2 text-xs" role="status">The current address is set for all nodes in a shared section of rpt.conf. Turning it off here cannot remove it; use Edit in Advanced for that.</p>
    {/if}
  {/snippet}
</FormShell>
