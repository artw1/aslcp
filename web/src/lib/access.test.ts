import test from 'node:test';
import assert from 'node:assert/strict';
import { accessAddError, accessModeText, accessSummary, accessWarning, validAccessNode, validAccessNote, type AccessLists } from './access.ts';

const lists = (over: Partial<AccessLists> = {}): AccessLists => ({ node: '2001', mode: 'open', allow: [], deny: [], enforced: true, limit: 500, noteMax: 40, ...over });
const e = (remote: string, family: 'allowlist' | 'denylist' = 'allowlist') => ({ family, local: '2001', remote });

test('node and note validation matches the server', () => {
  for (const ok of ['1', '1999', '2002', '1234567']) assert.equal(validAccessNode(ok), true, ok);
  for (const bad of ['', '0', '01', '12345678', '12a', '1/2', '1 2', '-5']) assert.equal(validAccessNode(bad), false, bad);
  assert.equal(validAccessNote('my hub-1.x_y'), true);
  assert.equal(validAccessNote(''), true);
  assert.equal(validAccessNote('a;b'), false);
  assert.equal(validAccessNote('x'.repeat(41)), false);
});

test('add row errors', () => {
  const l = lists({ allow: [e('2002')] });
  assert.equal(accessAddError(l, 'allowlist', '', ''), '');
  assert.equal(accessAddError(l, 'allowlist', '2003', ''), '');
  assert.match(accessAddError(l, 'allowlist', '2002', ''), /Already/);
  assert.match(accessAddError(l, 'denylist', 'abc', ''), /node number/);
  assert.match(accessAddError(l, 'denylist', '2001', ''), /own list/);
  assert.match(accessAddError(l, 'denylist', '2003', 'a;b'), /Note/);
  assert.match(accessAddError(lists({ limit: 1, allow: [e('2002')] }), 'allowlist', '2003', ''), /at most 1/);
});

test('mode texts and summary', () => {
  assert.match(accessModeText('allow'), /only the nodes on it/);
  assert.match(accessModeText('deny'), /except/);
  assert.match(accessModeText('open'), /every node/);
  assert.equal(accessSummary(null), 'Loading…');
  assert.match(accessSummary(lists({ enforced: false })), /no effect/);
  assert.match(accessSummary(lists({ allow: [e('2002')] })), /Allow list with 1 node:/);
  assert.match(accessSummary(lists({ allow: [e('2002'), e('2003')], deny: [e('1999', 'denylist')] })), /2 nodes.*ignored/);
  assert.match(accessSummary(lists({ deny: [e('1999', 'denylist')] })), /Deny list with 1 node/);
  assert.match(accessSummary(lists()), /Open/);
});

test('warnings before a change', () => {
  assert.match(accessWarning(lists(), 'add', 'allowlist'), /starts an allow list/);
  assert.equal(accessWarning(lists({ allow: [e('2002')] }), 'add', 'allowlist'), '');
  assert.match(accessWarning(lists({ allow: [e('2002')] }), 'add', 'denylist'), /not used/);
  assert.match(accessWarning(lists({ allow: [e('2002')] }), 'remove', 'allowlist'), /last allow list entry/);
  assert.equal(accessWarning(lists({ allow: [e('2002'), e('2003')] }), 'remove', 'allowlist'), '');
});
