<script lang="ts">
  import { api, ApiError, MAX_BACKUP_BYTES } from '../lib/api';
  import { toast } from '../lib/toast.svelte';
  import type { RestoreAction, RestorePreview, RestoreResult, RestoreStep } from '../lib/types';
  import Confirm from './Confirm.svelte';
  import Check from '@lucide/svelte/icons/check';
  import X from '@lucide/svelte/icons/x';
  import Copy from '@lucide/svelte/icons/copy';
  import Refresh from '@lucide/svelte/icons/refresh-cw';
  import Warn from '@lucide/svelte/icons/triangle-alert';
  import Upload from '@lucide/svelte/icons/upload';

  /** `admin`: a logged-in admin re-enters the password. `setup`: first-run page, authorised by `code`. */
  let { mode, code = '', oncancel }: { mode: 'admin' | 'setup'; code?: string; oncancel?: () => void } = $props();

  type Stage = 'pick' | 'uploading' | 'preview' | 'applying' | 'waiting' | 'done';
  let stage = $state<Stage>('pick');
  let file = $state<File | null>(null), password = $state(''), err = $state('');
  let progress = $state(0), id = $state(''), preview = $state<RestorePreview | null>(null);
  let del = $state<Record<string, boolean>>({}), filter = $state<RestoreAction | 'all'>('all');
  let confirmOpen = $state(false), steps = $state<RestoreStep[]>([]), result = $state<RestoreResult | null>(null);
  let waitNote = $state(''), copied = $state(false);

  const LABEL: Record<RestoreAction, string> = {
    restore: 'Restored from backup',
    restore_dist: 'Restored; new ASL3 default saved as .dpkg-dist, review',
    keep_stock: 'Kept new ASL3 default',
    keep_new: 'Kept (new in this ASL3)',
    delete_optional: 'Deleted on old node, tick to delete here',
    skip: 'Not restored (old package leftover)',
  };
  const SHORT: Record<RestoreAction, string> = { restore: 'Restored', restore_dist: 'Review', keep_stock: 'New default', keep_new: 'New here', delete_optional: 'Delete?', skip: 'Skipped' };
  const ORDER: RestoreAction[] = ['restore_dist', 'delete_optional', 'restore', 'keep_stock', 'keep_new', 'skip'];
  const counts = $derived.by(() => {
    const c: Partial<Record<RestoreAction, number>> = {};
    for (const f of preview?.files ?? []) c[f.action] = (c[f.action] ?? 0) + 1;
    return c;
  });
  const shown = $derived((preview?.files ?? []).filter((f) => filter === 'all' || f.action === filter).sort((a, b) => ORDER.indexOf(a.action) - ORDER.indexOf(b.action) || a.path.localeCompare(b.path)));
  const delCount = $derived(Object.values(del).filter(Boolean).length);
  const mb = (n: number) => (n / 1048576).toFixed(1);

  function pick(e: Event) {
    err = ''; const f = (e.currentTarget as HTMLInputElement).files?.[0] ?? null;
    if (f && f.size > MAX_BACKUP_BYTES) { err = `That file is ${mb(f.size)} MiB; the limit is 256 MiB.`; file = null; return; }
    file = f;
  }
  const UPLOAD_ERR: Record<string, string> = {
    bad_backup: 'This is not a valid ASLCP backup (or it is damaged).',
    backup_too_new: 'This backup was made by a newer ASLCP. Update ASLCP first, then try again.',
    too_large: 'The file is too large (limit 256 MiB).',
    bad_password: 'Wrong password.',
    bad_setup_code: 'Wrong setup code.',
    helper_unavailable: 'The privileged helper is not available.',
    busy: 'Another backup or restore is running.',
  };
  /** Codes whose friendly sentence is followed by the server's own reason. */
  const WITH_DETAIL = new Set(['bad_backup', 'helper_unavailable', 'busy']);
  function uploadError(x: unknown): string {
    if (!(x instanceof ApiError)) return x instanceof Error ? x.message : String(x);
    const base = UPLOAD_ERR[x.code];
    if (!base) return x.message;
    return WITH_DETAIL.has(x.code) && x.message ? `${base} Details: ${x.message}` : base;
  }
  async function upload(e: Event) {
    e.preventDefault(); err = '';
    if (!file) { err = 'Choose a backup file.'; return; }
    if (mode === 'admin' && !password) { err = 'Enter your password.'; return; }
    if (mode === 'setup' && !code.trim()) { err = 'Enter the setup code first.'; return; }
    stage = 'uploading'; progress = 0;
    try {
      const r = await api.restoreUpload(file, mode === 'admin' ? { password } : { code: code.trim() }, (f) => (progress = f));
      id = r.id; preview = r.preview; del = {}; filter = 'all'; password = ''; stage = 'preview';
    } catch (x) {
      stage = 'pick';
      err = uploadError(x);
    }
  }
  async function cancel() {
    if (id) { try { await api.restoreCancel(id); } catch { /* already gone */ } }
    id = ''; preview = null; oncancel?.();
  }

  async function apply() {
    stage = 'applying'; err = '';
    try {
      const r = await api.restoreApply(id, Object.keys(del).filter((p) => del[p]), mode === 'setup' ? code.trim() : undefined);
      steps = r.steps;
      if (r.restarting) { stage = 'waiting'; void waitForServer(); }
      else { await loadResult(); stage = 'done'; void pollHealth(); }
    } catch (x) {
      err = x instanceof Error ? x.message : String(x); stage = 'preview';
    }
  }
  const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));
  /** The server restarts: poll until it answers again (connection errors are expected). */
  async function waitForServer() {
    const start = Date.now(); let sawDown = false;
    waitNote = 'Waiting for ASLCP to restart…';
    while (Date.now() - start < 4 * 60_000) {
      await sleep(2000);
      try {
        await api.setupNeeded();
        if (sawDown || Date.now() - start > 12_000) break;
      } catch { sawDown = true; waitNote = 'ASLCP is restarting…'; }
    }
    await loadResult(); stage = 'done'; void pollHealth();
  }
  /** The server runs its post-restart health checks for up to three minutes (`health.pending`); keep reading until they are final. */
  async function pollHealth() {
    for (let i = 0; i < 90 && result?.health.pending; i++) {
      await sleep(2500);
      try { result = await api.restoreResult(); } catch { /* keep the last one */ }
    }
  }
  async function loadResult() {
    for (let i = 0; i < 5 && !result; i++) {
      try { result = await api.restoreResult(); } catch (x) { if (x instanceof ApiError && x.status !== 0 && x.status < 500) break; await sleep(2000); }
    }
  }
  const finishSteps = $derived(result?.steps ?? steps);
  const checklist = $derived(result?.checklist ?? preview?.checklist ?? []);
  function login() { location.hash = '#/'; location.reload(); }

  async function copyCmd() {
    try { await navigator.clipboard.writeText(preview?.installCommand ?? ''); copied = true; setTimeout(() => (copied = false), 2000); } catch { toast('Copy failed; select the command and copy it by hand.', 'error'); }
  }
  const when = (s: string) => { const d = new Date(s); return isNaN(d.getTime()) ? s : d.toLocaleString(); };
</script>

<div class="space-y-4 text-sm">
  {#if stage === 'pick' || stage === 'uploading'}
    <form class="space-y-3" onsubmit={upload}>
      <p class="text-muted">Upload a backup file made by ASLCP. You will see exactly what will change before anything is touched.</p>
      <div>
        <label class="label" for="rf-file">Backup file (.tar.gz, up to 256 MiB)</label>
        <input id="rf-file" class="input" type="file" accept=".gz,.tgz,application/gzip" disabled={stage === 'uploading'} onchange={pick} />
        {#if file}<p class="mt-1 text-xs text-muted">{file.name}, {mb(file.size)} MiB</p>{/if}
      </div>
      {#if mode === 'admin'}
        <div>
          <label class="label" for="rf-pw">Your password</label>
          <input id="rf-pw" class="input" type="password" autocomplete="current-password" bind:value={password} disabled={stage === 'uploading'} />
          <p class="mt-1 text-xs text-muted">Re-enter your password to confirm.</p>
        </div>
      {/if}
      {#if stage === 'uploading'}
        <div role="progressbar" aria-valuemin="0" aria-valuemax="100" aria-valuenow={Math.round(progress * 100)} class="h-2 overflow-hidden rounded bg-surface-2"><div class="h-full bg-accent transition-[width]" style="width:{progress * 100}%"></div></div>
        <p class="text-xs text-muted" role="status">{progress < 1 ? `Uploading… ${Math.round(progress * 100)}%` : 'Checking the backup…'}</p>
      {/if}
      {#if err}<p class="text-danger" role="alert">{err}</p>{/if}
      <div class="flex flex-wrap gap-2">
        <button class="btn btn-primary" disabled={stage === 'uploading' || !file}><Upload size={14} />Upload and check</button>
        {#if oncancel}<button type="button" class="btn" disabled={stage === 'uploading'} onclick={() => oncancel?.()}>Cancel</button>{/if}
      </div>
    </form>
  {:else if preview && (stage === 'preview' || stage === 'applying')}
    {@const b = preview.backup}
    <section aria-label="Backup summary">
      <h3 class="text-base font-semibold">Backup of {b.hostname}</h3>
      <dl class="mt-2 grid grid-cols-[auto_1fr] gap-x-4 gap-y-1">
        <dt class="text-muted">Nodes</dt><dd class="font-mono break-words">{b.nodes.join(', ') || 'none'}</dd>
        <dt class="text-muted">Created</dt><dd>{when(b.created)}{#if b.createdBy}{" "}by {b.createdBy}{/if}</dd>
        <dt class="text-muted">ASLCP</dt><dd class="font-mono">{b.aslcpVersion}</dd>
        {#if b.osName || b.osCodename}<dt class="text-muted">System</dt><dd class="break-words">{b.osName || b.osCodename}{#if preview.node.osCodename && b.osCodename && preview.node.osCodename !== b.osCodename}{" "}<span class="text-warn">(this node: {preview.node.osName || preview.node.osCodename})</span>{/if}</dd>{/if}
        <dt class="text-muted">ASL3</dt><dd class="font-mono break-all">{b.asl3Version}</dd>
        <dt class="text-muted">Asterisk</dt><dd class="font-mono break-all">{b.asteriskVersion}</dd>
        <dt class="text-muted">Architecture</dt><dd class="font-mono">{b.arch}</dd>
        {#if b.channel}<dt class="text-muted">Update channel</dt><dd>{b.channel}</dd>{/if}
      </dl>
    </section>

    {#if preview.warnings.length}
      <section class="space-y-1 rounded border border-warn bg-warn/10 p-3" aria-label="Warnings" role="alert">
        <h3 class="flex items-center gap-1.5 font-semibold text-warn"><Warn size={16} />Please read</h3>
        <ul class="list-disc space-y-0.5 pl-5">{#each preview.warnings as w}<li>{w.message}</li>{/each}</ul>
      </section>
    {/if}

    <section aria-label="Asterisk files">
      <h3 class="font-semibold">/etc/asterisk ({preview.files.length} files)</h3>
      <div class="mt-2 flex flex-wrap gap-1.5" role="group" aria-label="Filter files by action">
        <button class="btn btn-sm {filter === 'all' ? 'btn-primary' : ''}" aria-pressed={filter === 'all'} onclick={() => (filter = 'all')}>All {preview.files.length}</button>
        {#each ORDER.filter((a) => counts[a]) as a}
          <button class="btn btn-sm {filter === a ? 'btn-primary' : ''}" aria-pressed={filter === a} title={LABEL[a]} onclick={() => (filter = a)}>{SHORT[a]} {counts[a]}</button>
        {/each}
      </div>
      {#if filter !== 'all'}<p class="mt-1 text-xs text-muted">{LABEL[filter]}</p>{/if}
      <ul class="mt-2 max-h-72 divide-y divide-line overflow-y-auto rounded border border-line">
        {#each shown as f (f.path)}
          <li class="flex flex-wrap items-start gap-x-3 gap-y-0.5 px-2 py-1.5">
            {#if f.action === 'delete_optional'}
              <input type="checkbox" class="mt-0.5 size-4 accent-[var(--accent)]" aria-label="Delete {f.path} here" bind:checked={del[f.path]} disabled={stage === 'applying'} />
            {/if}
            <code class="min-w-0 flex-1 basis-48 font-mono text-xs break-all">{f.path}</code>
            <span class="text-xs {f.action === 'restore_dist' || f.action === 'delete_optional' ? 'text-warn' : 'text-muted'}">{LABEL[f.action]}</span>
            {#if f.note}<span class="basis-full text-xs text-muted">{f.note}</span>{/if}
          </li>
        {/each}
      </ul>
    </section>

    <section aria-label="ASLCP data">
      <h3 class="font-semibold">ASLCP</h3>
      <p class="mt-1">These users will exist afterwards. Log in with your old password.</p>
      <ul class="mt-1 flex flex-wrap gap-1.5">{#each preview.users as u}<li class="badge">{u.username} · {u.role}</li>{/each}</ul>
      <p class="mt-1 text-muted">{preview.favorites} favorite{preview.favorites === 1 ? '' : 's'}, {preview.schedules} schedule{preview.schedules === 1 ? '' : 's'}, settings and audit log.
        {#if preview.parts.history}Config history included.{/if}
        {#if preview.parts.allmon3} Allmon3 config included.{/if}
        {#if preview.parts.sounds} Custom sounds included.{/if}
        {#if preview.parts.dvswitch} DVSwitch configuration included (written only if DVSwitch is installed).{/if}</p>
    </section>

    {#if preview.radio}
      {@const r = preview.radio}
      <section class="rounded border border-line p-3" aria-label="Radio settings">
        <h3 class="font-semibold">Radio module settings (SA818)</h3>
        <dl class="mt-1 grid grid-cols-[auto_1fr] gap-x-4 gap-y-0.5">
          <dt class="text-muted">Band</dt><dd>{r.band}</dd>
          <dt class="text-muted">RX / TX</dt><dd class="font-mono">{r.freqRx} / {r.freqTx} MHz</dd>
          <dt class="text-muted">Tone</dt><dd class="break-words">{r.tone || `CTCSS ${r.ctcssRx}/${r.ctcssTx}, DCS ${r.dcsRx}/${r.dcsTx}`}</dd>
          <dt class="text-muted">Port</dt><dd class="font-mono break-all">{r.port}</dd>
        </dl>
        {#if !r.portPresent}
          <p class="mt-2 flex items-start gap-1.5 text-warn"><Warn size={14} class="mt-0.5 shrink-0" />This serial port does not exist on this system. Plug the radio module in, or adjust the port, before you rely on these settings.</p>
        {/if}
        <p class="mt-1 text-xs text-muted">The module keeps its own settings; this file is restored to /etc/sa818.conf. It is not sent to the module automatically.</p>
      </section>
    {/if}

    {#if preview.missingPackages.length}
      <section aria-label="Missing packages">
        <h3 class="font-semibold">Packages to install</h3>
        <p class="mt-1">The old node had these packages, which are missing here: <span class="font-mono">{preview.missingPackages.join(', ')}</span>. Run this on the node:</p>
        <div class="mt-1 flex items-start gap-2 rounded border border-line bg-surface-2/40 p-2">
          <code class="min-w-0 flex-1 font-mono text-xs break-all select-all">{preview.installCommand}</code>
          <button class="btn btn-sm shrink-0" onclick={copyCmd}>{#if copied}<Check size={12} />Copied{:else}<Copy size={12} />Copy{/if}</button>
        </div>
      </section>
    {/if}

    {#if preview.checklist.length}
      <section aria-label="Set up separately">
        <h3 class="font-semibold">Set up separately</h3>
        <p class="text-xs text-muted">A backup does not include these. Set them up on this node yourself.</p>
        <dl class="mt-1 grid grid-cols-[auto_1fr] gap-x-4 gap-y-0.5">{#each preview.checklist as c}<dt class="text-muted">{c.item}</dt><dd class="break-words">{c.value}</dd>{/each}</dl>
      </section>
    {/if}

    {#if err}<p class="text-danger" role="alert">{err}</p>{/if}
    <div class="flex flex-wrap items-center gap-2 border-t border-line pt-3">
      <button class="btn btn-primary" disabled={stage === 'applying'} onclick={() => (confirmOpen = true)}>{#if stage === 'applying'}<Refresh size={14} class="animate-spin" />Restoring…{:else}Restore{/if}</button>
      <button class="btn" disabled={stage === 'applying'} onclick={cancel}>Cancel</button>
      {#if delCount}<span class="text-xs text-muted">{delCount} file{delCount === 1 ? '' : 's'} will be deleted</span>{/if}
    </div>
  {:else if stage === 'waiting'}
    <section class="space-y-3" aria-label="Restarting">
      <p class="flex items-center gap-2 font-medium" role="status"><Refresh size={14} class="animate-spin" />{waitNote}</p>
      <p class="text-muted">Asterisk and ASLCP are restarting. This takes a minute or two. Keep this page open.</p>
      {@render stepList(steps)}
    </section>
  {:else if stage === 'done'}
    <section class="space-y-4" aria-label="Restore result">
      <h3 class="text-base font-semibold">{result ? (result.health.pending ? 'Restore finished, checking health' : result.health.ok ? 'Restore finished' : 'Restore finished, with problems') : 'Restore finished'}</h3>
      {#if result}<p class="text-xs text-muted">Backup from {when(result.backupCreated)}, restored {when(result.at)}.</p>
      {:else}<p class="text-muted">The server is back, but the result could not be read yet. Log in to continue.</p>{/if}
      {@render stepList(finishSteps)}
      {#if result}
        <div>
          <h4 class="label">Health check</h4>
          {#if result.health.pending}
            <p class="mb-1 flex items-center gap-2 font-medium" role="status"><Refresh size={14} class="animate-spin" />Running health checks…</p>
          {:else}
            <p class="mb-1 font-medium {result.health.ok ? 'text-ok' : 'text-danger'}" role="status">{result.health.ok ? 'Healthy' : 'Health check failed'}</p>
          {/if}
          <ul class="space-y-0.5">
            {#each result.health.checks as c}
              <li class="flex items-start gap-1.5">
                {#if c.ok}<Check size={14} class="mt-0.5 shrink-0 text-ok" aria-label="OK" />{:else}<X size={14} class="mt-0.5 shrink-0 text-danger" aria-label="Failed" />{/if}
                <span><span class="font-medium">{c.name}</span>{#if c.detail}<span class="text-muted">&nbsp;· {c.detail}</span>{/if}</span>
              </li>
            {/each}
          </ul>
        </div>
      {/if}
      {#if checklist.length}
        <div>
          <h4 class="label">Still to do on this node</h4>
          <dl class="grid grid-cols-[auto_1fr] gap-x-4 gap-y-0.5">{#each checklist as c}<dt class="text-muted">{c.item}</dt><dd class="break-words">{c.value}</dd>{/each}</dl>
        </div>
      {/if}
      <p class="text-muted">Sessions are not restored. Log in with your old account and password.</p>
      <button class="btn btn-primary" onclick={login}>Log in</button>
    </section>
  {/if}
</div>

{#snippet stepList(list: RestoreStep[])}
  {#if list.length}
    <ul class="space-y-0.5">
      {#each list as s}
        <li class="flex items-start gap-1.5">
          {#if s.ok}<Check size={14} class="mt-0.5 shrink-0 text-ok" aria-label="OK" />{:else}<X size={14} class="mt-0.5 shrink-0 text-danger" aria-label="Failed" />{/if}
          <span><span class="font-medium">{s.name}</span>{#if s.detail}<span class="text-muted">&nbsp;· {s.detail}</span>{/if}</span>
        </li>
      {/each}
    </ul>
  {/if}
{/snippet}

<Confirm bind:open={confirmOpen} title="Restore this backup?" confirmLabel="Restore and restart"
  message="Asterisk will stop, all links will drop and the node will restart. Do not run the old SD card or old node at the same time: both would register the same node numbers. Your current /etc/asterisk is saved in the config history first."
  onconfirm={apply} />
