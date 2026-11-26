<script lang="ts">
  import { api } from '../lib/api';
  import { app } from '../lib/store.svelte';
  import { toast, toastError } from '../lib/toast.svelte';
  import type { PublicSettings } from '../lib/types';
  import Globe from '@lucide/svelte/icons/globe';

  let cur = $state<PublicSettings | null>(null);
  let f = $state({ enabled: false, title: '', showCallsigns: true, showHeard: true, allowEmbedding: false, trustProxy: false });
  let busy = $state(false);
  function adopt(s: PublicSettings) {
    cur = s;
    f = { enabled: s.enabled, title: s.title, showCallsigns: s.showCallsigns, showHeard: s.showHeard, allowEmbedding: s.allowEmbedding, trustProxy: s.trustProxy };
  }
  async function load() { try { adopt(await api.publicSettings()); } catch (e) { toastError(e); } }
  $effect(() => { load(); });
  const dirty = $derived(!!cur && (f.enabled !== cur.enabled || f.title !== cur.title || f.showCallsigns !== cur.showCallsigns || f.showHeard !== cur.showHeard || f.allowEmbedding !== cur.allowEmbedding || f.trustProxy !== cur.trustProxy));
  async function save(e: Event) {
    e.preventDefault();
    busy = true;
    try { adopt(await api.putPublic({ ...f, title: f.title.trim() })); app.publicEnabled = f.enabled; toast(f.enabled ? 'Public page saved' : 'Public page is off'); }
    catch (x) { toastError(x); } finally { busy = false; }
  }
  const origin = location.origin;
  const snippet = $derived(`<iframe src="${origin}/embed" title="${(f.title || cur?.defaultTitle || 'Node status').replace(/"/g, '')}" width="100%" height="640" style="border:0" loading="lazy"></iframe>`);
  const checkbox = 'mt-0.5 size-4 accent-[var(--accent)]';
</script>

<section class="card space-y-3 p-4" aria-label="Public page">
  <h2 class="flex items-center gap-2 text-base font-semibold"><Globe size={16} />Public page</h2>
  {#if !cur}<p class="text-sm text-muted">Loading…</p>
  {:else}
    <p class="text-sm text-muted">A read-only status page for visitors who are not logged in. It looks like the Dashboard (station, Now calling, Last heard) but has no buttons: it shows the active mode, keying and recent calls only, never IP addresses, system information, favorites or users. Off by default.</p>
    <form class="space-y-3" onsubmit={save}>
      <label class="flex cursor-pointer items-start gap-2 text-sm"><input type="checkbox" class={checkbox} bind:checked={f.enabled} /><span class="font-medium">Show the public status page to visitors who are not logged in<span class="block text-xs font-normal text-muted">When on, the address of this panel ({origin}/) shows the status page with a "Log in" button instead of the login form.</span></span></label>
      <div class="grid gap-4 md:grid-cols-2">
        <div class="space-y-3">
          <div><label class="label" for="pub-title">Page title</label><input id="pub-title" class="input" maxlength="80" placeholder={cur.defaultTitle} bind:value={f.title} /><p class="mt-1 text-xs text-muted">Leave empty for “{cur.defaultTitle}”.</p></div>
        </div>
        <fieldset class="space-y-2"><legend class="label">What visitors can see</legend>
          <label class="flex cursor-pointer items-start gap-2 text-sm"><input type="checkbox" class={checkbox} bind:checked={f.showHeard} /><span>Last heard<span class="block text-xs text-muted">Recent calls of every mode: mode, callsign, target and time.</span></span></label>
          <label class="flex cursor-pointer items-start gap-2 text-sm"><input type="checkbox" class={checkbox} bind:checked={f.showCallsigns} /><span>Callsigns, names and places<span class="block text-xs text-muted">Off: only the mode, target and times are shown.</span></span></label>
          <label class="flex cursor-pointer items-start gap-2 text-sm"><input type="checkbox" class={checkbox} bind:checked={f.allowEmbedding} /><span>Allow embedding in other web pages<span class="block text-xs text-muted">Lets other sites show the status page in an iframe, at <code>/embed</code> only. The login page can never be embedded.</span></span></label>
          <label class="flex cursor-pointer items-start gap-2 text-sm"><input type="checkbox" class={checkbox} bind:checked={f.trustProxy} /><span>Trust proxy headers (X-Forwarded-For)<span class="block text-xs text-muted">Turn on only when this panel is behind a reverse proxy or tunnel (Caddy, Cloudflare Tunnel) so the per-visitor limits use the real visitor address. Leave it off otherwise: visitors could fake the header.</span></span></label>
        </fieldset>
      </div>
      {#if f.allowEmbedding && cur.enabled && cur.allowEmbedding}
        <div><label class="label" for="pub-embed">Embed code</label><textarea id="pub-embed" class="input font-mono text-xs" rows="2" readonly onfocus={(e) => e.currentTarget.select()}>{snippet}</textarea></div>
      {/if}
      <div class="flex flex-wrap items-center gap-2">
        <button class="btn btn-primary" disabled={busy || !dirty}>{busy ? 'Saving…' : 'Save'}</button>
        {#if cur.enabled}<a class="btn" href="#/public">Preview</a>{/if}
        {#if cur.enabled}<span class="text-xs text-muted" role="status">{cur.clients} live visitor{cur.clients === 1 ? '' : 's'}</span>{/if}
      </div>
    </form>
    <div class="rounded border border-line bg-surface-2 p-3 text-xs text-muted">
      <p class="mb-1 font-semibold text-fg">Security</p>
      <ul class="list-disc space-y-0.5 pl-4">
        <li>The page is served on the same address and port as this panel. Making it reachable from the internet also exposes the login page, so use strong passwords.</li>
        <li>Prefer a tunnel or reverse proxy (Cloudflare Tunnel, Caddy) over forwarding a router port to this panel.</li>
        <li>The page is read-only. It never shows IP addresses, system details, favorites or users, and visitors are rate-limited.</li>
      </ul>
    </div>
  {/if}
</section>
