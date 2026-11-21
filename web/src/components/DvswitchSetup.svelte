<script lang="ts">
  // Credentials and config of a node with DVSwitch installed (docs/DVSWITCH.md, M4).
  // The DMR password is write-only: the field is never prefilled, the server only says whether one is set.
  import { onMount } from 'svelte';
  import { api, ApiError } from '../lib/api';
  import { toast, toastError } from '../lib/toast.svelte';
  import type { DVDMRStatus, DVListItem, DVListMode, DVResume, DVRestartScope, DVSwitchSetup, DVSwitchStatus } from '../lib/types';
  import { badge, bridgeInUse, IDLE_POLL_MS, outcomeFor, waitForLogin, type Outcome } from '../lib/dmrlogin';
  import { credentialsPatch, dvErrorMessage, formChanged } from '../lib/dvmsg';
  import { indexList, optionLabel, searchList } from '../lib/dvlist';
  import { freqToHz, hzToMHz, validCoord, validInfoText, validInfoUrl } from '../lib/dvpanel';
  import { cardEnabled, lastUsed, modesBody, OTHER_EXCLUSIVE, RESTART_WARNING, RESUME_NOTICE, resumeHint, resumeToast, tgifSource, TGIF_EXCLUSIVE, type DVCard } from '../lib/dvmodes';
  import Confirm from './Confirm.svelte';
  import Toggle from './Toggle.svelte';
  import NodePicker from './NodePicker.svelte';
  import DvswitchBridge from './DvswitchBridge.svelte';
  import DvswitchBM from './DvswitchBM.svelte';
  import { isBMHost, otherOptions } from '../lib/dvbm';

  let { units, onunits }: { units: DVSwitchStatus['units']; onunits: (u: DVSwitchStatus['units']) => void } = $props();

  const FILE_LABEL: Record<string, string> = { analog_bridge: 'Analog_Bridge.ini', mmdvm_bridge: 'MMDVM_Bridge.ini', ysfgateway: 'YSFGateway.ini', p25gateway: 'P25Gateway.ini', nxdngateway: 'NXDNGateway.ini', dvswitch: 'DVSwitch.ini' };
  const WARNING = RESTART_WARNING;
  const NXDN_WARNING = 'NXDN needs your NXDN ID from radioid.net';

  let v = $state<DVSwitchSetup | null>(null), err = $state(''), busy = $state(false);
  let hint = $state<string[]>([]);
  // Host and TG lists of the pickers. A mode without a list (404 no_list, or any error) stays empty:
  // its field is then plain text, which is always allowed.
  const MODES: DVListMode[] = ['dmr', 'ysf', 'p25', 'nxdn'];
  let lists = $state<Record<DVListMode, DVListItem[]>>({ dmr: [], ysf: [], p25: [], nxdn: [] });
  // DMR - Other never offers a BrandMeister master: those belong to the BrandMeister section.
  const index = $derived({ dmr: indexList(otherOptions(lists.dmr)), ysf: indexList(lists.ysf), p25: indexList(lists.p25), nxdn: indexList(lists.nxdn) });
  const find = (m: DVListMode) => (t: string) => searchList(index[m], t);
  const label = (m: DVListMode) => (o: DVListItem) => optionLabel(m, o);
  async function loadLists() {
    await Promise.all(MODES.map(async (m) => { try { lists[m] = (await api.dvswitchList(m)).items; } catch { /* no list: free text */ } }));
  }
  let id = $state({ callsign: '', dmrId: '', essid: '', nxdnId: '' });
  // [Info] of MMDVM_Bridge.ini. The frequency is typed in MHz (or Hz) and written to both RXFrequency and TXFrequency.
  let info = $state({ location: '', description: '', url: '', freq: '', lat: '', lon: '' });
  let net = $state({ address: '', port: '', password: '' });
  let clearPw = $state(false);
  // TGIF: only the password is typed; the master comes from DMR_Hosts.txt.
  let tgifPw = $state(''), tgifClear = $state(false);
  // The last mode and tune and the resume toggle (GET /api/dvswitch/resume).
  let resume = $state<DVResume | null>(null), now = $state(Date.now());
  async function loadResume() { try { resume = await api.dvswitchResume(); now = Date.now(); } catch { /* no tracker: no hints */ } }
  let scope = $state<DVRestartScope>('bridge');
  let confirmRestart = $state(false), rbOpen = $state(false), rollbackFile = $state('');

  // The form as loaded: a save with nothing different sends nothing.
  let snap = $state.raw({ id: { callsign: '', dmrId: '', essid: '', nxdnId: '' }, info: { location: '', description: '', url: '', freq: '', lat: '', lon: '' }, net: { address: '', port: '', password: '' } });
  function fill(s: DVSwitchSetup) {
    v = s;
    id = { callsign: s.identity.callsign, dmrId: s.identity.dmrId, essid: s.identity.essid, nxdnId: s.identity.nxdnId ?? '' };
    const i = s.identity.info;
    info = i ? { location: i.location, description: i.description, url: i.url, freq: hzToMHz(i.frequencyHz), lat: i.latitude, lon: i.longitude } : { location: '', description: '', url: '', freq: '', lat: '', lon: '' };
    net = { address: s.dmr.address, port: s.dmr.port ? String(s.dmr.port) : '', password: '' };
    clearPw = false;
    tgifPw = ''; tgifClear = false;
    snap = { id: { ...id }, info: { ...info }, net: { ...net } };
  }
  async function load() { try { fill(await api.dvswitchSetup()); err = ''; } catch (e) { err = e instanceof Error ? e.message : String(e); } }

  // DMR login: a live badge (light poll while the page is open) and the check after a save.
  let dmr = $state<DVDMRStatus | null>(null);
  let verify = $state<{ working: string; outcome: Outcome | null }>({ working: '', outcome: null });
  let gen = 0; // a newer check, or leaving the page, cancels the running one
  const sleep = (ms: number) => new Promise<void>((r) => setTimeout(r, ms));
  async function pollOnce() {
    if (verify.working) return;
    try { dmr = await api.dvswitchDmrStatus(); } catch { /* keep the last badge */ }
  }
  onMount(() => {
    load(); loadLists(); loadResume(); pollOnce();
    const t = setInterval(() => { if (!document.hidden) { pollOnce(); loadResume(); } }, IDLE_POLL_MS);
    return () => { clearInterval(t); gen++; };
  });
  const bd = $derived(badge(dmr));
  const TONE = { ok: 'border-ok text-ok', warn: 'border-warn text-warn', danger: 'border-danger text-danger', muted: '' } as const;
  const KIND = { ok: 'border-ok text-ok', error: 'border-danger text-danger', warn: 'border-warn text-warn', info: 'border-line text-muted' } as const;

  async function watch(my: number) {
    verify = { working: 'Waiting for the DMR master…', outcome: null };
    const r = await waitForLogin(() => api.dvswitchDmrStatus(), { sleep, cancelled: () => my !== gen, onStatus: (x) => { dmr = x; } });
    if (!r || my !== gen) return;
    verify = { working: '', outcome: r.outcome };
  }
  // Saved credentials only take effect after mmdvm_bridge restarts: restart that unit alone, then wait for a verdict.
  // DMR - Other logs in through mmdvm_bridge; BrandMeister (stfu) has its own section.
  const dmrUnit = 'mmdvm_bridge';
  const bmOn = $derived(!!v?.brandmeister?.configured && !!v?.brandmeister?.enabled);
  // The DMR network card the login check belongs to (TGIF or DMR - Other share mmdvm_bridge).
  const liveDmr = $derived<'TGIF' | 'DMR' | null>(!v ? null : cardEnabled(v, 'TGIF') ? 'TGIF' : cardEnabled(v, 'DMR') ? 'DMR' : null);
  let verifyFor = $state<'TGIF' | 'DMR'>('DMR');
  async function verifyLogin() {
    const my = ++gen;
    verifyFor = liveDmr ?? verifyFor;
    const done = (outcome: Outcome) => { verify = { working: '', outcome }; };
    if (!v?.modes.dmr) { done(outcomeFor({ state: 'disabled' }, true)!); return; }
    const unit = dmrUnit;
    const { inUse, running } = bridgeInUse(units, unit);
    if (!inUse) { done({ kind: 'info', message: `Saved. ${unit} is neither enabled nor running, so the login was not tried.` }); return; }
    if (!running) { done({ kind: 'warn', message: `Saved. ${unit} is not running, so the login has not been tried.`, needStart: true }); return; }
    verify = { working: `Restarting ${unit}…`, outcome: null };
    try {
      const r = await api.dvswitchDmrRestart('restart');
      onunits(r.states);
      hint = hint.filter((h) => h !== 'mmdvm_bridge' && h !== unit);
    } catch (e) { verify = { working: '', outcome: null }; toastError(e); return; }
    await watch(my);
  }
  async function startBridge() {
    const my = ++gen;
    const unit = dmrUnit;
    verify = { working: `Starting ${unit}…`, outcome: null };
    try {
      const r = await api.dvswitchDmrRestart('start');
      onunits(r.states);
      hint = hint.filter((h) => h !== 'mmdvm_bridge' && h !== unit);
    } catch (e) { verify = { working: '', outcome: null }; toastError(e); return; }
    await watch(my);
  }

  const base = () => Object.fromEntries((v?.files ?? []).filter((f) => f.sha256).map((f) => [f.id, f.sha256]));

  // Nothing differs from what is saved: no request, no restart.
  const noChanges = (what: string) => { toast(`${what}: No changes.`, 'info'); };

  async function save(what: string, call: () => Promise<DVSwitchSetup>, check = false) {
    busy = true;
    try {
      const s = await call();
      fill(s);
      hint = s.restartHint ?? [];
      const changed = !!(s.changed && s.changed.length);
      toast(changed ? `${what} saved.` : `${what}: nothing to change.`, 'info');
      if (check) { if (changed) { busy = false; await verifyLogin(); } else pollOnce(); }
    } catch (e) {
      if (e instanceof ApiError && e.code === 'conflict') { toast('A file changed on disk. The values were reloaded; check them and save again.', 'error'); await load(); }
      else toast(dvErrorMessage(e), 'error');
    } finally { busy = false; }
  }
  // The first invalid [Info] field, or '' when all are fine (the server checks the same rules again).
  const infoProblem = $derived.by(() => {
    if (!validInfoText(info.location.trim(), 20)) return 'Location: 1 to 20 characters, letters or digits first, none of ; # = "';
    if (!validInfoText(info.description.trim(), 19)) return 'Description: 1 to 19 characters, letters or digits first, none of ; # = "';
    if (!validInfoUrl(info.url.trim())) return 'URL: empty, or http:// or https:// without spaces';
    if (freqToHz(info.freq) === null) return 'Frequency: 30 to 3000 MHz';
    const a = info.lat.trim(), o = info.lon.trim();
    if ((a || o) && !(validCoord(a, 90) && validCoord(o, 180))) return 'Latitude -90 to 90 and longitude -180 to 180, both or neither';
    return '';
  });
  const infoChanged = $derived(formChanged(snap.info, info));
  const infoBody = () => {
    const a = info.lat.trim(), o = info.lon.trim();
    return { location: info.location.trim(), description: info.description.trim(), url: info.url.trim() || undefined, frequencyHz: freqToHz(info.freq) ?? 0, latitude: a || undefined, longitude: o || undefined };
  };
  function saveIdentity() {
    const idChanged = formChanged(snap.id, id);
    if (!idChanged && !infoChanged) return noChanges('Identity');
    if (infoChanged && infoProblem) { toast(infoProblem, 'error'); return; }
    return save('Identity', () => api.dvswitchIdentity({ ...id, nxdnId: id.nxdnId.trim() || undefined, info: infoChanged ? infoBody() : undefined, base: base() }), true);
  }
  function saveNet() {
    const p = credentialsPatch(snap.net, net, clearPw);
    if (!p) return noChanges('DMR master');
    if (p.dmrAddress && isBMHost(p.dmrAddress, lists.dmr)) { toast('That is a BrandMeister master: set it up under BrandMeister instead.', 'error'); return; }
    return save('DMR master', async () => { try { return await api.dvswitchCredentials({ ...p, base: base() }); } finally { net.password = ''; clearPw = false; } }, !!v && cardEnabled(v, 'DMR'));
  }
  const CARD_LABEL: Record<DVCard, string> = { DMR: 'DMR - Other', TGIF: 'TGIF', YSF: 'System Fusion (YSF)', P25: 'P25', NXDN: 'NXDN' };
  // Enable/Disable of a card. DMR - Other and TGIF restart mmdvm_bridge only and check the login when turned on;
  // turning one of them on turns the other off (they share [DMR Network]). The settings stay when a card is off.
  async function setCard(card: DVCard, on: boolean) {
    if (!on) { gen++; verify = { working: '', outcome: null }; }
    const what = `${CARD_LABEL[card]} ${on ? 'enabled' : 'disabled'}`;
    const call = card === 'TGIF' ? () => api.dvswitchTgifEnabled(on, base()) : () => api.dvswitchModes({ ...modesBody(card, on), base: base() });
    await save(what, call, on && (card === 'TGIF' || card === 'DMR'));
    if (!on) pollOnce();
  }
  function saveTgif() {
    if (!tgifPw && !tgifClear) return noChanges('TGIF');
    const live = !!v?.tgif.enabled;
    return save('TGIF', async () => { try { return await api.dvswitchTgif({ ...(tgifClear ? { clearPassword: true } : { password: tgifPw }), base: base() }); } finally { tgifPw = ''; tgifClear = false; } }, live);
  }
  // The BrandMeister section saved (s) or asks for a reload (null).
  async function connSaved(s: DVSwitchSetup | null) {
    if (!s) { await load(); return; }
    fill(s);
    hint = s.restartHint ?? [];
    pollOnce();
    try { onunits((await api.dvswitch()).units); } catch { /* the next poll updates them */ }
  }
  async function setResume(on: boolean) {
    busy = true;
    try { resume = await api.dvswitchSetResume(on); now = Date.now(); toast(resumeToast(on), 'info'); }
    catch (e) { toastError(e); } finally { busy = false; }
  }
  async function rollback(file: string) {
    await save(`${FILE_LABEL[file] ?? file} restored`, () => api.dvswitchRollback(file, base()));
  }
  async function run(action: 'restart' | 'start') {
    confirmRestart = false; busy = true;
    try {
      const r = await api.dvswitchRestart(scope, action);
      onunits(r.states);
      hint = [];
      toast(`${action === 'start' ? 'Started' : 'Restarted'}: ${r.units.join(', ')}.`, 'info');
      await load();
      if (scope !== 'gateways') pollOnce();
    } catch (e) { toastError(e); } finally { busy = false; }
  }
  const downNames = $derived(units.filter((u) => u.loaded && u.active !== 'active').map((u) => u.name));
  const ok = $derived(!!v && v.files.some((f) => f.present));
</script>

{#snippet toggle(card: DVCard, on: boolean)}
  {#if on}
    <button type="button" class="btn btn-sm" data-testid="dv-toggle-{card}" disabled={busy || !!verify.working} onclick={() => setCard(card, false)}>Disable</button>
  {:else}
    <button type="button" class="btn btn-sm btn-primary" data-testid="dv-toggle-{card}" disabled={busy || !!verify.working} onclick={() => setCard(card, true)}>Enable</button>
  {/if}
{/snippet}
{#snippet verifyBox()}
  {#if verify.working}
    <p class="rounded border border-line p-2 text-sm text-muted" role="status">{verify.working}</p>
  {:else if verify.outcome}
    <p class="rounded border p-2 text-sm {KIND[verify.outcome.kind]}" role="status">{verify.outcome.message}{#if verify.outcome.needStart}<button type="button" class="btn btn-sm ml-2" onclick={startBridge}>Start {dmrUnit}</button>{/if}</p>
  {/if}
{/snippet}

{#if !v}
  <p class="text-sm text-muted">{err || 'Loading setup…'}</p>
{:else if !ok}
  <p class="text-sm text-muted" role="status">The DVSwitch ini files are not there, so there is nothing to set up.</p>
{:else}
  {#if hint.length}
    <p class="rounded border border-warn/60 p-2 text-sm text-warn" role="status">Saved. These services pick the change up on a restart: <span class="font-mono">{hint.join(', ')}</span>. {WARNING}</p>
  {/if}
  <div class="grid grid-cols-1 items-start gap-4 lg:grid-cols-2">
    <section class="card space-y-3 p-4 lg:col-span-2" aria-label="Identity">
      <h2 class="text-base font-semibold">Identity</h2>
      {#each (v.warnings ?? []).filter((w) => w !== NXDN_WARNING && !(v?.dmr.pointsAtBm && w.startsWith('DMR - Other points at'))) as w}<p class="text-xs text-warn" role="status">{w}</p>{/each}
      {#if !v.identity.consistent}<p class="text-xs text-warn" role="status">The identity differs between the ini files (edited by hand?). Saving writes it to all of them.</p>{/if}
      <form class="grid grid-cols-1 gap-2 sm:grid-cols-3" onsubmit={(e) => { e.preventDefault(); saveIdentity(); }}>
        <div><label class="label" for="dv-call">Callsign</label><input id="dv-call" class="input font-mono" bind:value={id.callsign} maxlength="10" autocapitalize="characters" /></div>
        <div><label class="label" for="dv-id">DMR ID</label><input id="dv-id" class="input font-mono" bind:value={id.dmrId} inputmode="numeric" maxlength="7" placeholder="1234567" /></div>
        <div><label class="label" for="dv-essid">ESSID</label><input id="dv-essid" class="input font-mono" bind:value={id.essid} inputmode="numeric" maxlength="2" placeholder="01" aria-describedby={bmOn ? 'dv-essid-hint' : undefined} />{#if bmOn}<p id="dv-essid-hint" class="mt-1 text-xs text-muted">Not used by BrandMeister: STFU logs in with the 7-digit DMR ID.</p>{/if}</div>
        <div class="sm:col-span-3"><label class="label" for="dv-nxdnid">NXDN ID</label><input id="dv-nxdnid" class="input font-mono sm:w-40" bind:value={id.nxdnId} inputmode="numeric" maxlength="5" placeholder="12345" aria-describedby="dv-nxdnid-hint" /><p id="dv-nxdnid-hint" class="mt-1 text-xs text-muted">from radioid.net, separate from the DMR ID</p></div>
        <fieldset class="grid grid-cols-1 gap-2 sm:col-span-3 sm:grid-cols-3" data-testid="dv-info">
          <legend class="label">Hotspot info <span class="font-normal text-muted">(the [Info] section; shown to DMR networks)</span></legend>
          <div><label class="label" for="dv-loc">Location</label><input id="dv-loc" class="input" bind:value={info.location} maxlength="20" autocomplete="off" /></div>
          <div><label class="label" for="dv-desc">Description</label><input id="dv-desc" class="input" bind:value={info.description} maxlength="19" autocomplete="off" /></div>
          <div><label class="label" for="dv-url">URL</label><input id="dv-url" class="input font-mono" bind:value={info.url} maxlength="124" placeholder="https://example.org" autocomplete="off" /></div>
          <div><label class="label" for="dv-freq">Frequency (MHz)</label><input id="dv-freq" class="input font-mono" bind:value={info.freq} inputmode="decimal" placeholder="434" aria-describedby="dv-freq-hint" /><p id="dv-freq-hint" class="mt-1 text-xs text-muted">One value, used for receive and transmit. Hz also works.</p></div>
          <div><label class="label" for="dv-lat">Latitude <span class="font-normal">(optional)</span></label><input id="dv-lat" class="input font-mono" bind:value={info.lat} inputmode="decimal" placeholder="51.5" autocomplete="off" /></div>
          <div><label class="label" for="dv-lon">Longitude <span class="font-normal">(optional)</span></label><input id="dv-lon" class="input font-mono" bind:value={info.lon} inputmode="decimal" placeholder="-0.12" autocomplete="off" /></div>
          {#if v.identity.info?.stock}<p class="text-xs text-warn sm:col-span-3" role="status" data-testid="dv-info-stock">The [Info] values are still the stock ones. Set your own before the bridge talks to a network.</p>{/if}
          {#if infoChanged && infoProblem}<p class="text-xs text-warn sm:col-span-3" role="status">{infoProblem}</p>{/if}
        </fieldset>
        <div class="sm:col-span-3"><button class="btn btn-primary" disabled={busy}>Save identity</button><span class="ml-2 text-xs text-muted">Written to every place that holds it.</span></div>
      </form>
    </section>

    <section class="card space-y-2 p-4 lg:col-span-2" aria-label="Resume" data-testid="dv-notice">
      <p class="text-sm">{RESUME_NOTICE}</p>
      {#if resume}
        <div class="flex flex-wrap items-center gap-x-4 gap-y-1">
          <span class="inline-flex items-center gap-2 text-sm"><Toggle checked={resume.resume} disabled={busy} label="Resume after restart" onchange={setResume} />Resume after restart</span>
          {#if lastUsed(resume, now)}<span class="text-xs text-muted" data-testid="dv-last-used">{lastUsed(resume, now)}</span>{/if}
        </div>
        <p class="text-xs text-muted" data-testid="dv-resume-hint">{resumeHint(resume.resume)}</p>
      {/if}
    </section>

    <DvswitchBM {v} {base} status={dmr?.bm ?? null} hosts={lists.dmr} disabled={busy} onsaved={connSaved} />

    <section class="card space-y-3 p-4" aria-label="TGIF">
      <div class="flex flex-wrap items-center gap-2">
        <h2 class="text-base font-semibold">TGIF</h2>
        {#if v.tgif.enabled}<span class="badge {TONE[bd.tone]}" role="status" title={dmr?.lastLine ?? ''} data-testid="dv-tgif-state">{bd.text}</span>{:else}<span class="badge" role="status" data-testid="dv-tgif-state">Disabled</span>{/if}
        <span class="ml-auto"></span>
        {@render toggle('TGIF', v.tgif.enabled)}
      </div>
      <p class="text-xs text-muted">The TGIF network through MMDVM_Bridge. {TGIF_EXCLUSIVE}</p>
      <dl class="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-sm">
        <dt class="text-muted">Master</dt><dd class="font-mono break-all" data-testid="dv-tgif-master">{v.tgif.address}:{v.tgif.port}</dd>
        <dt class="text-muted">Source</dt><dd class="text-xs {v.tgif.source === 'default' ? 'text-warn' : 'text-muted'}" data-testid="dv-tgif-source">{tgifSource(v.tgif)}</dd>
      </dl>
      <form class="space-y-1" onsubmit={(e) => { e.preventDefault(); saveTgif(); }}>
        <label class="label" for="dv-tgif-pw">TGIF password <span class="text-muted" data-testid="dv-tgif-pw-state">({v.tgif.passwordSet ? 'Password saved' : 'No password'})</span></label>
        <input id="dv-tgif-pw" class="input font-mono" type="password" autocomplete="new-password" bind:value={tgifPw} disabled={tgifClear} placeholder={tgifClear ? 'The password will be cleared' : v.tgif.passwordSet ? 'Leave empty to keep the password' : 'Enter the password'} maxlength="64" />
        {#if v.tgif.passwordSet}<label class="flex items-center gap-2 text-sm"><input type="checkbox" bind:checked={tgifClear} /> Clear password</label>{/if}
        <p class="text-xs text-muted">The hotspot password of your TGIF account, kept apart from DMR - Other's. Write-only: it is never shown again.</p>
        <div class="flex flex-wrap items-center gap-2 pt-1"><button class="btn btn-primary" disabled={busy || !!verify.working}>Save TGIF</button><span class="text-xs text-muted">{v.tgif.enabled ? `Saving restarts ${dmrUnit} only, then checks the login.` : 'Enabling restarts mmdvm_bridge only, then checks the login.'}</span></div>
      </form>
      {#if verifyFor === 'TGIF'}{@render verifyBox()}{/if}
    </section>

    <section class="card space-y-3 p-4" aria-label="DMR - Other">
      <form class="grid grid-cols-1 gap-2 sm:grid-cols-3" onsubmit={(e) => { e.preventDefault(); saveNet(); }}>
        <div class="flex flex-wrap items-center gap-2 sm:col-span-3">
          <h2 class="text-base font-semibold">DMR - Other</h2>
          {#if cardEnabled(v, 'DMR')}<span class="badge {TONE[bd.tone]}" role="status" title={dmr?.lastLine ?? ''} data-testid="dv-dmr-state">{bd.text}</span>{:else}<span class="badge" role="status" data-testid="dv-dmr-state">Disabled</span>{/if}
          <span class="ml-auto"></span>
          {@render toggle('DMR', cardEnabled(v, 'DMR'))}
          <span class="w-full text-xs text-muted">Any DMR network but BrandMeister and TGIF, through MMDVM_Bridge. {OTHER_EXCLUSIVE}</span>
        </div>
        {#if v.dmr.pointsAtBm}<p class="text-xs text-warn sm:col-span-3" role="status" data-testid="dv-dmr-points-at-bm">DMR - Other points at a BrandMeister master. Set BrandMeister up under BrandMeister, then point DMR - Other at another network or disable it.</p>{/if}
        <div class="sm:col-span-2"><label class="label" for="dv-addr">DMR master address</label>
          <NodePicker id="dv-addr" bind:value={net.address} options={otherOptions(lists.dmr)} search={find('dmr')} label={label('dmr')} onpickoption={(o) => { net.port = String(o.port); }} placeholder="Search a master or type an address" inputmode="text" maxlength={253} describedby="dv-addr-hint" /></div>
        <div><label class="label" for="dv-port">Port</label><input id="dv-port" class="input font-mono" bind:value={net.port} inputmode="numeric" maxlength="5" /></div>
        <p id="dv-addr-hint" class="text-xs text-muted sm:col-span-3">{lists.dmr.length ? 'Search by name, network ID or address. Picking a master fills the address and the port; you can also type your own.' : 'Type the master address and port.'} The list never holds your password: enter your own below.</p>
        <div class="sm:col-span-3">
          <label class="label" for="dv-pw">DMR password <span class="text-muted" data-testid="dv-pw-state">({v.dmr.passwordSet ? 'Password saved' : 'No password'})</span></label>
          <input id="dv-pw" class="input font-mono" type="password" autocomplete="new-password" bind:value={net.password} disabled={clearPw} placeholder={clearPw ? 'The password will be cleared' : v.dmr.passwordSet ? 'Leave empty to keep the password' : 'Enter the password'} maxlength="64" />
          {#if v.dmr.passwordSet}
            <label class="mt-1 flex items-center gap-2 text-sm"><input type="checkbox" bind:checked={clearPw} /> Clear password <span class="text-xs text-muted">(for masters that don't need a password)</span></label>
          {/if}
          <p class="mt-1 text-xs text-muted">Write-only: it is never shown again. Printable ASCII, up to 64 characters, without quotes, # or ;.</p>
        </div>
        <div class="flex flex-wrap items-center gap-2 sm:col-span-3"><button class="btn btn-primary" disabled={busy || !!verify.working}>Save DMR master</button><span class="text-xs text-muted">{cardEnabled(v, 'DMR') ? `Saving restarts ${dmrUnit} only, then checks the login.` : 'Saved only while DMR - Other is disabled.'}</span></div>
      </form>
      {#if verifyFor === 'DMR'}{@render verifyBox()}{/if}
    </section>

    {#each [['YSF', 'ysf'], ['P25', 'p25'], ['NXDN', 'nxdn']] as const as [card, k] (card)}
      <section class="card space-y-2 p-4" aria-label={CARD_LABEL[card]}>
        <div class="flex flex-wrap items-center gap-2">
          <h2 class="text-base font-semibold">{CARD_LABEL[card]}</h2>
          <span class="badge {cardEnabled(v, card) ? 'border-ok text-ok' : ''}" role="status" data-testid="dv-{k}-state">{cardEnabled(v, card) ? 'Enabled' : 'Disabled'}</span>
          <span class="ml-auto"></span>
          {@render toggle(card, cardEnabled(v, card))}
        </div>
        {#if !v.gateways[k].present}<p class="text-xs text-muted">{card}Gateway is not installed.</p>{/if}
        {#if card === 'NXDN' && (v.warnings ?? []).includes(NXDN_WARNING)}<p class="text-xs text-warn" role="status" data-testid="dv-nxdn-warning">{NXDN_WARNING} (Identity, NXDN ID).</p>{/if}
      </section>
    {/each}

    <div class="lg:col-span-2"><DvswitchBridge b={v.bridgeNode} base={base()} onsaved={load} /></div>

    <section class="card space-y-3 p-4" aria-label="Services">
      <h2 class="text-base font-semibold">Services</h2>
      <ul class="divide-y divide-line rounded border border-line text-xs">
        {#each units.filter((u) => u.loaded && u.name !== 'netcheck' && u.name !== 'md380-emu') as u}
          <li class="flex flex-wrap items-baseline gap-x-2 px-2 py-1"><span class="font-mono font-semibold">{u.name}</span><span class="{u.active === 'active' ? 'text-ok' : 'text-warn'}">{u.active}</span></li>
        {/each}
      </ul>
      <div class="flex flex-wrap items-center gap-2">
        <label class="sr-only" for="dv-scope">Scope</label>
        <select id="dv-scope" class="input w-auto max-w-full min-w-0" bind:value={scope}><option value="dmr">DMR only ({bmOn ? 'MMDVM_Bridge, and STFU while it runs' : 'MMDVM_Bridge'})</option><option value="bridge">Bridge (Analog_Bridge, MMDVM_Bridge)</option><option value="gateways">Gateways (YSF, P25, NXDN)</option><option value="all">All</option></select>
        <button class="btn" disabled={busy} onclick={() => run('start')} title="Start the units that are stopped">Start</button>
        <button class="btn btn-primary" disabled={busy} onclick={() => (confirmRestart = true)}>Restart</button>
      </div>
      {#if downNames.length}<p class="text-xs text-muted">Stopped after the install: {downNames.join(', ')}. Start them once the settings are right.</p>{/if}
      <p class="text-xs text-warn">{WARNING}</p>
    </section>

    <section class="card space-y-3 p-4" aria-label="Rollback">
      <h2 class="text-base font-semibold">Rollback</h2>
      <p class="text-xs text-muted">Each save keeps the previous version of the file (the last 10). Restoring puts back the newest one.</p>
      <ul class="divide-y divide-line rounded border border-line text-sm">
        {#each v.files.filter((f) => f.present) as f}
          <li class="flex flex-wrap items-center gap-2 px-2 py-1">
            <span class="font-mono text-xs font-semibold">{FILE_LABEL[f.id] ?? f.id}</span>
            <span class="text-xs text-muted">{f.history} earlier {f.history === 1 ? 'version' : 'versions'}</span>
            <button class="btn btn-sm ml-auto" disabled={busy || !f.history || !f.writable} onclick={() => { rollbackFile = f.id; rbOpen = true; }}>Restore previous</button>
          </li>
        {/each}
      </ul>
    </section>
  </div>
{/if}

<Confirm bind:open={confirmRestart} title="Restart services?" confirmLabel="Restart" message={`This restarts the ${({ all: 'bridge and the gateways', bridge: 'bridge', dmr: bmOn ? 'DMR connections (MMDVM_Bridge, and STFU while it runs)' : 'DMR master connection (MMDVM_Bridge)', gateways: 'gateways' } as const)[scope]}. ${WARNING}`} onconfirm={() => run('restart')} />
<Confirm bind:open={rbOpen} title="Restore the previous version?" confirmLabel="Restore" message={`${FILE_LABEL[rollbackFile] ?? rollbackFile} goes back to its previous version. Services pick it up on a restart.`} onconfirm={() => rollback(rollbackFile)} />
