<script lang="ts">
  // BrandMeister through STFU (docs/DVSWITCH.md "BrandMeister and DMR - Other"). It runs next to DMR - Other
  // (MMDVM_Bridge); the panel switches between the two. The password is write-only: never prefilled, never shown.
  import { onMount } from 'svelte';
  import { api, ApiError } from '../lib/api';
  import { toast } from '../lib/toast.svelte';
  import { dvErrorMessage } from '../lib/dvmsg';
  import { badge, waitForLogin, type Outcome } from '../lib/dmrlogin';
  import { indexList, optionLabel, searchList } from '../lib/dvlist';
  import { bmBlocker, bmOptions, bmRequest, DEFAULT_STFU_SERVER, formError, formOf, rxLine, STFU_PORT, TALKER_ALIAS_MAX, type BMForm } from '../lib/dvbm';
  import type { DVDMRStatus, DVListItem, DVSTFUStatus, DVSwitchSetup } from '../lib/types';
  import Confirm from './Confirm.svelte';
  import NodePicker from './NodePicker.svelte';

  let { v, base, status, hosts, disabled = false, onsaved }: {
    v: DVSwitchSetup; base: () => Record<string, string>; status: DVDMRStatus | null; hosts: DVListItem[]; disabled?: boolean;
    onsaved: (s: DVSwitchSetup | null) => void;
  } = $props();

  const bm = $derived(v.brandmeister);
  let f = $state<BMForm>(formOf(undefined)), busy = $state(false), confirmClear = $state(false);
  let stfu = $state<DVSTFUStatus | null>(null);
  let verify = $state<{ working: string; outcome: Outcome | null }>({ working: '', outcome: null });
  let gen = 0;
  let loadedFrom: DVSwitchSetup | null = null;
  $effect(() => { if (v !== loadedFrom) { loadedFrom = v; f = formOf(v.brandmeister); } });

  const options = $derived(bmOptions(hosts));
  const index = $derived(indexList(options));
  const find = (t: string) => searchList(index, t);
  const label = (o: DVListItem) => optionLabel('dmr', o);

  let timer: ReturnType<typeof setInterval> | undefined;
  async function loadStfu() {
    try {
      const s = (await api.dvswitch()).stfu;
      const was = stfu?.job.job;
      stfu = s;
      if (s.job.job === 'running' && !timer) timer = setInterval(loadStfu, 2000);
      if (s.job.job !== 'running' && timer) { clearInterval(timer); timer = undefined; }
      if (was === 'running' && s.job.job !== 'running') {
        toast(s.job.job === 'succeeded' ? 'STFU installed.' : 'The STFU install failed: see the log.', s.job.job === 'succeeded' ? 'info' : 'error');
        onsaved(null);
      }
    } catch { /* keep the last state */ }
  }
  onMount(() => { loadStfu(); return () => { gen++; if (timer) clearInterval(timer); }; });

  async function install() {
    busy = true;
    try { stfu = (await api.dvswitchStfuInstall()).stfu; loadStfu(); }
    catch (e) { toast(dvErrorMessage(e), 'error'); loadStfu(); }
    finally { busy = false; }
  }

  const blocker = $derived(bmBlocker(stfu ?? undefined, bm));
  const problem = $derived(formError(f, bm.passwordSet));
  const bd = $derived(badge(status ?? (bm.installed ? null : { state: 'not_installed', via: 'stfu' })));
  const rx = $derived(rxLine(status?.rx));
  const TONE = { ok: 'border-ok text-ok', warn: 'border-warn text-warn', danger: 'border-danger text-danger', muted: '' } as const;
  const KIND = { ok: 'border-ok text-ok', error: 'border-danger text-danger', warn: 'border-warn text-warn', info: 'border-line text-muted' } as const;

  // After a save the helper has enabled and (re)started stfu: wait for its login verdict.
  async function watch() {
    const my = ++gen;
    verify = { working: 'Waiting for BrandMeister…', outcome: null };
    const sleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));
    const r = await waitForLogin(async () => (await api.dvswitchDmrStatus()).bm ?? { state: 'not_installed', via: 'stfu' }, { sleep, cancelled: () => my !== gen });
    if (r && my === gen) verify = { working: '', outcome: r.outcome };
  }

  function failed(e: unknown) {
    if (e instanceof ApiError && e.code === 'conflict') { toast('A file changed on disk. The values were reloaded; check them and save again.', 'error'); onsaved(null); }
    else toast(dvErrorMessage(e), 'error');
  }
  async function save() {
    if (problem) { toast(problem, 'error'); return; }
    const r = bmRequest(bm, f);
    if (!r) { toast('BrandMeister: No changes.', 'info'); return; }
    busy = true;
    try {
      const s = await api.dvswitchBrandMeister({ ...r, base: base() });
      toast('BrandMeister saved. STFU is enabled and started.', 'info');
      onsaved(s);
      busy = false;
      await watch();
    } catch (e) { failed(e); } finally { f.password = ''; busy = false; }
  }
  async function clear() {
    confirmClear = false; busy = true; gen++;
    verify = { working: '', outcome: null };
    try { onsaved(await api.dvswitchBrandMeisterClear()); toast('BrandMeister connection cleared. STFU is stopped.', 'info'); }
    catch (e) { failed(e); } finally { busy = false; }
  }
</script>

<section class="card space-y-3 p-4" aria-label="BrandMeister">
  <div class="flex flex-wrap items-center gap-2">
    <h2 class="text-base font-semibold">BrandMeister</h2>
    <span class="badge {TONE[bd.tone]}" role="status" title={status?.lastLine ?? ''} data-testid="dv-bm-state">{bd.text}</span>
  </div>
  <p class="text-xs text-muted">Through STFU, BrandMeister's Open DMR Terminal protocol. It runs next to DMR - Other; the panel switches between them. In BrandMeister SelfCare the connection is listed under DMR Terminals as "Open DMR Terminal".</p>
  {#if blocker}
    <p class="text-xs text-muted" role="status" data-testid="dv-stfu-blocker">{blocker}
      {#if stfu && !stfu.installed && stfu.job.job !== 'running'}<button type="button" class="btn btn-sm ml-2" disabled={busy || disabled || !stfu.canInstall} onclick={install}>Install STFU</button>{/if}
    </p>
  {/if}
  {#if stfu?.job.job === 'running' || stfu?.job.job === 'failed'}
    <pre class="max-h-32 overflow-auto rounded border border-line p-2 text-xs" aria-label="STFU install log">{stfu.job.log || '…'}</pre>
  {/if}
  <form class="grid grid-cols-1 gap-2 sm:grid-cols-3" onsubmit={(e) => { e.preventDefault(); save(); }}>
    <fieldset class="contents" disabled={busy || disabled || !!blocker}>
      <div class="sm:col-span-3"><label class="label" for="dv-bm-server">BrandMeister master</label>
        <NodePicker id="dv-bm-server" bind:value={f.server} {options} search={find} {label} placeholder={DEFAULT_STFU_SERVER} inputmode="text" maxlength={253} describedby="dv-bm-server-hint" />
        <p id="dv-bm-server-hint" class="mt-1 text-xs text-muted">Pick a BrandMeister master or type its host name, e.g. {DEFAULT_STFU_SERVER}. Port {STFU_PORT}.</p></div>
      <div class="sm:col-span-3">
        <label class="label" for="dv-bm-pw">Hotspot security password <span class="text-muted" data-testid="dv-bm-pw-state">({bm.passwordSet ? 'Password saved' : 'No password'})</span></label>
        <input id="dv-bm-pw" class="input font-mono" type="password" autocomplete="new-password" bind:value={f.password} maxlength="64" placeholder={bm.passwordSet ? 'Leave empty to keep the password' : 'Enter the password'} />
        <p class="mt-1 text-xs text-muted">Set in BrandMeister SelfCare, not your account password. Write-only: it is never shown again.</p>
      </div>
      <div class="sm:col-span-2"><label class="label" for="dv-bm-alias">Talker alias</label><input id="dv-bm-alias" class="input font-mono" bind:value={f.talkerAlias} maxlength={TALKER_ALIAS_MAX} placeholder={v.identity.callsign} /></div>
      <div><label class="label" for="dv-bm-tg">Start talkgroup</label><input id="dv-bm-tg" class="input font-mono" bind:value={f.startTg} inputmode="numeric" maxlength="8" /></div>
      <p class="text-xs text-muted sm:col-span-3">Logs in with your 7-digit DMR ID ({v.identity.dmrId || 'not set'}); the ESSID is not used.</p>
      {#if problem && (f.password || bm.passwordSet)}<p class="text-xs text-danger sm:col-span-3" role="alert">{problem}</p>{/if}
      <div class="flex flex-wrap items-center gap-2 sm:col-span-3">
        <button class="btn btn-primary" disabled={!!problem || !!verify.working}>Save BrandMeister</button>
        {#if bm.enabled || bm.passwordSet}<button type="button" class="btn" disabled={!!verify.working} onclick={() => (confirmClear = true)}>Clear BrandMeister connection</button>{/if}
        <span class="text-xs text-muted">Saving enables and restarts stfu only.</span>
      </div>
    </fieldset>
  </form>
  {#if verify.working}
    <p class="rounded border border-line p-2 text-sm text-muted" role="status">{verify.working}</p>
  {:else if verify.outcome}
    <p class="rounded border p-2 text-sm {KIND[verify.outcome.kind]}" role="status">{verify.outcome.message}</p>
  {/if}
  {#if rx}<p class="text-xs text-muted" role="status" data-testid="dv-stfu-rx">{rx}</p>{/if}
</section>

<Confirm bind:open={confirmClear} title="Clear the BrandMeister connection?" confirmLabel="Clear" message="This removes the BrandMeister password, stops and disables stfu, and takes BrandMeister off the panel. DMR - Other is not touched." onconfirm={clear} />
