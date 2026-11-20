import { test } from 'node:test';
import assert from 'node:assert/strict';
import { wsURL, resizeMsg, endMsg, endText, mockSession } from './termconn.ts';

test('wsURL picks the scheme and encodes the ticket', () => {
  assert.equal(wsURL({ protocol: 'https:', host: 'node2001.local' }, 'ab+c', 24, 80), 'wss://node2001.local/api/system/terminal/ws?ticket=ab%2Bc&rows=24&cols=80');
  assert.equal(wsURL({ protocol: 'http:', host: '192.0.2.10:8080' }, 'x', 0, 80), 'ws://192.0.2.10:8080/api/system/terminal/ws?ticket=x');
  assert.match(wsURL({ protocol: 'http:', host: 'h' }, 'x', 9999, 1.9), /rows=500&cols=1$/);
});

test('resizeMsg is bounded JSON', () => {
  assert.deepEqual(JSON.parse(resizeMsg(30.7, 100)), { type: 'resize', rows: 30, cols: 100 });
  assert.deepEqual(JSON.parse(resizeMsg(0, 9999)), { type: 'resize', rows: 1, cols: 500 });
});

test('endMsg is the explicit end', () => {
  assert.deepEqual(JSON.parse(endMsg()), { type: 'end' });
});

test('endText covers the server reasons', () => {
  for (const r of ['idle', 'limit', 'disabled', 'logout', 'error', 'detach', 'admin-ended', 'closed', 'whatever']) assert.ok(endText(r).length > 5);
  assert.match(endText('idle'), /15 minutes/);
  assert.match(endText('admin-ended'), /administrator/);
  assert.match(endText('detach'), /reattach/);
  assert.equal(endText('start: the session did not start (login exited with status 1 after 0.2s)'), 'Could not start the terminal: the session did not start (login exited with status 1 after 0.2s)');
});

test('mockSession echoes printable input only', () => {
  let got = '';
  const dec = new TextDecoder();
  const s = mockSession((b) => { got += dec.decode(b); });
  assert.match(got, /not a real login/);
  got = '';
  s.send(new Uint8Array([97, 98, 1, 127]));
  assert.equal(got, 'ab\b \b');
  got = '';
  s.send(new Uint8Array([13]));
  assert.match(got, /Login incorrect/);
});
