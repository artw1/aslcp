import test from 'node:test';
import assert from 'node:assert/strict';
import { dismissKey, isDismissed, jobSummary, jobView, setDismissed } from './dvjob.ts';
import type { JobView } from './dvjob.ts';

const ok = (at?: number): JobView => ({ state: 'succeeded', log: 'x', ...(at ? { at } : {}) });

test('jobView maps the server state and the log', () => {
  assert.deepEqual(jobView(null), { state: 'idle', log: '' });
  assert.deepEqual(jobView({ job: '' }), { state: 'idle', log: '' });
  assert.equal(jobView({ job: 'running', log: 'a\nb' }).state, 'running');
  assert.equal(jobView({ job: 'done' }).state, 'succeeded');
  assert.equal(jobView({ job: 'succeeded' }).state, 'succeeded');
  assert.equal(jobView({ job: 'failed', lines: ['one', 'two'], at: 5 }).log, 'one\ntwo');
  assert.equal(jobView({ job: 'failed', lines: ['one'], at: 5 }).at, 5);
  assert.equal(jobView({ job: 'bogus' as never }).state, 'idle');
});

test('jobSummary words the result and date', () => {
  const f = (n: number) => `D${n}`;
  assert.equal(jobSummary(ok(7), f), 'Last enable: succeeded, D7');
  assert.equal(jobSummary({ state: 'failed', log: '', at: 8 }, f), 'Last enable: failed, D8');
  assert.equal(jobSummary({ state: 'failed', log: '' }, f), 'Last enable: failed');
  assert.equal(jobSummary({ state: 'idle', log: '' }, f), '');
  assert.equal(jobSummary({ state: 'running', log: '' }, f), '');
});

test('dismiss is keyed by at, and survives broken storage', () => {
  const m = new Map<string, string>();
  const s = { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => void m.set(k, v) };
  const k = dismissKey(ok(7));
  assert.notEqual(k, dismissKey(ok(8)));
  assert.equal(isDismissed(k, s), false);
  setDismissed(k, s);
  assert.equal(isDismissed(k, s), true);
  assert.equal(isDismissed(dismissKey(ok(8)), s), false);
  const bad = { getItem: () => { throw new Error('x'); }, setItem: () => { throw new Error('x'); } };
  assert.equal(isDismissed(k, bad), false);
  assert.doesNotThrow(() => setDismissed(k, bad));
  assert.equal(isDismissed(k, null), false);
});
