import test from 'node:test';
import assert from 'node:assert/strict';
import { aslHeardItems, dvHeardItems, lookupCall, mergeHeard, personLine } from './lastheard.ts';
import type { NowCall } from './types';

test('lookup: only a real callsign gets a QRZ link', () => {
  assert.equal(lookupCall('N0CALL'), 'https://www.qrz.com/db/N0CALL');
  assert.equal(lookupCall('n1call'), 'https://www.qrz.com/db/N1CALL');
  assert.equal(lookupCall('Echo test'), '');
  assert.equal(lookupCall('Private call'), '');
  assert.equal(lookupCall('1234567'), '');
  assert.equal(lookupCall('Parrot (private call)'), '');
  assert.equal(lookupCall(undefined), '');
});

test('DV rows: cells, links, time', () => {
  const [a, b, c, d] = dvHeardItems([
    { call: 'N0CALL', dmrId: '1234567', dst: '91', slot: 2, at: '2026-11-25 10:00:01' },
    { call: 'N1CALL', dst: '203.0.113.5:42000', mode: 'YSF', dstName: 'YSF-TEST', at: '2026-11-25 09:00:00' },
    { call: 'Echo test' },
    { dmrId: '9990', private: true, dst: '1234567' },
  ]);
  assert.deepEqual([a.call, a.mode, a.target], ['N0CALL', 'DMR', 'TG 91 · TS2']);
  assert.equal(a.qrz, 'https://www.qrz.com/db/N0CALL');
  assert.equal(a.radioid, 'https://radioid.net/database/view?id=1234567');
  assert.equal(a.at, Date.parse('2026-11-25T10:00:01'));
  assert.equal(a.title, 'DMR ID 1234567');
  assert.deepEqual([b.mode, b.target, b.radioid], ['YSF', 'YSF-TEST', '']);
  assert.ok(b.qrz);
  assert.deepEqual([c.qrz, c.radioid, c.at], ['', '', 0]);
  assert.deepEqual([d.qrz, d.radioid], ['', '']);
});

test('a DMR ID with no callsign still links to radioid.net only', () => {
  const [r] = dvHeardItems([{ dmrId: '1234567', dst: '91' }]);
  assert.equal(r.call, '1234567');
  assert.equal(r.qrz, '');
  assert.ok(r.radioid);
});

test('AllStar rows: mode AllStar, target the node, no radioid link', () => {
  const recent: NowCall[] = [
    { id: 'a', kind: 'allstar', call: 'N1CALL', node: '2000', desc: 'Example hub', start: '2026-01-01T12:00:00Z', end: '2026-01-01T12:00:10Z', active: false },
    { id: 'b', kind: 'allstar', node: '1998', start: '2026-01-01T12:01:00Z', active: false },
    { id: 'c', kind: 'dmr', call: 'N0CALL', start: '', active: false },
  ];
  const rows = aslHeardItems(recent);
  assert.equal(rows.length, 2);
  assert.deepEqual([rows[0].call, rows[0].mode, rows[0].target, rows[0].radioid], ['N1CALL', 'AllStar', 'Node 2000', '']);
  assert.ok(rows[0].qrz);
  assert.deepEqual([rows[1].call, rows[1].qrz], ['Node 1998', '']);
});

test('merge: ASL and DV together, newest first, limited', () => {
  const t = (s: number) => new Date(Date.UTC(2026, 0, 1, 12, 0, s)).toISOString();
  const recent: NowCall[] = [
    { id: 'a', kind: 'allstar', call: 'N1CALL', node: '2000', start: t(0), end: t(10), active: false },
    { id: 'd', kind: 'dmr', call: 'N0CALL', start: t(30), end: t(40), active: false },
  ];
  const heard = [{ call: 'N0CALL', dst: '91', slot: 2, at: '2026-01-01 12:00:30' }];
  const out = mergeHeard(recent, heard);
  assert.deepEqual(out.map((o) => o.mode), ['DMR', 'AllStar']);
  assert.equal(mergeHeard(undefined, undefined).length, 0);
  assert.equal(mergeHeard(recent, heard, 1).length, 1);
});

test('person: name and place under the callsign, not for a private call or an AllStar row', () => {
  assert.equal(personLine('Pat Example', 'Springfield, XX'), 'Pat Example · Springfield, XX');
  assert.equal(personLine('Pat Example', ''), 'Pat Example');
  assert.equal(personLine(undefined, ' Springfield, XX '), 'Springfield, XX');
  assert.equal(personLine(undefined, undefined), '');
  const [a, b, c] = dvHeardItems([
    { call: 'N1CALL', mode: 'YSF', dst: '203.0.113.5:42000', dstName: 'YSF-TEST', name: 'Pat Example', place: 'Springfield, XX' },
    { call: 'N0CALL', dmrId: '1234567', dst: '91' },
    { dmrId: '9990', private: true, dst: '1234567', name: 'Nobody', place: 'Nowhere' },
  ]);
  assert.equal(a.person, 'Pat Example · Springfield, XX');
  assert.equal(a.target, 'YSF-TEST');
  assert.equal(b.person, '');
  assert.equal(c.person, '');
  assert.equal(aslHeardItems([{ id: 'x', kind: 'allstar', call: 'N1CALL', node: '1998', name: 'Pat', start: '2026-01-01T12:00:00Z', active: false }])[0].person, '');
});
