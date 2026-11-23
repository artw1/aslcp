<script lang="ts">
  import { app, logout, isAdmin } from '../lib/store.svelte';
  import { router, navigate } from '../lib/router.svelte';
  import { isSystemPath } from '../lib/sysnav';
  import { theme, cycleTheme } from '../lib/theme.svelte';
  import Radio from '@lucide/svelte/icons/radio-tower';
  import Sun from '@lucide/svelte/icons/sun';
  import Moon from '@lucide/svelte/icons/moon';
  import Monitor from '@lucide/svelte/icons/monitor';
  import Wifi from '@lucide/svelte/icons/wifi';
  import WifiOff from '@lucide/svelte/icons/wifi-off';
  import User from '@lucide/svelte/icons/user';
  import LogOut from '@lucide/svelte/icons/log-out';
  import MenuIcon from '@lucide/svelte/icons/menu';
  import X from '@lucide/svelte/icons/x';
  import Eye from '@lucide/svelte/icons/eye';
  import { fade, fly } from 'svelte/transition';
  import NowCalling from './NowCalling.svelte';

  const nav = $derived([['/', 'Dashboard'], ['/schedules', 'Schedules'], ['/lookup', 'Lookup'], ...(isAdmin() ? [['/config', 'Config']] : []), ...(isAdmin() && app.tune.devices.length ? [['/tune', 'Audio Tuning']] : []), ...(isAdmin() ? [['/digital', 'Digital modes']] : []), ['/settings', 'Settings'], ...(isAdmin() ? [['/backup', 'Backup'], ['/system', 'System']] : [])] as [string, string][]);
  // Admin only: counts only (SSE "updates"); security updates or a reboot use the warn colour.
  const updCount = $derived((app.updates.aslcp || (app.system?.updateAvailable ?? false) ? 1 : 0) + app.updates.os);
  const updWarn = $derived(app.updates.security > 0 || app.updates.reboot);
  const updTitle = $derived([app.updates.aslcp || app.system?.updateAvailable ? 'ASLCP update' : '', app.updates.os ? `${app.updates.os} system package${app.updates.os === 1 ? '' : 's'}${app.updates.security ? ` (${app.updates.security} security)` : ''}` : '', app.updates.reboot ? 'reboot required' : ''].filter(Boolean).join(', '));
  let menu = $state(false);
  // Phone menu: the tab row only fits from the xl breakpoint (80rem) up, so below it the tabs live in a panel.
  let open = $state(false);
  let menuBtn: HTMLButtonElement | undefined = $state();
  const showDot = $derived(isAdmin() && (updCount > 0 || app.updates.reboot));
  const pageName = $derived(isSystemPath(router.path) ? 'System' : router.path === '/public' ? 'Public view' : (nav.find(([p]) => p === router.path)?.[1] ?? ''));
  const dur = () => (matchMedia('(prefers-reduced-motion: reduce)').matches ? 0 : 150);
  const operate: [string, string][] = [['/', 'Dashboard'], ['/schedules', 'Schedules'], ['/lookup', 'Lookup']];
  const admin = $derived(nav.filter(([p]) => ['/config', '/tune', '/digital', '/backup', '/system'].includes(p)));
  const isHere = (path: string) => (path === '/system' ? isSystemPath(router.path) : router.path === path);
  $effect(() => { void router.path; open = false; });
  $effect(() => {
    const mq = matchMedia('(min-width: 80rem)');
    const f = () => { if (mq.matches) open = false; };
    mq.addEventListener('change', f);
    return () => mq.removeEventListener('change', f);
  });
  $effect(() => {
    if (!open) return;
    const prev = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    return () => { document.body.style.overflow = prev; };
  });
  function onKey(e: KeyboardEvent) {
    if (e.key !== 'Escape') return;
    menu = false;
    if (open) { open = false; menuBtn?.focus(); }
  }
  const connLabel = $derived({ live: 'Live', connecting: 'Connecting…', reconnecting: 'Reconnecting…', offline: 'Offline' }[app.conn]);
  const nextTheme = $derived(theme.value === 'dark' ? 'light' : theme.value === 'light' ? 'system' : 'dark');
  const themeLabel = $derived(`Theme: ${theme.value}. Switch to ${nextTheme}.`);
</script>

<svelte:window onclick={(e) => { if (menu && !(e.target as HTMLElement).closest('[data-usermenu]')) menu = false; }} onkeydown={onKey} />

{#if open}
  <div class="fixed inset-0 z-20 bg-black/50 xl:hidden" aria-hidden="true" onclick={() => (open = false)} transition:fade={{ duration: dur() }}></div>
{/if}

<header class="sticky top-0 z-30 border-b border-line bg-surface/90 backdrop-blur">
  <div class="relative">
  <div class="mx-auto flex max-w-[1100px] items-center gap-2 px-3 py-1 sm:px-4 xl:gap-3">
    <a href="#/" class="flex shrink-0 items-center gap-1.5 font-bold tracking-tight"><Radio size={18} class="text-accent" /><span class="text-base">ASLCP</span></a>
    <span class="min-w-0 flex-1 truncate text-sm font-medium text-muted xl:hidden" data-pagename>{pageName}</span>
    <nav class="hidden items-center gap-0.5 xl:flex" aria-label="Main">
      {#each nav as [path, label]}
        {@const here = isHere(path)}
        <a href={'#' + path} aria-current={here ? 'page' : undefined}
          class="rounded px-2 py-1 text-[13px] font-medium {here ? 'bg-surface-2 text-fg' : 'text-muted hover:text-fg'}">{label}{#if path === '/system' && (updCount > 0 || app.updates.reboot)}<span class="ml-1 inline-flex min-w-4 items-center justify-center rounded-full px-1 text-[10px] font-bold leading-4 text-black {updWarn ? 'bg-warn' : 'bg-accent'}" aria-label={updTitle}>{updCount > 0 ? updCount : '!'}</span>{/if}</a>
      {/each}
    </nav>
    <div class="flex items-center gap-1.5 xl:ml-auto">
      {#if isAdmin() && (updCount > 0 || app.updates.reboot) && !app.updating}
        <a href="#/system/updates" class="badge hidden xl:inline-flex {updWarn ? 'text-warn' : 'text-accent'}" title={updTitle}>{app.updates.reboot && updCount === 0 ? 'Reboot needed' : `${updCount} update${updCount === 1 ? '' : 's'}`}</a>
      {/if}
      <span class="badge {app.conn === 'live' ? 'text-ok' : 'text-warn'}" role="status" aria-label="Connection: {connLabel}">
        {#if app.conn === 'live'}<Wifi size={13} />{:else}<WifiOff size={13} />{/if}{connLabel}
      </span>
      <button class="btn btn-sm btn-icon hidden xl:inline-flex" onclick={cycleTheme} aria-label={themeLabel} title={themeLabel}>
        {#if theme.value === 'dark'}<Moon size={16} />{:else if theme.value === 'light'}<Sun size={16} />{:else}<Monitor size={16} />{/if}
      </button>
      <div class="relative hidden xl:block" data-usermenu>
        <button class="btn btn-sm" aria-haspopup="menu" aria-expanded={menu} onclick={() => (menu = !menu)}>
          <User size={15} /><span>{app.user?.username}</span>
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
      <button bind:this={menuBtn} class="btn relative -mr-1 size-11 shrink-0 px-0 xl:hidden" aria-label="Menu" aria-expanded={open} aria-controls="mobile-menu" aria-describedby={showDot ? 'menu-dot-desc' : undefined} onclick={() => (open = !open)}>
        {#if open}<X size={20} />{:else}<MenuIcon size={20} />{/if}
        {#if showDot}<span class="absolute right-1.5 top-1.5 size-2.5 rounded-full ring-2 ring-surface {updWarn ? 'bg-warn' : 'bg-accent'}" aria-hidden="true"></span>{/if}
      </button>
      {#if showDot}<span id="menu-dot-desc" class="sr-only">{updTitle}</span>{/if}
    </div>
  </div>
  {#if open}
    {@const item = (here: boolean) => `flex min-h-11 w-full items-center justify-between gap-2 rounded px-3 text-left text-sm font-medium ${here ? 'bg-surface-2 text-fg' : 'text-muted hover:bg-surface-2 hover:text-fg'}`}
    <div id="mobile-menu" class="absolute inset-x-0 top-full z-10 max-h-[calc(100dvh-3rem)] overflow-y-auto overscroll-contain border-b border-line bg-surface shadow-md xl:hidden" transition:fly={{ y: -8, duration: dur() }}>
      <div class="mx-auto max-w-[1100px] px-3 py-2 sm:px-4">
        <nav class="sm:ml-auto sm:max-w-xs" aria-label="Main">
          {#if isAdmin() && (updCount > 0 || app.updates.reboot) && !app.updating}
            <a href="#/system/updates" onclick={() => (open = false)} class="badge mb-1 min-h-11 w-full justify-center rounded-md text-sm {updWarn ? 'text-warn' : 'text-accent'}" title={updTitle}>{app.updates.reboot && updCount === 0 ? 'Reboot needed' : `${updCount} update${updCount === 1 ? '' : 's'}`}</a>
          {/if}
          <p class="px-3 pb-1 pt-2 text-[11px] font-semibold uppercase tracking-wider text-muted">Operate</p>
          {#each operate as [path, label]}
            <a href={'#' + path} onclick={() => (open = false)} aria-current={isHere(path) ? 'page' : undefined} class={item(isHere(path))}>{label}</a>
          {/each}
          {#if admin.length}
            <p class="px-3 pb-1 pt-3 text-[11px] font-semibold uppercase tracking-wider text-muted">Admin</p>
            {#each admin as [path, label]}
              <a href={'#' + path} onclick={() => (open = false)} aria-current={isHere(path) ? 'page' : undefined} class={item(isHere(path))}>{label}{#if path === '/system' && (updCount > 0 || app.updates.reboot)}<span class="inline-flex min-w-4 items-center justify-center rounded-full px-1 text-[10px] font-bold leading-4 text-black {updWarn ? 'bg-warn' : 'bg-accent'}" aria-label={updTitle}>{updCount > 0 ? updCount : '!'}</span>{/if}</a>
            {/each}
          {/if}
          <div class="mt-2 border-t border-line pt-2">
            <a href="#/settings" onclick={() => (open = false)} aria-current={router.path === '/settings' ? 'page' : undefined} class={item(router.path === '/settings')}>Settings</a>
          </div>
        </nav>
        <div class="mt-2 border-t border-line pt-2 sm:ml-auto sm:max-w-xs">
          <button class={item(false)} onclick={cycleTheme} aria-label={themeLabel}>
            <span class="flex items-center gap-2">{#if theme.value === 'dark'}<Moon size={16} />{:else if theme.value === 'light'}<Sun size={16} />{:else}<Monitor size={16} />{/if}Theme</span>
            <span class="text-xs capitalize">{theme.value}</span>
          </button>
          {#if app.publicEnabled}
            <button class={item(router.path === '/public')} onclick={() => { open = false; navigate('/public'); }}><span class="flex items-center gap-2"><Eye size={16} />Public view</span></button>
          {/if}
          <p class="flex min-h-11 items-center gap-2 px-3 text-xs text-muted"><User size={15} />{app.user?.username} · {app.user?.role}</p>
          <button class={item(false)} onclick={logout}><span class="flex items-center gap-2"><LogOut size={16} />Sign out</span></button>
        </div>
      </div>
    </div>
  {/if}
  </div>
  {#if app.user}<NowCalling />{/if}
</header>
