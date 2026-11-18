import test from 'node:test';
import assert from 'node:assert/strict';
import { formError, linkButton, linkLine, nodeError, nodeWarning, pausedHint, planConfirm, prefill, suggestNode, uninstallNote, validPort } from './dvbridge.ts';
import type { DVLink } from './types';

const L = (o: Partial<DVLink> = {}): DVLink => ({ mode: 'ondemand', localNode: '1997', linked: false, paused: false, ...o });

test('suggestion is the first free node of 1900-1997', () => {
  assert.equal(suggestNode([]), '1900');
  assert.equal(suggestNode(['1900', '1901', '2000']), '1902');
  const all = Array.from({ length: 98 }, (_, i) => String(1900 + i));
  assert.equal(suggestNode(all), '');
  assert.equal(suggestNode([...all.slice(0, 97)]), '1997');
});

test('node validation', () => {
  assert.equal(nodeError('1998', []), null);
  assert.match(nodeError('2000', [])!, /below 2000/);
  assert.match(nodeError('abc', [])!, /digits/);
  assert.match(nodeError('1997', ['1997'])!, /in use/);
  assert.equal(nodeError('1997', ['1997'], '1997'), null);
  assert.match(nodeError('1996', ['1997'], '1997')!, /already has/);
  assert.ok(nodeWarning('1999'));
  assert.ok(nodeWarning('1998'));
  assert.equal(nodeWarning('1997'), null);
});

test('ports', () => {
  assert.ok(validPort('34001'));
  assert.ok(!validPort('80'));
  assert.ok(!validPort('70000'));
  assert.ok(!validPort('x'));
});

test('prefill uses the stock ports and the suggestion', () => {
  const b = { node: '', suggested: '1900', rxPort: 0, txPort: 0, link: { mode: 'ondemand' as const, localNode: '', bridge: '' }, localNodes: ['1997'] };
  assert.deepEqual(prefill(b), { node: '1900', rxPort: '34001', txPort: '32001', linkMode: 'ondemand', localNode: '1997' });
  assert.equal(prefill({ ...b, node: '1999', rxPort: 34010, txPort: 32010 }).rxPort, '34010');
});

test('form errors', () => {
  const b = { node: '', used: ['1997'] };
  const f = { node: '1900', rxPort: '34001', txPort: '32001', linkMode: 'ondemand', localNode: '1997' };
  assert.equal(formError(f, b), null);
  assert.match(formError({ ...f, txPort: '34001' }, b)!, /differ/);
  assert.match(formError({ ...f, localNode: '' }, b)!, /local node/);
  assert.match(formError({ ...f, localNode: '1900' }, b)!, /itself/);
  assert.match(formError({ ...f, rxPort: '22' }, b)!, /Ports/);
});

test('card text', () => {
  assert.equal(linkLine('1999', L({ mode: 'permanent', localNode: '2006', linked: true })), 'Bridge node 1999 linked to 2006 (permanent)');
  assert.match(linkLine('1999', L({ mode: 'permanent', localNode: '2006', paused: true })), /paused/);
  assert.match(linkLine('1999', L({ mode: 'permanent', localNode: '2006' })), /reconnecting/);
  assert.equal(linkLine('1999', L({ localNode: '2006' })), 'Bridge node 1999 not linked to 2006 (on demand)');
  assert.equal(linkLine('1999', L({ localNode: '' })), 'Bridge node 1999, no local node chosen');
  assert.ok(pausedHint(L({ mode: 'permanent', paused: true })));
  assert.equal(pausedHint(L({ mode: 'permanent', paused: true, linked: true })), '');
});

test('node link button is distinct from the talkgroup Unlink', () => {
  assert.equal(linkButton(L(), '1999')?.label, 'Link node');
  assert.equal(linkButton(L({ linked: true }), '1999')?.label, 'Unlink node');
  assert.equal(linkButton(L({ linked: true }), '1999')?.action, 'disconnect');
  assert.equal(linkButton(L({ localNode: '' }), '1999'), null);
  assert.equal(linkButton(L(), ''), null);
});

test('confirmation texts', () => {
  assert.match(planConfirm({ node: '1900', restartAsterisk: true, restarts: ['asterisk', 'analog_bridge'] }), /restarts Asterisk.*analog_bridge/);
  assert.match(planConfirm({ node: '1900', restartAsterisk: false, restarts: [] }), /Nothing needs a restart/);
  const s = { bridgeNode: { present: true, node: '1999' }, link: L({ mode: 'permanent' }) };
  const t = uninstallNote(s, true);
  assert.match(t[0], /restarts Asterisk/);
  assert.match(t[1], /permanent link/);
  assert.match(uninstallNote(s, false)[0], /not added by ASLCP/);
  assert.deepEqual(uninstallNote({ bridgeNode: { present: false }, link: L() }, false), []);
});
