<script lang="ts">
  import { router } from '../../lib/router.svelte';
  import { app } from '../../lib/store.svelte';
  import { sectionFromPath } from '../../lib/sysnav';
  import { sections, defaultSection, groupSections } from './sections';

  const groups = groupSections(sections);
  const id = $derived(sectionFromPath(router.path, sections.map((s) => s.id), defaultSection));
  const sec = $derived(sections.find((s) => s.id === id) ?? sections[0]);
  // Updates carries the same badge as the header entry.
  const updCount = $derived((app.updates.aslcp || (app.system?.updateAvailable ?? false) ? 1 : 0) + app.updates.os);
  const hasBadge = (sid: string) => sid === 'updates' && (updCount > 0 || app.updates.reboot);
</script>

<div class="space-y-3">
  <h1 class="text-xl font-bold tracking-tight">System</h1>
  <div class="flex flex-col gap-3 md:flex-row md:items-start md:gap-5">
    <!-- Phones: a select; desktop: a list on the left. -->
    <label class="md:hidden">
      <span class="label">Section</span>
      <select class="input" value={id} onchange={(e) => (location.hash = '/system/' + e.currentTarget.value)}>
        {#each groups as grp (grp[0].id)}
          <optgroup label={' '}>
            {#each grp as s (s.id)}<option value={s.id}>{s.label}</option>{/each}
          </optgroup>
        {/each}
      </select>
    </label>
    <nav class="hidden w-44 shrink-0 flex-col gap-0.5 md:flex" aria-label="System sections">
      {#each groups as grp, gi (grp[0].id)}
        {#each grp as s, si (s.id)}
        <a href={'#/system/' + s.id} aria-current={s.id === id ? 'page' : undefined}
          class:mt-3={gi > 0 && si === 0}
          class="flex items-center justify-between rounded px-2.5 py-1.5 text-[13px] font-medium {s.id === id ? 'bg-surface-2 text-fg' : 'text-muted hover:bg-surface-2 hover:text-fg'}">
          {s.label}{#if hasBadge(s.id)}<span class="size-2 rounded-full {app.updates.security > 0 || app.updates.reboot ? 'bg-warn' : 'bg-accent'}" aria-label="updates available"></span>{/if}
        </a>
        {/each}
      {/each}
    </nav>
    <section class="min-w-0 flex-1 space-y-3" aria-label={sec.label}>
      {#if sec.id !== 'updates'}<h2 class="text-lg font-semibold tracking-tight">{sec.title ?? sec.label}</h2>{/if}
      {#key sec.id}
        {#await sec.load() then m}<m.default />{:catch}<p class="text-sm text-danger">Could not load this section. Reload the page.</p>{/await}
      {/key}
    </section>
  </div>
</div>
