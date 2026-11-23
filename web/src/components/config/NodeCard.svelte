<script lang="ts">
  // One concept of a node: title, plain-language summary, and Edit (a form) or Edit in Advanced (a file in the raw editor).
  import type { Snippet } from 'svelte';
  import { advancedLink } from '../../lib/nodecfg';

  let { title, summary, onedit, advanced, advancedLine, disabledReason = '', children }: {
    title: string; summary: string; onedit?: () => void;
    /** File to open in Advanced when there is no form (or in addition to it). */
    advanced?: string; advancedLine?: number; disabledReason?: string; children?: Snippet;
  } = $props();
</script>

<section class="card flex min-w-0 flex-col gap-1.5 p-3" aria-label={title}>
  <h3 class="text-sm font-semibold">{title}</h3>
  <p class="min-w-0 grow break-words text-sm">{summary}</p>
  {@render children?.()}
  <div class="mt-1 flex flex-wrap items-center gap-2">
    {#if onedit}<button type="button" class="btn btn-sm btn-primary" disabled={!!disabledReason} title={disabledReason} onclick={onedit}>Edit</button>{/if}
    {#if advanced}<a class="btn btn-sm" href={'#' + advancedLink(advanced, advancedLine)}>Edit in Advanced</a>{/if}
  </div>
</section>
