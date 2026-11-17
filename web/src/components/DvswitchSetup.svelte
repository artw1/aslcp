<script lang="ts">
  // Credentials and config of a node with DVSwitch installed (docs/DVSWITCH.md, M4).
  // The DMR password is write-only: the field is never prefilled, the server only says whether one is set.
  import { onMount } from 'svelte';
  import { api, ApiError } from '../lib/api';
  import { toast, toastError } from '../lib/toast.svelte';
  import type { DVSwitchSetup, DVSwitchStatus } from '../lib/types';
  import Confirm from './Confirm.svelte';

  let { units, onunits }: { units: DVSwitchStatus['units']; onunits: (u: DVSwitchStatus['units']) => void } = $props();

  const FILE_LABEL: Record<string, string> = { analog_bridge: 'Analog_Bridge.ini', mmdvm_bridge: 'MMDVM_Bridge.ini', ysfgateway: 'YSFGateway.ini', p25gateway: 'P25Gateway.ini', nxdngateway: 'NXDNGateway.ini' };
  const WARNING = 'A restart resets the running mode and talkgroup to the startup values.';

  let v = $state<DVSwitchSetup | null>(null), err = $state(''), busy = $state(false);
  let hint = $state<string[]>([]);
  let id = $state({ callsign: '', dmrId: '', essid: '', nxdnId: '' });
  let net = $state({ address: '', port: '', password: '' });
  let modes = $state({ dmr: false, ysf: false, p25: false, nxdn: false });
  let st = $state({ ambeMode: 'DMR', txTg: '', txTs: '2', colorCode: '1', ysf: '', p25: '', nxdn: '' });
  let scope = $state<'bridge' | 'gateways' | 'all'>('bridge');
  let confirmRestart = $state(false), rbOpen = $state(false), rollbackFile = $state('');

  function fill(s: DVSwitchSetup) {
    v = s;
    id = { callsign: s.identity.callsign, dmrId: s.identity.dmrId, essid: s.identity.essid, nxdnId: s.identity.nxdnId ?? '' };
    net = { address: s.dmr.address, port: s.dmr.port ? String(s.dmr.port) : '', password: '' };
    modes = { ...s.modes };
    st = { ambeMode: s.startup.ambeMode || 'DMR', txTg: s.startup.txTg, txTs: String(s.startup.txTs || 2), colorCode: String(s.startup.colorCode), ysf: s.gateways.ysf.startup, p25: s.gateways.p25.startup, nxdn: s.gateways.nxdn.startup };
  }
  async function load() { try { fill(await api.dvswitchSetup()); err = ''; } catch (e) { err = e instanceof Error ? e.message : String(e); } }
  onMount(load);

  const base = () => Object.fromEntries((v?.files ?? []).filter((f) => f.sha256).map((f) => [f.id, f.sha256]));

  async function save(what: string, call: () => Promise<DVSwitchSetup>) {
    busy = true;
    try {
      const s = await call();
      fill(s);
      hint = s.restartHint ?? [];
      toast(s.changed && s.changed.length ? `${what} saved.` : `${what}: nothing to change.`, 'info');
    } catch (e) {
      if (e instanceof ApiError && e.code === 'conflict') { toast('A file changed on disk. The values were reloaded; check them and save again.', 'error'); await load(); }
      else toastError(e);
    } finally { busy = false; }
  }
  const saveIdentity = () => save('Identity', () => api.dvswitchIdentity({ ...id, nxdnId: id.nxdnId.trim() || undefined, base: base() }));
  function saveNet() {
    const b: { dmrAddress?: string; dmrPort?: number; dmrPassword?: string; base: Record<string, string> } = { base: base() };
    if (net.address !== v?.dmr.address) b.dmrAddress = net.address;
    if (Number(net.port) !== v?.dmr.port) b.dmrPort = Number(net.port);
    if (net.password) b.dmrPassword = net.password;
    return save('DMR master', async () => { try { return await api.dvswitchCredentials(b); } finally { net.password = ''; } });
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
        <div class="sm:col-span-2"><label class="label" for="dv-addr">DMR master address</label><input id="dv-addr" class="input font-mono" bind:value={net.address} placeholder="master.example.invalid" /></div>
        <div><label class="label" for="dv-port">Port</label><input id="dv-port" class="input font-mono" bind:value={net.port} inputmode="numeric" maxlength="5" /></div>
        <div class="sm:col-span-3">
          <label class="label" for="dv-pw">DMR password <span class="text-muted">({v.dmr.passwordSet ? 'set' : 'not set'})</span></label>
          <input id="dv-pw" class="input font-mono" type="password" autocomplete="new-password" bind:value={net.password} placeholder={v.dmr.passwordSet ? 'Leave empty to keep the password' : 'Enter the password'} maxlength="64" />
          <p class="mt-1 text-xs text-muted">Write-only: it is never shown again. Printable ASCII, up to 64 characters, without quotes, # or ;.</p>
        </div>
        <div class="sm:col-span-3"><button class="btn btn-primary" disabled={busy}>Save DMR master</button></div>
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
        {#if v.gateways.ysf.present}<div class="col-span-2"><label class="label" for="dv-ysf">YSF reflector</label><input id="dv-ysf" class="input font-mono" bind:value={st.ysf} maxlength="32" aria-describedby="dv-ysf-hint" /><p id="dv-ysf-hint" class="mt-1 text-xs text-muted">reflector ID (5 digits, e.g. 12345) or name exactly as in YSFHosts.txt</p></div>{/if}
        {#if v.gateways.p25.present}<div><label class="label" for="dv-p25">P25 reflector</label><input id="dv-p25" class="input font-mono" bind:value={st.p25} inputmode="numeric" maxlength="5" /></div>{/if}
        {#if v.gateways.nxdn.present}<div><label class="label" for="dv-nxdn">NXDN reflector</label><input id="dv-nxdn" class="input font-mono" bind:value={st.nxdn} inputmode="numeric" maxlength="5" /></div>{/if}
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
        <select id="dv-scope" class="input w-auto" bind:value={scope}><option value="bridge">Bridge (Analog_Bridge, MMDVM_Bridge)</option><option value="gateways">Gateways (YSF, P25, NXDN)</option><option value="all">All</option></select>
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

<Confirm bind:open={confirmRestart} title="Restart services?" confirmLabel="Restart" message={`This restarts the ${scope === 'all' ? 'bridge and the gateways' : scope === 'bridge' ? 'bridge' : 'gateways'}. ${WARNING}`} onconfirm={() => run('restart')} />
<Confirm bind:open={rbOpen} title="Restore the previous version?" confirmLabel="Restore" message={`${FILE_LABEL[rollbackFile] ?? rollbackFile} goes back to its previous version. Services pick it up on a restart.`} onconfirm={() => rollback(rollbackFile)} />
