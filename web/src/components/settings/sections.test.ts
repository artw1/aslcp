import { test } from 'node:test';
import assert from 'node:assert/strict';
import { sections, groupSections, visibleSections, defaultFor, type SettingsSection } from './sections.ts';
import { MOVED } from '../../lib/sysnav.ts';

const sec = (id: string, group?: string): SettingsSection => ({ id, label: id, group, load: async () => ({ default: null as never }) });

test('groupSections splits consecutive runs by group', () => {
  const out = groupSections([sec('a', 'x'), sec('b', 'x'), sec('c', 'y'), sec('d', 'z'), sec('e', 'z')]);
  assert.deepEqual(out.map((g) => g.map((s) => s.id)), [['a', 'b'], ['c'], ['d', 'e']]);
});

test('groupSections keeps a non-adjacent repeat as its own run', () => {
  const out = groupSections([sec('a', 'x'), sec('b', 'y'), sec('c', 'x')]);
  assert.deepEqual(out.map((g) => g.map((s) => s.id)), [['a'], ['b'], ['c']]);
});

test('groupSections on an empty list', () => {
  assert.deepEqual(groupSections([]), []);
});

test('section ids are unique kebab-case', () => {
  const ids = sections.map((s) => s.id);
  assert.equal(new Set(ids).size, ids.length);
  for (const id of ids) assert.match(id, /^[a-z0-9]+(-[a-z0-9]+)*$/);
});

test('groups come in the menu order', () => {
  const names = groupSections(sections).map((g) => g[0].group);
  assert.deepEqual(names, ['AllStarLink', 'Digital Modes', 'Access', 'System', 'Maintenance', 'About', 'Account']);
});

test('sections of each group, in order', () => {
  const by = (g: string) => sections.filter((s) => s.group === g).map((s) => s.id);
  assert.deepEqual(by('AllStarLink'), ['asterisk', 'audio-levels', 'node-list']);
  assert.deepEqual(by('Digital Modes'), ['dv-engine', 'dv-identity', 'dv-bridge', 'dv-audio', 'dv-user-list', 'dv-callsigns']);
  assert.deepEqual(by('Access'), ['users', 'public']);
  assert.deepEqual(by('System'), ['overview', 'updates', 'network', 'wifi', 'web-port', 'asl-services', 'terminal']);
  assert.deepEqual(by('Maintenance'), ['backup', 'logs', 'diagnostics', 'audit']);
});

test('an admin sees every section, anyone else only About and Account', () => {
  assert.equal(visibleSections(sections, true).length, sections.length);
  assert.deepEqual(visibleSections(sections, false).map((s) => s.id), ['about', 'account']);
});

test('Audio levels is listed only when the node has audio devices', () => {
  assert.equal(visibleSections(sections, true, false).some((s) => s.id === 'audio-levels'), false);
  assert.equal(visibleSections(sections, true, true).some((s) => s.id === 'audio-levels'), true);
  assert.equal(visibleSections(sections, true, false).length, sections.length - 1);
});

test('the default section is one the role can open', () => {
  assert.ok(visibleSections(sections, true).some((s) => s.id === defaultFor(true)));
  assert.ok(visibleSections(sections, false).some((s) => s.id === defaultFor(false)));
});

test('every redirect lands on a real section', () => {
  const ids = new Set(sections.map((s) => s.id));
  for (const to of Object.values(MOVED)) assert.ok(ids.has(to.replace('/settings/', '')), to);
});
