<script lang="ts">
  /** Renders a unified diff with added/removed line colors. */
  let { diff, empty = 'No changes.' }: { diff: string; empty?: string } = $props();
  const lines = $derived(diff ? diff.replace(/\n$/, '').split('\n') : []);
  function cls(l: string): string {
    if (l.startsWith('+++') || l.startsWith('---') || l.startsWith('diff ') || l.startsWith('index ')) return 'text-muted';
    if (l.startsWith('@@')) return 'bg-surface-2 text-accent';
    if (l.startsWith('+')) return 'bg-ok/15';
    if (l.startsWith('-')) return 'bg-danger/15';
    return '';
  }
</script>

{#if lines.length}
  <pre class="max-h-72 overflow-auto rounded border border-line bg-surface font-mono text-xs leading-snug" aria-label="Diff"><div class="w-max min-w-full">{#each lines as l}<div class="px-2 whitespace-pre {cls(l)}">{l || ' '}</div>{/each}</div></pre>
{:else}
  <p class="text-xs text-muted">{empty}</p>
{/if}
