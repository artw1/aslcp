<script lang="ts">
  // BrandMeister through STFU (docs/DVSWITCH.md "BrandMeister and DMR - Other"). It runs next to DMR - Other
  // (MMDVM_Bridge); the panel switches between the two. The password is write-only: never prefilled, never shown.
  import { onMount } from 'svelte';
  import { api, ApiError } from '../lib/api';
  import { toast } from '../lib/toast.svelte';
  import { app } from '../lib/store.svelte';
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
  let f = $state<BMForm>(formOf(undefined)), busy = $state(false), confirmOff = $state(false);
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

  // After a save the helper restarted stfu and checked its login (bmLogin) before stopping it again, if it is not
  // wanted. Watching the status therefore only makes sense while stfu keeps running.
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
      const running = bm.enabled && (f.stayLoggedIn || app.dvswitch?.bridge.mode === 'BM');
      toast(!bm.enabled ? 'BrandMeister saved. Enable it to use it.' : running ? 'BrandMeister saved. STFU is restarted.' : 'BrandMeister saved. STFU logs in when you switch to BM.', 'info');
      const was = bm.enabled;
      onsaved(s);
      busy = false;
      if (!was) return;
      if (s.bmLogin === 'logged_in') verify = { working: '', outcome: { kind: 'ok', message: 'The login to BrandMeister works.' } };
      else if (s.bmLogin === 'pending') {
        if (running) await watch();
        else verify = { working: '', outcome: { kind: 'warn', message: 'No BrandMeister login was seen in time: check the hotspot security password and the DMR ID.' } };
      } else await watch();
      if (s.stfuStopFailed) toast('STFU could not be stopped after the login check.', 'error');
    } catch (e) { failed(e); } finally { f.password = ''; busy = false; }
  }
  // Enable/Disable: the settings and the password stay either way. Disable runs systemctl disable --now stfu; Enable
  // brings BrandMeister back on the panel (and stfu at boot with "Stay logged in") without retyping anything.
  async function setEnabled(on: boolean) {
    confirmOff = false; busy = true; gen++;
    verify = { working: '', outcome: null };
    try {
      const s = await api.dvswitchBrandMeisterEnabled(on);
      onsaved(s);
      toast(on ? 'BrandMeister enabled.' : 'BrandMeister disabled. STFU is stopped; the settings are kept.', 'info');
      if (on && s.bmLogin === 'logged_in') verify = { working: '', outcome: { kind: 'ok', message: 'The login to BrandMeister works.' } };
    } catch (e) { failed(e); } finally { busy = false; }
  }
</script>

<section class="card space-y-3 p-4" aria-label="BrandMeister">
  <div class="flex flex-wrap items-center gap-2">
    <h2 class="text-base font-semibold">BrandMeister</h2>
    {#if bm.enabled}<span class="badge {TONE[bd.tone]}" role="status" title={status?.lastLine ?? ''} data-testid="dv-bm-state">{bd.text}</span>{:else}<span class="badge" role="status" data-testid="dv-bm-state">Disabled</span>{/if}
    <span class="ml-auto"></span>
    {#if bm.enabled}
      <button type="button" class="btn btn-sm" data-testid="dv-bm-toggle" disabled={busy || disabled || !!verify.working} onclick={() => (confirmOff = true)}>Disable</button>
    {:else}
      <button type="button" class="btn btn-sm btn-primary" data-testid="dv-bm-toggle" disabled={busy || disabled || !!verify.working || !bm.configured} title={bm.configured ? undefined : 'Save the password first (and set the DMR ID under Identity)'} onclick={() => setEnabled(true)}>Enable</button>
    {/if}
  </div>
  {#if !bm.enabled}<p class="text-xs text-muted" data-testid="dv-bm-off">Off: BrandMeister is not on the panel and STFU never starts. {bm.configured ? 'The settings are saved; Enable turns it back on.' : 'Save the settings below, then Enable.'}</p>{/if}
  <p class="text-xs text-muted">Through STFU, BrandMeister's Open DMR Terminal protocol. It runs next to DMR - Other; the panel switches between them. STFU logs in only while BrandMeister is the active mode, so it does not take the connection from your phone app (BrandMeister keeps one login per DMR ID); choose "Stay logged in" to keep it logged in always. In BrandMeister SelfCare the connection is listed under DMR Terminals as "Open DMR Terminal".</p>
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
      <div class="sm:col-span-3">
        <label class="flex items-center gap-2 text-sm"><input id="dv-bm-stay" type="checkbox" bind:checked={f.stayLoggedIn} />Stay logged in when not in BM mode</label>
        <p class="mt-1 text-xs text-muted">Off: STFU logs in only while BM is the active mode and is stopped otherwise, so another BrandMeister client with the same DMR ID (a phone app) keeps its connection. On: STFU is enabled at boot and always running.</p>
      </div>
      {#if problem && (f.password || bm.passwordSet)}<p class="text-xs text-danger sm:col-span-3" role="alert">{problem}</p>{/if}
      <div class="flex flex-wrap items-center gap-2 sm:col-span-3">
        <button class="btn btn-primary" disabled={!!problem || !!verify.working}>Save BrandMeister</button>
        <span class="text-xs text-muted">{bm.enabled ? 'Saving restarts stfu only, to check the login.' : 'Saved only: nothing starts while BrandMeister is disabled.'}</span>
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

<Confirm bind:open={confirmOff} title="Disable BrandMeister?" confirmLabel="Disable" message="This stops and disables stfu and takes BrandMeister off the panel. The master, the password and the other settings stay, so Enable brings it back. The other DMR networks are not touched." onconfirm={() => setEnabled(false)} />
