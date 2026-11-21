import test from 'node:test';
import assert from 'node:assert/strict';
import { credentialsPatch, dvErrorMessage, fieldLabel, formChanged } from './dvmsg.ts';

class ApiError extends Error { code: string; status: number; constructor(status: number, code: string, m: string) { super(m); this.code = code; this.status = status; } }
const inv = (m: string) => new ApiError(400, 'invalid_field', m);

test('invalid_field names the field', () => {
  assert.equal(fieldLabel('invalid_field: a value is not allowed (identity/dmrId)'), 'DMR ID');
  assert.equal(fieldLabel('invalid_field: a value is not allowed (mmdvm_bridge/DMR Network/Password)'), 'DMR master password');
  assert.equal(fieldLabel('invalid_field: a value is not allowed (brandmeister/talkerAlias)'), 'Talker alias');
  assert.equal(fieldLabel('invalid_field: a value is not allowed (brandmeister/ports)'), 'STFU ports 36100/36103 (in use)');
  assert.match(dvErrorMessage(inv('invalid_field: a value is not allowed (mmdvm_bridge/DMR Network/Port)')), /DMR master port is not valid/);
  assert.doesNotMatch(dvErrorMessage(inv('invalid_field: a value is not allowed (credentials)')), /invalid_field/);
});
test('unknown field and other errors', () => {
  assert.match(dvErrorMessage(inv('invalid_field: a value is not allowed')), /A value is not valid/);
  assert.equal(dvErrorMessage(new ApiError(409, 'conflict', 'busy')), 'busy');
  assert.equal(dvErrorMessage('x'), 'x');
  assert.match(dvErrorMessage(new ApiError(409, 'mode_disabled', 'x')), /disabled\. Enable it on its card/);
  assert.match(dvErrorMessage(new ApiError(409, 'bm_not_configured', 'x')), /BrandMeister is not set up/);
});
test('formChanged', () => {
  assert.equal(formChanged({ a: '1', p: '' }, { a: '1', p: '' }), false);
  assert.equal(formChanged({ a: '1', p: '' }, { a: '1', p: 'example-pass' }), true);
});

test('credentialsPatch: nothing changed sends nothing', () => {
  const n = { address: '192.0.2.10', port: '62031', password: '' };
  assert.equal(credentialsPatch(n, { ...n }, false), null);
});
test('credentialsPatch: clear counts as a change and sends no password', () => {
  const n = { address: '192.0.2.10', port: '62031', password: '' };
  assert.deepEqual(credentialsPatch(n, { ...n }, true), { clearPassword: true });
  assert.deepEqual(credentialsPatch(n, { ...n, password: 'example-pass' }, true), { clearPassword: true });
});
test('credentialsPatch: typed password and changed fields', () => {
  const n = { address: '192.0.2.10', port: '62031', password: '' };
  assert.deepEqual(credentialsPatch(n, { address: '192.0.2.11', port: '62032', password: 'example-pass' }, false),
    { dmrAddress: '192.0.2.11', dmrPort: 62032, dmrPassword: 'example-pass' });
});
