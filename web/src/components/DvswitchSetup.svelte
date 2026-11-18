<script lang="ts">
  // Credentials and config of a node with DVSwitch installed (docs/DVSWITCH.md, M4).
  // The DMR password is write-only: the field is never prefilled, the server only says whether one is set.
  import { onMount } from 'svelte';
  import { api, ApiError } from '../lib/api';
  import { toast, toastError } from '../lib/toast.svelte';
  import type { DVDMRStatus, DVListItem, DVListMode, DVResume, DVRestartScope, DVSwitchSetup, DVSwitchStatus } from '../lib/types';
  import { badge, bridgeInUse, IDLE_POLL_MS, outcomeFor, waitForLogin, type Outcome } from '../lib/dmrlogin';
  import { credentialsPatch, dvErrorMessage, formChanged } from '../lib/dvmsg';
  import { findByValue, indexList, optionLabel, searchList } from '../lib/dvlist';
  import { ambeModeFor, cardOrder, cardPatch, lastUsed, MODE_LABEL, startupModeOf, STARTUP_MODES, type StartupMode } from '../lib/dvstartup';
  import Confirm from './Confirm.svelte';
  import NodePicker from './NodePicker.svelte';

  let { units, onunits }: { units: DVSwitchStatus['units']; onunits: (u: DVSwitchStatus['units']) => void } = $props();

  const FILE_LABEL: Record<string, string> = { analog_bridge: 'Analog_Bridge.ini', mmdvm_bridge: 'MMDVM_Bridge.ini', ysfgateway: 'YSFGateway.ini', p25gateway: 'P25Gateway.ini', nxdngateway: 'NXDNGateway.ini' };
  const WARNING = 'A restart resets the running mode and talkgroup to the startup values.';

  let v = $state<DVSwitchSetup | null>(null), err = $state(''), busy = $state(false);
  let hint = $state<string[]>([]);
  // Host and TG lists of the pickers. A mode without a list (404 no_list, or any error) stays empty:
  // its field is then plain text, which is always allowed.
  const MODES: DVListMode[] = ['dmr', 'ysf', 'p25', 'nxdn'];
  let lists = $state<Record<DVListMode, DVListItem[]>>({ dmr: [], ysf: [], p25: [], nxdn: [] });
  const index = $derived({ dmr: indexList(lists.dmr), ysf: indexList(lists.ysf), p25: indexList(lists.p25), nxdn: indexList(lists.nxdn) });
  const find = (m: DVListMode) => (t: string) => searchList(index[m], t);
  const label = (m: DVListMode) => (o: DVListItem) => optionLabel(m, o);
  async function loadLists() {
    await Promise.all(MODES.map(async (m) => { try { lists[m] = (await api.dvswitchList(m)).items; } catch { /* no list: free text */ } }));
  }
  const hit = (m: Exclude<DVListMode, 'dmr'>, value: string) => findByValue(lists[m], value);
  let id = $state({ callsign: '', dmrId: '', essid: '', nxdnId: '' });
  let net = $state({ address: '', port: '', password: '' });
  let clearPw = $state(false);
  let modes = $state({ dmr: false, ysf: false, p25: false, nxdn: false });
  let st = $state({ ambeMode: 'DMR', txTg: '', txTs: '2', colorCode: '1', ysf: '', p25: '', nxdn: '' });
  // The startup mode choice, saved on its own; each card below saves only its own fields.
  let choice = $state<StartupMode>('DMR');
  // The last tune of each mode and the resume toggle (GET /api/dvswitch/resume).
  let resume = $state<DVResume | null>(null), now = $state(Date.now());
  async function loadResume() { try { resume = await api.dvswitchResume(); now = Date.now(); } catch { /* no tracker: no hints */ } }
  const order = $derived(cardOrder(choice));
  const gw = (m: StartupMode) => m === 'DMR' || !!v?.gateways[m.toLowerCase() as 'ysf' | 'p25' | 'nxdn'].present;
  let scope = $state<DVRestartScope>('bridge');
  let confirmRestart = $state(false), rbOpen = $state(false), rollbackFile = $state('');

  // The form as loaded: a save with nothing different sends nothing.
  let snap = $state.raw({ id: { callsign: '', dmrId: '', essid: '', nxdnId: '' }, net: { address: '', port: '', password: '' }, modes: { dmr: false, ysf: false, p25: false, nxdn: false }, st: { ambeMode: 'DMR', txTg: '', txTs: '2', colorCode: '1', ysf: '', p25: '', nxdn: '' } });
  function fill(s: DVSwitchSetup) {
    v = s;
    id = { callsign: s.identity.callsign, dmrId: s.identity.dmrId, essid: s.identity.essid, nxdnId: s.identity.nxdnId ?? '' };
    net = { address: s.dmr.address, port: s.dmr.port ? String(s.dmr.port) : '', password: '' };
    clearPw = false;
    modes = { ...s.modes };
    st = { ambeMode: s.startup.ambeMode || 'DMR', txTg: s.startup.txTg, txTs: String(s.startup.txTs || 2), colorCode: String(s.startup.colorCode), ysf: s.gateways.ysf.startup, p25: s.gateways.p25.startup, nxdn: s.gateways.nxdn.startup };
    choice = startupModeOf(st.ambeMode);
    snap = { id: { ...id }, net: { ...net }, modes: { ...modes }, st: { ...st } };
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
  async function verifyLogin() {
    const my = ++gen;
    const done = (outcome: Outcome) => { verify = { working: '', outcome }; };
    if (!v?.modes.dmr) { done(outcomeFor({ state: 'disabled' }, true)!); return; }
    const { inUse, running } = bridgeInUse(units);
    if (!inUse) { done({ kind: 'info', message: 'Saved. mmdvm_bridge is neither enabled nor running, so the login was not tried.' }); return; }
    if (!running) { done({ kind: 'warn', message: 'Saved. mmdvm_bridge is not running, so the login has not been tried.', needStart: true }); return; }
    verify = { working: 'Restarting mmdvm_bridge…', outcome: null };
    try {
      const r = await api.dvswitchDmrRestart('restart');
      onunits(r.states);
      hint = hint.filter((h) => h !== 'mmdvm_bridge');
    } catch (e) { verify = { working: '', outcome: null }; toastError(e); return; }
    await watch(my);
  }
  async function startBridge() {
    const my = ++gen;
    verify = { working: 'Starting mmdvm_bridge…', outcome: null };
    try {
      const r = await api.dvswitchDmrRestart('start');
      onunits(r.states);
      hint = hint.filter((h) => h !== 'mmdvm_bridge');
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
  const saveIdentity = () => formChanged(snap.id, id) ? save('Identity', () => api.dvswitchIdentity({ ...id, nxdnId: id.nxdnId.trim() || undefined, base: base() }), true) : noChanges('Identity');
  function saveNet() {
    const p = credentialsPatch(snap.net, net, clearPw);
    if (!p) return noChanges('DMR master');
    return save('DMR master', async () => { try { return await api.dvswitchCredentials({ ...p, base: base() }); } finally { net.password = ''; clearPw = false; } }, true);
  }
  const saveModes = () => formChanged(snap.modes, modes) ? save('Modes', () => api.dvswitchModes({ ...modes, base: base() })) : noChanges('Modes');
  const saveMode = () => choice === startupModeOf(snap.st.ambeMode) ? noChanges('Startup mode') : save('Startup mode', () => api.dvswitchStartup({ ambeMode: ambeModeFor(choice, snap.st.ambeMode), base: base() }));
  function saveCard(m: StartupMode) {
    const p = cardPatch(m, snap.st, st);
    return p ? save(`${MODE_LABEL[m]} startup`, () => api.dvswitchStartup({ ...p, base: base() })) : noChanges(`${MODE_LABEL[m]} startup`);
  }
  async function setResume(on: boolean) {
    busy = true;
    try { resume = await api.dvswitchSetResume(on); now = Date.now(); toast(on ? 'The last tune is resumed after a restart.' : 'The startup values are used after a restart.', 'info'); }
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

{#if !v}
  <p class="text-sm text-muted">{err || 'Loading setup…'}</p>
{:else if !ok}
  <p class="text-sm text-muted" role="status">The DVSwitch ini files are not there, so there is nothing to set up.</p>
{:else}
  {#if hint.length}
    <p class="rounded border border-warn/60 p-2 text-sm text-warn" role="status">Saved. These services pick the change up on a restart: <span class="font-mono">{hint.join(', ')}</span>. {WARNING}</p>
  {/if}
  <div class="grid grid-cols-1 items-start gap-4 lg:grid-cols-2">
    <section class="card space-y-3 p-4" aria-label="Identity and network">
      <h2 class="text-base font-semibold">Identity and network</h2>
      {#each v.warnings ?? [] as w}<p class="text-xs text-warn" role="status">{w}</p>{/each}
      {#if !v.identity.consistent}<p class="text-xs text-warn" role="status">The identity differs between the ini files (edited by hand?). Saving writes it to all of them.</p>{/if}
      <form class="grid grid-cols-1 gap-2 sm:grid-cols-3" onsubmit={(e) => { e.preventDefault(); saveIdentity(); }}>
        <div><label class="label" for="dv-call">Callsign</label><input id="dv-call" class="input font-mono" bind:value={id.callsign} maxlength="10" autocapitalize="characters" /></div>
        <div><label class="label" for="dv-id">DMR ID</label><input id="dv-id" class="input font-mono" bind:value={id.dmrId} inputmode="numeric" maxlength="7" placeholder="1234567" /></div>
        <div><label class="label" for="dv-essid">ESSID</label><input id="dv-essid" class="input font-mono" bind:value={id.essid} inputmode="numeric" maxlength="2" placeholder="01" /></div>
        <div class="sm:col-span-3"><label class="label" for="dv-nxdnid">NXDN ID</label><input id="dv-nxdnid" class="input font-mono sm:w-40" bind:value={id.nxdnId} inputmode="numeric" maxlength="5" placeholder="12345" aria-describedby="dv-nxdnid-hint" /><p id="dv-nxdnid-hint" class="mt-1 text-xs text-muted">from radioid.net, separate from the DMR ID</p></div>
        <div class="sm:col-span-3"><button class="btn btn-primary" disabled={busy}>Save identity</button><span class="ml-2 text-xs text-muted">Written to every place that holds it.</span></div>
      </form>
      <form class="grid grid-cols-1 gap-2 border-t border-line pt-3 sm:grid-cols-3" onsubmit={(e) => { e.preventDefault(); saveNet(); }}>
        <div class="flex flex-wrap items-center gap-2 sm:col-span-3" aria-label="DMR login">
          <h3 class="text-sm font-semibold">DMR login</h3>
          <span class="badge {TONE[bd.tone]}" role="status" title={dmr?.lastLine ?? ''}>{bd.text}</span>
        </div>
        <div class="sm:col-span-2"><label class="label" for="dv-addr">DMR master address</label>
          <NodePicker id="dv-addr" bind:value={net.address} options={lists.dmr} search={find('dmr')} label={label('dmr')} onpickoption={(o) => { net.port = String(o.port); }} placeholder="Search a master or type an address" inputmode="text" maxlength={253} describedby="dv-addr-hint" /></div>
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
        <div class="sm:col-span-3"><button class="btn btn-primary" disabled={busy || !!verify.working}>Save DMR master</button><span class="ml-2 text-xs text-muted">Saving restarts mmdvm_bridge only, then checks the login.</span></div>
        {#if verify.working}
          <p class="rounded border border-line p-2 text-sm text-muted sm:col-span-3" role="status">{verify.working}</p>
        {:else if verify.outcome}
          <p class="rounded border p-2 text-sm sm:col-span-3 {KIND[verify.outcome.kind]}" role="status">{verify.outcome.message}{#if verify.outcome.needStart}<button type="button" class="btn btn-sm ml-2" onclick={startBridge}>Start mmdvm_bridge</button>{/if}</p>
        {/if}
      </form>
    </section>

    <section class="card space-y-3 p-4" aria-label="Modes and startup">
      <h2 class="text-base font-semibold">Enabled modes</h2>
      <div class="flex flex-wrap gap-x-4 gap-y-1 text-sm">
        {#each [['dmr', 'DMR'], ['ysf', 'System Fusion'], ['p25', 'P25'], ['nxdn', 'NXDN']] as [k, label]}
          <label class="inline-flex cursor-pointer items-center gap-1.5"><input type="checkbox" class="size-4 accent-[var(--accent)]" bind:checked={modes[k as 'dmr']} disabled={busy} />{label}</label>
        {/each}
      </div>
      <div><button class="btn btn-primary" disabled={busy} onclick={saveModes}>Save modes</button><span class="ml-2 text-xs text-muted">Sets the flag in the mode and its network section.</span></div>

      <h2 class="border-t border-line pt-3 text-base font-semibold">Startup</h2>
      <p class="text-xs text-muted">What Analog_Bridge and the gateways start with. A restart goes back to these{resume?.resume ? ', unless the last tune is resumed (below)' : ''}.</p>
      <form class="flex flex-wrap items-end gap-2" onsubmit={(e) => { e.preventDefault(); saveMode(); }} aria-label="Startup mode">
        <div><label class="label" for="dv-mode">Startup mode</label><select id="dv-mode" class="input" bind:value={choice}>{#each STARTUP_MODES as m}<option value={m}>{MODE_LABEL[m]}</option>{/each}</select></div>
        <button class="btn btn-primary" disabled={busy}>Save startup mode</button>
      </form>
      <div class="flex flex-col gap-3">

        {#each order as m (m)}{#if gw(m)}{#if m === 'DMR'}
        <div class="space-y-2 rounded border p-3 {choice === 'DMR' ? 'border-accent' : 'border-line'}" aria-label="DMR startup">
          <h3 class="flex flex-wrap items-center gap-2 text-sm font-semibold">{MODE_LABEL['DMR']}{#if choice === 'DMR'}<span class="badge border-accent text-accent">used at startup</span>{/if}</h3>
          <form class="grid grid-cols-3 gap-2" onsubmit={(e) => { e.preventDefault(); saveCard('DMR'); }}>
            <div><label class="label" for="dv-tg">Talkgroup</label><input id="dv-tg" class="input font-mono" bind:value={st.txTg} inputmode="numeric" maxlength="8" /></div>
            <div><label class="label" for="dv-ts">Slot</label><select id="dv-ts" class="input" bind:value={st.txTs}><option value="1">1</option><option value="2">2</option></select></div>
            <div><label class="label" for="dv-cc">Color code</label><input id="dv-cc" class="input font-mono" bind:value={st.colorCode} inputmode="numeric" maxlength="2" /></div>
            <div class="col-span-3"><button class="btn btn-primary" disabled={busy}>Save DMR startup</button></div>
          </form>
          {#if lastUsed(resume, 'DMR', now)}<p class="text-xs text-muted" data-testid="dv-last-DMR">{lastUsed(resume, 'DMR', now)}</p>{/if}
        </div>
        {:else if m === 'YSF'}
        <div class="space-y-2 rounded border p-3 {choice === 'YSF' ? 'border-accent' : 'border-line'}" aria-label="YSF startup">
          <h3 class="flex flex-wrap items-center gap-2 text-sm font-semibold">{MODE_LABEL['YSF']}{#if choice === 'YSF'}<span class="badge border-accent text-accent">used at startup</span>{/if}</h3>
          <form onsubmit={(e) => { e.preventDefault(); saveCard('YSF'); }}>
            <label class="label" for="dv-ysf">YSF reflector</label><NodePicker id="dv-ysf" bind:value={st.ysf} options={lists.ysf} search={find('ysf')} label={label('ysf')} placeholder="Search or type an ID" inputmode="text" maxlength={32} describedby="dv-ysf-hint" /><p id="dv-ysf-hint" class="mt-1 text-xs text-muted">{#if hit('ysf', st.ysf)}{hit('ysf', st.ysf)?.name}, {hit('ysf', st.ysf)?.address}:{hit('ysf', st.ysf)?.port}{:else}reflector ID (5 digits, e.g. 12345) or name exactly as in YSFHosts.txt{/if}</p>
            <button class="btn btn-primary mt-2" disabled={busy}>Save YSF startup</button>
          </form>
          {#if lastUsed(resume, 'YSF', now)}<p class="text-xs text-muted" data-testid="dv-last-YSF">{lastUsed(resume, 'YSF', now)}</p>{/if}
        </div>
        {:else if m === 'P25'}
        <div class="space-y-2 rounded border p-3 {choice === 'P25' ? 'border-accent' : 'border-line'}" aria-label="P25 startup">
          <h3 class="flex flex-wrap items-center gap-2 text-sm font-semibold">{MODE_LABEL['P25']}{#if choice === 'P25'}<span class="badge border-accent text-accent">used at startup</span>{/if}</h3>
          <form onsubmit={(e) => { e.preventDefault(); saveCard('P25'); }}>
            <label class="label" for="dv-p25">P25 reflector</label><NodePicker id="dv-p25" bind:value={st.p25} options={lists.p25} search={find('p25')} label={label('p25')} placeholder="Search or type a TG" inputmode="numeric" maxlength={5} describedby="dv-p25-hint" /><p id="dv-p25-hint" class="mt-1 text-xs text-muted">{#if hit('p25', st.p25)}{hit('p25', st.p25)?.address}:{hit('p25', st.p25)?.port}{:else}talkgroup number, up to 5 digits{/if}</p>
            <button class="btn btn-primary mt-2" disabled={busy}>Save P25 startup</button>
          </form>
          {#if lastUsed(resume, 'P25', now)}<p class="text-xs text-muted" data-testid="dv-last-P25">{lastUsed(resume, 'P25', now)}</p>{/if}
        </div>
        {:else}
        <div class="space-y-2 rounded border p-3 {choice === 'NXDN' ? 'border-accent' : 'border-line'}" aria-label="NXDN startup">
          <h3 class="flex flex-wrap items-center gap-2 text-sm font-semibold">{MODE_LABEL['NXDN']}{#if choice === 'NXDN'}<span class="badge border-accent text-accent">used at startup</span>{/if}</h3>
          <form onsubmit={(e) => { e.preventDefault(); saveCard('NXDN'); }}>
            <label class="label" for="dv-nxdn">NXDN reflector</label><NodePicker id="dv-nxdn" bind:value={st.nxdn} options={lists.nxdn} search={find('nxdn')} label={label('nxdn')} placeholder="Search or type a TG" inputmode="numeric" maxlength={5} describedby="dv-nxdn-hint" /><p id="dv-nxdn-hint" class="mt-1 text-xs text-muted">{#if hit('nxdn', st.nxdn)}{hit('nxdn', st.nxdn)?.address}:{hit('nxdn', st.nxdn)?.port}{:else}talkgroup number, up to 5 digits{/if}</p>
            <button class="btn btn-primary mt-2" disabled={busy}>Save NXDN startup</button>
          </form>
          {#if lastUsed(resume, 'NXDN', now)}<p class="text-xs text-muted" data-testid="dv-last-NXDN">{lastUsed(resume, 'NXDN', now)}</p>{/if}
        </div>
        {/if}{/if}{/each}
      </div>
      {#if resume}
        <div class="space-y-1 border-t border-line pt-3">
          <label class="inline-flex cursor-pointer items-center gap-2 text-sm"><input type="checkbox" class="size-4 accent-[var(--accent)]" checked={resume.resume} disabled={busy} onchange={(e) => setResume(e.currentTarget.checked)} />Resume last tune after restart</label>
          <p class="text-xs text-muted" data-testid="dv-resume-hint">{resume.resume ? 'After a restart of Analog_Bridge the last tune of the last mode is set again within a few minutes. The startup values above are only a fallback, used when nothing was tuned yet.' : 'Off: a restart goes back to the startup values. Turn it on to continue with the last tune instead.'}</p>
        </div>
      {/if}
    </section>

    <section class="card space-y-3 p-4" aria-label="Services">
      <h2 class="text-base font-semibold">Services</h2>
      <ul class="divide-y divide-line rounded border border-line text-xs">
        {#each units.filter((u) => u.loaded && u.name !== 'netcheck' && u.name !== 'md380-emu') as u}
          <li class="flex flex-wrap items-baseline gap-x-2 px-2 py-1"><span class="font-mono font-semibold">{u.name}</span><span class="{u.active === 'active' ? 'text-ok' : 'text-warn'}">{u.active}</span></li>
        {/each}
      </ul>
      <div class="flex flex-wrap items-center gap-2">
        <label class="sr-only" for="dv-scope">Scope</label>
        <select id="dv-scope" class="input w-auto" bind:value={scope}><option value="dmr">DMR master only (MMDVM_Bridge)</option><option value="bridge">Bridge (Analog_Bridge, MMDVM_Bridge)</option><option value="gateways">Gateways (YSF, P25, NXDN)</option><option value="all">All</option></select>
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

<Confirm bind:open={confirmRestart} title="Restart services?" confirmLabel="Restart" message={`This restarts the ${({ all: 'bridge and the gateways', bridge: 'bridge', dmr: 'DMR master connection (MMDVM_Bridge)', gateways: 'gateways' } as const)[scope]}. ${WARNING}`} onconfirm={() => run('restart')} />
<Confirm bind:open={rbOpen} title="Restore the previous version?" confirmLabel="Restore" message={`${FILE_LABEL[rollbackFile] ?? rollbackFile} goes back to its previous version. Services pick it up on a restart.`} onconfirm={() => rollback(rollbackFile)} />
