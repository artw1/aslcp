<script lang="ts">
  import { api } from '../../lib/api';
  import { advancedLink, amiSummary, echolinkSummary, fieldById, keyById, radioSummary, registrationSummary, summaryLine, valueLabel, type Finding, type Overview, type NodeModel, type SchemaDoc } from '../../lib/nodecfg';
  import NodeCard from './NodeCard.svelte';
  import IdentityForm from './IdentityForm.svelte';
  import DuplexForm from './DuplexForm.svelte';
  import RegistrationForm from './RegistrationForm.svelte';
  import EchoLinkForm from './EchoLinkForm.svelte';
  import SetupWizard from './SetupWizard.svelte';
  import AddNodeWizard from './AddNodeWizard.svelte';
  import RenumberWizard from './RenumberWizard.svelte';
  import RadioWizard from './RadioWizard.svelte';
  import RadioForm from './RadioForm.svelte';
  import LinkingForm from './LinkingForm.svelte';
  import AmiForm from './AmiForm.svelte';
  import TriangleAlert from '@lucide/svelte/icons/triangle-alert';

  let ov = $state<Overview | null>(null), doc = $state<SchemaDoc | null>(null), err = $state('');
  let sel = $state(read());
  let wizard = $state(false), skipped = $state(false), skipErr = $state('');
  let form = $state<'identity' | 'duplex' | 'registration' | 'echolink' | 'linking' | 'ami' | 'device' | null>(null);
  let wiz = $state<'add' | 'renumber' | 'radio' | null>(null);
  function read() { try { return localStorage.getItem('aslcp.cfg.node') ?? ''; } catch { return ''; } }
  function pick(n: string) { sel = n; try { localStorage.setItem('aslcp.cfg.node', n); } catch { /* private window */ } }

  async function load() {
    try {
      const [o, d] = await Promise.all([api.nodeCfgOverview(), doc ? Promise.resolve(doc) : api.nodeCfgSchema()]);
      ov = o; doc = d; err = '';
      api.nodeSetupSkipped().then((r) => (skipped = r.skipped), () => {});
      // Registration status asks Asterisk, so it comes in after the page is up.
      api.nodeCfgOverview(true).then((s) => { if (ov) for (const n of ov.nodes) { const m = s.nodes.find((x) => x.node === n.node); if (m?.registration.status) n.registration.status = m.registration.status; } }, () => {});
    } catch (e) { err = e instanceof Error ? e.message : String(e); }
  }
  $effect(() => { void load(); });

  const node = $derived<NodeModel | undefined>(ov?.nodes.find((n) => n.node === sel) ?? ov?.nodes[0]);
  const blocked = $derived(node?.refusals?.[0] ?? null);
  const why = $derived(blocked ? 'Forms are off for this node (see above)' : '');
  const stanzaLine = $derived(node?.stanza?.line);

  async function setSkipped(v: boolean) {
    skipErr = '';
    try { skipped = (await api.nodeSetupSetSkipped(v)).skipped; } catch (e) { skipErr = e instanceof Error ? e.message : String(e); }
  }

  function fmt(n: NodeModel, ids: string[]) { return summaryLine(doc, n.fields, ids); }
  function identity(n: NodeModel) {
    const every = fieldById(n.fields, 'rpt.conf:node:idtime');
    const k = keyById(doc, 'rpt.conf:node:idtime');
    return `${n.callsign ? `Callsign ${n.callsign}` : 'No callsign set yet'}${every ? `, identifies every ${valueLabel(k, every.value)}` : ''}`;
  }
  function duplex(n: NodeModel) {
    const d = fieldById(n.fields, 'rpt.conf:node:duplex'), h = fieldById(n.fields, 'rpt.conf:node:hangtime'), t = fieldById(n.fields, 'rpt.conf:node:totime');
    const parts = [d && valueLabel(keyById(doc, d.id), d.value), h && `hang time ${valueLabel(keyById(doc, h.id), h.value)}`, t && `time-out ${valueLabel(keyById(doc, t.id), t.value)}`];
    return parts.filter(Boolean).join(' · ');
  }
  const flagged = (list: Finding[]) => list.filter((f) => f.level !== 'info');
  const macroKeys = (n: NodeModel) => n.other.filter((o) => ['functions', 'macro', 'phone_functions', 'link_functions'].includes(o.key.toLowerCase())).map((o) => `${o.key}: ${o.value}`).join(' · ');
</script>

{#if err}
  <p class="card p-3 text-sm text-danger" role="alert">{err} <button class="btn btn-sm ml-2" onclick={load}>Retry</button></p>
{:else if !ov || !doc}
  <p class="text-sm text-muted" role="status">Loading…</p>
{:else if ov.refusal}
  <div class="card p-3 text-sm" role="alert">
    <p class="font-medium">Forms cannot read this configuration.</p>
    <p class="mt-1">{ov.refusal.message}</p>
    {#if ov.refusal.file}<p class="mt-2"><a class="btn btn-sm" href={'#' + advancedLink(ov.refusal.file, ov.refusal.line)}>Edit {ov.refusal.file} in Advanced</a></p>{/if}
  </div>
{:else if !node}
  <p class="card p-3 text-sm">rpt.conf has no node section yet. <a class="underline" href={'#' + advancedLink('rpt.conf')}>Open rpt.conf in Advanced</a>.</p>
{:else}
  <div class="mb-2 flex flex-wrap items-center gap-1.5">
    {#if ov.nodes.length > 1}
      <div class="flex flex-wrap items-center gap-1.5" role="group" aria-label="Node">
        {#each ov.nodes as n (n.node)}
          <button type="button" class="btn btn-sm {n.node === node.node ? 'btn-primary' : ''}" aria-pressed={n.node === node.node} onclick={() => pick(n.node)}>Node {n.node}{#if n.callsign}<span class="font-normal opacity-80">&nbsp;{n.callsign}</span>{/if}</button>
        {/each}
      </div>
    {/if}
    <button type="button" class="btn btn-sm" onclick={() => (wiz = 'add')} title="Add a second node to this system">Add node</button>
  </div>

  {#if node.placeholder && !skipped}
    <section class="mb-2 rounded border border-accent/50 bg-surface-2 p-3 text-sm" aria-label="Set up this node">
      <p class="font-medium">This looks like a fresh install: the sample node {node.node} has no callsign yet.</p>
      <p class="mt-1 text-muted">The setup wizard asks for your node number, password and callsign, picks a radio interface and a duplex preset, registers the node and checks that everything works. You review every change before it is saved, and you can roll it back.</p>
      <div class="mt-2 flex flex-wrap items-center gap-2">
        <button class="btn btn-primary" onclick={() => (wizard = true)}>Set up this node</button>
        <button class="btn" onclick={() => setSkipped(true)}>Skip for now</button>
        <span class="text-xs text-muted">Or fill in the cards below, or edit the files in Advanced.</span>
      </div>
      {#if skipErr}<p class="mt-1 text-xs text-danger" role="alert">{skipErr}</p>{/if}
    </section>
  {:else}
    {#if node.placeholder}
      <p class="mb-2 rounded border border-line bg-surface-2 p-2 text-sm" role="status">This looks like a fresh install: the sample node {node.node} has no callsign yet. Fill in Identity and Registration below, or edit the files in Advanced.</p>
    {/if}
    {#if !blocked}
      <p class="mb-2 text-xs text-muted">Starting over on a new node? <button class="underline" onclick={() => (wizard = true)}>Run the setup wizard</button>{#if skipped && node.placeholder}{' '}(you skipped it earlier){/if}.</p>
    {/if}
  {/if}
  {#if blocked}
    <div class="mb-2 rounded border border-warn/50 bg-warn/10 p-2 text-sm" role="alert">
      <p class="flex items-start gap-1.5"><TriangleAlert size={16} class="mt-0.5 shrink-0 text-warn" /><span><span class="font-medium">Forms do not edit this node.</span> {blocked.message}</span></p>
      {#if blocked.file}<p class="mt-1"><a class="btn btn-sm" href={'#' + advancedLink(blocked.file, blocked.line)}>Edit {blocked.file} in Advanced</a></p>{/if}
    </div>
  {/if}
  {#each flagged([...ov.warnings, ...node.warnings]) as w}
    <p class="mb-1 flex items-start gap-1.5 text-sm" role={w.level === 'error' ? 'alert' : 'status'}><TriangleAlert size={15} class="mt-0.5 shrink-0 {w.level === 'error' ? 'text-danger' : 'text-warn'}" /><span>{w.message}{#if w.file}{' '}<a class="underline" href={'#' + advancedLink(w.file)}>Open {w.file}</a>{/if}</span></p>
  {/each}

  <div class="mt-2 grid gap-2 md:grid-cols-2">
    <NodeCard title="Identity" summary={identity(node)} onedit={() => (form = 'identity')} disabledReason={why} advanced="rpt.conf" advancedLine={stanzaLine}>
      <p class="text-xs text-muted">Node number {node.node}. <button type="button" class="underline disabled:no-underline disabled:opacity-60" disabled={!!why} title={why} onclick={() => (wiz = 'renumber')}>Change node number</button></p>
    </NodeCard>
    <NodeCard title="Radio" summary={radioSummary(node.radio)} onedit={node.radio.supported && node.radio.type === 'simpleusb' && node.radio.deviceStanza ? () => (form = 'device') : undefined} disabledReason={why} advanced={node.radio.supported ? (node.radio.deviceFile || 'rpt.conf') : (node.radio.advancedFile || 'rpt.conf')}>
      {#if !node.radio.supported}
        <p class="text-xs text-warn">{node.radio.note ?? 'This interface is edited in Advanced; forms and wizards do not support it.'}</p>
      {:else}
        {#if node.radio.type === 'simpleusb' && !node.radio.deviceStanza}
          <p class="text-xs text-warn">The device section for this radio is missing. Change radio interface to SimpleUSB creates it.</p>
        {:else}
          <p class="text-xs text-muted">Levels are tuned under <a class="underline" href="#/tune">Audio Tuning</a>.</p>
        {/if}
        <p class="text-xs"><button type="button" class="underline disabled:no-underline disabled:opacity-60" disabled={!!why} title={why} onclick={() => (wiz = 'radio')}>Change radio interface</button></p>
      {/if}
    </NodeCard>
    <NodeCard title="Duplex and timers" summary={duplex(node)} onedit={() => (form = 'duplex')} disabledReason={why} advanced="rpt.conf" advancedLine={stanzaLine} />
    <NodeCard title="Registration" summary={registrationSummary(node.registration)} onedit={() => (form = 'registration')} disabledReason={why} advanced={node.registration.http?.file ?? node.registration.httpFile ?? 'rpt_http_registrations.conf'}>
      {#if node.registration.status?.state === 'failed'}<p class="text-xs text-danger">Registration is failing{node.registration.status.detail ? `: ${node.registration.status.detail}` : ''}</p>{/if}
    </NodeCard>
    <NodeCard title="EchoLink" summary={echolinkSummary(node.echolink)} onedit={() => (form = 'echolink')} disabledReason={why} advanced="echolink.conf" />
    <NodeCard title="Linking and telemetry" summary={fmt(node, ['rpt.conf:node:context', 'rpt.conf:node:linkmongain', 'rpt.conf:node:erxgain', 'rpt.conf:node:etxgain', 'rpt.conf:node:startup_macro', 'rpt.conf:node:telemwait', 'rpt.conf:node:telemnomdb', 'rpt.conf:node:telemduckdb', 'rpt.conf:node:parrottime']) || 'Default settings'} onedit={() => (form = 'linking')} disabledReason={why} advanced="rpt.conf" advancedLine={stanzaLine} />
    <NodeCard title="Manager interface (AMI)" summary={amiSummary(ov.ami)} onedit={ov.ami?.present ? () => (form = 'ami') : undefined} advanced="manager.conf">
      {#if ov.ami?.present && !ov.ami.loopback}<p class="flex items-start gap-1.5 text-xs text-warn"><TriangleAlert size={14} class="mt-0.5 shrink-0" /><span>Reachable from other computers.</span></p>{/if}
    </NodeCard>
    <NodeCard title="Access lists" summary="Allow and deny lists for connecting nodes are kept in the Asterisk database, so there is no file to edit here yet." />
    <NodeCard title="Status reporting" summary={fmt(node, ['rpt.conf:node:statpost_url', 'rpt.conf:node:statpost_time']) || 'Not set up'} advanced="rpt.conf" advancedLine={stanzaLine} />
    <NodeCard title="DTMF commands and macros" summary={macroKeys(node) || 'Command and macro sections are defined in rpt.conf.'} advanced="rpt.conf" />
  </div>

  {#if node.other.length}
    <details class="card mt-2 p-3">
      <summary class="cursor-pointer text-sm font-semibold">Other settings on this node ({node.other.length})</summary>
      <p class="mt-1 text-xs text-muted">Shown as they are, never changed by forms. Edit them in <a class="underline" href={'#' + advancedLink('rpt.conf', stanzaLine)}>Advanced</a>.</p>
      <dl class="mt-2 grid gap-x-3 gap-y-0.5 text-xs sm:grid-cols-[minmax(8rem,auto)_1fr]">
        {#each node.other as o}<dt class="font-mono font-semibold">{o.key}</dt><dd class="min-w-0 break-words font-mono">{o.masked ? 'hidden' : o.value}{#if o.source.kind === 'template'}<span class="text-muted"> (from {o.source.section})</span>{/if}</dd>{/each}
      </dl>
    </details>
  {/if}

  {#if wizard && doc}<SetupWizard oldNode={node.node} presets={doc.presets} onclose={() => { wizard = false; }} ondone={load} />{/if}
  {#if wiz === 'add'}<AddNodeWizard nodes={ov.nodes} from={node.node} presets={doc.presets} onclose={() => (wiz = null)} ondone={load} />
  {:else if wiz === 'renumber'}<RenumberWizard {node} nodes={ov.nodes} onclose={() => (wiz = null)} ondone={load} />
  {:else if wiz === 'radio'}<RadioWizard {node} onclose={() => (wiz = null)} ondone={load} />{/if}
  {#if form === 'identity'}<IdentityForm {node} {doc} onclose={() => (form = null)} ondone={load} />
  {:else if form === 'duplex'}<DuplexForm {node} {doc} onclose={() => (form = null)} ondone={load} />
  {:else if form === 'registration'}<RegistrationForm {node} onclose={() => (form = null)} ondone={load} />
  {:else if form === 'echolink'}<EchoLinkForm {node} {doc} onclose={() => (form = null)} ondone={load} />
  {:else if form === 'linking'}<LinkingForm {node} {doc} onclose={() => (form = null)} ondone={load} />
  {:else if form === 'device'}<RadioForm {node} {doc} onclose={() => (form = null)} ondone={load} />
  {:else if form === 'ami' && ov.ami}<AmiForm node={node.node} ami={ov.ami} {doc} onclose={() => (form = null)} ondone={load} />{/if}
{/if}
