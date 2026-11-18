// Run with `npm test`. svelte-check type-checks this file too.
import test from 'node:test';
import assert from 'node:assert/strict';
import { badge, bridgeInUse, MSG_NO_ANSWER, MSG_PENDING, MSG_REFUSED, outcomeFor, waitForLogin } from './dmrlogin.ts';
import type { DVDMRStatus } from './types';

const M = '3103.master.brandmeister.network:62031';
const st = (state: DVDMRStatus['state'], extra: Partial<DVDMRStatus> = {}): DVDMRStatus => ({ state, ...extra });

// A scripted get(): returns the states in turn, the last one forever.
function script(...seq: DVDMRStatus[]) {
  let i = 0, calls = 0, slept = 0;
  return { get: async () => { calls++; return seq[Math.min(i++, seq.length - 1)]; }, sleep: async (ms: number) => { slept += ms; }, calls: () => calls, slept: () => slept };
}

test('badge labels', () => {
  assert.equal(badge(null).text, 'Checking…');
  assert.deepEqual(badge(st('logged_in', { master: M })), { text: `Logged in to ${M}`, tone: 'ok' });
  assert.equal(badge(st('login_failed')).text, 'Login failed');
  assert.equal(badge(st('timed_out')).text, 'Timed out');
  assert.equal(badge(st('connecting')).text, 'Connecting…');
  assert.equal(badge(st('not_running')).text, 'Not running');
  assert.equal(badge(st('disabled')).text, 'DMR disabled');
});

test('outcome texts', () => {
  assert.equal(outcomeFor(st('logged_in', { master: M }), false)?.message, `Logged in to ${M}`);
  assert.equal(outcomeFor(st('login_failed'), false)?.message, MSG_REFUSED);
  assert.match(MSG_REFUSED, /hotspot security password .*BrandMeister SelfCare.*ESSID/);
  assert.equal(outcomeFor(st('timed_out'), false)?.message, MSG_NO_ANSWER);
  assert.equal(outcomeFor(st('socket_failed'), false)?.message, MSG_NO_ANSWER);
  assert.equal(outcomeFor(st('connecting'), false), null);
  assert.equal(outcomeFor(st('connecting'), true)?.message, MSG_PENDING);
  assert.equal(outcomeFor(st('not_running'), false)?.needStart, true);
});

test('success ends the wait at once', async () => {
  const s = script(st('connecting'), st('connecting'), st('logged_in', { master: M }));
  const r = await waitForLogin(s.get, { sleep: s.sleep, cancelled: () => false });
  assert.equal(r?.outcome.kind, 'ok');
  assert.equal(s.calls(), 3);
  assert.equal(s.slept(), 4000);
});

test('a failure needs two polls in a row, a retry that succeeds wins', async () => {
  let s = script(st('connecting'), st('login_failed'), st('login_failed'));
  let r = await waitForLogin(s.get, { sleep: s.sleep, cancelled: () => false });
  assert.equal(r?.outcome.message, MSG_REFUSED);
  assert.equal(s.calls(), 3);
  s = script(st('login_failed'), st('logged_in', { master: M }));
  r = await waitForLogin(s.get, { sleep: s.sleep, cancelled: () => false });
  assert.equal(r?.outcome.kind, 'ok');
  s = script(st('timed_out'), st('connecting'), st('timed_out'), st('timed_out'));
  r = await waitForLogin(s.get, { sleep: s.sleep, cancelled: () => false });
  assert.equal(r?.outcome.message, MSG_NO_ANSWER);
  assert.equal(s.calls(), 4);
});

test('still connecting after 40 s: no answer yet', async () => {
  const s = script(st('connecting'));
  const r = await waitForLogin(s.get, { sleep: s.sleep, cancelled: () => false });
  assert.equal(r?.outcome.message, MSG_PENDING);
  assert.equal(s.calls(), 20);
  assert.equal(s.slept(), 38000);
});

test('not running offers a start, disabled and errors end cleanly', async () => {
  let s = script(st('not_running'));
  let r = await waitForLogin(s.get, { sleep: s.sleep, cancelled: () => false });
  assert.equal(r?.outcome.needStart, true);
  assert.equal(s.calls(), 1);
  s = script(st('disabled'));
  r = await waitForLogin(s.get, { sleep: s.sleep, cancelled: () => false });
  assert.equal(r?.outcome.kind, 'info');
  const calls: number[] = [];
  r = await waitForLogin(async () => { calls.push(1); throw new Error('down'); }, { sleep: async () => {}, cancelled: () => false });
  assert.equal(r?.status, null);
  assert.match(r?.outcome.message ?? '', /Could not read/);
  assert.equal(calls.length, 20);
});

test('cancel stops polling and reports nothing', async () => {
  let stop = false;
  const s = script(st('connecting'));
  const r = await waitForLogin(async () => { stop = true; return s.get(); }, { sleep: s.sleep, cancelled: () => stop });
  assert.equal(r, null);
});

test('bridgeInUse: enabled or running', () => {
  const u = (active: string, enabled: string, loaded = true) => [{ name: 'mmdvm_bridge', loaded, active, enabled }];
  assert.deepEqual(bridgeInUse(u('active', 'disabled')), { inUse: true, running: true });
  assert.deepEqual(bridgeInUse(u('inactive', 'enabled')), { inUse: true, running: false });
  assert.deepEqual(bridgeInUse(u('inactive', 'disabled')), { inUse: false, running: false });
  assert.deepEqual(bridgeInUse(u('active', 'enabled', false)), { inUse: false, running: false });
  assert.deepEqual(bridgeInUse([]), { inUse: false, running: false });
});
