// Run with `npm test`. Keyed {#each} blocks that get a duplicate key abort rendering, so every keyed each on the status pages must include the loop index.
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

for (const f of ['public/PublicActivity', 'public/PublicNetwork', 'public/PublicStatusTable', 'public/PublicView', 'NetworkList', 'StatusTable']) {
  test(f + ' each keys include the index', () => {
    const src = readFileSync(new URL(`../components/${f}.svelte`, import.meta.url), 'utf8');
    const each = src.match(/\{#each [^}]*\([^}]*\)\}/g) ?? [];
    assert.ok(each.length > 0);
    for (const e of each) assert.match(e, /, i \(.*\bi\b/);
  });
}
