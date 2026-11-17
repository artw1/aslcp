<script lang="ts">
  import { app, logout, isAdmin } from '../lib/store.svelte';
  import { router, navigate } from '../lib/router.svelte';
  import { theme, cycleTheme } from '../lib/theme.svelte';
  import Radio from '@lucide/svelte/icons/radio-tower';
  import Sun from '@lucide/svelte/icons/sun';
  import Moon from '@lucide/svelte/icons/moon';
  import Monitor from '@lucide/svelte/icons/monitor';
  import Wifi from '@lucide/svelte/icons/wifi';
  import WifiOff from '@lucide/svelte/icons/wifi-off';
  import User from '@lucide/svelte/icons/user';
  import LogOut from '@lucide/svelte/icons/log-out';

  const nav = $derived([['/', 'Dashboard'], ['/schedules', 'Schedules'], ['/lookup', 'Lookup'], ...(isAdmin() ? [['/config', 'Config']] : []), ...(isAdmin() && app.tune.devices.length ? [['/tune', 'Audio Tuning']] : []), ['/settings', 'Settings'], ...(isAdmin() ? [['/backup', 'Backup'], ['/updates', 'Updates']] : [])] as [string, string][]);
  // Admin only: counts only (SSE "updates"); security updates or a reboot use the warn colour.
  const updCount = $derived((app.updates.aslcp || (app.system?.updateAvailable ?? false) ? 1 : 0) + app.updates.os);
  const updWarn = $derived(app.updates.security > 0 || app.updates.reboot);
  const updTitle = $derived([app.updates.aslcp || app.system?.updateAvailable ? 'ASLCP update' : '', app.updates.os ? `${app.updates.os} system package${app.updates.os === 1 ? '' : 's'}${app.updates.security ? ` (${app.updates.security} security)` : ''}` : '', app.updates.reboot ? 'reboot required' : ''].filter(Boolean).join(', '));
  let menu = $state(false);
  const connLabel = $derived({ live: 'Live', connecting: 'Connecting…', reconnecting: 'Reconnecting…', offline: 'Offline' }[app.conn]);
  const nextTheme = $derived(theme.value === 'dark' ? 'light' : theme.value === 'light' ? 'system' : 'dark');
  const themeLabel = $derived(`Theme: ${theme.value}. Switch to ${nextTheme}.`);
</script>

<svelte:window onclick={(e) => { if (menu && !(e.target as HTMLElement).closest('[data-usermenu]')) menu = false; }} onkeydown={(e) => { if (e.key === 'Escape') menu = false; }} />

<header class="sticky top-0 z-30 border-b border-line bg-surface/90 backdrop-blur">
  <div class="mx-auto flex max-w-[1100px] flex-wrap items-center gap-x-2 gap-y-0.5 px-3 py-1 sm:flex-nowrap sm:gap-3 sm:px-4">
    <a href="#/" class="flex items-center gap-1.5 font-bold tracking-tight"><Radio size={18} class="text-accent" /><span class="text-base">ASLCP</span></a>
    <nav class="order-last flex w-full items-center gap-0.5 sm:order-none sm:w-auto" aria-label="Main">
      {#each nav as [path, label]}
        <a href={'#' + path} aria-current={router.path === path ? 'page' : undefined}
          class="rounded px-2 py-1 text-[13px] font-medium {router.path === path ? 'bg-surface-2 text-fg' : 'text-muted hover:text-fg'}">{label}{#if path === '/updates' && (updCount > 0 || app.updates.reboot)}<span class="ml-1 inline-flex min-w-4 items-center justify-center rounded-full px-1 text-[10px] font-bold leading-4 text-black {updWarn ? 'bg-warn' : 'bg-accent'}" aria-label={updTitle}>{updCount > 0 ? updCount : '!'}</span>{/if}</a>
      {/each}
    </nav>
    <div class="ml-auto flex items-center gap-1.5">
      {#if isAdmin() && (updCount > 0 || app.updates.reboot) && !app.updating}
        <a href="#/updates" class="badge {updWarn ? 'text-warn' : 'text-accent'}" title={updTitle}>{app.updates.reboot && updCount === 0 ? 'Reboot needed' : `${updCount} update${updCount === 1 ? '' : 's'}`}</a>
      {/if}
      <span class="badge {app.conn === 'live' ? 'text-ok' : 'text-warn'}" role="status" aria-label="Connection: {connLabel}">
        {#if app.conn === 'live'}<Wifi size={13} />{:else}<WifiOff size={13} />{/if}{connLabel}
      </span>
      <button class="btn btn-sm btn-icon" onclick={cycleTheme} aria-label={themeLabel} title={themeLabel}>
        {#if theme.value === 'dark'}<Moon size={16} />{:else if theme.value === 'light'}<Sun size={16} />{:else}<Monitor size={16} />{/if}
      </button>
      <div class="relative" data-usermenu>
        <button class="btn btn-sm" aria-haspopup="menu" aria-expanded={menu} onclick={() => (menu = !menu)}>
          <User size={15} /><span class="hidden sm:inline">{app.user?.username}</span>
        </button>
        {#if menu}
          <div role="menu" class="absolute right-0 mt-1 w-48 rounded border border-line bg-surface p-1 shadow-md">
            <div class="px-3 py-1.5 text-xs text-muted">{app.user?.username} · {app.user?.role}</div>
            <button role="menuitem" class="btn w-full justify-start border-transparent" onclick={() => { menu = false; navigate('/settings'); }}>Settings</button>
            {#if app.publicEnabled}<button role="menuitem" class="btn w-full justify-start border-transparent" onclick={() => { menu = false; navigate('/public'); }}>Public view</button>{/if}
            <button role="menuitem" class="btn w-full justify-start border-transparent" onclick={logout}><LogOut size={15} />Sign out</button>
          </div>
        {/if}
      </div>
    </div>
  </div>
</header>
