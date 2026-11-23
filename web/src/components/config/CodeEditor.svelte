<script lang="ts">
  // CodeMirror 6 editor. Loaded lazily (see Advanced.svelte) so the dashboard bundle does not grow.
  // `value` is only the initial document: the parent re-creates the editor (via {#key}) to replace it.
  import { untrack } from 'svelte';
  import { EditorState } from '@codemirror/state';
  import { EditorView, keymap, lineNumbers, highlightActiveLine, highlightActiveLineGutter, drawSelection } from '@codemirror/view';
  import { defaultKeymap, history, historyKeymap } from '@codemirror/commands';
  import { search, searchKeymap, openSearchPanel, highlightSelectionMatches } from '@codemirror/search';
  import { asterisk } from './asterisk';

  let { value, onchange, onsave, label = 'Configuration file', line = 0 }: { value: string; onchange: (text: string) => void; onsave?: () => void; label?: string; line?: number } = $props();
  let host: HTMLDivElement | undefined = $state();
  let view: EditorView | undefined;

  /** Opens the search / replace panel (also Ctrl/Cmd+F while the editor has focus). */
  export function find() { if (view) { openSearchPanel(view); } }

  $effect(() => {
    if (!host) return;
    const v = new EditorView({
      parent: host,
      state: EditorState.create({
        doc: untrack(() => value),
        extensions: [
          lineNumbers(), highlightActiveLine(), highlightActiveLineGutter(), drawSelection(), history(),
          search({ top: true }), highlightSelectionMatches(),
          keymap.of([{ key: 'Mod-s', preventDefault: true, run: () => { onsave?.(); return true; } }, ...searchKeymap, ...defaultKeymap, ...historyKeymap]),
          asterisk,
          EditorView.contentAttributes.of({ 'aria-label': label, spellcheck: 'false', autocorrect: 'off', autocapitalize: 'off' }),
          EditorView.updateListener.of((u) => { if (u.docChanged) onchange(u.state.doc.toString()); }),
        ],
      }),
    });
    view = v;
    const ln = untrack(() => line);
    if (ln > 0 && ln <= v.state.doc.lines) {
      const at = v.state.doc.line(ln).from;
      v.dispatch({ selection: { anchor: at }, effects: EditorView.scrollIntoView(at, { y: 'center' }) });
    }
    return () => { v.destroy(); if (view === v) view = undefined; };
  });
</script>

<div bind:this={host} class="cm-host h-full"></div>

<style>
  :global(:root) {
    --cm-section: #0369a1; --cm-template: #7c3aed; --cm-key: #0f172a; --cm-op: #566274; --cm-comment: #64748b;
    --cm-directive: #b45309; --cm-number: #b45309; --cm-var: #0e7490; --cm-value: #15803d;
    --cm-active: #0f172a0d; --cm-sel: #0369a133;
    --cm-match: #f59e0b40; --cm-match-cur: #f59e0b99; --cm-sel-match: #0369a11f;
  }
  :global(.dark) {
    --cm-section: #38bdf8; --cm-template: #c4b5fd; --cm-key: #e6edf6; --cm-op: #93a1b5; --cm-comment: #7d8ba0;
    --cm-directive: #fbbf24; --cm-number: #fbbf24; --cm-var: #67e8f9; --cm-value: #86efac;
    --cm-active: #ffffff0d; --cm-sel: #38bdf844;
    --cm-match: #fbbf2433; --cm-match-cur: #fbbf2488; --cm-sel-match: #38bdf826;
  }
  .cm-host :global(.cm-editor) { height: 100%; background: var(--surface); color: var(--fg); font-size: 13px; }
  .cm-host :global(.cm-editor.cm-focused) { outline: 2px solid var(--accent); outline-offset: -2px; }
  .cm-host :global(.cm-scroller) { font-family: var(--font-mono); line-height: 1.45; overflow: auto; }
  .cm-host :global(.cm-gutters) { background: var(--surface-2); color: var(--muted); border-right: 1px solid var(--line); }
  .cm-host :global(.cm-activeLine) { background: var(--cm-active); }
  .cm-host :global(.cm-activeLineGutter) { background: var(--cm-active); color: var(--fg); }
  .cm-host :global(.cm-cursor) { border-left-color: var(--fg); }
  .cm-host :global(.cm-selectionBackground), .cm-host :global(.cm-focused > .cm-scroller > .cm-selectionLayer .cm-selectionBackground) { background: var(--cm-sel); }
  /* Search / replace panel */
  .cm-host :global(.cm-panels) { background: var(--surface-2); color: var(--fg); border-color: var(--line); }
  .cm-host :global(.cm-panels-top) { border-bottom: 1px solid var(--line); }
  .cm-host :global(.cm-search) { display: flex; flex-wrap: wrap; align-items: center; gap: 4px 6px; padding: 6px 28px 6px 8px; font-size: 13px; }
  .cm-host :global(.cm-search br) { flex-basis: 100%; height: 0; content: ''; }
  .cm-host :global(.cm-search .cm-textfield) { min-height: 1.75rem; padding: 2px 6px; border: 1px solid var(--line); border-radius: 4px; background: var(--surface); color: var(--fg); font: inherit; font-family: var(--font-mono); }
  .cm-host :global(.cm-search .cm-textfield:focus) { outline: 2px solid var(--accent); outline-offset: -1px; }
  .cm-host :global(.cm-search .cm-button) { min-height: 1.75rem; padding: 2px 8px; border: 1px solid var(--line); border-radius: 4px; background: var(--surface); background-image: none; color: var(--fg); font: inherit; cursor: pointer; }
  .cm-host :global(.cm-search .cm-button:hover) { border-color: var(--accent); }
  .cm-host :global(.cm-search label) { display: inline-flex; align-items: center; gap: 3px; color: var(--muted); font-size: 12px; }
  .cm-host :global(.cm-search label input) { accent-color: var(--accent); }
  .cm-host :global(.cm-search button[name=close]) { color: var(--muted); font-size: 18px; top: 4px; right: 8px; }
  .cm-host :global(.cm-searchMatch) { background: var(--cm-match); border-radius: 2px; }
  .cm-host :global(.cm-searchMatch-selected) { background: var(--cm-match-cur); }
  .cm-host :global(.cm-selectionMatch) { background: var(--cm-sel-match); }
</style>
