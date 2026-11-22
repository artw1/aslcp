// Run with `npm test`. The generic network form and the availability helpers.
import test from 'node:test';
import assert from 'node:assert/strict';
import { BM_HELP, buildPut, enableBlock, formError, formOf, hasChanges, identityError, loginText, modeAvailable, passwordUx, validAddress } from './dvnetform.ts';
import { cardState } from './dvmodes.ts';
import type { DVNetView } from './types';

const bm: DVNetView = {
  mode: 'BM', label: 'BrandMeister', available: true, enabled: false, address: '', port: 0, defaultPort: 54006, passwordSet: false,
  fields: { address: true, port: true, password: true }, needsIdentity: true, help: BM_HELP,
  options: [
    { key: 'keepalive_ms', label: 'Keep-alive (ms)', kind: 'int', default: '5000', min: 500, max: 60000 },
    { key: 'client_version', label: 'Client version', kind: 'text', default: 'aslcp-dv' },
  ],
  values: {},
};
const id = { callsign: 'N0CALL', dmrId: 1234567 };

test('help line is the exact BrandMeister text', () => {
  assert.equal(BM_HELP, 'Logs in as an Open DMR Terminal with your DMR ID and the password set in your BrandMeister SelfCare (hotspot security).');
});

test('availability: the engine wins, the static native list is the fallback', () => {
  const modes = [{ id: 'LOOP' as const, enabled: true, available: true, unlink: '' }, { id: 'BM' as const, enabled: false, available: false, unlink: '4000' }];
  assert.equal(modeAvailable(modes, 'BM'), false);
  assert.equal(modeAvailable(modes, 'LOOP'), true);
  assert.equal(modeAvailable(modes, 'P25'), true); // not listed: the static native list
  assert.equal(modeAvailable([{ id: 'LOOP', enabled: true, available: true, unlink: '' }], 'BM'), true);
  assert.equal(modeAvailable(undefined, 'BM'), true);
  assert.equal(modeAvailable(undefined, 'P25'), true);
  const on = [{ id: 'BM' as const, enabled: true, available: true, unlink: '4000' }];
  assert.deepEqual(cardState(on, 'BM'), { available: true, enabled: true });
  assert.deepEqual(cardState(undefined, 'BM'), { available: false, enabled: false });
});

test('address, port, password and option checks', () => {
  assert.ok(validAddress('192.0.2.10') && validAddress('master.example.net') && !validAddress('bad host') && !validAddress('300.1.1.1') && !validAddress(''));
  const f = formOf(bm);
  assert.equal(formError(bm, f), '');
  assert.match(formError(bm, { ...f, port: '70000' }), /^Port/);
  assert.match(formError(bm, { ...f, address: 'a b' }), /^Master address/);
  assert.match(formError(bm, { ...f, password: 'has space' }), /^Password/);
  assert.match(formError(bm, { ...f, options: { keepalive_ms: 'x' } }), /Keep-alive/);
  assert.match(formError(bm, { ...f, options: { keepalive_ms: '100' } }), /Keep-alive/);
});

test('buildPut sends only what changed, and the password only when typed', () => {
  const f = formOf(bm);
  assert.deepEqual(buildPut(bm, f), {});
  assert.equal(hasChanges(buildPut(bm, f)), false);
  const b = buildPut(bm, { ...f, enabled: true, address: ' 192.0.2.10 ', port: '62031', password: 'example-pass', options: { keepalive_ms: '1000', client_version: '' } });
  assert.deepEqual(b, { enabled: true, address: '192.0.2.10', port: 62031, password: 'example-pass', options: { keepalive_ms: '1000' } });
  const set = { ...bm, passwordSet: true, address: '192.0.2.10', port: 62031, values: { keepalive_ms: '1000' } };
  assert.deepEqual(buildPut(set, formOf(set)), {});
  assert.deepEqual(buildPut(set, { ...formOf(set), clearPassword: true }), { clearPassword: true });
  assert.deepEqual(buildPut(set, { ...formOf(set), port: '' }), { port: 0 });
  assert.deepEqual(buildPut(set, { ...formOf(set), options: {} }), { options: {} });
});

test('password UX: Set first, Change afterwards, never shown', () => {
  assert.deepEqual(passwordUx(false, false), { status: 'No password set', action: 'Set', show: true });
  assert.deepEqual(passwordUx(true, false), { status: 'Password set', action: 'Change', show: false });
  assert.deepEqual(passwordUx(true, true), { status: 'Password set', action: 'Cancel', show: true });
});

test('what blocks Enable', () => {
  const f = formOf(bm);
  assert.equal(enableBlock(bm, f, id), 'Set the master address first');
  assert.equal(enableBlock(bm, { ...f, address: '192.0.2.10' }, id), 'Set the password first');
  assert.equal(enableBlock(bm, { ...f, address: '192.0.2.10', password: 'example-pass' }, null), 'Set the callsign and DMR ID in the identity first');
  assert.equal(enableBlock(bm, { ...f, address: '192.0.2.10', password: 'example-pass' }, id), '');
});

test('login text and identity checks', () => {
  assert.equal(loginText('ok', ''), 'Logged in');
  assert.equal(loginText('pending', ''), 'Logging in...');
  assert.equal(loginText('failed', 'bad password'), 'Login failed: bad password');
  assert.equal(loginText('off', ''), '');
  assert.equal(identityError('N0CALL', '1234567', ''), '');
  assert.match(identityError('N0', '1234567', ''), /^Callsign/);
  assert.match(identityError('N0CALL', '123', ''), /^DMR ID/);
  assert.match(identityError('N0CALL', '1234567', 'abc'), /^Repeater ID/);
});

test('TGIF, DMR and NXDN forms', () => {
  const tgif: DVNetView = {
    mode: 'TGIF', label: 'TGIF', available: true, enabled: false, address: '', port: 0, defaultPort: 62031, defaultAddress: 'tgif.network', passwordSet: false,
    fields: { address: true, port: true, password: true }, needsIdentity: true,
    options: [
      { key: 'options', label: 'Master options', kind: 'text', default: '', maxLen: 200 },
      { key: 'tune_kerchunk', label: 'Link by short key-up', kind: 'bool', default: 'true', advanced: true },
    ],
    values: {},
  };
  const f = formOf(tgif);
  // the public master is the default: no address needed, the password is
  assert.equal(enableBlock(tgif, f, id), 'Set the password first');
  assert.equal(enableBlock(tgif, { ...f, password: 'example-pass' }, id), '');
  assert.equal(enableBlock(tgif, { ...f, password: 'example-pass' }, null), 'Set the callsign and DMR ID in the identity first');
  // long text option allowed up to its maxLen, bools are true/false
  assert.equal(formError(tgif, { ...f, options: { options: 'TS2=1997,1998,1999;'.repeat(5) } }), '');
  assert.match(formError(tgif, { ...f, options: { options: 'x'.repeat(201) } }), /at most 200/);
  assert.match(formError(tgif, { ...f, options: { tune_kerchunk: '1' } }), /on or off/);
  assert.deepEqual(buildPut(tgif, { ...f, enabled: true, password: 'example-pass', options: { options: 'TS2=1997;', tune_kerchunk: 'false' } }),
    { enabled: true, password: 'example-pass', options: { options: 'TS2=1997;', tune_kerchunk: 'false' } });
  const dmr: DVNetView = { ...tgif, mode: 'DMR', label: 'DMR - Other', defaultAddress: undefined };
  assert.equal(enableBlock(dmr, formOf(dmr), id), 'Set the master address first');
  const nxdn: DVNetView = { ...tgif, mode: 'NXDN', label: 'NXDN', fields: { address: false, port: false, password: false }, defaultPort: undefined, defaultAddress: undefined };
  assert.equal(enableBlock(nxdn, formOf(nxdn), id), '');
  assert.equal(enableBlock(nxdn, formOf(nxdn), null), 'Set the callsign and DMR ID in the identity first');
  assert.equal(modeAvailable(undefined, 'TGIF') && modeAvailable(undefined, 'DMR') && modeAvailable(undefined, 'NXDN'), true);
  assert.equal(modeAvailable(undefined, 'YSF'), true);
  assert.equal(modeAvailable(undefined, 'P25'), true);
});

test('YSF form: the reflector address is optional', () => {
  const ysf: DVNetView = {
    mode: 'YSF', label: 'System Fusion (YSF)', available: true, enabled: false, address: '', port: 0, defaultPort: 42000, addressOptional: true, passwordSet: false,
    fields: { address: true, port: true, password: false }, needsIdentity: true,
    options: [{ key: 'poll_ms', label: 'Poll period (ms)', kind: 'int', default: '5000', min: 1000, max: 60000, advanced: true }],
    values: {},
  };
  const f = formOf(ysf);
  assert.equal(enableBlock(ysf, f, id), '');
  assert.equal(enableBlock(ysf, f, null), 'Set the callsign and DMR ID in the identity first');
  assert.match(formError(ysf, { ...f, options: { poll_ms: '10' } }), /1000 to 60000/);
  assert.deepEqual(buildPut(ysf, { ...f, enabled: true, address: '192.0.2.20', port: '42000', options: { poll_ms: '4000' } }),
    { enabled: true, address: '192.0.2.20', port: 42000, options: { poll_ms: '4000' } });
});

test('P25 form: unit ID range and optional address', () => {
  const p25: DVNetView = {
    mode: 'P25', label: 'P25', available: true, enabled: false, address: '', port: 0, defaultPort: 41000, addressOptional: true, passwordSet: false,
    fields: { address: true, port: true, password: false }, needsIdentity: true,
    options: [{ key: 'p25_id', label: 'P25 unit ID', kind: 'int', default: '', min: 1, max: 16777215 }],
    values: {},
  };
  const f = formOf(p25);
  assert.equal(enableBlock(p25, f, id), '');
  assert.equal(enableBlock(p25, f, null), 'Set the callsign and DMR ID in the identity first');
  assert.match(formError(p25, { ...f, options: { p25_id: '16777216' } }), /1 to 16777215/);
  assert.deepEqual(buildPut(p25, { ...f, enabled: true, options: { p25_id: '1999' } }), { enabled: true, options: { p25_id: '1999' } });
});
