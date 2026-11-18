import test from 'node:test';
import assert from 'node:assert/strict';
import { dismissKey, isDismissed, jobSummary, pickJobKind, setDismissed } from './dvjob.ts';
import type { DVSwitchJob } from './types';

const idle: DVSwitchJob = { job: 'idle', log: '' };
const ok = (at: number): DVSwitchJob => ({ job: 'succeeded', log: 'x', at });

test('pickJobKind prefers running, then the newer job', () => {
  assert.equal(pickJobKind({ install: idle, uninstall: idle }), 'install');
  assert.equal(pickJobKind({ install: ok(5), uninstall: { job: 'running', log: '' } }), 'uninstall');
  assert.equal(pickJobKind({ install: ok(5), uninstall: idle }), 'install');
  assert.equal(pickJobKind({ install: idle, uninstall: ok(5) }), 'uninstall');
  assert.equal(pickJobKind({ install: ok(9), uninstall: ok(5) }), 'install');
  assert.equal(pickJobKind({ install: ok(5), uninstall: ok(9) }), 'uninstall');
});

test('jobSummary words the result and date', () => {
  const f = (n: number) => `D${n}`;
  assert.equal(jobSummary('install', ok(7), f), 'Last install: succeeded, D7');
  assert.equal(jobSummary('uninstall', { job: 'failed', log: '', at: 8 }, f), 'Last uninstall: failed, D8');
  assert.equal(jobSummary('install', { job: 'failed', log: '' }, f), 'Last install: failed');
  assert.equal(jobSummary('install', idle, f), '');
  assert.equal(jobSummary('install', { job: 'running', log: '' }, f), '');
});

test('dismiss is keyed by kind and at, and survives broken storage', () => {
  const m = new Map<string, string>();
  const s = { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => void m.set(k, v) };
  const k = dismissKey('install', ok(7));
  assert.notEqual(k, dismissKey('uninstall', ok(7)));
  assert.notEqual(k, dismissKey('install', ok(8)));
  assert.equal(isDismissed(k, s), false);
  setDismissed(k, s);
  assert.equal(isDismissed(k, s), true);
  assert.equal(isDismissed(dismissKey('install', ok(8)), s), false);
  const bad = { getItem: () => { throw new Error('x'); }, setItem: () => { throw new Error('x'); } };
  assert.equal(isDismissed(k, bad), false);
  assert.doesNotThrow(() => setDismissed(k, bad));
  assert.equal(isDismissed(k, null), false);
});
