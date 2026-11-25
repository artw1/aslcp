<script lang="ts">
  import { router } from '../../lib/router.svelte';
  import { app, isAdmin } from '../../lib/store.svelte';
  import { settingsRoute } from '../../lib/sysnav';
  import { groups, defaultFor, visibleGroups, visibleTabs } from './sections';

  const admin = $derived(isAdmin());
  // Resolution knows every tab the role may open (a direct link to Audio levels works before the device list loads);
  // the tab bar hides the ones that need audio devices the node does not have.
  const openable = $derived(visibleGroups(groups, admin));
  const route = $derived(
    settingsRoute(router.path, openable.map((g) => ({ id: g.id, tabs: (g.tabs ?? []).map((t) => t.id) })), defaultFor(admin), admin)
      ?? { group: defaultFor(admin), tab: null },
  );
  const grp = $derived(openable.find((g) => g.id === route.group) ?? openable[0]);
  const tabs = $derived(visibleTabs(grp, app.tune.devices.length > 0));
  const tab = $derived(grp.tabs?.find((t) => t.id === route.tab));
  // Updates carries the same badge as the header, on the System group and on its Updates tab.
  const updCount = $derived((app.updates.aslcp || (app.system?.updateAvailable ?? false) ? 1 : 0) + app.updates.os);
  const hasBadge = $derived(admin && (updCount > 0 || app.updates.reboot));
  const badgeClass = $derived(app.updates.security > 0 || app.updates.reboot ? 'bg-warn' : 'bg-accent');
</script>

<div class="space-y-3">
  <h1 class="text-xl font-bold tracking-tight">Settings</h1>
  <div class="flex flex-col gap-3 md:flex-row md:items-start md:gap-5">
    <!-- Phones: a select; desktop: a list on the left. Operators and viewers get just Account and About. -->
    <label class="md:hidden">
      <span class="label">Section</span>
      <select class="input" value={grp.id} onchange={(e) => (location.hash = '/settings/' + e.currentTarget.value)}>
        {#each openable as g (g.id)}<option value={g.id}>{g.label}</option>{/each}
      </select>
    </label>
    <nav class="hidden w-44 shrink-0 flex-col gap-0.5 md:flex" aria-label="Settings sections">
      {#each openable as g (g.id)}
        <a href={'#/settings/' + g.id} aria-current={g.id === grp.id ? 'page' : undefined}
          class="flex items-center justify-between rounded px-2.5 py-1.5 text-[13px] font-medium {g.id === grp.id ? 'bg-surface-2 text-fg' : 'text-muted hover:bg-surface-2 hover:text-fg'}">
          {g.label}{#if g.id === 'system' && hasBadge}<span class="size-2 rounded-full {badgeClass}" aria-label="updates available"></span>{/if}
        </a>
      {/each}
    </nav>
    <section class="min-w-0 flex-1 space-y-3" aria-label={grp.label}>
      {#if grp.tabs}
        <nav class="flex flex-wrap gap-0.5" aria-label="{grp.label} tabs">
          {#each tabs as t (t.id)}
            <a href={'#/settings/' + grp.id + '/' + t.id} aria-current={t.id === route.tab ? 'page' : undefined}
              class="inline-flex items-center gap-1.5 rounded px-2 py-1 text-[13px] font-medium {t.id === route.tab ? 'bg-surface-2 text-fg' : 'text-muted hover:text-fg'}">
              {t.label}{#if t.id === 'updates' && hasBadge}<span class="size-2 rounded-full {badgeClass}" aria-label="updates available"></span>{/if}
            </a>
          {/each}
        </nav>
        {#if tab}
          {#if !tab.bare}<h2 class="text-lg font-semibold tracking-tight">{tab.title ?? tab.label}</h2>{/if}
          {#key tab.id}
            {#await tab.load() then m}<m.default />{:catch}<p class="text-sm text-danger">Could not load this page. Reload the page.</p>{/await}
          {/key}
        {/if}
      {:else}
        {#each grp.pages ?? [] as load, i (i)}
          {#await load() then m}<m.default />{:catch}<p class="text-sm text-danger">Could not load this page. Reload the page.</p>{/await}
        {/each}
      {/if}
    </section>
  </div>
</div>
