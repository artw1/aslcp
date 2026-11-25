import { test } from 'node:test';
import assert from 'node:assert/strict';
import { groups, visibleGroups, visibleTabs, defaultFor } from './sections.ts';
import { MOVED, OLD_IDS } from '../../lib/sysnav.ts';

const tabIds = (id: string) => groups.find((g) => g.id === id)?.tabs?.map((t) => t.id);

test('seven groups in the menu order', () => {
  assert.deepEqual(groups.map((g) => g.label), ['AllStarLink', 'Digital Modes', 'Access', 'System', 'Maintenance', 'About', 'Account']);
  assert.deepEqual(groups.map((g) => g.id), ['allstarlink', 'digital-modes', 'access', 'system', 'maintenance', 'about', 'account']);
});

test('group and tab ids are unique kebab-case', () => {
  const ids = [...groups.map((g) => g.id), ...groups.flatMap((g) => (g.tabs ?? []).map((t) => g.id + '/' + t.id))];
  assert.equal(new Set(ids).size, ids.length);
  for (const g of groups) {
    assert.match(g.id, /^[a-z0-9]+(-[a-z0-9]+)*$/);
    for (const t of g.tabs ?? []) assert.match(t.id, /^[a-z0-9]+(-[a-z0-9]+)*$/);
  }
});

test('a group has tabs or pages, never both', () => {
  for (const g of groups) assert.equal(!!g.tabs?.length !== !!g.pages?.length, true, g.id);
});

test('tabs of each tabbed group, in order', () => {
  assert.deepEqual(tabIds('allstarlink'), ['asterisk', 'audio-levels', 'node-list']);
  assert.deepEqual(tabIds('system'), ['overview', 'updates', 'network', 'wifi', 'web-port', 'asl-services', 'terminal']);
  assert.deepEqual(tabIds('maintenance'), ['backup', 'logs', 'diagnostics', 'audit']);
});

test('Digital Modes is one page, Access stacks Users and Public page', () => {
  const dm = groups.find((g) => g.id === 'digital-modes')!;
  assert.equal(dm.tabs, undefined);
  assert.equal(dm.pages?.length, 1);
  assert.equal(groups.find((g) => g.id === 'access')!.pages?.length, 2);
});

test('an admin sees every group, anyone else only About and Account', () => {
  assert.equal(visibleGroups(groups, true).length, 7);
  assert.deepEqual(visibleGroups(groups, false).map((g) => g.id), ['about', 'account']);
});

test('Audio levels is a tab only when the node has audio devices', () => {
  const asl = groups.find((g) => g.id === 'allstarlink')!;
  assert.deepEqual(visibleTabs(asl, false).map((t) => t.id), ['asterisk', 'node-list']);
  assert.deepEqual(visibleTabs(asl, true).map((t) => t.id), ['asterisk', 'audio-levels', 'node-list']);
  assert.deepEqual(visibleTabs(groups.find((g) => g.id === 'about')!), []);
});

test('the default group is one the role can open', () => {
  assert.equal(defaultFor(true), 'system');
  assert.ok(visibleGroups(groups, true).some((g) => g.id === defaultFor(true)));
  assert.ok(visibleGroups(groups, false).some((g) => g.id === defaultFor(false)));
});

test('every redirect and old id lands on a real group and tab', () => {
  const real = (to: string) => {
    const [, , gid, tid] = to.split('/');
    const g = groups.find((x) => x.id === gid);
    assert.ok(g, to);
    if (tid) assert.ok(g.tabs?.some((t) => t.id === tid), to);
    else assert.ok(!g.tabs || g.tabs.length > 0);
  };
  for (const to of Object.values(MOVED)) real(to);
  for (const [g, t] of Object.values(OLD_IDS)) real('/settings/' + g + (t ? '/' + t : ''));
});
