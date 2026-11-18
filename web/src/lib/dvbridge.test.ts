import test from 'node:test';
import assert from 'node:assert/strict';
import { DUPLEX_TEXT, NODE_HINT, canChange, doneText, formError, planApplyLabel, planOrder, planTitle, linkButton, linkLine, nodeError, pausedHint, planConfirm, prefill, linkExclusiveOnClick, exclusiveHint, suggestNode, uninstallNote, validPort } from './dvbridge.ts';
import type { DVLink } from './types';

const L = (o: Partial<DVLink> = {}): DVLink => ({ mode: 'ondemand', localNode: '1997', linked: false, paused: false, exclusive: false, ...o });

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
  assert.match(nodeError('1996', ['1997'], '1997')!, /already has.*Change number/);
  assert.equal(nodeError('1996', ['1997'], '1997', true), null);
  assert.match(nodeError('1997', ['1997'], '1996', true)!, /in use/);
});

test('ports', () => {
  assert.ok(validPort('34001'));
  assert.ok(!validPort('80'));
  assert.ok(!validPort('70000'));
  assert.ok(!validPort('x'));
});

test('prefill uses the stock ports and the suggestion', () => {
  const b = { node: '', suggested: '1900', rxPort: 0, txPort: 0, link: { mode: 'ondemand' as const, localNode: '', bridge: '', exclusive: true }, localNodes: ['1997'] };
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

test('Link node honours the saved "Disconnect other links first"', () => {
  assert.match(linkButton(L({ exclusive: true }), '1999')!.title, /disconnects other links first/);
  assert.doesNotMatch(linkButton(L({ exclusive: false }), '1999')!.title, /disconnects/);
  assert.doesNotMatch(linkButton(L({ exclusive: true, linked: true }), '1999')!.title, /disconnects other/);
  assert.equal(linkExclusiveOnClick(L({ exclusive: true })), true);
  assert.equal(linkExclusiveOnClick(L({ exclusive: false })), false);
  assert.match(exclusiveHint('permanent', true), /when you save the bridge node setup.*never disconnect/);
  assert.match(exclusiveHint('ondemand', true), /Link node button/);
  assert.match(exclusiveHint('ondemand', false), /left alone/);
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

test('change number and remove are for an ASLCP-owned node only', () => {
  assert.ok(canChange({ node: '1999', owned: true }));
  assert.ok(!canChange({ node: '1999', owned: false }));
  assert.ok(!canChange({ node: '', owned: false }));
  assert.equal(formError({ node: '1996', rxPort: '34001', txPort: '32001', linkMode: 'ondemand', localNode: '1997' }, { node: '1999', used: ['1997', '1999'] }, true), null);
  assert.match(formError({ node: '1996', rxPort: '34001', txPort: '32001', linkMode: 'ondemand', localNode: '1997' }, { node: '1999', used: ['1997', '1999'] })!, /Change number/);
});

test('fixed texts', () => {
  assert.equal(DUPLEX_TEXT, 'Duplex 0: fixed for a bridge node (a higher value repeats DMR audio back to DMR and adds hang time and courtesy tones).');
  assert.equal(NODE_HINT, 'To use a different number, use Change number. Remove bridge node takes it out completely.');
});

test('review dialog texts per kind', () => {
  const p = { node: '1999', restartAsterisk: true, restarts: ['asterisk', 'analog_bridge'] };
  assert.equal(planTitle('remove'), 'Remove the bridge node?');
  assert.equal(planTitle('renumber'), 'Change the bridge node number?');
  assert.equal(planTitle('set'), 'Apply the bridge node?');
  assert.equal(planTitle(undefined), 'Apply the bridge node?');
  assert.match(planConfirm({ ...p, kind: 'remove' }), /removed completely/);
  assert.match(planConfirm({ ...p, kind: 'renumber', oldNode: '1998' }), /1998 becomes 1999/);
  assert.equal(planApplyLabel({ kind: 'remove', restartAsterisk: true }, false), 'Remove and restart Asterisk');
  assert.equal(planApplyLabel({ kind: 'renumber', restartAsterisk: true }, false), 'Change number and restart Asterisk');
  assert.equal(planApplyLabel({ kind: 'set', restartAsterisk: false }, false), 'Apply');
  assert.equal(planApplyLabel({ kind: 'remove', restartAsterisk: true }, true), 'Removing…');
  assert.match(planOrder({ kind: 'remove', unlinks: ['2006'] }), /^Disconnect 2006 from the bridge node, then revert/);
  assert.match(planOrder({ kind: 'remove', unlinks: [] }), /^Revert the Asterisk config/);
  assert.match(planOrder({ kind: 'set', unlinks: [] }), /^Asterisk config, the Analog_Bridge ini/);
  assert.equal(doneText({ kind: 'remove', node: '1999' }), 'Bridge node 1999 removed.');
  assert.match(doneText({ kind: 'remove', node: '1999', keepRecord: true }), /record/);
  assert.equal(doneText({ kind: 'renumber', node: '1996' }), 'Bridge node renumbered to 1996.');
  assert.equal(doneText({ kind: 'set', node: '1996' }), 'Bridge node saved.');
});
