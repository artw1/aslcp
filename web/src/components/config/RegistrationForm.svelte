<script lang="ts">
  import { untrack } from 'svelte';
  import { registrationProblem, registrationRequest, type NodeModel } from '../../lib/nodecfg';
  import FormShell from './FormShell.svelte';

  let { node, onclose, ondone }: { node: NodeModel; onclose: () => void; ondone: () => void } = $props();
  const cur = untrack(() => node.registration);
  let method = $state<'none' | 'http' | 'iax'>(cur.method === 'iax' ? 'iax' : 'http');
  let host = $state(cur.host ?? 'register.allstarlink.org');
  let password = $state('');
  const problem = $derived(registrationProblem(cur, method, host, password));
  const methods = [['http', 'Web (HTTP), the default', 'Registers over HTTPS; works behind most routers.'], ['iax', 'IAX', 'The older method; needs the IAX port reachable.'], ['none', 'Not registered', 'The node is not listed and cannot be reached by node number.']] as const;
</script>

<FormShell title="Registration, node {node.node}" canReview={!problem} build={() => registrationRequest(node, method, host, password)} {onclose} {ondone}
  intro="How the node tells the AllStar network where it is. Switching method removes the line of the other one.">
  {#snippet children()}
    {#if cur.method === 'both'}<p class="rounded border border-danger/50 bg-danger/10 p-2 text-sm" role="alert">This node is registered both ways. Choose one below to fix it.</p>{/if}
    <fieldset class="grid gap-1.5">
      <legend class="label">Method</legend>
      {#each methods as [v, label, desc]}
        <label class="flex items-start gap-2 text-sm"><input type="radio" name="reg-method" value={v} bind:group={method} class="mt-1" /><span><span class="font-medium">{label}</span><span class="block text-xs text-muted">{desc}</span></span></label>
      {/each}
    </fieldset>
    {#if method !== 'none'}
      <div>
        <label class="label" for="reg-host">Server</label>
        <input id="reg-host" class="input" bind:value={host} autocomplete="off" spellcheck="false" />
      </div>
      <div>
        <label class="label" for="reg-pass">Node password</label>
        <input id="reg-pass" class="input" type="password" autocomplete="new-password" bind:value={password} placeholder={cur.http?.hasPassword || cur.iax?.hasPassword ? 'Saved. Type to change' : 'Required'} />
        <p class="mt-0.5 text-xs text-muted">The password you set for this node number on the AllStar portal. It is never shown again after saving.</p>
      </div>
      {#if problem && (password || host !== (cur.host ?? ''))}<p class="text-xs text-danger" role="alert">{problem}</p>{/if}
    {/if}
    {#if cur.status}<p class="text-xs text-muted">Last status: {cur.status.state}{cur.status.detail ? ` (${cur.status.detail})` : ''}</p>{/if}
  {/snippet}
</FormShell>
