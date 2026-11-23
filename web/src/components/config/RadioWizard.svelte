<script lang="ts">
  // "Change radio interface": SimpleUSB or hub. rxchannel, the modules.conf load line and the device section change
  // together in one reviewed plan (POST /api/config/form with a radio type); PlanFlow shows what stays as it is.
  import { untrack } from 'svelte';
  import type { NodeModel } from '../../lib/nodecfg';
  import { radioChangeRequest, radioOptions, radioWizardBlock } from '../../lib/nodewizards';
  import type { SetupRadio } from '../../lib/nodesetup';
  import FormShell from './FormShell.svelte';

  let { node, onclose, ondone }: { node: NodeModel; onclose: () => void; ondone: () => void } = $props();
  const options = $derived(radioOptions(node.radio));
  const blocked = $derived(radioWizardBlock(node));
  let to = $state<SetupRadio>(untrack(() => (node.radio.type === 'hub' ? 'simpleusb' : 'hub')));
  const dev = $derived(node.radio.device || node.node);
</script>

<FormShell title="Change radio interface, node {node.node}" canReview={!blocked} build={() => radioChangeRequest(node, to)} {onclose} {ondone}
  intro="Switches how this node connects to a radio. The channel, the driver and the device section are changed together so they cannot disagree. Asterisk is restarted for it to take effect.">
  {#snippet children()}
    {#if blocked}
      <p class="rounded border border-warn/50 bg-warn/10 p-2 text-sm" role="alert">This wizard does not change this node. {blocked}</p>
    {:else}
      <fieldset class="space-y-2">
        <legend class="label">Radio interface (now: {options.find((o) => o.current)?.label ?? 'none'})</legend>
        {#each options as o (o.value)}
          <label class="flex items-start gap-2 text-sm"><input type="radio" name="rw-radio" value={o.value} bind:group={to} class="mt-1" /><span><span class="font-medium">{o.label}{o.current ? ' (current)' : ''}</span><br /><span class="text-xs text-muted">{o.desc}</span></span></label>
        {/each}
        <p class="text-xs text-muted">USBRadio and other interfaces are not offered: edit them in Advanced.</p>
      </fieldset>
      <section class="rounded border border-line bg-surface-2 p-2 text-sm" aria-label="What this does">
        {#if to === 'hub'}
          <p class="font-medium">Switching to a hub</p>
          <ul class="mt-1 list-inside list-disc space-y-0.5 text-xs text-muted">
            <li>rxchannel becomes Local/pseudo.</li>
            <li>The SimpleUSB device section [{dev}] is kept with all its settings, so you can switch back.</li>
            <li>The chan_simpleusb.so load line is kept, because another node may use it and it does no harm.</li>
          </ul>
        {:else}
          <p class="font-medium">Switching to SimpleUSB</p>
          <ul class="mt-1 list-inside list-disc space-y-0.5 text-xs text-muted">
            <li>rxchannel becomes SimpleUSB/{dev}.</li>
            <li>chan_simpleusb.so is loaded in modules.conf when it is not.</li>
            <li>The device section [{dev}] in simpleusb.conf is reused when it exists, and created empty otherwise. Tune its levels under Audio Tuning.</li>
          </ul>
        {/if}
      </section>
    {/if}
  {/snippet}
</FormShell>
