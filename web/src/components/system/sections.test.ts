import { test } from 'node:test';
import assert from 'node:assert/strict';
import { groupSections, type SystemSection } from './sections.ts';

const sec = (id: string, group?: number): SystemSection => ({ id, label: id, group, load: async () => ({ default: null as never }) });

test('groupSections splits consecutive runs by group', () => {
  const out = groupSections([sec('a', 1), sec('b', 1), sec('c', 2), sec('d', 3), sec('e', 3)]);
  assert.deepEqual(out.map((g) => g.map((s) => s.id)), [['a', 'b'], ['c'], ['d', 'e']]);
});

test('groupSections keeps a non-adjacent repeat as its own run', () => {
  const out = groupSections([sec('a', 1), sec('b', 2), sec('c', 1)]);
  assert.deepEqual(out.map((g) => g.map((s) => s.id)), [['a'], ['b'], ['c']]);
});

test('groupSections on an empty list', () => {
  assert.deepEqual(groupSections([]), []);
});
