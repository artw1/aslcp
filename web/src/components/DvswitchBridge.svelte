<script lang="ts">
  // The "Bridge node" section of the DVSwitch setup (docs/DVSWITCH.md "Private bridge node"), admin only:
  // the private node that carries the USRP channel to Analog_Bridge, with a plan preview before anything is written.
  import { api, ApiError } from '../lib/api';
  import { toast, toastError } from '../lib/toast.svelte';
  import type { DVApplyResult, DVBridgeSetup, DVPlan, DVLinkMode } from '../lib/types';
  import { formError, nodeError, nodeWarning, planConfirm, prefill, pausedHint } from '../lib/dvbridge';
  import Dialog from './Dialog.svelte';

  let { b, base, onsaved }: { b: DVBridgeSetup; base: Record<string, string>; onsaved: () => void | Promise<void> } = $props();

  let f = $state({ node: '', rxPort: '', txPort: '', linkMode: 'ondemand' as DVLinkMode, localNode: '' });
  let seeded = '';
  // Fill the form from the server's view once, and again after a save changed it.
  $effect(() => {
    const key = JSON.stringify([b.node, b.rxPort, b.txPort, b.link, b.suggested]);
    if (key === seeded) return;
    seeded = key;
    f = prefill(b);
  });
  const nodes = $derived(b.localNodes.filter((n) => n !== f.node.trim()));
  const err = $derived(formError(f, b));
  const warn = $derived(nodeWarning(f.node));
  const nodeMsg = $derived(f.node ? nodeError(f.node, b.used, b.node) : null);

  let busy = $state(false), plan = $state<DVPlan | null>(null), open = $state(false), problem = $state('');
  let result = $state<DVApplyResult | null>(null), failed = $state<{ message: string; step: string; rollback: boolean } | null>(null);
  const body = () => ({ node: f.node.trim(), rxPort: Number(f.rxPort), txPort: Number(f.txPort), linkMode: f.linkMode, localNode: f.localNode, base });

  async function preview() {
    if (err || busy) return;
    busy = true; problem = ''; result = null; failed = null;
    try { plan = await api.dvswitchPrivatePlan(body()); open = true; }
    catch (e) { problem = e instanceof Error ? e.message : String(e); }
    finally { busy = false; }
  }
  async function apply() {
    if (!plan) return;
    busy = true;
    try {
      result = await api.dvswitchPrivateApply({ ...body(), base: plan.base, restart: plan.restartAsterisk });
      open = false;
      toast('Bridge node saved.', 'info');
      await onsaved();
    } catch (e) {
      if (e instanceof ApiError && e.code === 'apply_failed') {
        const x = e.body as { step?: string; rollback?: { available?: boolean } } | undefined;
        failed = { message: e.message, step: x?.step ?? '', rollback: !!x?.rollback?.available };
        open = false;
        await onsaved();
      } else if (e instanceof ApiError && e.code === 'conflict') {
        open = false; problem = 'A file changed on disk since the plan was made. Review the plan again.'; await onsaved();
      } else { open = false; problem = e instanceof Error ? e.message : String(e); }
    } finally { busy = false; }
  }
  async function rollback() {
    busy = true;
    try {
      const r = await api.dvswitchPrivateRollback();
      failed = null;
      toast(r.rolledBack ? 'The previous setup was restored.' : `Rolled back with problems: ${r.problems.join('; ')}`, r.rolledBack ? 'info' : 'error');
      await onsaved();
    } catch (e) { toastError(e); } finally { busy = false; }
  }
</script>

<section class="card space-y-3 p-4" aria-label="Bridge node">
  <h2 class="text-base font-semibold">Bridge node</h2>
  <p class="text-xs text-muted">A private node that carries audio between AllStar and Analog_Bridge over USRP. It is never listed publicly. {b.node ? (b.owned ? `Node ${b.node} was added by ASLCP.` : `Node ${b.node} was found in rpt.conf.`) : 'No bridge node yet.'}</p>
  <form class="grid grid-cols-2 gap-2 sm:grid-cols-4" onsubmit={(e) => { e.preventDefault(); void preview(); }}>
    <div class="col-span-2 sm:col-span-1"><label class="label" for="dvb-node">Node number</label>
      <input id="dvb-node" class="input font-mono" bind:value={f.node} inputmode="numeric" maxlength="7" readonly={!!b.node} aria-describedby="dvb-node-hint" />
      <p id="dvb-node-hint" class="mt-1 text-xs {nodeMsg ? 'text-danger' : warn ? 'text-warn' : 'text-muted'}">{nodeMsg ?? warn ?? (b.node ? 'Fixed once added.' : 'The first free number of 1900-1997 is suggested.')}</p></div>
    <div><label class="label" for="dvb-rx">USRP receive port</label><input id="dvb-rx" class="input font-mono" bind:value={f.rxPort} inputmode="numeric" maxlength="5" /></div>
    <div><label class="label" for="dvb-tx">USRP transmit port</label><input id="dvb-tx" class="input font-mono" bind:value={f.txPort} inputmode="numeric" maxlength="5" /></div>
    <div><label class="label" for="dvb-duplex">Duplex</label><input id="dvb-duplex" class="input font-mono" value="0" disabled /></div>
    <fieldset class="col-span-2 sm:col-span-2"><legend class="label">Link mode</legend>
      <label class="mr-4 inline-flex items-center gap-1.5 text-sm"><input type="radio" name="dvb-mode" value="ondemand" bind:group={f.linkMode} />On demand</label>
      <label class="inline-flex items-center gap-1.5 text-sm"><input type="radio" name="dvb-mode" value="permanent" bind:group={f.linkMode} />Permanent</label>
      <p class="mt-1 text-xs text-muted">{f.linkMode === 'permanent' ? 'ASLCP keeps the link up and re-links after a restart. Disconnecting it by hand pauses that until the next save or Asterisk restart.' : 'You link and unlink from the dashboard card.'}</p></fieldset>
    <div class="col-span-2"><label class="label" for="dvb-local">Link to local node</label>
      <select id="dvb-local" class="input" bind:value={f.localNode}><option value="">Choose a node</option>{#each nodes as n}<option value={n}>{n}</option>{/each}</select></div>
    <div class="col-span-2 sm:col-span-4"><button class="btn btn-primary" disabled={busy || !!err}>Review changes</button>
      <span class="ml-2 text-xs {err && f.node ? 'text-warn' : 'text-muted'}">{err ?? 'Shows what would change before anything is written.'}</span></div>
  </form>
  {#if b.link.mode === 'permanent' && b.paused}<p class="text-xs text-warn" role="status">{pausedHint({ mode: 'permanent', localNode: b.link.localNode, linked: false, paused: true })}</p>{/if}
  {#if problem}<p class="rounded border border-danger p-2 text-sm text-danger" role="alert">{problem}</p>{/if}
  {#if failed}
    <div class="space-y-2 rounded border border-danger p-2 text-sm text-danger" role="alert">
      <p>{failed.message}{failed.step ? ` (step: ${failed.step})` : ''}</p>
      {#if failed.rollback}<button type="button" class="btn btn-sm" disabled={busy} onclick={rollback}>Roll back Asterisk and the ini file</button>{/if}
    </div>
  {/if}
  {#if result}
    <ol class="rounded border border-line p-2 text-xs" aria-label="Apply steps">
      {#each result.steps as s}<li class={s.ok ? 'text-ok' : 'text-warn'}>{s.ok ? 'done' : 'skipped'}: {s.name}{s.detail ? `, ${s.detail}` : ''}</li>{/each}
    </ol>
    {#if result.rollback.available}<button type="button" class="btn btn-sm" disabled={busy} onclick={rollback}>Undo this change</button>{/if}
  {/if}
</section>

<Dialog bind:open title="Apply the bridge node?">
  {#if plan}
    <div class="space-y-3 text-sm">
      <p class={plan.restartAsterisk ? 'rounded border border-warn/60 p-2 text-warn' : ''} role={plan.restartAsterisk ? 'alert' : undefined}>{planConfirm(plan)}</p>
      {#each plan.warnings as w}<p class="text-xs text-warn">{w}</p>{/each}
      {#each plan.files as file (file.path)}
        <div><h3 class="font-mono text-xs font-semibold">{file.path}{file.changed ? '' : ' (no change)'}</h3>
          {#if file.changed}<pre class="max-h-60 overflow-auto rounded border border-line bg-surface-2 p-2 text-xs">{file.diff}</pre>{/if}</div>
      {/each}
      <p class="text-xs text-muted">Order: Asterisk config, the Analog_Bridge ini, then the restarts and a health check. If the check fails you can roll back both files.</p>
    </div>
  {/if}
  {#snippet footer()}
    <button class="btn" onclick={() => (open = false)}>Cancel</button>
    <button class="btn btn-primary" disabled={busy} onclick={apply}>{busy ? 'Applying…' : plan?.restartAsterisk ? 'Apply and restart Asterisk' : 'Apply'}</button>
  {/snippet}
</Dialog>
