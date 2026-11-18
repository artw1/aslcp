<script lang="ts">
  import { nodeDialog } from '../lib/form.svelte';
  import Dialog from './Dialog.svelte';
  import ConnectControls from './ConnectControls.svelte';
</script>

<Dialog bind:open={nodeDialog.open} title="Node {nodeDialog.node}" wide>
  <ConnectControls bind:target={nodeDialog.node} idp="nd" showFavorites={false} onDone={() => (nodeDialog.open = false)} />
</Dialog>
