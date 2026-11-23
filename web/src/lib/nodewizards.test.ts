import test from 'node:test';
import assert from 'node:assert/strict';
import { addNodeBody, addStepError, amiRequest, bindIsLoopback, bindWarning, deviceRequest, emptyRenumber, linkingKeys, radioChangeRequest, radioOptions, radioWizardBlock, randomSecret, renumberBody, renumberError, renumberKindNote, renumberNeedsPassword } from './nodewizards.ts';
import { emptyDraft } from './nodesetup.ts';
import type { Field, NodeModel, Radio, SchemaKey } from './nodecfg.ts';

const presets = [{ name: 'simplex', label: 'Simplex', desc: '', values: {} }, { name: 'hub', label: 'Hub', desc: '', values: {}, hub: true }];
const draft = () => ({ ...emptyDraft(''), node: '2002', password: 'example-pass', callsign: 'n0call' });
const radio = (type: Radio['type'], supported = true): Radio => ({ type, channel: '', deviceStanza: true, supported });
const node = (type: Radio['type'], supported = true): NodeModel => ({ node: '1999', radio: radio(type, supported), refusals: [] } as unknown as NodeModel);

test('add node: shared checks plus an existing number', () => {
  assert.equal(addStepError('node', draft(), presets, ['1999']), '');
  assert.match(addStepError('node', draft(), presets, ['1999', '2002']), /already exists/);
  assert.match(addStepError('node', { ...draft(), node: 'x' }, presets, []), /node number/);
  assert.match(addStepError('identity', { ...draft(), callsign: '' }, presets, []), /Letters/);
});

test('add node body: private nodes are not registered', () => {
  assert.deepEqual(addNodeBody({ ...draft(), node: '1998' }).registration, { method: 'none' });
  const b = addNodeBody({ ...draft(), regHost: ' register.example.net ' }, '1999');
  assert.deepEqual(b.registration, { method: 'http', host: 'register.example.net' });
  assert.equal(b.callsign, 'N0CALL');
  assert.equal(b.from, '1999');
  assert.equal('from' in addNodeBody(draft()), false);
});

test('renumber: errors', () => {
  const d = { ...emptyRenumber(), node: '2002' };
  assert.match(renumberError('1999', { ...d, node: '1999' }, false, []), /current number/);
  assert.match(renumberError('1999', { ...d, node: '2001' }, false, ['2001']), /already exists/);
  assert.match(renumberError('1999', { ...d, node: 'x' }, false, []), /1 to 7 digits/);
  assert.equal(renumberError('1999', d, false, []), '');
  // registered public target and savenode need the password
  assert.match(renumberError('1999', d, true, []), /password/);
  assert.match(renumberError('1999', { ...d, node: '1998', savenode: true }, false, []), /password/);
  assert.equal(renumberError('1999', { ...d, node: '1998' }, true, []), '');
  assert.match(renumberError('1999', { ...d, password: 'a b' }, false, []), /letters/i);
});

test('renumber: body and notes', () => {
  assert.equal(renumberNeedsPassword(false, { ...emptyRenumber(), node: '2002' }), false);
  assert.deepEqual(renumberBody('1999', { node: ' 2002 ', password: 'example-pass', savenode: true }, false), { oldNode: '1999', node: '2002', savenode: true, password: 'example-pass' });
  // a password that nothing needs is not sent
  assert.equal('password' in renumberBody('1999', { node: '1998', password: 'example-pass', savenode: false }, true), false);
  assert.match(renumberKindNote('2002', { ...emptyRenumber(), node: '1998' }, true), /removed/);
  assert.match(renumberKindNote('1999', { ...emptyRenumber(), node: '2002' }, false), /public/);
  assert.equal(renumberKindNote('2001', { ...emptyRenumber(), node: '2002' }, true), '');
});

test('radio wizard: options and refusal', () => {
  assert.deepEqual(radioOptions(radio('hub')).map((o) => [o.value, o.current]), [['simpleusb', false], ['hub', true]]);
  assert.equal(radioWizardBlock(node('simpleusb')), '');
  assert.match(radioWizardBlock(node('usbradio', false)), /Advanced/);
  const n = node('usbradio', false); n.radio.note = 'USBRadio is edited in Advanced.';
  assert.equal(radioWizardBlock(n), 'USBRadio is edited in Advanced.');
  assert.equal(radioChangeRequest(node('hub'), 'hub'), null);
  assert.deepEqual(radioChangeRequest(node('simpleusb'), 'hub'), { node: '1999', mode: 'override', radio: { type: 'hub' } });
});

const key = (id: string, over: Partial<SchemaKey> = {}): SchemaKey => ({ id, file: 'x', scope: 'device', name: id.split(':').pop()!, label: id, form: 'radio', type: 'yesno', desc: '', reload: 'assumed', ...over });
const field = (id: string, value: string, set = true): Field => ({ id, key: id.split(':').pop()!, value, set, source: { kind: 'section' }, canSet: true, canReset: false, canTemplate: false });

test('device and AMI requests send only changes; empty secret keeps', () => {
  const keys = [key('simpleusb.conf:device:plfilter'), key('simpleusb.conf:device:rxboost')];
  const n = node('simpleusb'); n.radio.deviceFields = [field(keys[0].id, 'yes'), field(keys[1].id, 'no')];
  assert.equal(deviceRequest(n, keys, { [keys[0].id]: 'yes', [keys[1].id]: 'no' }), null);
  assert.deepEqual(deviceRequest(n, keys, { [keys[0].id]: 'no', [keys[1].id]: 'no' })?.changes, [{ id: keys[0].id, value: 'no' }]);
  const sk = key('manager.conf:admin:secret', { secret: true, type: 'string' });
  const fs = [{ ...field(sk.id, '********'), masked: true }];
  assert.equal(amiRequest('1999', fs, [sk], { [sk.id]: '' }), null);
  assert.deepEqual(amiRequest('1999', fs, [sk], { [sk.id]: 'example-pass' })?.changes, [{ id: sk.id, value: 'example-pass' }]);
});

test('AMI bind address warning', () => {
  for (const a of ['127.0.0.1', 'localhost', '::1', '[::1]:5038', '127.0.0.1:5038', ' 127.0.0.1 ']) assert.ok(bindIsLoopback(a), a);
  for (const a of ['', '0.0.0.0', '192.0.2.10', '::', '203.0.113.5:5038']) assert.ok(!bindIsLoopback(a), a);
  assert.equal(bindWarning('127.0.0.1'), '');
  assert.match(bindWarning('0.0.0.0'), /other computers/);
  assert.match(bindWarning(''), /other computers/);
});

test('random secret passes the password rule and varies', () => {
  const s = randomSecret(); const t = randomSecret();
  assert.match(s, /^[A-Za-z0-9]{32}$/);
  assert.notEqual(s, t);
  let i = 0;
  assert.equal(randomSecret(3, () => i++), 'abc');
});

test('linking keys: node scope of rpt.conf only', () => {
  const ks = [key('rpt.conf:node:telemwait', { form: 'linking', file: 'rpt.conf', scope: 'node' }), key('iax.conf:general:bindport', { form: 'linking', file: 'iax.conf', scope: 'general' }), key('rpt.conf:node:duplex', { form: 'duplex', file: 'rpt.conf', scope: 'node' })];
  assert.deepEqual(linkingKeys(ks).map((k) => k.name), ['telemwait']);
});
