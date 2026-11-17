<script lang="ts">
  import { api } from '../lib/api';
  import type { NodeInfo } from '../lib/types';
  let { value = $bindable(''), placeholder = 'Node number, callsign or name', id, onenter, onpick }: {
    value: string; placeholder?: string; id?: string; onenter?: () => void; onpick?: (i: NodeInfo) => void;
  } = $props();
  let results = $state<NodeInfo[]>([]);
  let open = $state(false);
  let idx = $state(-1);
  let timer: ReturnType<typeof setTimeout> | undefined;
  let seq = 0;
  const listId = `np-${Math.random().toString(36).slice(2, 8)}`;

  function oninput() {
    clearTimeout(timer);
    const text = value.trim();
    if (text.length < 2) { results = []; open = false; return; }
    timer = setTimeout(async () => {
      const my = ++seq;
      try { const r = await api.search(text, 8); if (my === seq) { results = r; open = r.length > 0; idx = -1; } } catch { /* ignore */ }
    }, 180);
  }
  function pick(i: NodeInfo) { value = i.node; open = false; results = []; onpick?.(i); }
  function onkey(e: KeyboardEvent) {
    if (e.key === 'ArrowDown' && open) { e.preventDefault(); idx = (idx + 1) % results.length; }
    else if (e.key === 'ArrowUp' && open) { e.preventDefault(); idx = (idx - 1 + results.length) % results.length; }
    else if (e.key === 'Escape') open = false;
    else if (e.key === 'Enter') {
      if (open && idx >= 0) { e.preventDefault(); pick(results[idx]); }
      else if (onenter) { e.preventDefault(); open = false; onenter(); }
    }
  }
</script>
<div class="relative">
  <input {id} class="input font-mono" role="combobox" aria-expanded={open} aria-controls={listId} aria-autocomplete="list" aria-activedescendant={idx >= 0 ? `${listId}-${idx}` : undefined}
    {placeholder} bind:value {oninput} onkeydown={onkey} onblur={() => setTimeout(() => (open = false), 150)} autocomplete="off" autocapitalize="off" spellcheck="false" inputmode="search" />
  {#if open}
    <ul id={listId} role="listbox" class="absolute z-20 mt-1 max-h-72 w-full overflow-auto rounded-lg border border-line bg-surface p-1 shadow-lg">
      {#each results as r, i (r.node)}
        <li id="{listId}-{i}" role="option" aria-selected={i === idx}>
          <button type="button" tabindex="-1" class="flex w-full flex-col rounded-md px-2.5 py-1.5 text-left text-sm hover:bg-surface-2 {i === idx ? 'bg-surface-2' : ''}" onmousedown={(e) => { e.preventDefault(); pick(r); }}>
            <span><span class="font-mono font-semibold">{r.node}</span> <span class="font-medium">{r.callsign}</span></span>
            <span class="truncate text-xs text-muted">{[r.description, r.location].filter(Boolean).join(' · ')}</span>
          </button>
        </li>
      {/each}
    </ul>
  {/if}
</div>
