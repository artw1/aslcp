import test from 'node:test';
import assert from 'node:assert/strict';
import { emptyDraft, firstStepError, nodeKind, presetsFor, setupBody, stepError, validNode, validPassword } from './nodesetup.ts';
import type { Preset } from './nodecfg.ts';

const presets: Preset[] = [
  { name: 'simplex', label: 'Simplex', desc: '', values: {} }, { name: 'repeater', label: 'Repeater', desc: '', values: {} },
  { name: 'hub', label: 'Hub', desc: '', values: {}, hub: true },
];
const good = () => ({ ...emptyDraft('1999'), node: '2001', password: 'example-pass', callsign: 'n0call' });

test('node numbers: public vs private', () => {
  assert.equal(nodeKind('1'), 'private');
  assert.equal(nodeKind('1999'), 'private');
  assert.equal(nodeKind('2000'), 'public');
  assert.equal(nodeKind('2001'), 'public');
  assert.equal(nodeKind('0'), '');
  assert.equal(nodeKind('01999'), '');
  assert.equal(nodeKind('12345678'), '');
  assert.equal(nodeKind('abc'), '');
  assert.ok(validNode(' 1999 '));
});

test('password and page validation', () => {
  assert.ok(validPassword('example-pass'));
  assert.ok(!validPassword('has space'));
  assert.ok(!validPassword('semi;colon'));
  assert.ok(!validPassword('$(rm)'));
  assert.ok(!validPassword(''));
  assert.match(stepError('node', { ...good(), node: 'x' }), /node number/);
  assert.match(stepError('node', { ...good(), password: '' }), /password is required/);
  assert.equal(stepError('node', good()), '');
  assert.match(stepError('identity', { ...good(), callsign: 'N0 CALL' }), /Letters/);
  assert.equal(stepError('identity', good()), '');
});

test('presets follow the radio choice', () => {
  assert.deepEqual(presetsFor(presets, 'simpleusb').map((p) => p.name), ['simplex', 'repeater']);
  assert.deepEqual(presetsFor(presets, 'hub').map((p) => p.name), ['hub']);
  assert.match(stepError('duplex', { ...good(), radio: 'hub', preset: 'simplex' }, presets), /preset/);
  assert.equal(stepError('duplex', { ...good(), radio: 'hub', preset: 'hub' }, presets), '');
});

test('registration host is only checked for public nodes', () => {
  assert.equal(stepError('registration', { ...good(), node: '1999', regHost: '!!' }), '');
  assert.match(stepError('registration', { ...good(), regHost: '!!' }), /host name/);
  assert.equal(stepError('registration', { ...good(), regHost: 'register.example.net' }), '');
});

test('EchoLink fields', () => {
  const d = { ...good(), echolink: true, elCall: 'n0call-l', elPassword: 'example-pass', elEmail: 'op@example.net', elName: 'Test Op', elQth: 'Exampleton' };
  assert.equal(stepError('echolink', d), '');
  assert.match(stepError('echolink', { ...d, elCall: 'N0CALL' }), /-L/);
  assert.match(stepError('echolink', { ...d, elEmail: 'nope' }), /e-mail/);
  assert.match(stepError('echolink', { ...d, elQth: '' }), /required/);
  assert.equal(stepError('echolink', { ...d, echolink: false, elCall: '' }), '');
  assert.equal(firstStepError(d, presets), null);
  assert.equal(firstStepError({ ...d, password: '' }, presets)?.step, 'node');
});

test('request body', () => {
  const pub = setupBody('1999', { ...good(), regHost: 'register.example.net' });
  assert.deepEqual(pub, { oldNode: '1999', node: '2001', password: 'example-pass', callsign: 'N0CALL', radio: 'simpleusb', preset: 'simplex', ami: true, registration: { method: 'http', host: 'register.example.net' } });
  const priv = setupBody('1999', { ...good(), node: '1999' });
  assert.deepEqual(priv.registration, { method: 'none' });
  assert.equal('echolink' in priv, false);
  const el = setupBody('1999', { ...good(), echolink: true, elCall: 'n0call-r', elPassword: 'p', elEmail: ' op@example.net ', elName: 'A', elQth: 'B' });
  assert.deepEqual(el.echolink, { call: 'N0CALL-R', password: 'p', email: 'op@example.net', name: 'A', qth: 'B' });
});
