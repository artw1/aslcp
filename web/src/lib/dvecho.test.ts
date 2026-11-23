// Run with `npm test`. The echo test control of Digital modes setup.
import test from 'node:test';
import assert from 'node:assert/strict';
import { ECHO_HINT, echoRunning, forgetBefore, modeBefore, rememberBefore, stopTarget } from './dvecho.ts';
import type { DVSnapshot } from './types';

const modes: DVSnapshot['modes'] = [
  { id: 'LOOP', enabled: true, available: true, unlink: '' },
  { id: 'BM', enabled: true, available: true, unlink: '4000' },
  { id: 'TGIF', enabled: false, available: true, unlink: '4000' },
  { id: 'YSF', enabled: true, available: true, unlink: 'disconnect' },
];
const bridge = (mode: string, source: 'aslcp-dv' | 'none' = 'aslcp-dv') => ({ bridge: { mode, tune: '', slot: 0, muted: false, source, updated: '' } });

test('the hint', () => {
  assert.equal(ECHO_HINT, 'Key up, talk, unkey: you hear yourself back a second later. Local only, no network.');
});

test('running while the engine is in LOOP', () => {
  assert.equal(echoRunning(bridge('LOOP')), true);
  assert.equal(echoRunning(bridge('LOOP', 'none')), false);
  assert.equal(echoRunning(bridge('BM')), false);
  assert.equal(echoRunning(null), false);
});

test('Stop returns to the mode before Start, else the last mode, else the first enabled one', () => {
  assert.equal(stopTarget('YSF', { lastMode: 'BM' }, modes), 'YSF');
  assert.equal(stopTarget('', { lastMode: 'BM' }, modes), 'BM');
  assert.equal(stopTarget('LOOP', { lastMode: 'BM' }, modes), 'BM');
  // a mode that is disabled now is skipped
  assert.equal(stopTarget('TGIF', { lastMode: 'TGIF' }, modes), 'BM');
  assert.equal(stopTarget('', null, modes), 'BM');
  assert.equal(stopTarget('', { lastMode: 'LOOP' }, [modes[0]]), '');
  // no snapshot: a network mode is trusted
  assert.equal(stopTarget('P25', null, undefined), 'P25');
  assert.equal(stopTarget('', null, undefined), '');
});

test('the mode before Start is remembered, never LOOP', () => {
  forgetBefore();
  rememberBefore('YSF');
  assert.equal(modeBefore(), 'YSF');
  rememberBefore('LOOP');
  rememberBefore('');
  rememberBefore(undefined);
  assert.equal(modeBefore(), 'YSF');
  forgetBefore();
  assert.equal(modeBefore(), '');
});
