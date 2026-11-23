<script lang="ts">
  // The questions of one wizard page, shared by "Set up this node" and "Add node" (one set of rules and wording).
  import type { Preset } from '../../lib/nodecfg';
  import { nodeKind, presetsFor, type SetupDraft, type WizardStep } from '../../lib/nodesetup';

  let { step, d = $bindable(), presets, variant = 'setup' }: { step: WizardStep; d: SetupDraft; presets: Preset[]; variant?: 'setup' | 'add' } = $props();
  const kind = $derived(nodeKind(d.node));
  const pubPresets = $derived(presetsFor(presets, d.radio));
</script>

{#if step === 'node'}
  <div class="grid gap-3 sm:grid-cols-2">
    <div class="min-w-0">
      <label class="label" for="sw-node">Node number</label>
      <input id="sw-node" class="input font-mono" inputmode="numeric" bind:value={d.node} autocomplete="off" placeholder={variant === 'add' ? '2002' : '1999'} aria-invalid={d.node && !kind ? 'true' : undefined} />
      <p class="mt-0.5 text-xs text-muted">
        {#if kind === 'private'}Private node (1 to 1999): not registered with AllStarLink, so it can link only to nodes you connect to by address.
        {:else if kind === 'public'}Public node (2000 and up): registered with AllStarLink so others can reach it. You get the number and password from your AllStarLink portal.
        {:else}1 to 1999 is a private node (no registration). 2000 and up is a public node.{/if}
      </p>
    </div>
    <div class="min-w-0">
      <label class="label" for="sw-pass">Node password</label>
      <input id="sw-pass" class="input font-mono" type="password" bind:value={d.password} autocomplete="new-password" spellcheck="false" />
      <p class="mt-0.5 text-xs text-muted">{kind === 'public' ? 'The password of this node on the AllStarLink portal; it is also used to register.' : 'Any password you like for this node.'} {variant === 'add' ? 'For a public node it is stored in the registration line.' : 'It is stored in savenode.conf and, for a public node, the registration line.'} It is never shown again.</p>
    </div>
  </div>
{:else if step === 'identity'}
  <label class="label" for="sw-call">Callsign</label>
  <input id="sw-call" class="input max-w-xs uppercase" bind:value={d.callsign} autocomplete="off" spellcheck="false" placeholder="N0CALL" />
  <p class="mt-0.5 text-xs text-muted">Sent in Morse code to identify the node (<span class="font-mono">idrecording = |i{d.callsign.trim().toUpperCase() || 'CALL'}</span>).</p>
{:else if step === 'radio'}
  <fieldset class="space-y-2">
    <legend class="label">How does this node connect to a radio?</legend>
    <label class="flex items-start gap-2 text-sm"><input type="radio" name="sw-radio" value="simpleusb" bind:group={d.radio} class="mt-1" /><span><span class="font-medium">USB sound interface (SimpleUSB)</span><br /><span class="text-xs text-muted">A CM1xx style USB sound card with a radio. Levels are tuned later under Audio Tuning.</span></span></label>
    <label class="flex items-start gap-2 text-sm"><input type="radio" name="sw-radio" value="hub" bind:group={d.radio} class="mt-1" /><span><span class="font-medium">Hub (no radio)</span><br /><span class="text-xs text-muted">A node that only links other nodes together.</span></span></label>
    <p class="text-xs text-muted">USBRadio is not supported by this wizard: edit it in Advanced.</p>
  </fieldset>
{:else if step === 'duplex'}
  <fieldset class="space-y-2">
    <legend class="label">Duplex preset</legend>
    {#each pubPresets as p (p.name)}
      <label class="flex items-start gap-2 text-sm"><input type="radio" name="sw-preset" value={p.name} bind:group={d.preset} class="mt-1" /><span><span class="font-medium">{p.label}</span><br /><span class="text-xs text-muted">{p.desc}</span></span></label>
    {/each}
  </fieldset>
{:else if step === 'registration'}
  {#if kind === 'public'}
    <fieldset class="space-y-2">
      <legend class="label">How should the node register with AllStarLink?</legend>
      <label class="flex items-start gap-2 text-sm"><input type="radio" name="sw-reg" value="http" bind:group={d.regMethod} class="mt-1" /><span><span class="font-medium">HTTP (recommended)</span><br /><span class="text-xs text-muted">Works behind most routers and firewalls.</span></span></label>
      <label class="flex items-start gap-2 text-sm"><input type="radio" name="sw-reg" value="iax" bind:group={d.regMethod} class="mt-1" /><span><span class="font-medium">IAX (optional)</span><br /><span class="text-xs text-muted">The older method; needs UDP port 4569 reachable from the internet.</span></span></label>
    </fieldset>
    <div class="mt-3 max-w-sm">
      <label class="label" for="sw-host">Registration server (optional)</label>
      <input id="sw-host" class="input font-mono" bind:value={d.regHost} autocomplete="off" spellcheck="false" placeholder="default server" />
    </div>
  {:else}
    <p class="text-sm">Node {d.node.trim() || '…'} is a private node (1 to 1999), so it is not registered. Go back and choose a number of 2000 or more to register it.</p>
  {/if}
{:else if step === 'echolink'}
  <label class="flex items-center gap-2 text-sm"><input type="checkbox" bind:checked={d.echolink} />Set up EchoLink (optional)</label>
  {#if d.echolink}
    <p class="mt-1 text-xs text-muted">You need an EchoLink account validated for this callsign. The EchoLink module is loaded for you.</p>
    <div class="mt-3 grid gap-3 sm:grid-cols-2">
      <div class="min-w-0"><label class="label" for="sw-elcall">EchoLink call</label><input id="sw-elcall" class="input uppercase" bind:value={d.elCall} placeholder="N0CALL-L" autocomplete="off" spellcheck="false" /><p class="mt-0.5 text-xs text-muted">-L links nodes to EchoLink, -R makes the node an EchoLink repeater.</p></div>
      <div class="min-w-0"><label class="label" for="sw-elpass">EchoLink password</label><input id="sw-elpass" class="input" type="password" bind:value={d.elPassword} autocomplete="new-password" /></div>
      <div class="min-w-0"><label class="label" for="sw-elemail">E-mail</label><input id="sw-elemail" class="input" type="email" bind:value={d.elEmail} autocomplete="off" /></div>
      <div class="min-w-0"><label class="label" for="sw-elname">Name</label><input id="sw-elname" class="input" bind:value={d.elName} autocomplete="off" /></div>
      <div class="min-w-0"><label class="label" for="sw-elqth">Location (QTH)</label><input id="sw-elqth" class="input" bind:value={d.elQth} autocomplete="off" /></div>
    </div>
  {/if}
{/if}
