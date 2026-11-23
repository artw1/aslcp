<script lang="ts">
  // DTMF commands and macros of a node, read-only. Every item links to its line in Advanced.
  import { api } from '../../lib/api';
  import { codeLabel, groupHref, itemHref, itemText, type Dtmf } from '../../lib/dtmf';
  import Dialog from '../Dialog.svelte';

  let { node, onclose }: { node: string; onclose: () => void } = $props();
  let open = $state(true);
  $effect(() => { if (!open) onclose(); });
  let d = $state<Dtmf | null>(null), err = $state('');
  $effect(() => { api.dtmfView(node).then((r) => { d = r; }, (e) => { err = e instanceof Error ? e.message : String(e); }); });
</script>

<Dialog bind:open title="DTMF commands and macros, node {node}" wide>
  <p class="mb-2 text-sm text-muted">What each code does on this node, as written in rpt.conf. This view only reads; change a line with its link to Advanced.</p>
  {#if err}<p class="text-sm text-danger" role="alert">{err}</p>
  {:else if !d}<p class="text-sm text-muted" role="status">Loading…</p>
  {:else}
    <div class="grid gap-3">
      {#each d.groups as g (g.kind)}
        <section aria-label={g.title}>
          <h4 class="text-sm font-semibold">{g.title}{#if g.stanza}<span class="font-mono font-normal text-muted"> [{g.stanza}]</span>{/if}
            {#if groupHref(g)}<a class="ml-2 text-xs font-normal underline" href={groupHref(g)}>Open in Advanced</a>{/if}</h4>
          {#if g.assumed}<p class="text-xs text-warn">The section name is assumed because this node has no setting naming it.</p>{/if}
          {#if g.sameAs}<p class="text-xs text-muted">Uses the same commands as the {g.sameAs} section.</p>{/if}
          {#if g.note}<p class="text-xs text-muted">{g.note}</p>{/if}
          {#if g.items.length}
            <ul class="mt-1 divide-y divide-line rounded border border-line text-sm">
              {#each g.items as it (it.code + ':' + it.line)}
                {@const t = itemText(it)}
                <li class="flex flex-wrap items-baseline gap-2 px-2 py-1">
                  <span class="font-mono font-semibold">{codeLabel(g.kind, it.code)}</span>
                  <span class="min-w-0 grow break-words {t.known ? '' : 'font-mono text-xs'}">{t.text}</span>
                  <a class="text-xs underline" href={itemHref(it)} aria-label="Open {it.code} in Advanced">{it.file}:{it.line}</a>
                </li>
              {/each}
            </ul>
          {:else if !g.note && !g.sameAs}<p class="text-xs text-muted">Nothing defined.</p>{/if}
        </section>
      {/each}
    </div>
  {/if}
  {#snippet footer()}<button class="btn btn-primary" onclick={() => (open = false)}>Close</button>{/snippet}
</Dialog>
