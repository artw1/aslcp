import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ssidError, pskError, moveItem, signalLabel, delayLabel } from './wifi.ts';

test('ssidError counts bytes, not characters', () => {
  assert.equal(ssidError('ExampleNet'), '');
  assert.notEqual(ssidError(''), '');
  assert.equal(ssidError('a'.repeat(32)), '');
  assert.notEqual(ssidError('a'.repeat(33)), '');
  assert.equal(ssidError('é'.repeat(16)), '');
  assert.notEqual(ssidError('é'.repeat(17)), '');
  assert.notEqual(ssidError('bad\nname'), '');
});

test('pskError: open, passphrase and hex key', () => {
  assert.equal(pskError(''), '');
  assert.equal(pskError('example-pass'), '');
  assert.notEqual(pskError('short'), '');
  assert.equal(pskError('a'.repeat(63)), '');
  assert.equal(pskError('0123456789abcdef'.repeat(4)), '');
  assert.notEqual(pskError('g'.repeat(64)), '');
  assert.notEqual(pskError('a'.repeat(65)), '');
  assert.notEqual(pskError('pässword-1'), '');
});

test('moveItem keeps the list when the move is out of range', () => {
  assert.deepEqual(moveItem(['a', 'b', 'c'], 0, 1), ['b', 'a', 'c']);
  assert.deepEqual(moveItem(['a', 'b', 'c'], 2, 0), ['c', 'a', 'b']);
  assert.deepEqual(moveItem(['a', 'b', 'c'], 0, -1), ['a', 'b', 'c']);
  assert.deepEqual(moveItem(['a', 'b', 'c'], 2, 3), ['a', 'b', 'c']);
  const l = ['a', 'b'];
  assert.notEqual(moveItem(l, 0, 0), l);
});

test('labels', () => {
  assert.equal(signalLabel(80), 'strong');
  assert.equal(signalLabel(10), 'very weak');
  assert.equal(delayLabel(180), '3 minutes');
  assert.equal(delayLabel(60), '1 minute');
  assert.equal(delayLabel(90), '90 seconds');
});
