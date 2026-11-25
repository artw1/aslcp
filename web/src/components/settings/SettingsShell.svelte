<script lang="ts">
  import { router } from '../../lib/router.svelte';
  import { app, isAdmin } from '../../lib/store.svelte';
  import { settingsSection } from '../../lib/sysnav';
  import { sections, defaultFor, visibleSections, groupSections } from './sections';

  const admin = $derived(isAdmin());
  // Resolution knows every section the role may open (a direct link to Audio levels works before the device list loads);
  // the list hides the ones that need audio devices the node does not have.
  const openable = $derived(visibleSections(sections, admin));
  const listed = $derived(visibleSections(sections, admin, app.tune.devices.length > 0));
  const groups = $derived(groupSections(listed));
  const id = $derived(settingsSection(router.path, openable.map((s) => s.id), defaultFor(admin), admin) ?? defaultFor(admin));
  const sec = $derived(openable.find((s) => s.id === id) ?? openable[0]);
  // Updates carries the same badge as the header.
  const updCount = $derived((app.updates.aslcp || (app.system?.updateAvailable ?? false) ? 1 : 0) + app.updates.os);
  const hasBadge = (sid: string) => admin && sid === 'updates' && (updCount > 0 || app.updates.reboot);
</script>

<div class="space-y-3">
  <h1 class="text-xl font-bold tracking-tight">Settings</h1>
  <div class="flex flex-col gap-3 md:flex-row md:items-start md:gap-5">
    <!-- Phones: a select; desktop: a list on the left. Operators and viewers get just Account and About. -->
    <label class="md:hidden">
      <span class="label">Section</span>
      <select class="input" value={id} onchange={(e) => (location.hash = '/settings/' + e.currentTarget.value)}>
        {#each groups as grp (grp[0].id)}
          <optgroup label={grp[0].group ?? ' '}>
            {#each grp as s (s.id)}<option value={s.id}>{s.label}</option>{/each}
          </optgroup>
        {/each}
      </select>
    </label>
    <nav class="hidden w-52 shrink-0 flex-col gap-0.5 md:flex" aria-label="Settings sections">
      {#each groups as grp, gi (grp[0].id)}
        {#if admin && grp[0].group}<p class="px-2.5 pb-0.5 text-[11px] font-semibold uppercase tracking-wider text-muted {gi > 0 ? 'mt-3' : ''}">{grp[0].group}</p>{/if}
        {#each grp as s (s.id)}
        <a href={'#/settings/' + s.id} aria-current={s.id === id ? 'page' : undefined}
          class="flex items-center justify-between rounded px-2.5 py-1.5 text-[13px] font-medium {s.id === id ? 'bg-surface-2 text-fg' : 'text-muted hover:bg-surface-2 hover:text-fg'}">
          {s.label}{#if hasBadge(s.id)}<span class="size-2 rounded-full {app.updates.security > 0 || app.updates.reboot ? 'bg-warn' : 'bg-accent'}" aria-label="updates available"></span>{/if}
        </a>
        {/each}
      {/each}
    </nav>
    <section class="min-w-0 flex-1 space-y-3" aria-label={sec.label}>
      {#if !sec.bare}<h2 class="text-lg font-semibold tracking-tight">{sec.title ?? sec.label}</h2>{/if}
      {#key sec.id}
        {#await sec.load() then m}<m.default />{:catch}<p class="text-sm text-danger">Could not load this section. Reload the page.</p>{/await}
      {/key}
    </section>
  </div>
</div>
