<script lang="ts">
  // DMR connection method (docs/DVSWITCH.md "DMR connection method"): MMDVM_Bridge's own DMR network, or
  // BrandMeister through STFU. STFU reuses the saved DMR password; this card never sends or shows one.
  import { onMount } from 'svelte';
  import { api, ApiError } from '../lib/api';
  import { toast } from '../lib/toast.svelte';
  import { dvErrorMessage } from '../lib/dvmsg';
  import type { DVDMRStatus, DVSTFUStatus, DVSwitchSetup } from '../lib/types';
  import { connRequest, DEFAULT_STFU_SERVER, formError, formOf, rxLine, stfuBlocker, TALKER_ALIAS_MAX, type ConnForm } from '../lib/dvconn';

  let { v, base, dmr, disabled = false, onsaved }: {
    v: DVSwitchSetup; base: () => Record<string, string>; dmr: DVDMRStatus | null; disabled?: boolean;
    onsaved: (s: DVSwitchSetup | null) => void;
  } = $props();

  let f = $state<ConnForm>(formOf(undefined)), busy = $state(false);
  let stfu = $state<DVSTFUStatus | null>(null);
  let loadedFrom: DVSwitchSetup | null = null;
  $effect(() => { if (v !== loadedFrom) { loadedFrom = v; f = formOf(v.dmrConn); } });

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
  onMount(() => { loadStfu(); return () => { if (timer) clearInterval(timer); }; });

  async function install() {
    busy = true;
    try { stfu = (await api.dvswitchStfuInstall()).stfu; loadStfu(); }
    catch (e) { toast(dvErrorMessage(e), 'error'); loadStfu(); }
    finally { busy = false; }
  }

  const blocker = $derived(stfuBlocker(stfu ?? (v.dmrConn.stfu.present ? { installed: true, canInstall: false, job: { job: 'idle', log: '' } } : undefined), v.dmr.passwordSet));
  const problem = $derived(formError(f));
  const using = $derived(v.dmrConn.via);
  const rx = $derived(using === 'stfu' ? rxLine(dmr?.rx) : '');

  async function save() {
    if (problem) { toast(problem, 'error'); return; }
    const r = connRequest(v.dmrConn, f);
    if (!r) { toast('DMR connection: No changes.', 'info'); return; }
    busy = true;
    try {
      const s = await api.dvswitchDmrConnection({ ...r, base: base() });
      toast(r.via === 'stfu' ? 'DMR now connects to BrandMeister through STFU.' : 'DMR now connects through MMDVM_Bridge.', 'info');
      onsaved(s);
    } catch (e) {
      if (e instanceof ApiError && e.code === 'conflict') { toast('A file changed on disk. The values were reloaded; check them and save again.', 'error'); onsaved(null); }
      else toast(dvErrorMessage(e), 'error');
    } finally { busy = false; }
  }
</script>

<section class="card space-y-3 p-4" aria-label="DMR connection">
  <h2 class="text-base font-semibold">DMR connection</h2>
  {#if v.dmrConn.error}<p class="text-xs text-warn" role="status">ASLCP's record of the DMR connection is unreadable. Choose MMDVM_Bridge and save to repair it.</p>{/if}
  <fieldset class="space-y-1 text-sm" disabled={busy || disabled}>
    <legend class="label">BrandMeister connection</legend>
    <label class="flex items-start gap-2"><input type="radio" name="dv-via" value="stfu" class="mt-1" bind:group={f.via} disabled={!!blocker && using !== 'stfu'} />
      <span><span class="font-medium">STFU</span> <span class="text-xs text-muted">(recommended)</span><br /><span class="text-xs text-muted">BrandMeister's Open DMR Terminal protocol. SelfCare lists it as "Open DMR Terminal". MMDVM_Bridge keeps running for YSF, P25 and NXDN, with its own DMR login off.</span></span></label>
    <label class="flex items-start gap-2"><input type="radio" name="dv-via" value="mmdvm_bridge" class="mt-1" bind:group={f.via} />
      <span><span class="font-medium">MMDVM_Bridge</span><br /><span class="text-xs text-muted">The bridge logs in to the DMR master set under DMR login (any network).</span></span></label>
  </fieldset>
  {#if blocker && using !== 'stfu'}
    <p class="text-xs text-muted" role="status" data-testid="dv-stfu-blocker">{blocker}
      {#if stfu && !stfu.installed && stfu.job.job !== 'running'}<button type="button" class="btn btn-sm ml-2" disabled={busy || disabled || !stfu.canInstall} onclick={install}>Install STFU</button>{/if}
    </p>
  {/if}
  {#if stfu?.job.job === 'running' || stfu?.job.job === 'failed'}
    <pre class="max-h-32 overflow-auto rounded border border-line p-2 text-xs" aria-label="STFU install log">{stfu.job.log || '…'}</pre>
  {/if}
  {#if f.via === 'stfu'}
    <form class="grid grid-cols-1 gap-2 sm:grid-cols-3" onsubmit={(e) => { e.preventDefault(); save(); }}>
      <div class="sm:col-span-3"><label class="label" for="dv-stfu-server">BrandMeister server</label><input id="dv-stfu-server" class="input font-mono" bind:value={f.server} maxlength="253" placeholder={DEFAULT_STFU_SERVER} aria-describedby="dv-stfu-server-hint" />
        <p id="dv-stfu-server-hint" class="mt-1 text-xs text-muted">The Open DMR Terminal server of your region, e.g. {DEFAULT_STFU_SERVER}. Port {v.dmrConn.stfu.port || 54006}.</p></div>
      <div class="sm:col-span-2"><label class="label" for="dv-stfu-alias">Talker alias</label><input id="dv-stfu-alias" class="input font-mono" bind:value={f.talkerAlias} maxlength={TALKER_ALIAS_MAX} placeholder={v.identity.callsign} /></div>
      <div><label class="label" for="dv-stfu-tg">Start talkgroup</label><input id="dv-stfu-tg" class="input font-mono" bind:value={f.startTg} inputmode="numeric" maxlength="8" /></div>
      <p class="text-xs text-muted sm:col-span-3">Uses the hotspot password saved under DMR login ({v.dmr.passwordSet ? 'Password saved' : 'No password'}) and your DMR ID.</p>
      {#if problem}<p class="text-xs text-danger sm:col-span-3" role="alert">{problem}</p>{/if}
      <div class="sm:col-span-3"><button class="btn btn-primary" disabled={busy || disabled || !!problem || (!!blocker && using !== 'stfu')}>Save DMR connection</button><span class="ml-2 text-xs text-muted">Restarts stfu and MMDVM_Bridge; the previous settings come back if that fails.</span></div>
    </form>
  {:else}
    <div><button class="btn btn-primary" disabled={busy || disabled} onclick={save}>Save DMR connection</button></div>
  {/if}
  {#if rx}<p class="text-xs text-muted" role="status" data-testid="dv-stfu-rx">{rx}</p>{/if}
</section>
