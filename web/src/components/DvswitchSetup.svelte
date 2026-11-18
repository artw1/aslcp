<script lang="ts">
  // Credentials and config of a node with DVSwitch installed (docs/DVSWITCH.md, M4).
  // The DMR password is write-only: the field is never prefilled, the server only says whether one is set.
  import { onMount } from 'svelte';
  import { api, ApiError } from '../lib/api';
  import { toast, toastError } from '../lib/toast.svelte';
  import type { DVDMRStatus, DVListItem, DVListMode, DVRestartScope, DVSwitchSetup, DVSwitchStatus } from '../lib/types';
  import { badge, bridgeInUse, IDLE_POLL_MS, outcomeFor, waitForLogin, type Outcome } from '../lib/dmrlogin';
  import { findByValue, indexList, optionLabel, searchList } from '../lib/dvlist';
  import Confirm from './Confirm.svelte';
  import NodePicker from './NodePicker.svelte';

  let { units, onunits }: { units: DVSwitchStatus['units']; onunits: (u: DVSwitchStatus['units']) => void } = $props();

  const FILE_LABEL: Record<string, string> = { analog_bridge: 'Analog_Bridge.ini', mmdvm_bridge: 'MMDVM_Bridge.ini', ysfgateway: 'YSFGateway.ini', p25gateway: 'P25Gateway.ini', nxdngateway: 'NXDNGateway.ini' };
  const WARNING = 'A restart resets the running mode and talkgroup to the startup values.';

  let v = $state<DVSwitchSetup | null>(null), err = $state(''), busy = $state(false);
  let hint = $state<string[]>([]), notes = $state<string[]>([]);
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
  let modes = $state({ dmr: false, ysf: false, p25: false, nxdn: false });
  let st = $state({ ambeMode: 'DMR', txTg: '', txTs: '2', colorCode: '1', ysf: '', p25: '', nxdn: '' });
  let scope = $state<DVRestartScope>('bridge');
  let confirmRestart = $state(false), rbOpen = $state(false), rollbackFile = $state('');

  function fill(s: DVSwitchSetup) {
    v = s;
    id = { callsign: s.identity.callsign, dmrId: s.identity.dmrId, essid: s.identity.essid, nxdnId: s.identity.nxdnId ?? '' };
    net = { address: s.dmr.address, port: s.dmr.port ? String(s.dmr.port) : '', password: '' };
    modes = { ...s.modes };
    st = { ambeMode: s.startup.ambeMode || 'DMR', txTg: s.startup.txTg, txTs: String(s.startup.txTs || 2), colorCode: String(s.startup.colorCode), ysf: s.gateways.ysf.startup, p25: s.gateways.p25.startup, nxdn: s.gateways.nxdn.startup };
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
    load(); loadLists(); pollOnce();
    const t = setInterval(() => { if (!document.hidden) pollOnce(); }, IDLE_POLL_MS);
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

  async function save(what: string, call: () => Promise<DVSwitchSetup>, check = false) {
    busy = true;
    try {
      const s = await call();
      fill(s);
      hint = s.restartHint ?? [];
      notes = s.applyWarnings ?? [];
      const changed = !!(s.changed && s.changed.length);
      toast(changed ? `${what} saved.` : `${what}: nothing to change.`, 'info');
      if (check) { if (changed) { busy = false; await verifyLogin(); } else pollOnce(); }
    } catch (e) {
      if (e instanceof ApiError && e.code === 'conflict') { toast('A file changed on disk. The values were reloaded; check them and save again.', 'error'); await load(); }
      else toastError(e);
    } finally { busy = false; }
  }
  const saveIdentity = () => save('Identity', () => api.dvswitchIdentity({ ...id, nxdnId: id.nxdnId.trim() || undefined, base: base() }), true);
  function saveNet() {
    const b: { dmrAddress?: string; dmrPort?: number; dmrPassword?: string; base: Record<string, string> } = { base: base() };
    if (net.address !== v?.dmr.address) b.dmrAddress = net.address;
    if (Number(net.port) !== v?.dmr.port) b.dmrPort = Number(net.port);
    if (net.password) b.dmrPassword = net.password;
    return save('DMR master', async () => { try { return await api.dvswitchCredentials(b); } finally { net.password = ''; } }, true);
  }
  const saveModes = () => save('Modes', () => api.dvswitchModes({ ...modes, base: base() }));
  const saveStartup = () => save('Startup values', () => api.dvswitchStartup({
    ambeMode: st.ambeMode, txTg: st.txTg, txTs: Number(st.txTs), colorCode: Number(st.colorCode),
    ...(v?.gateways.ysf.present ? { ysf: st.ysf } : {}), ...(v?.gateways.p25.present ? { p25: st.p25 } : {}), ...(v?.gateways.nxdn.present ? { nxdn: st.nxdn } : {}), base: base(),
  }));
  const useCurrent = () => save('Startup values', () => api.dvswitchStartup({ useCurrent: true, base: base() }));
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
  {#each notes as n}<p class="rounded border border-warn/60 p-2 text-sm text-warn" role="status">{n}</p>{/each}
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
          <label class="label" for="dv-pw">DMR password <span class="text-muted">({v.dmr.passwordSet ? 'set' : 'not set'})</span></label>
          <input id="dv-pw" class="input font-mono" type="password" autocomplete="new-password" bind:value={net.password} placeholder={v.dmr.passwordSet ? 'Leave empty to keep the password' : 'Enter the password'} maxlength="64" />
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

      <h2 class="border-t border-line pt-3 text-base font-semibold">Startup values</h2>
      <p class="text-xs text-muted">What Analog_Bridge and the gateways start with. A restart goes back to these.</p>
      <form class="grid grid-cols-2 gap-2 sm:grid-cols-4" onsubmit={(e) => { e.preventDefault(); saveStartup(); }}>
        <div><label class="label" for="dv-mode">Mode</label><select id="dv-mode" class="input" bind:value={st.ambeMode}>{#each ['DMR', 'YSFN', 'YSFW', 'P25', 'NXDN'] as m}<option value={m}>{m}</option>{/each}</select></div>
        <div><label class="label" for="dv-tg">Talkgroup</label><input id="dv-tg" class="input font-mono" bind:value={st.txTg} inputmode="numeric" maxlength="8" /></div>
        <div><label class="label" for="dv-ts">Slot</label><select id="dv-ts" class="input" bind:value={st.txTs}><option value="1">1</option><option value="2">2</option></select></div>
        <div><label class="label" for="dv-cc">Color code</label><input id="dv-cc" class="input font-mono" bind:value={st.colorCode} inputmode="numeric" maxlength="2" /></div>
        {#if v.gateways.ysf.present}<div class="col-span-2"><label class="label" for="dv-ysf">YSF reflector</label><NodePicker id="dv-ysf" bind:value={st.ysf} options={lists.ysf} search={find('ysf')} label={label('ysf')} placeholder="Search or type an ID" inputmode="text" maxlength={32} describedby="dv-ysf-hint" /><p id="dv-ysf-hint" class="mt-1 text-xs text-muted">{#if hit('ysf', st.ysf)}{hit('ysf', st.ysf)?.name}, {hit('ysf', st.ysf)?.address}:{hit('ysf', st.ysf)?.port}{:else}reflector ID (5 digits, e.g. 12345) or name exactly as in YSFHosts.txt{/if}</p></div>{/if}
        {#if v.gateways.p25.present}<div class="col-span-2"><label class="label" for="dv-p25">P25 reflector</label><NodePicker id="dv-p25" bind:value={st.p25} options={lists.p25} search={find('p25')} label={label('p25')} placeholder="Search or type a TG" inputmode="numeric" maxlength={5} describedby="dv-p25-hint" /><p id="dv-p25-hint" class="mt-1 text-xs text-muted">{#if hit('p25', st.p25)}{hit('p25', st.p25)?.address}:{hit('p25', st.p25)?.port}{:else}talkgroup number, up to 5 digits{/if}</p></div>{/if}
        {#if v.gateways.nxdn.present}<div class="col-span-2"><label class="label" for="dv-nxdn">NXDN reflector</label><NodePicker id="dv-nxdn" bind:value={st.nxdn} options={lists.nxdn} search={find('nxdn')} label={label('nxdn')} placeholder="Search or type a TG" inputmode="numeric" maxlength={5} describedby="dv-nxdn-hint" /><p id="dv-nxdn-hint" class="mt-1 text-xs text-muted">{#if hit('nxdn', st.nxdn)}{hit('nxdn', st.nxdn)?.address}:{hit('nxdn', st.nxdn)?.port}{:else}talkgroup number, up to 5 digits{/if}</p></div>{/if}
        <div class="col-span-2 flex flex-wrap items-center gap-2 sm:col-span-4">
          <button class="btn btn-primary" disabled={busy}>Save startup values</button>
          <button type="button" class="btn" disabled={busy || !v.current.found} onclick={useCurrent} title="Take mode, talkgroup, slot and color code from the running bridge">Use current</button>
          {#if v.current.found}<span class="text-xs text-muted">Now: {v.current.mode} TG {v.current.tg} TS {v.current.slot} CC {v.current.colorCode}</span>{:else}<span class="text-xs text-muted">The bridge is not running, so there is no current state.</span>{/if}
        </div>
      </form>
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
