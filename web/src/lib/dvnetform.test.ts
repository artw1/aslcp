// Run with `npm test`. The generic network form and the availability helpers.
import test from 'node:test';
import assert from 'node:assert/strict';
import { m17NodeFits, nxdnOffNote, BM_HELP, MASTER_OTHER, applyMaster, masterChoice, masterListMode, buildPut, enableBlock, essidToRepeaterId, formError, formOf, hasChanges, identityError, loginText, liveLogin, savedMessage, modeAvailable, passwordUx, repeaterIdToEssid, validAddress } from './dvnetform.ts';
import { cardState } from './dvmodes.ts';
import type { DVListItem, DVNetView } from './types';

const bm: DVNetView = {
  mode: 'BM', label: 'BrandMeister', available: true, enabled: false, address: '', port: 0, passwordSet: false,
  fields: { address: true, port: false, password: true }, needsIdentity: true, help: BM_HELP,
  options: [
    { key: 'keepalive_ms', label: 'Keep-alive (ms)', kind: 'int', default: '5000', min: 500, max: 60000 },
    { key: 'client_version', label: 'Client version', kind: 'text', default: 'aslcp-dv' },
  ],
  values: {},
};
const id = { callsign: 'N0CALL', dmrId: 1234567 };

test('help line is the exact BrandMeister text', () => {
  assert.equal(BM_HELP, 'Logs in as an Open DMR Terminal with your DMR ID and the password set in your BrandMeister SelfCare (hotspot security). Pick the master closest to you.');
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

// a network with a port field (TGIF, DMR, YSF, P25 have one; BrandMeister does not)
const withPort: DVNetView = { ...bm, fields: { ...bm.fields, port: true }, defaultPort: 54006 };

test('address, port, password and option checks', () => {
  assert.ok(validAddress('192.0.2.10') && validAddress('master.example.net') && !validAddress('bad host') && !validAddress('300.1.1.1') && !validAddress(''));
  const f = formOf(bm);
  assert.equal(formError(bm, f), '');
  assert.match(formError(withPort, { ...f, port: '70000' }), /^Port/);
  assert.equal(formError(bm, { ...f, port: '70000' }), ''); // BM has no port field: ignored
  assert.match(formError(bm, { ...f, address: 'a b' }), /^Master address/);
  assert.match(formError(bm, { ...f, password: 'has space' }), /^Password/);
  assert.match(formError(bm, { ...f, options: { keepalive_ms: 'x' } }), /Keep-alive/);
  assert.match(formError(bm, { ...f, options: { keepalive_ms: '100' } }), /Keep-alive/);
});

test('buildPut sends only what changed, and the password only when typed', () => {
  const f = formOf(withPort);
  assert.deepEqual(buildPut(withPort, f), {});
  assert.equal(hasChanges(buildPut(withPort, f)), false);
  assert.deepEqual(buildPut(bm, { ...formOf(bm), port: '62031' }), {}); // BM: the port is never sent
  const b = buildPut(withPort, { ...f, enabled: true, address: ' 192.0.2.10 ', port: '62031', password: 'example-pass', options: { keepalive_ms: '1000', client_version: '' } });
  assert.deepEqual(b, { enabled: true, address: '192.0.2.10', port: 62031, password: 'example-pass', options: { keepalive_ms: '1000' } });
  const set = { ...withPort, passwordSet: true, address: '192.0.2.10', port: 62031, values: { keepalive_ms: '1000' } };
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
  assert.equal(loginText('pending', 'no answer from the master'), 'Logging in... (no answer from the master)');
  assert.equal(loginText('failed', 'bad password'), 'Login failed: bad password');
  assert.equal(loginText('off', ''), '');
  assert.equal(identityError('N0CALL', '1234567', ''), '');
  assert.match(identityError('N0', '1234567', ''), /^Callsign/);
  assert.match(identityError('N0CALL', '123', ''), /^DMR ID/);
});

test('identity ESSID: 2 digits 01-99 or empty', () => {
  assert.equal(identityError('N0CALL', '1234567', '41'), '');
  assert.equal(identityError('N0CALL', '1234567', ''), '');
  for (const bad of ['00', '1', 'abc', '123']) assert.match(identityError('N0CALL', '1234567', bad), /^ESSID/);
  assert.equal(essidToRepeaterId('1234567', '41'), 123456741);
  assert.equal(essidToRepeaterId('1234567', ''), undefined);
  assert.equal(repeaterIdToEssid(1234567, 123456741), '41');
  assert.equal(repeaterIdToEssid(1234567, 999999901), '');
  assert.equal(repeaterIdToEssid(1234567, undefined), '');
  assert.equal(repeaterIdToEssid(1234567, 123456705), '05');
});

test('TGIF, DMR and NXDN forms', () => {
  const tgif: DVNetView = {
    mode: 'TGIF', label: 'TGIF', available: true, enabled: false, address: '', port: 0, passwordSet: false,
    fields: { address: false, port: false, password: true }, needsIdentity: true,
    options: [
      { key: 'tune_kerchunk', label: 'Link by short key-up', kind: 'bool', default: 'true', advanced: true },
    ],
    values: {},
  };
  const f = formOf(tgif);
  // the master follows the host list: no address or port, the password is needed
  assert.equal(enableBlock(tgif, f, id), 'Set the password first');
  assert.equal(enableBlock(tgif, { ...f, password: 'example-pass' }, id), '');
  assert.equal(enableBlock(tgif, { ...f, password: 'example-pass' }, null), 'Set the callsign and DMR ID in the identity first');
  // bools are true/false; TGIF sends no address or port
  assert.match(formError(tgif, { ...f, options: { tune_kerchunk: '1' } }), /on or off/);
  assert.deepEqual(buildPut(tgif, { ...f, enabled: true, password: 'example-pass', options: { tune_kerchunk: 'false' } }),
    { enabled: true, password: 'example-pass', options: { tune_kerchunk: 'false' } });
  const dmr: DVNetView = {
    ...tgif, mode: 'DMR', label: 'DMR - Other', fields: { address: true, port: true, password: true },
    options: [{ key: 'options', label: 'Master options', kind: 'text', default: '', maxLen: 200 }, ...tgif.options],
  };
  assert.equal(enableBlock(dmr, formOf(dmr), id), 'Set the master address first');
  // long text option allowed up to its maxLen (DMR - Other keeps the master options)
  assert.equal(formError(dmr, { ...formOf(dmr), options: { options: 'TS2=1997,1998,1999;'.repeat(5) } }), '');
  assert.match(formError(dmr, { ...formOf(dmr), options: { options: 'x'.repeat(201) } }), /at most 200/);
  const nxdn: DVNetView = { ...tgif, mode: 'NXDN', label: 'NXDN', fields: { address: false, port: false, password: false }, defaultPort: undefined, defaultAddress: undefined };
  const nxdnId: DVNetView = { ...nxdn, options: [{ key: 'nxdn_id', label: 'NXDN ID', kind: 'int', default: '', min: 1, max: 65535 }] };
  // NXDN needs its NXDN ID to be enabled
  assert.equal(enableBlock(nxdnId, formOf(nxdnId), id), 'Set the NXDN ID first');
  assert.equal(enableBlock(nxdnId, { ...formOf(nxdnId), options: { nxdn_id: '   ' } }, id), 'Set the NXDN ID first');
  assert.equal(enableBlock(nxdnId, { ...formOf(nxdnId), options: { nxdn_id: '12345' } }, id), '');
  assert.equal(enableBlock(nxdnId, { ...formOf(nxdnId), options: { nxdn_id: '12345' } }, null), 'Set the callsign and DMR ID in the identity first');
  // an enabled NXDN network with the ID erased is not blocked: saving turns it off, and the form says so
  const on: DVNetView = { ...nxdnId, enabled: true, values: { nxdn_id: '12345' } };
  const erased = { ...formOf(on), options: { nxdn_id: '' } };
  assert.equal(enableBlock(on, erased, id), '');
  assert.equal(nxdnOffNote(on, erased), 'Saving without an NXDN ID turns NXDN off.');
  assert.equal(nxdnOffNote(on, formOf(on)), '');
  assert.equal(nxdnOffNote(nxdnId, { ...formOf(nxdnId), enabled: true }), ''); // turning on: the enable block applies instead
  assert.equal(savedMessage('NXDN', {}, undefined, true), 'NXDN settings saved, NXDN is off');
  assert.equal(enableBlock(tgif, f, id) === '', false); // other modes are not asked for it
  // erasing the NXDN ID, or the DMR master options, is sent as a cleared field
  const stored: DVNetView = { ...nxdnId, enabled: true, values: { nxdn_id: '12345' } };
  assert.deepEqual(buildPut(stored, { ...formOf(stored), options: { nxdn_id: '' } }), { options: {} });
  assert.deepEqual(buildPut(stored, { ...formOf(stored), options: { nxdn_id: '  ' } }), { options: {} });
  const dmrSet: DVNetView = { ...dmr, values: { options: 'TS2=9;', tune_kerchunk: 'false' } };
  assert.deepEqual(buildPut(dmrSet, { ...formOf(dmrSet), options: { options: '', tune_kerchunk: 'false' } }), { options: { tune_kerchunk: 'false' } });
  assert.equal(modeAvailable(undefined, 'TGIF') && modeAvailable(undefined, 'DMR') && modeAvailable(undefined, 'NXDN'), true);
  assert.equal(modeAvailable(undefined, 'YSF'), true);
  assert.equal(modeAvailable(undefined, 'P25'), true);
});

test('YSF form: enable only, no address or port', () => {
  const ysf: DVNetView = {
    mode: 'YSF', label: 'System Fusion (YSF)', available: true, enabled: false, address: '', port: 0, passwordSet: false,
    fields: { address: false, port: false, password: false }, needsIdentity: true,
    options: [{ key: 'poll_ms', label: 'Poll period (ms)', kind: 'int', default: '5000', min: 1000, max: 60000, advanced: true }],
    values: {},
  };
  const f = formOf(ysf);
  assert.equal(enableBlock(ysf, f, id), '');
  assert.equal(enableBlock(ysf, f, null), 'Set the callsign and DMR ID in the identity first');
  assert.match(formError(ysf, { ...f, options: { poll_ms: '10' } }), /1000 to 60000/);
  assert.deepEqual(buildPut(ysf, { ...f, enabled: true, address: '192.0.2.20', port: '42000', options: { poll_ms: '4000' } }),
    { enabled: true, options: { poll_ms: '4000' } });
});

test('M17 form: identity, suffix and timers', () => {
  const m17: DVNetView = {
    mode: 'M17', label: 'M17', available: true, enabled: false, address: '', port: 0, passwordSet: false,
    fields: { address: false, port: false, password: false }, needsIdentity: true,
    options: [
      { key: 'suffix', label: 'Callsign suffix', kind: 'text', default: '', maxLen: 3 },
      { key: 'can', label: 'Channel access number', kind: 'int', default: '0', min: 0, max: 15, advanced: true },
      { key: 'timeout_ms', label: 'Link timeout (ms)', kind: 'int', default: '30000', min: 5000, max: 120000, advanced: true },
    ],
    values: {},
  };
  const f = formOf(m17);
  assert.equal(enableBlock(m17, f, id), '');
  assert.equal(enableBlock(m17, f, null), 'Set the callsign and DMR ID in the identity first');
  assert.match(formError(m17, { ...f, options: { can: '16' } }), /0 to 15/);
  assert.match(formError(m17, { ...f, options: { suffix: 'ABCD' } }), /at most 3/);
  assert.match(formError(m17, { ...f, options: { timeout_ms: '10' } }), /5000 to 120000/);
  assert.equal(m17NodeFits('N0CALL', 'R'), true);
  assert.equal(m17NodeFits('N0CALL', '-RPT'), false); // 6 + 4 = 10 characters
  assert.equal(m17NodeFits('N0CALL', '-RP'), true);
  assert.equal(m17NodeFits('N0CALL', 'ABC'), false); // 6 + space + 3
  assert.equal(m17NodeFits('N0CALL', 'AB'), true);
  assert.equal(m17NodeFits('N0CALL', ''), true);
  assert.equal(enableBlock(m17, { ...f, options: { suffix: 'ABC' } }, { ...id, callsign: 'N0CALLXY' }), 'The callsign (with the suffix) cannot be used on M17');
  assert.deepEqual(buildPut(m17, { ...f, enabled: true, options: { can: '3', suffix: 'R' } }), { enabled: true, options: { can: '3', suffix: 'R' } });
  assert.equal(modeAvailable(undefined, 'M17'), true);
});

test('P25 form: enable only, no address, port or unit ID', () => {
  const p25: DVNetView = {
    mode: 'P25', label: 'P25', available: true, enabled: false, address: '', port: 0, passwordSet: false,
    fields: { address: false, port: false, password: false }, needsIdentity: true,
    options: [{ key: 'poll_ms', label: 'Poll period (ms)', kind: 'int', default: '5000', min: 1000, max: 60000, advanced: true }],
    values: {},
  };
  const f = formOf(p25);
  assert.equal(enableBlock(p25, f, id), '');
  assert.equal(enableBlock(p25, f, null), 'Set the callsign and DMR ID in the identity first');
  assert.deepEqual(buildPut(p25, { ...f, enabled: true }), { enabled: true });
});

test('masterChoice picks the list entry, Other or nothing', () => {
  const items = [{ value: '3104.master.example.net', name: '3104 – United States', address: '3104.master.example.net', port: 0 }];
  assert.equal(masterChoice('', items), '');
  assert.equal(masterChoice('3104.master.example.net', items), '3104.master.example.net');
  assert.equal(masterChoice('3104.MASTER.example.net', items), '3104.master.example.net');
  assert.equal(masterChoice('192.0.2.10', items), MASTER_OTHER);
  assert.equal(masterChoice('192.0.2.10', []), MASTER_OTHER);
});

const dmr: DVNetView = {
  mode: 'DMR', label: 'DMR - Other', available: true, enabled: false, address: '', port: 0, defaultPort: 62031, defaultPassword: 'passw0rd', passwordSet: false,
  fields: { address: true, port: true, password: true }, needsIdentity: true, options: [], values: {},
};
const dmrItems: DVListItem[] = [
  { value: 'freedmr.example.net', name: 'FreeDMR_Example', address: 'freedmr.example.net', port: 62031, password: 'passw0rd' },
  { value: '192.0.2.30', name: 'XLX_Example', address: '192.0.2.30', port: 62030, password: 'example-pass' },
  { value: '203.0.113.40', name: 'Plain_Example', address: '203.0.113.40', port: 55555 },
];

test('master list mode: BM and DMR - Other have a dropdown', () => {
  assert.equal(masterListMode('BM'), 'bm');
  assert.equal(masterListMode('DMR'), 'dmr-other');
  assert.equal(masterListMode('TGIF'), null);
});

test('DMR - Other: saved addresses select their entry, others are Other', () => {
  assert.equal(masterChoice('192.0.2.30', dmrItems), '192.0.2.30');
  assert.equal(masterChoice('FreeDMR.example.net', dmrItems), 'freedmr.example.net');
  assert.equal(masterChoice('192.0.2.99', dmrItems), MASTER_OTHER);
  assert.equal(masterChoice('', dmrItems), '');
});

test('DMR - Other: picking fills address and port, the list password only into an empty field', () => {
  const f = formOf(dmr);
  const a = applyMaster(dmr, f, dmrItems[1]);
  assert.deepEqual([a.address, a.port, a.password], ['192.0.2.30', '62030', 'example-pass']);
  // the same as the default: nothing to store
  assert.equal(applyMaster(dmr, f, dmrItems[0]).password, '');
  // no password in the list: the field stays empty (the default applies)
  assert.deepEqual([applyMaster(dmr, f, dmrItems[2]).port, applyMaster(dmr, f, dmrItems[2]).password], ['55555', '']);
  // a typed password wins, a stored one is kept
  assert.equal(applyMaster(dmr, { ...f, password: 'typed-pass' }, dmrItems[1]).password, 'typed-pass');
  assert.equal(applyMaster({ ...dmr, passwordSet: true }, f, dmrItems[1]).password, '');
  // the BrandMeister list has no port: it is left alone
  assert.equal(applyMaster(bm, { ...f, port: '' }, { value: '2001.master.example.net', name: '2001', address: '2001.master.example.net', port: 0 }).port, '');
});

test('DMR - Other: no password needed to enable (the default applies); TGIF and BM still need one', () => {
  const f = { ...formOf(dmr), address: '192.0.2.30' };
  assert.equal(enableBlock(dmr, f, id), '');
  assert.equal(enableBlock({ ...dmr, defaultPassword: undefined }, f, id), 'Set the password first');
  assert.equal(buildPut(dmr, f).password, undefined);
});

test('login line follows the live snapshot, not the view read right after a save', () => {
  const v = { mode: 'DMR' as const, login: 'pending' as const, detail: undefined };
  const modes = [{ id: 'DMR' as const, enabled: true, available: true, unlink: '4000', login: 'ok' as const }];
  assert.deepEqual(liveLogin(v, modes), { login: 'ok', detail: undefined });
  assert.equal(liveLogin(v, undefined).login, 'pending');
  assert.equal(liveLogin(v, [{ id: 'DMR' as const, enabled: true, available: true, unlink: '4000' }]).login, 'pending');
});

test('save toast says when a password was stored or removed', () => {
  assert.equal(savedMessage('DMR - Other', { password: 'example-pass' }), 'DMR - Other settings saved, password saved');
  assert.equal(savedMessage('DMR - Other', { clearPassword: true }), 'DMR - Other settings saved, password removed');
  assert.equal(savedMessage('DMR - Other', { port: 1 }, 'engine down'), 'DMR - Other settings saved. engine down');
});
