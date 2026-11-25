<script lang="ts">
  import { api } from '../lib/api';
  import { toastError, toast } from '../lib/toast.svelte';
  import { drafts } from '../lib/configDrafts.svelte';
  import type { ConfigFile, ConfigFileInfo, ConfigCommitDetail } from '../lib/types';
  import ApplyDialog, { type Mode } from './config/ApplyDialog.svelte';
  import HistoryPane from './config/HistoryPane.svelte';
  import RestartAsterisk from './config/RestartAsterisk.svelte';
  import FileText from '@lucide/svelte/icons/file-text';
  import ChevronDown from '@lucide/svelte/icons/chevron-down';
  import TriangleAlert from '@lucide/svelte/icons/triangle-alert';
  import Copy from '@lucide/svelte/icons/copy';
  import Search from '@lucide/svelte/icons/search';

  const GROUPS = ['Core', 'Radio', 'Linking', 'Platform', 'Custom', 'Other'];
  let tab = $state<'files' | 'history'>('files');
  let files = $state<ConfigFileInfo[]>([]);
  let listErr = $state(''), filter = $state(''), listOpen = $state(false);
  let current = $state(''), loaded = $state<Record<string, ConfigFile>>({}), fileErr = $state(''), fileBusy = $state(false);
  let rev = $state(0), histRev = $state(0);
  let conflicts = $state<string[]>([]);
  let dlg = $state<Mode | null>(null);
  const editorPromise = import('./config/CodeEditor.svelte');
  let editor: { find: () => void } | undefined = $state();

  const dirtyPaths = $derived(Object.keys(drafts.files));
  const grouped = $derived.by(() => {
    const f = filter.trim().toLowerCase();
    const list = files.filter((x) => !f || x.path.toLowerCase().includes(f));
    const names = [...GROUPS, ...new Set(list.map((x) => x.group).filter((g) => !GROUPS.includes(g)))];
    return names.map((g) => ({ name: g, items: list.filter((x) => (x.group || 'Other') === g) })).filter((g) => g.items.length);
  });

  async function loadList() {
    try { files = await api.configFiles(); listErr = ''; } catch (e) { listErr = e instanceof Error ? e.message : String(e); }
  }
  async function fetchFile(path: string): Promise<boolean> {
    fileBusy = true; fileErr = '';
    try {
      const f = await api.configFile(path);
      loaded[path] = f;
      const d = drafts.files[path];
      if (d && d.base !== f.sha256 && !conflicts.includes(path)) conflicts = [...conflicts, path];
      return true;
    } catch (e) { fileErr = e instanceof Error ? e.message : String(e); return false; } finally { fileBusy = false; }
  }
  async function select(path: string) {
    current = path; listOpen = false;
    if (!loaded[path]) await fetchFile(path);
    rev++;
  }
  $effect(() => { loadList().then(() => { if (!current && files.length) select((files.find((f) => f.path === 'rpt.conf') ?? files[0]).path); }); });

  const text = $derived(drafts.files[current]?.content ?? loaded[current]?.content ?? '');
  const isDirty = (p: string) => p in drafts.files;
  function edited(path: string, content: string) {
    const f = loaded[path]; if (!f) return;
    if (content === f.content && !(path in drafts.files && drafts.files[path].base !== f.sha256)) delete drafts.files[path];
    else drafts.files[path] = { content, base: drafts.files[path]?.base ?? f.sha256 };
  }
  function revert() {
    delete drafts.files[current]; conflicts = conflicts.filter((c) => c !== current); rev++;
  }
  function save() {
    if (!dirtyPaths.length) return;
    dlg = { kind: 'apply', files: dirtyPaths.map((path) => ({ path, content: drafts.files[path].content, base: drafts.files[path].base })) };
  }
  async function changed(paths: string[] | 'all') {
    const touched = paths === 'all' ? Object.keys(loaded) : paths;
    for (const p of touched) {
      if (paths !== 'all') { delete drafts.files[p]; delete drafts.side[p]; conflicts = conflicts.filter((c) => c !== p); }
      if (paths === 'all' && isDirty(p)) continue; // keep the user's text; it will conflict on apply, which is accurate
      delete loaded[p];
    }
    histRev++;
    await loadList();
    if (current && !loaded[current]) await fetchFile(current);
    rev++;
  }
  function onconflict(paths: string[]) {
    conflicts = [...new Set([...conflicts, ...paths])];
    if (paths.length && !paths.includes(current)) void select(paths[0]);
  }
  async function loadFromDisk(path: string) {
    const mine = drafts.files[path]?.content;
    if (mine !== undefined) drafts.side[path] = mine;
    delete drafts.files[path];
    if (await fetchFile(path)) { conflicts = conflicts.filter((c) => c !== path); rev++; loadList(); }
    else if (mine !== undefined) drafts.files[path] = { content: mine, base: loaded[path]?.sha256 ?? '' };
  }
  async function copySide(path: string) {
    try { await navigator.clipboard.writeText(drafts.side[path]); toast('Copied'); } catch { toast('Select the text and copy it manually', 'info'); }
  }
  function onrestore(c: ConfigCommitDetail) {
    dlg = { kind: 'restore', commit: c.commit, summary: c.message, files: c.files.map((f) => f.path) };
  }
  const dirtyOthers = $derived(dirtyPaths.filter((p) => p !== current).length);
</script>

<svelte:window onbeforeunload={(e) => { if (dirtyPaths.length) e.preventDefault(); }} />

<div class="flex flex-wrap items-center gap-2 pb-2">
  <h2 class="mr-2 text-base font-semibold">Asterisk files</h2>
  <div class="flex gap-0.5" role="tablist" aria-label="Asterisk files sections">
    {#each [['files', 'Files'], ['history', 'History']] as const as [id, label]}
      <button role="tab" aria-selected={tab === id} class="rounded px-2 py-1 text-[13px] font-medium {tab === id ? 'bg-surface-2 text-fg' : 'text-muted hover:text-fg'}" onclick={() => (tab = id)}>{label}{#if id === 'files' && dirtyPaths.length}<span class="ml-1 text-accent" title="{dirtyPaths.length} modified">●</span>{/if}</button>
    {/each}
  </div>
  <div class="ml-auto"><RestartAsterisk /></div>
</div>

{#if tab === 'history'}
  <HistoryPane {onrestore} rev={histRev} />
{:else}
  <div class="grid gap-2 md:grid-cols-[13rem_minmax(0,1fr)]">
    <aside class="min-w-0">
      <button type="button" class="btn w-full justify-between md:hidden" aria-expanded={listOpen} onclick={() => (listOpen = !listOpen)}>
        <span class="flex items-center gap-1.5 truncate font-mono text-xs"><FileText size={14} />{current || 'Choose a file'}</span><ChevronDown size={14} class={listOpen ? 'rotate-180' : ''} />
      </button>
      <div class="{listOpen ? 'mt-1' : 'hidden'} md:mt-0 md:block">
        <label class="sr-only" for="cfg-filter">Filter files</label>
        <input id="cfg-filter" class="input mb-1" type="search" placeholder="Filter files…" bind:value={filter} />
        <div class="card max-h-[50dvh] overflow-y-auto md:max-h-[calc(100dvh-13rem)]">
          {#if listErr}<p class="p-2 text-xs text-danger" role="alert">{listErr}</p>{/if}
          {#each grouped as g (g.name)}
            <h2 class="sticky top-0 border-b border-line bg-surface-2 px-2 py-0.5 text-[11px] font-semibold uppercase tracking-wide text-muted">{g.name}</h2>
            <ul>
              {#each g.items as f (f.path)}
                <li>
                  <button type="button" class="flex w-full items-center gap-1 px-2 py-1 text-left font-mono text-xs hover:bg-surface-2 {f.path === current ? 'bg-surface-2 font-semibold' : ''}" aria-current={f.path === current ? 'true' : undefined} onclick={() => select(f.path)}>
                    <span class="min-w-0 grow truncate" title={f.path}>{f.path}</span>
                    {#if isDirty(f.path)}<span class="text-accent" title="Modified">●<span class="sr-only"> modified</span></span>{/if}
                  </button>
                </li>
              {/each}
            </ul>
          {:else}
            {#if !listErr}<p class="p-2 text-xs text-muted">{files.length ? 'No matching files.' : 'Loading…'}</p>{/if}
          {/each}
        </div>
      </div>
    </aside>

    <section class="min-w-0">
      {#each conflicts as p (p)}
        <div class="mb-2 flex flex-wrap items-center gap-2 rounded border border-warn/50 bg-warn/10 p-2 text-sm" role="alert">
          <TriangleAlert size={16} class="shrink-0 text-warn" />
          <span class="min-w-0 grow"><span class="font-mono text-xs">{p}</span> changed on disk after you opened it (another tool or an edit by hand), so it was not saved. Load the version from disk to continue; your text is kept in a side panel to copy from.</span>
          <button class="btn btn-sm" onclick={() => loadFromDisk(p)}>Load from disk</button>
        </div>
      {/each}
      <div class="mb-1 flex flex-wrap items-center gap-2">
        <span class="min-w-0 truncate font-mono text-xs font-semibold">{current || '—'}</span>
        {#if isDirty(current)}<span class="badge text-accent">modified</span>{/if}
        {#if dirtyOthers}<span class="text-xs text-muted">+{dirtyOthers} other {dirtyOthers === 1 ? 'file' : 'files'} modified</span>{/if}
        <div class="ml-auto flex gap-1.5">
          <button class="btn btn-sm" disabled={!editor} onclick={() => editor?.find()} title="Find and replace (Ctrl+F in the editor)" aria-label="Find and replace"><Search size={13} />Find</button>
          <button class="btn btn-sm" disabled={!isDirty(current)} onclick={revert}>Revert</button>
          <button class="btn btn-sm btn-primary" disabled={!dirtyPaths.length} onclick={save} title="Ctrl+S">Save…</button>
        </div>
      </div>
      <div class="grid gap-2 {drafts.side[current] !== undefined ? 'lg:grid-cols-2' : ''}">
        <div class="card h-[calc(100dvh-11rem)] min-h-72 overflow-hidden">
          {#if fileErr}<p class="p-3 text-sm text-danger" role="alert">{fileErr}</p>
          {:else if current && loaded[current]}
            {#await editorPromise}
              <p class="p-3 text-sm text-muted" role="status">Loading editor…</p>
            {:then m}
              {@const Editor = m.default}
              {#key current + ':' + rev}
                {@const p = current}
                <Editor bind:this={editor} value={text} label={p} onchange={(t) => edited(p, t)} onsave={save} />
              {/key}
            {/await}
          {:else}<p class="p-3 text-sm text-muted" role="status">{fileBusy ? 'Loading…' : 'Select a file.'}</p>{/if}
        </div>
        {#if drafts.side[current] !== undefined}
          <div class="card flex h-[40dvh] min-h-48 flex-col overflow-hidden lg:h-[calc(100dvh-11rem)]">
            <div class="flex items-center gap-2 border-b border-line bg-surface-2 px-2 py-1">
              <span class="text-xs font-semibold">Your unsaved text</span>
              <button class="btn btn-sm ml-auto" onclick={() => copySide(current)}><Copy size={13} />Copy all</button>
              <button class="btn btn-sm" onclick={() => delete drafts.side[current]}>Discard</button>
            </div>
            <textarea readonly class="min-h-0 grow resize-none bg-surface p-2 font-mono text-xs leading-snug text-fg" aria-label="Your unsaved text" spellcheck="false" value={drafts.side[current]}></textarea>
          </div>
        {/if}
      </div>
    </section>
  </div>
{/if}

{#if dlg}
  <ApplyDialog mode={dlg} onclose={() => (dlg = null)} onchanged={changed} {onconflict} />
{/if}
