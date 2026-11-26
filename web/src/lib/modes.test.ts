import test from 'node:test';
import assert from 'node:assert/strict';
import { draftOf, savePayload, missingField, dirty, targetText, targetBody, favoritesOf, recentTargets, stateText, sortModes, modeTitle } from './modes.ts';
import type { ModeInfo, Favorite, Heard, NowCall } from './types';

const bm: ModeInfo = {
  name: 'bm', title: 'BrandMeister', configured: true, active: false,
  fields: [
    { key: 'dmrId', label: 'DMR ID', kind: 'text', required: true },
    { key: 'password', label: 'Password', kind: 'password', required: true, secret: true },
    { key: 'host', label: 'Host', kind: 'text', default: 'master.example.net', advanced: true },
  ],
  settings: { values: { dmrId: '1234567' } }, secretsSet: { password: true },
};

test('draft starts with saved values, defaults and empty secrets', () => {
  assert.deepEqual(draftOf(bm), { dmrId: '1234567', password: '', host: 'master.example.net' });
});

test('a blank secret is left out of the payload', () => {
  const d = draftOf(bm);
  assert.deepEqual(savePayload(bm.fields!, d), { dmrId: '1234567', host: 'master.example.net' });
  d.password = 'pw-example';
  assert.equal(savePayload(bm.fields!, d).password, 'pw-example');
});

test('missing required fields, with a saved secret counting as present', () => {
  const d = draftOf(bm);
  assert.equal(missingField(bm, d), null);
  d.dmrId = ' ';
  assert.equal(missingField(bm, d)?.key, 'dmrId');
  const fresh = { ...bm, secretsSet: {} };
  assert.equal(missingField(fresh, { ...draftOf(fresh) })?.key, 'password');
});

test('dirty compares with the saved values', () => {
  const d = draftOf(bm);
  assert.equal(dirty(bm, d), false);
  d.password = 'x';
  assert.equal(dirty(bm, d), true);
});

test('target text and body', () => {
  assert.equal(targetText(undefined), '');
  assert.equal(targetText({ id: '91' }), '91');
  assert.equal(targetText({ id: '91', name: 'Worldwide' }), '91 Worldwide');
  assert.equal(targetText({ id: '91', name: '91' }), '91');
  assert.deepEqual(targetBody(' 91 '), { target: '91' });
  assert.deepEqual(targetBody('YSF1', { host: '192.0.2.5', port: 42000, name: 'Test' }), { target: 'YSF1', name: 'Test', host: '192.0.2.5', port: 42000 });
});

test('favorites of a mode: pinned first, then by position', () => {
  const f = (id: number, mode: string, pinned: boolean, position: number): Favorite => ({ id, mode, target: String(id), label: '', group: '', position, pinned, info: null });
  const out = favoritesOf([f(1, 'bm', false, 0), f(2, 'ysf', false, 0), f(3, 'bm', true, 5), f(4, 'bm', false, 1)], 'bm');
  assert.deepEqual(out.map((x) => x.id), [3, 1, 4]);
});

test('recent targets are distinct and newest first, from calls then heard', () => {
  const calls = [{ id: 'a', kind: 'bm', target: '91', start: '', active: true }, { id: 'b', kind: 'ysf', target: 'X', start: '', active: true }] as NowCall[];
  const heard = [{ mode: 'bm', target: '91', start: '' }, { mode: 'bm', target: '3100', start: '' }, { mode: 'bm', start: '' }] as Heard[];
  assert.deepEqual(recentTargets(heard, calls, 'bm'), ['91', '3100']);
  assert.deepEqual(recentTargets(heard, calls, 'bm', 1), ['91']);
});

test('state text', () => {
  assert.equal(stateText('connected', 'TG 91'), 'Connected · TG 91');
  assert.equal(stateText('idle', 'x'), 'Idle');
  assert.equal(stateText('connected', '', 'login refused'), 'login refused');
  assert.equal(stateText(undefined), '');
});

test('mode order and titles', () => {
  assert.deepEqual(sortModes([{ name: 'zzz' }, { name: 'ysf' }, { name: 'allstar' }, { name: 'aaa' }]).map((m) => m.name), ['allstar', 'ysf', 'aaa', 'zzz']);
  assert.equal(modeTitle('echolink'), 'EchoLink');
  assert.equal(modeTitle('abc'), 'ABC');
  assert.equal(modeTitle('bm', [{ ...bm, title: 'BM Custom' }]), 'BM Custom');
});
