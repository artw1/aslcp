<script lang="ts" module>
  import type { FormRequest } from '../../lib/nodecfg';
  /** Starts the dry-run flow for one request while the form stays open behind it. */
  export type Act = (r: FormRequest, title?: string) => void;
</script>

<script lang="ts">
  // Edit dialog for one form; "Review changes" hands the request to PlanFlow (dry run, diff, apply, result).
  import type { Snippet } from 'svelte';
  import Dialog from '../Dialog.svelte';
  import PlanFlow from './PlanFlow.svelte';

  let { title, build, canReview = true, children, onclose, ondone, intro = '' }: {
    title: string;
    /** The request for the current inputs, or null when nothing differs. */
    build: () => FormRequest | null;
    canReview?: boolean; children: Snippet<[Act]>; onclose: () => void; ondone: () => void; intro?: string;
  } = $props();

  let editOpen = $state(true);
  let flow = $state<{ request: FormRequest; title: string; back: boolean } | null>(null);
  let nothing = $state(false);
  $effect(() => { if (!editOpen && !flow) onclose(); });

  const act: Act = (r, t = title) => { flow = { request: r, title: t, back: false }; };
  function review() {
    const r = build();
    nothing = !r;
    if (r) { flow = { request: r, title, back: true }; editOpen = false; }
  }
  function back() { flow = null; editOpen = true; }
  let changed = false;
  function finished() { changed = true; ondone(); }
</script>

<Dialog bind:open={editOpen} {title} wide>
  {#if intro}<p class="mb-3 text-sm text-muted">{intro}</p>{/if}
  <div class="grid gap-3">{@render children(act)}</div>
  {#if nothing}<p class="mt-3 text-sm text-muted" role="status">Nothing differs from what is saved.</p>{/if}
  {#snippet footer()}
    <button class="btn" onclick={() => (editOpen = false)}>Cancel</button>
    <button class="btn btn-primary" disabled={!canReview} onclick={review}>Review changes</button>
  {/snippet}
</Dialog>

{#if flow}
  {#key flow.request}
    <PlanFlow request={flow.request} title={flow.title} onback={flow.back ? back : undefined} onclose={() => { const keep = flow && !flow.back && !changed; flow = null; if (!keep) editOpen = false; }} ondone={finished} />
  {/key}
{/if}
