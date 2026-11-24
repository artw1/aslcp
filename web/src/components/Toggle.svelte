<script lang="ts">
  /** Small on/off switch (role="switch"). `tap` pads the touch target to 32 px. `onchange` gets the requested value; the parent decides the real state. */
  let { checked, onchange, label, disabled = false, tap = false }: { checked: boolean; onchange: (v: boolean) => void; label: string; disabled?: boolean; tap?: boolean } = $props();
</script>
<button type="button" role="switch" aria-checked={checked} aria-label={label} title={label} {disabled} onclick={() => onchange(!checked)}
  class="{tap ? 'tap ' : ''}relative inline-block h-5 w-9 shrink-0 rounded-full border transition-colors disabled:cursor-not-allowed disabled:opacity-50 {checked ? 'border-transparent bg-accent' : 'border-line bg-surface-2'}">
  <span class="absolute left-0.5 top-0.5 size-3.5 rounded-full transition-transform {checked ? 'translate-x-4 bg-accent-fg' : 'bg-muted'}"></span>
</button>
