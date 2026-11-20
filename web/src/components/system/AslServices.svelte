<script lang="ts">
  import { api } from '../../lib/api';
  import { toast, toastError } from '../../lib/toast.svelte';
  import { LOSSES, allmonStillOpen, offersAllmon, firewallText, listenText } from '../../lib/svcweb';
  import type { SvcStatus, SvcScope } from '../../lib/svcweb';
  import Dialog from '../Dialog.svelte';
  import Refresh from '@lucide/svelte/icons/refresh-cw';
  import TriangleAlert from '@lucide/svelte/icons/triangle-alert';

  let st = $state<SvcStatus | null>(null), err = $state(''), busy = $state(false);
  let ask = $state<{ scope: SvcScope; action: 'enable' | 'disable' } | null>(null), askOpen = $state(false);
  let fw = $state(false), alsoAllmon = $state(true);
  let note = $state('');

  async function load() {
    busy = true;
    try { st = await api.svcStatus(); err = ''; } catch (e) { err = e instanceof Error ? e.message : String(e); } finally { busy = false; }
  }
  $effect(() => { load(); });

  const allmonOpen = $derived(!!st && allmonStillOpen(st.scopes));
  const allmonOffer = $derived(!!st && !!ask && offersAllmon(st.scopes, ask.scope.scope, ask.action));

  function start(scope: SvcScope, action: 'enable' | 'disable') {
    ask = { scope, action };
    fw = false; alsoAllmon = true;
    askOpen = true;
  }

  async function run() {
    if (!ask) return;
    const { scope, action } = ask;
    askOpen = false;
    busy = true; note = '';
    try {
      let res = await api.svcSet(scope.scope, action, fw);
      const parts = [`${scope.label}: ${action === 'disable' ? 'turned off' : 'turned on'}`];
      if (res.firewallChanged.length) parts.push(`firewall ${action === 'disable' ? 'closed' : 'reopened'}: ${res.firewallChanged.join(', ')}`);
      if (res.firewallError) parts.push(`firewall: ${res.firewallError}`);
      if (allmonOffer && alsoAllmon) {
        const allmon = st?.scopes.find((s) => s.scope === 'allmon3');
        if (allmon) {
          res = await api.svcSet('allmon3', 'disable', false);
          parts.push('Allmon3: turned off');
        }
      }
      st = res;
      note = parts.join('. ') + '.';
      toast(note, 'info');
    } catch (e) { toastError(e); await load(); } finally { busy = false; }
  }

  function stateText(s: SvcScope): string {
    if (!s.installed) return 'not installed';
    return s.active ? 'running' : 'off';
  }
</script>

<p class="text-sm text-muted">ASLCP covers much of what these provide in one place. Turn them off if you do not use them. You can turn them back on here at any time. Nothing is uninstalled.</p>

{#if err}
  <p class="text-sm text-danger" role="alert">{err}</p>
{:else if !st}
  <p class="text-sm text-muted" role="status">Loading…</p>
{:else if !st.available}
  <p class="text-sm text-muted">Not available: systemctl is missing on this system.</p>
{:else}
  <div class="flex items-center justify-between">
    <h3 class="text-base font-semibold">Services</h3>
    <button class="btn btn-sm" disabled={busy} onclick={load}><Refresh size={13} />Refresh</button>
  </div>
  {#if allmonOpen}
    <div class="flex items-start gap-2 rounded border border-warn/50 bg-warn/10 p-3 text-sm" role="note">
      <TriangleAlert size={16} class="mt-0.5 shrink-0 text-warn" />
      <span>Apache is off, but Allmon3 still answers directly on ports 16080 and 16700+ on all addresses.</span>
    </div>
  {/if}
  <div class="grid grid-cols-1 gap-3 lg:grid-cols-3">
    {#each st.scopes as s (s.scope)}
      <section class="card space-y-2 p-4" aria-label={s.label}>
        <div class="flex items-center justify-between gap-2">
          <h3 class="text-base font-semibold">{s.label}</h3>
          <span class="badge {s.active ? 'text-ok' : ''}">{stateText(s)}</span>
        </div>
        <p class="font-mono text-xs text-muted">{s.units.map((u) => u.unit).join(', ') || '—'}</p>
        {#if s.recorded}<p class="text-xs text-muted">Turned off by ASLCP. Removing the ASLCP package turns it back on.</p>{/if}
        {#if !s.installed}
          <p class="text-sm text-muted">Not installed on this node.</p>
        {:else if s.active}
          <button class="btn btn-danger" disabled={busy} onclick={() => start(s, 'disable')}>Turn off</button>
        {:else if s.scope === 'apache' && st.aslcpOnPort80}
          <p class="text-sm" role="note">ASLCP is using port 80. Move ASLCP to another port first.</p>
          <a class="btn btn-sm" href="#/system/web-port">Go to Web port</a>
        {:else}
          <button class="btn btn-primary" disabled={busy} onclick={() => start(s, 'enable')}>Turn on</button>
        {/if}
      </section>
    {/each}
  </div>
  {#if note}<p class="text-sm" role="status">{note}</p>{/if}

  <section class="card overflow-x-auto" aria-label="Listening ports">
    <table class="tbl">
      <thead><tr><th>Port</th><th>Service</th><th>State</th></tr></thead>
      <tbody>
        {#if !st.portsAvailable}
          <tr><td colspan="3" class="text-muted">Not available: the ss tool is missing on this system.</td></tr>
        {:else}
          {#each st.ports as p (p.label)}
            <tr>
              <td class="font-mono">{p.label}</td>
              <td>{st.scopes.find((s) => s.scope === p.scope)?.label ?? p.scope}</td>
              <td><span class="badge {p.listening ? 'text-warn' : 'text-ok'}">{listenText(p)}</span></td>
            </tr>
          {/each}
        {/if}
      </tbody>
    </table>
  </section>
  <p class="text-xs text-muted">Read from the node's listening sockets. A closed port shows that the service really is off.{#if st.aslcpPort} ASLCP itself listens on port {st.aslcpPort}.{/if}</p>
{/if}

<Dialog bind:open={askOpen} title={ask ? `${ask.action === 'disable' ? 'Turn off' : 'Turn on'} ${ask.scope.label}?` : ''}>
  {#if ask}
    <div class="space-y-3 text-sm">
      {#if ask.action === 'disable'}
        <div class="flex items-start gap-2 rounded border border-warn/50 bg-warn/10 p-3" role="note">
          <TriangleAlert size={16} class="mt-0.5 shrink-0 text-warn" />
          <span>{LOSSES[ask.scope.scope]}</span>
        </div>
        <p>{ask.scope.label} is stopped now and does not start at boot. It stays installed.</p>
      {:else}
        <p>{ask.scope.label} is enabled and started now.</p>
      {/if}
      {#if st?.firewalld && firewallText(ask.scope, ask.action)}
        <label class="flex items-center gap-2"><input type="checkbox" bind:checked={fw} />{firewallText(ask.scope, ask.action)}</label>
      {/if}
      {#if allmonOffer}
        <label class="flex items-center gap-2"><input type="checkbox" bind:checked={alsoAllmon} />Also turn Allmon3 off (it answers on ports 16080 and 16700+ without Apache)</label>
      {/if}
    </div>
  {/if}
  {#snippet footer()}
    <button class="btn" onclick={() => (askOpen = false)}>Cancel</button>
    <button class="btn {ask?.action === 'disable' ? 'btn-danger' : 'btn-primary'}" onclick={run}>{ask?.action === 'disable' ? 'Turn off' : 'Turn on'}</button>
  {/snippet}
</Dialog>
