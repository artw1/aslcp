<script lang="ts">
  import { app, isAdmin } from './lib/store.svelte';
  import { router, navigate } from './lib/router.svelte';
  import { reconcilePending } from './lib/actions.svelte';
  import { settingsRoute } from './lib/sysnav';
  import { groups, visibleGroups, defaultFor } from './components/settings/sections';
  import Header from './components/Header.svelte';
  import HotspotBanner from './components/HotspotBanner.svelte';
  import Toasts from './components/Toasts.svelte';
  import RestartBanner from './components/RestartBanner.svelte';
  import Login from './components/Login.svelte';
  import Setup from './components/Setup.svelte';
  import Dashboard from './components/Dashboard.svelte';
  import PublicView from './components/public/PublicView.svelte';

  // Operators and viewers open only About and Account; any other settings page falls back to the Dashboard.
  const settingsOpen = $derived(settingsRoute(router.path, visibleGroups(groups, isAdmin()).map((g) => ({ id: g.id, tabs: (g.tabs ?? []).map((t) => t.id) })), defaultFor(isAdmin()), isAdmin()) !== null);

  // Clear in-flight connects once their link shows, whichever page is open.
  $effect(() => { app.nodes; reconcilePending(); });

  $effect(() => {
    if (!app.ready || app.embed) return;
    if (app.setupNeeded) { if (router.path !== '/setup') navigate('/setup'); }
    else if (!app.user) { if (router.path !== '/login' && !app.publicEnabled) navigate('/login'); }
    else if (router.path === '/login' || router.path === '/setup') navigate('/');
  });
</script>

<HotspotBanner />
{#if !app.ready}
  <div class="grid min-h-dvh place-items-center text-sm text-muted" role="status">Loading…</div>
{:else if app.embed}
  {#if app.publicEnabled}<PublicView mode="embed" />{:else}<p class="grid min-h-dvh place-items-center text-sm text-muted">This page is not available.</p>{/if}
{:else if app.setupNeeded}
  <Setup />
{:else if !app.user}
  {#if app.publicEnabled && router.path !== '/login'}<PublicView mode="visitor" />{:else}<Login />{/if}
{:else if router.path === '/public' && app.publicEnabled}
  <Header />
  <PublicView mode="preview" />
{:else}
  <Header />
  <main class="mx-auto max-w-[1100px] px-3 py-2 sm:px-4">
    {#if router.path === '/schedules'}{#await import('./components/Schedules.svelte') then m}<m.default />{/await}
    {:else if settingsOpen}{#await import('./components/settings/SettingsShell.svelte') then m}<m.default />{/await}
    {:else if router.path === '/allstar'}{#await import('./components/AllStarPage.svelte') then m}<m.default />{/await}
    {:else if router.path === '/dv'}{#await import('./components/DvPage.svelte') then m}<m.default />{/await}
    {:else}<Dashboard />{/if}
  </main>
{/if}
<Toasts />
<RestartBanner />
