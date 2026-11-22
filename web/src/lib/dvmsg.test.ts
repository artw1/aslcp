import test from 'node:test';
import assert from 'node:assert/strict';
import { dvErrorMessage, fieldLabel } from './dvmsg.ts';

class ApiError extends Error { code: string; status: number; constructor(status: number, code: string, m: string) { super(m); this.code = code; this.status = status; } }
const inv = (m: string) => new ApiError(400, 'invalid_field', m);

test('invalid_field names the field', () => {
  assert.equal(fieldLabel('invalid_field: a value is not allowed (tune)'), 'Target');
  assert.equal(fieldLabel('invalid_field: a value is not allowed (x/rxPort)'), 'Receive port');
  assert.match(dvErrorMessage(inv('invalid_field: a value is not allowed (mode)')), /Mode is not valid/);
  assert.doesNotMatch(dvErrorMessage(inv('invalid_field: a value is not allowed (mode)')), /invalid_field/);
});
test('unknown field and other errors', () => {
  assert.match(dvErrorMessage(inv('invalid_field: a value is not allowed')), /A value is not valid/);
  assert.equal(dvErrorMessage(new ApiError(409, 'conflict', 'busy')), 'busy');
  assert.equal(dvErrorMessage('x'), 'x');
  assert.match(dvErrorMessage(new ApiError(409, 'not_available', 'x')), /not yet available in this build/);
  assert.match(dvErrorMessage(new ApiError(422, 'unknown_target', 'x')), /host list/);
  assert.match(dvErrorMessage(new ApiError(409, 'not_enabled', 'x')), /not enabled/);
});
