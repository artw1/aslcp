// Run with `npm test`. The generic network form and the availability helpers.
import test from 'node:test';
import assert from 'node:assert/strict';
import { BM_HELP, buildPut, enableBlock, formError, formOf, hasChanges, identityError, loginText, modeAvailable, passwordUx, validAddress } from './dvnetform.ts';
import { cardState } from './dvmodes.ts';
import type { DVNetView } from './types';

const bm: DVNetView = {
  mode: 'BM', label: 'BrandMeister', available: true, enabled: false, address: '', port: 0, defaultPort: 54005, passwordSet: false,
  fields: { address: true, port: true, password: true }, needsIdentity: true, help: BM_HELP,
  options: [
    { key: 'client_id', label: 'Application ID', kind: 'int', default: '0', min: 0, max: 4294967295 },
    { key: 'super_header', label: 'Super header', kind: 'bool', default: 'true' },
  ],
  values: {},
};
const id = { callsign: 'N0CALL', dmrId: 1234567 };

test('help line is the exact BrandMeister text', () => {
  assert.equal(BM_HELP, 'BrandMeister requires a registered application ID for this client; until one is set in Options, logins may be refused.');
});

test('availability: the engine wins, the static native list is the fallback', () => {
  const modes = [{ id: 'LOOP' as const, enabled: true, available: true, unlink: '' }, { id: 'BM' as const, enabled: false, available: false, unlink: '4000' }];
  assert.equal(modeAvailable(modes, 'BM'), false);
  assert.equal(modeAvailable(modes, 'LOOP'), true);
  assert.equal(modeAvailable(modes, 'YSF'), false);
  assert.equal(modeAvailable([{ id: 'LOOP', enabled: true, available: true, unlink: '' }], 'BM'), true);
  assert.equal(modeAvailable(undefined, 'BM'), true);
  assert.equal(modeAvailable(undefined, 'P25'), false);
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
  assert.match(formError(bm, { ...f, options: { client_id: 'x' } }), /Application ID/);
  assert.match(formError(bm, { ...f, options: { super_header: '2' } }), /Super header/);
});

test('buildPut sends only what changed, and the password only when typed', () => {
  const f = formOf(bm);
  assert.deepEqual(buildPut(bm, f), {});
  assert.equal(hasChanges(buildPut(bm, f)), false);
  const b = buildPut(bm, { ...f, enabled: true, address: ' 192.0.2.10 ', port: '62031', password: 'example-pass', options: { client_id: '4321', super_header: '' } });
  assert.deepEqual(b, { enabled: true, address: '192.0.2.10', port: 62031, password: 'example-pass', options: { client_id: '4321' } });
  const set = { ...bm, passwordSet: true, address: '192.0.2.10', port: 62031, values: { client_id: '4321' } };
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
  assert.equal(modeAvailable(undefined, 'YSF') || modeAvailable(undefined, 'P25'), false);
});
