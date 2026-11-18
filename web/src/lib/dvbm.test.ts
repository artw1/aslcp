// Run with `npm test`. svelte-check type-checks this file too.
import test from 'node:test';
import assert from 'node:assert/strict';
import { bmBlocker, bmOptions, bmRequest, DEFAULT_STFU_SERVER, formError, formOf, isBMHost, isBMHostName, otherOptions, rxLine, STFU_PORT, validAlias, validHost, validPassword } from './dvbm.ts';
import { bridgeInUse, outcomeFor } from './dmrlogin.ts';
import type { DVBMView, DVListItem, DVSTFUStatus } from './types';

const view: DVBMView = { installed: true, present: true, enabled: true, configured: true, server: DEFAULT_STFU_SERVER, port: 54006, talkerAlias: 'N0CALL', startTg: '4000', txPort: 36100, rxPort: 36103, passwordSet: true };
const fresh: DVBMView = { ...view, enabled: false, configured: false, server: '', talkerAlias: '', startTg: '', passwordSet: false };

const list: DVListItem[] = [
  { value: 'example-master.invalid', name: 'BM_2001_Example', address: 'example-master.invalid', port: 62031 },
  { value: '192.0.2.12', name: 'BM_3100_Example', address: '192.0.2.12', port: 62031 },
  { value: '3102.repeater.net', name: 'Example_Repeater', address: '3102.repeater.net', port: 62031 },
  { value: '192.0.2.30', name: 'TGIF_Example', address: '192.0.2.30', port: 62031 },
];

test('validHost: host names only', () => {
  for (const h of ['3102.repeater.net', 'example.org', 'a', 'a-b.example']) assert.equal(validHost(h), true, h);
  for (const h of ['', 'bad host', '-a.example', 'a-.example', 'a..b', 'a;b', 'x'.repeat(254), 'a.' + 'b'.repeat(64)]) assert.equal(validHost(h), false, h);
});

test('validAlias: the helper rule, empty keeps the saved one', () => {
  for (const a of ['', 'N0CALL', 'N0CALL Example', 'x'.repeat(27)]) assert.equal(validAlias(a), true, a);
  for (const a of ['x'.repeat(28), ' N0CALL', 'N0CALL ', 'a"b', "a'b", 'a#b', 'a;b', 'a=b', 'é']) assert.equal(validAlias(a), false, a);
});

test('validPassword: the helper rule', () => {
  for (const p of ['example-pass', 'x'.repeat(64)]) assert.equal(validPassword(p), true, p);
  for (const p of ['', 'x'.repeat(65), 'a b', 'a"b', 'a#b', 'a;b']) assert.equal(validPassword(p), false, p);
});

test('BrandMeister hosts: by name or a BM_ entry', () => {
  for (const h of ['3102.repeater.net', 'repeater.net', 'Master.BrandMeister.Network', 'x.brandmeister.network.']) assert.equal(isBMHostName(h), true, h);
  for (const h of ['example.org', 'repeater.net.example', 'notrepeater.net']) assert.equal(isBMHostName(h), false, h);
  assert.equal(isBMHost('192.0.2.12', list), true);
  assert.equal(isBMHost('EXAMPLE-MASTER.invalid', list), true);
  assert.equal(isBMHost('192.0.2.30', list), false);
  assert.equal(isBMHost('', list), false);
});

test('pickers: BrandMeister gets the BM masters on port 54006, DMR - Other the rest', () => {
  const bm = bmOptions(list);
  assert.deepEqual(bm.map((o) => o.value), ['example-master.invalid', '192.0.2.12', '3102.repeater.net']);
  assert.ok(bm.every((o) => o.port === STFU_PORT));
  assert.deepEqual(otherOptions(list).map((o) => o.name), ['TGIF_Example']);
});

test('formOf and formError', () => {
  assert.deepEqual(formOf(undefined), { server: DEFAULT_STFU_SERVER, password: '', talkerAlias: '', startTg: '4000' });
  assert.deepEqual(formOf(view), { server: DEFAULT_STFU_SERVER, password: '', talkerAlias: 'N0CALL', startTg: '4000' });
  assert.equal(formError(formOf(view), true), '');
  assert.match(formError(formOf(fresh), false), /password/);
  assert.equal(formError({ ...formOf(fresh), password: 'example-pass' }, false), '');
  assert.match(formError({ ...formOf(view), password: 'a b' }, true), /password/);
  assert.match(formError({ ...formOf(view), server: 'bad host' }, true), /host name/);
  assert.match(formError({ ...formOf(view), talkerAlias: 'a#b' }, true), /talker alias/);
  assert.match(formError({ ...formOf(view), startTg: '123456789' }, true), /talkgroup/);
});

test('bmRequest: what to save; the password only when typed', () => {
  assert.equal(bmRequest(view, formOf(view)), null);
  // not enabled yet: saving the defaults turns it on
  assert.deepEqual(bmRequest({ ...view, enabled: false }, formOf(view)), { server: DEFAULT_STFU_SERVER, startTg: '4000', talkerAlias: 'N0CALL' });
  assert.deepEqual(bmRequest(view, { ...formOf(view), server: ' 3103.repeater.net ' }), { server: '3103.repeater.net', startTg: '4000', talkerAlias: 'N0CALL' });
  assert.deepEqual(bmRequest(fresh, { ...formOf(fresh), password: 'example-pass' }), { server: DEFAULT_STFU_SERVER, startTg: '4000', password: 'example-pass' });
  assert.ok(!('password' in (bmRequest(view, { ...formOf(view), startTg: '91' }) ?? {})));
});

test('bmBlocker: STFU must be installed', () => {
  const st = (installed: boolean, job: DVSTFUStatus['job']['job'] = 'idle'): DVSTFUStatus => ({ installed, canInstall: !installed, job: { job, log: '' } });
  assert.match(bmBlocker(undefined, undefined), /not installed/);
  assert.equal(bmBlocker(undefined, view), '');
  assert.match(bmBlocker(st(false), view), /not installed/);
  assert.match(bmBlocker(st(false, 'running'), undefined), /being installed/);
  assert.equal(bmBlocker(st(true), fresh), '');
});

test('rxLine', () => {
  assert.equal(rxLine(undefined), '');
  assert.equal(rxLine({ active: true, src: '1234567', dst: '91', group: true }), 'Receiving: 1234567 → TG 91');
  assert.equal(rxLine({ active: false, src: '1234567', dst: '1234567', group: false, time: '12:00:30' }), 'Last heard: 1234567 → 1234567 at 12:00:30');
});

test('the login unit of each section', () => {
  const units = [{ name: 'mmdvm_bridge', loaded: true, active: 'active', enabled: 'enabled' }, { name: 'stfu', loaded: true, active: 'inactive', enabled: 'disabled' }];
  assert.deepEqual(bridgeInUse(units, 'stfu'), { inUse: false, running: false });
  assert.deepEqual(bridgeInUse(units), { inUse: true, running: true });
  assert.match(outcomeFor({ state: 'not_running', via: 'stfu' }, true)!.message, /^stfu is not running/);
  assert.match(outcomeFor({ state: 'not_running' }, true)!.message, /^mmdvm_bridge is not running/);
  assert.equal(outcomeFor({ state: 'not_installed', via: 'stfu' }, true)!.message, 'STFU is not installed.');
});
