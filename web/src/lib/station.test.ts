import test from 'node:test';
import assert from 'node:assert/strict';
import { toneError, validateStation, stationBody, digitalIdError, digitalIdValue } from './station.ts';
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

test('digital ID ranges', () => {
  assert.equal(digitalIdError('DMR ID', undefined, 16777215), '');
  assert.equal(digitalIdError('DMR ID', null, 16777215), '');
  assert.equal(digitalIdError('DMR ID', '', 16777215), '');
  assert.equal(digitalIdError('DMR ID', 0, 16777215), '');
  assert.equal(digitalIdError('DMR ID', 1234567, 16777215), '');
  assert.equal(digitalIdError('DMR ID', 16777215, 16777215), '');
  assert.match(digitalIdError('DMR ID', 16777216, 16777215), /DMR ID/);
  assert.notEqual(digitalIdError('DMR ID', -5, 16777215), '');
  assert.notEqual(digitalIdError('DMR ID', 12.5, 16777215), '');
  assert.notEqual(digitalIdError('DMR ID', '12ab', 16777215), '');
  assert.equal(digitalIdError('NXDN ID', 65535, 65535), '');
  assert.match(digitalIdError('NXDN ID', 65536, 65535), /NXDN ID/);
});

test('station check includes the digital IDs, which are optional', () => {
  assert.equal(validateStation({ ...st(), dmrId: 1234567, nxdnId: 4321 }, true), '');
  assert.equal(validateStation({ ...st(), dmrId: undefined, nxdnId: undefined }, true), '');
  assert.match(validateStation({ ...st(), dmrId: 99999999 }, true), /DMR ID/);
  assert.match(validateStation({ ...st(), nxdnId: 70000 }, false), /NXDN ID/);
});

test('body leaves out unset digital IDs', () => {
  assert.equal(digitalIdValue(null), undefined);
  assert.equal(digitalIdValue(''), undefined);
  assert.equal(digitalIdValue(0), undefined);
  assert.equal(digitalIdValue('1234567'), 1234567);
  const b = stationBody({ ...st(), dmrId: '1234567' as unknown as number, nxdnId: null as unknown as number }, true);
  assert.equal(b.dmrId, 1234567);
  assert.equal('nxdnId' in b, false);
  assert.equal('dmrId' in stationBody({ ...st(), dmrId: 0 }, false), false);
});
