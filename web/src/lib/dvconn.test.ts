// Run with `npm test`. svelte-check type-checks this file too.
import test from 'node:test';
import assert from 'node:assert/strict';
import { connRequest, DEFAULT_STFU_SERVER, formError, formOf, rxLine, stfuBlocker, unitFor, validAlias, validHost, viaLabel } from './dvconn.ts';
import { bridgeInUse, outcomeFor } from './dmrlogin.ts';
import type { DVDMRConn, DVSTFUStatus } from './types';

const stfuView = { present: true, server: DEFAULT_STFU_SERVER, port: 54006, talkerAlias: 'N0CALL', startTg: '4000', txPort: 36100, rxPort: 36103, passwordSet: true };
const onMMDVM: DVDMRConn = { via: 'mmdvm_bridge', network: '', stfu: { ...stfuView, passwordSet: false } };
const onSTFU: DVDMRConn = { via: 'stfu', network: 'brandmeister', stfu: stfuView };

test('validHost: host names only', () => {
  for (const h of ['3102.repeater.net', 'example.org', 'a', 'a-b.example']) assert.equal(validHost(h), true, h);
  for (const h of ['', 'bad host', '-a.example', 'a-.example', 'a..b', 'a;b', 'x'.repeat(254), 'a.' + 'b'.repeat(64)]) assert.equal(validHost(h), false, h);
});

test('validAlias: the helper rule, empty keeps the saved one', () => {
  for (const a of ['', 'N0CALL', 'N0CALL Example', 'x'.repeat(27)]) assert.equal(validAlias(a), true, a);
  for (const a of ['x'.repeat(28), ' N0CALL', 'N0CALL ', 'a"b', "a'b", 'a#b', 'a;b', 'a=b', 'é']) assert.equal(validAlias(a), false, a);
});

test('formOf: defaults without a view', () => {
  assert.deepEqual(formOf(undefined), { via: 'mmdvm_bridge', server: DEFAULT_STFU_SERVER, talkerAlias: '', startTg: '4000' });
  assert.deepEqual(formOf(onSTFU), { via: 'stfu', server: DEFAULT_STFU_SERVER, talkerAlias: 'N0CALL', startTg: '4000' });
});

test('formError: only the STFU fields are checked', () => {
  assert.equal(formError({ via: 'mmdvm_bridge', server: 'bad host', talkerAlias: '#', startTg: 'x' }), '');
  assert.equal(formError({ ...formOf(onSTFU) }), '');
  assert.match(formError({ ...formOf(onSTFU), server: 'bad host' }), /host name/);
  assert.match(formError({ ...formOf(onSTFU), talkerAlias: 'a#b' }), /talker alias/);
  assert.match(formError({ ...formOf(onSTFU), startTg: '123456789' }), /talkgroup/);
});

test('connRequest: only what changed, never a password', () => {
  assert.equal(connRequest(onMMDVM, formOf(onMMDVM)), null);
  assert.equal(connRequest(onSTFU, formOf(onSTFU)), null);
  assert.deepEqual(connRequest(onSTFU, { ...formOf(onSTFU), via: 'mmdvm_bridge' }), { via: 'mmdvm_bridge' });
  // an unreadable record: saving MMDVM_Bridge repairs it
  assert.deepEqual(connRequest({ ...onMMDVM, error: true }, formOf(onMMDVM)), { via: 'mmdvm_bridge' });
  const r = connRequest(onMMDVM, { ...formOf(onMMDVM), via: 'stfu', talkerAlias: '' });
  assert.deepEqual(r, { via: 'stfu', network: 'brandmeister', server: DEFAULT_STFU_SERVER, startTg: '4000' });
  assert.deepEqual(connRequest(onSTFU, { ...formOf(onSTFU), server: ' 3103.repeater.net ' }),
    { via: 'stfu', network: 'brandmeister', server: '3103.repeater.net', startTg: '4000', talkerAlias: 'N0CALL' });
  for (const x of [r, connRequest(onSTFU, { ...formOf(onSTFU), startTg: '91' })]) assert.ok(!JSON.stringify(x).toLowerCase().includes('password'));
});

test('stfuBlocker: install first, then the password', () => {
  const st = (installed: boolean, job: DVSTFUStatus['job']['job'] = 'idle'): DVSTFUStatus => ({ installed, canInstall: !installed, job: { job, log: '' } });
  assert.match(stfuBlocker(undefined, true), /not installed/);
  assert.match(stfuBlocker(st(false), true), /not installed/);
  assert.match(stfuBlocker(st(false, 'running'), true), /being installed/);
  assert.match(stfuBlocker(st(true), false), /password/);
  assert.equal(stfuBlocker(st(true), true), '');
});

test('rxLine and viaLabel', () => {
  assert.equal(rxLine(undefined), '');
  assert.equal(rxLine({ active: true, src: '1234567', dst: '91', group: true }), 'Receiving: 1234567 → TG 91');
  assert.equal(rxLine({ active: false, src: '1234567', dst: '1234567', group: false, time: '12:00:30' }), 'Last heard: 1234567 → 1234567 at 12:00:30');
  assert.equal(viaLabel({ state: 'logged_in', via: 'stfu' }), 'BrandMeister through STFU');
  assert.equal(viaLabel({ state: 'logged_in', via: 'mmdvm_bridge' }), 'MMDVM_Bridge');
  assert.equal(viaLabel(null), '');
});

test('the login unit follows the method', () => {
  assert.equal(unitFor('stfu'), 'stfu');
  assert.equal(unitFor('mmdvm_bridge'), 'mmdvm_bridge');
  assert.equal(unitFor(undefined), 'mmdvm_bridge');
  const units = [{ name: 'mmdvm_bridge', loaded: true, active: 'active', enabled: 'enabled' }, { name: 'stfu', loaded: true, active: 'inactive', enabled: 'disabled' }];
  assert.deepEqual(bridgeInUse(units, unitFor('stfu')), { inUse: false, running: false });
  assert.deepEqual(bridgeInUse(units), { inUse: true, running: true });
  assert.match(outcomeFor({ state: 'not_running', via: 'stfu' }, true)!.message, /^stfu is not running/);
  assert.match(outcomeFor({ state: 'not_running' }, true)!.message, /^mmdvm_bridge is not running/);
  assert.equal(outcomeFor({ state: 'not_installed', via: 'stfu' }, true)!.message, 'STFU is not installed.');
});
