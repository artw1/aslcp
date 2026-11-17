<script lang="ts">
  import { app, isAdmin } from './lib/store.svelte';
  import { router, navigate } from './lib/router.svelte';
  import Header from './components/Header.svelte';
  import Toasts from './components/Toasts.svelte';
  import RestartBanner from './components/RestartBanner.svelte';
  import Login from './components/Login.svelte';
  import Setup from './components/Setup.svelte';
  import Dashboard from './components/Dashboard.svelte';
  import Lookup from './components/Lookup.svelte';
  import Settings from './components/Settings.svelte';
  import PublicView from './components/public/PublicView.svelte';

  $effect(() => {
    if (!app.ready || app.embed) return;
    if (app.setupNeeded) { if (router.path !== '/setup') navigate('/setup'); }
    else if (!app.user) { if (router.path !== '/login' && !app.publicEnabled) navigate('/login'); }
    else if (router.path === '/login' || router.path === '/setup') navigate('/');
  });
</script>

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
    {#if router.path === '/lookup'}<Lookup />
    {:else if router.path === '/schedules'}{#await import('./components/Schedules.svelte') then m}<m.default />{/await}
    {:else if router.path === '/settings'}<Settings />
    {:else if router.path === '/config' && isAdmin()}{#await import('./components/Config.svelte') then m}<m.default />{/await}
    {:else if router.path === '/backup' && isAdmin()}{#await import('./components/Backup.svelte') then m}<m.default />{/await}
    {:else if router.path === '/updates' && isAdmin()}{#await import('./components/Updates.svelte') then m}<m.default />{/await}
    {:else if router.path === '/tune' && isAdmin()}{#await import('./components/Tune.svelte') then m}<m.default />{/await}
    {:else}<Dashboard />{/if}
  </main>
{/if}
<Toasts />
<RestartBanner />
