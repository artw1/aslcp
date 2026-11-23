<script lang="ts">
  // The credit line under the reflector pickers (YSF, P25, NXDN and M17 lists are DVRef data; docs/DV.md "Host lists").
  import { DVREF_URL, DVREF_TEXT } from '../lib/dvlist';
</script>
<p class="mt-0.5 text-xs text-muted" data-testid="dv-dvref">{DVREF_TEXT} <a href={DVREF_URL} target="_blank" rel="noopener noreferrer" class="underline hover:no-underline">DVRef</a></p>
