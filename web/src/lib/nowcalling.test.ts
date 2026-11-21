// Run with `npm test`. Placeholder data only.
import test from 'node:test';
import assert from 'node:assert/strict';
import { agoText, ariaLabel, durationSec, durText, idleLine, mainLine, mmss, placeText, qrzUrl, radioidUrl, sourceLabel, sourceShort, stateAbbrev, stripState } from './nowcalling.ts';
import type { NowCall } from './types';

const base: NowCall = { id: 'a', kind: 'dmr', call: 'N0CALL', first: 'Alex', name: 'Alex Example', city: 'Springfield', state: 'Illinois', country: 'United States', dmrId: '1234567', tg: '91', tgName: 'Example', start: '2026-11-21T10:00:00Z', active: true };

test('state abbreviations', () => {
  assert.equal(stateAbbrev('Illinois'), 'IL');
  assert.equal(stateAbbrev('new york'), 'NY');
  assert.equal(stateAbbrev('Ontario'), 'Ontario');
  assert.equal(stateAbbrev(undefined), '');
});

test('place: city, ST in the US, city, country elsewhere', () => {
  assert.equal(placeText(base), 'Springfield, IL');
  assert.equal(placeText({ city: 'Anytown', state: 'Bavaria', country: 'Germany' }), 'Anytown, Germany');
  assert.equal(placeText({ city: 'Anytown', state: 'TX' }), 'Anytown, TX');
  assert.equal(placeText({ country: 'Canada' }), 'Canada');
  assert.equal(placeText({ location: 'Example Hill' }), 'Example Hill');
  assert.equal(placeText({}), '');
});

test('main line leaves out what is unknown', () => {
  assert.equal(mainLine(base), 'N0CALL · Alex · Springfield, IL');
  assert.equal(mainLine({ id: 'x', kind: 'allstar', node: '2001', start: base.start, active: true }), 'Node 2001');
  assert.equal(mainLine({ ...base, call: '', city: '', state: '', country: '', first: '', name: '' }), 'ID 1234567');
});

test('source labels', () => {
  assert.equal(sourceLabel(base), 'DMR · TG 91 Example');
  assert.equal(sourceLabel({ ...base, tgName: '' }), 'DMR · TG 91');
  assert.equal(sourceLabel({ ...base, private: true }), 'DMR · Private call');
  assert.equal(sourceLabel({ ...base, kind: 'ysf', room: 'Example Room' }), 'YSF · Example Room');
  assert.equal(sourceLabel({ ...base, kind: 'allstar' }), 'AllStar');
  assert.equal(sourceShort(base), 'DMR TG 91');
  assert.equal(sourceShort({ ...base, kind: 'ysf', room: 'Room' }), 'YSF Room');
});

test('links', () => {
  assert.equal(qrzUrl('n0call'), 'https://www.qrz.com/db/N0CALL');
  assert.equal(qrzUrl('bad call'), '');
  assert.equal(qrzUrl(''), '');
  assert.equal(radioidUrl(base), 'https://radioid.net/database/view?id=1234567');
  assert.equal(radioidUrl({ kind: 'allstar', dmrId: '1234567' }), '');
  assert.equal(radioidUrl({ kind: 'dmr', dmrId: 'x' }), '');
});

test('time text', () => {
  assert.equal(agoText(2), 'just now');
  assert.equal(agoText(12), '12 s ago');
  assert.equal(agoText(300), '5 min ago');
  assert.equal(agoText(7300), '2 h ago');
  assert.equal(agoText(90000), '1 d ago');
  assert.equal(mmss(65), '01:05');
  assert.equal(mmss(3725), '1:02:05');
  assert.equal(mmss(-3), '00:00');
  assert.equal(durText(12), '12 s');
  assert.equal(durText(65), '1 min 5 s');
  assert.equal(durText(120), '2 min');
  assert.equal(durText(null), '');
});

test('duration', () => {
  const t = Date.parse(base.start);
  assert.equal(durationSec(base, t + 12000), 12);
  assert.equal(durationSec({ ...base, active: false, end: '2026-11-21T10:00:30Z' }, t + 99000), 30);
  assert.equal(durationSec({ ...base, active: false }, t), null);
});

test('strip state and idle line', () => {
  assert.deepEqual(stripState(null), { mode: 'none' });
  assert.deepEqual(stripState({ current: null, recent: [] }), { mode: 'none' });
  const done = { ...base, active: false, end: '2026-11-21T10:00:10Z' };
  const idle = stripState({ current: null, recent: [done] });
  assert.equal(idle.mode, 'idle');
  const now = Date.parse('2026-11-21T10:05:10Z');
  assert.equal(idleLine(done, now), 'Last: N0CALL · DMR TG 91 · 5 min ago');
  assert.match(ariaLabel(idle, now), /^Last: N0CALL/);
  const talk = stripState({ current: base, recent: [done] });
  assert.equal(talk.mode, 'talking');
  assert.match(ariaLabel(talk, Date.parse(base.start) + 65000), /Now calling: N0CALL · Alex · Springfield, IL, DMR · TG 91 Example, 01:05/);
});
