import { test } from 'node:test';
import assert from 'node:assert/strict';
import { shouldReloadForVersion } from './reload.ts';

test('first hello (no known version) never reloads', () => {
  assert.equal(shouldReloadForVersion('', '2.0.0', null), false);
});

test('same version does not reload', () => {
  assert.equal(shouldReloadForVersion('2.0.0', '2.0.0', null), false);
});

test('a different version reloads', () => {
  assert.equal(shouldReloadForVersion('2.0.0', '2.0.1', null), true);
  assert.equal(shouldReloadForVersion('2.0.0', '2.0.1', '1.9.0'), true);
});

test('already reloaded for that version does not reload again', () => {
  assert.equal(shouldReloadForVersion('2.0.0', '2.0.1', '2.0.1'), false);
});

test('an empty version from the server never reloads', () => {
  assert.equal(shouldReloadForVersion('2.0.0', '', null), false);
});
