import test from 'node:test';
import assert from 'node:assert/strict';
import { toneError, validateStation, stationBody } from './station.ts';
import type { Station } from './types';

const st = (o: Partial<Station['radio']> = {}, callsign = 'N0CALL'): Station => ({ callsign, radio: { frequencyMHz: 146.52, tone: '100.0', squelch: 4, ...o }, id: {}, advanced: {} });

test('tones', () => {
  assert.equal(toneError(''), '');
  assert.equal(toneError('100.0'), '');
  assert.equal(toneError('d023n'), '');
  assert.notEqual(toneError('100'), '');
  assert.notEqual(toneError('D23N'), '');
});

test('callsign check', () => {
  assert.equal(validateStation(st(), true), '');
  assert.notEqual(validateStation(st({}, ''), true), '');
  assert.notEqual(validateStation(st({}, 'ABCDEF'), true), '');
  assert.notEqual(validateStation(st({}, 'N0 CALL'), false), '');
});

test('radio fields are skipped for a radioless node', () => {
  assert.equal(validateStation(st({ frequencyMHz: 5, tone: 'bad' }), false), '');
  assert.notEqual(validateStation(st({ frequencyMHz: 5 }), true), '');
  assert.notEqual(validateStation(st({ squelch: 9 }), true), '');
  assert.notEqual(validateStation(st({ tone: 'bad' }), true), '');
});

test('body upper-cases the callsign and drops empty numbers', () => {
  const b = stationBody(st({ frequencyMHz: undefined, squelch: undefined, tone: ' 100.0 ' }, ' n0call '), true);
  assert.equal(b.callsign, 'N0CALL');
  assert.equal(b.radio.tone, '100.0');
  assert.equal('frequencyMHz' in b.radio, false);
  assert.equal('squelch' in b.radio, false);
  assert.equal(stationBody(st({}, 'n1call'), false).callsign, 'N1CALL');
});
