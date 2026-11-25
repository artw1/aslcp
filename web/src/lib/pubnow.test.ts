import test from 'node:test';
import assert from 'node:assert/strict';
import { pubAria, pubIdleLine, pubNow, pubSheetRows } from './pubnow.ts';

const node = (o: object = {}) => ({ node: '1997', info: null, stale: false, txKeyed: false, rxKeyed: false, numLinks: 0, links: [], ...o });

test('talking comes from the banner, with the parts for the sheet', () => {
  const snap = { nodes: [node()], showDV: true, showActivity: false, dv: { now: { mode: 'YSF', callsign: 'N0CALL', target: 'YSF-TEST', at: 50, name: 'Pat Example', location: 'Springfield, XX' }, heard: [] } };
  const n = pubNow(snap as never, 0, {});
  assert.deepEqual([n?.mode, n?.source, n?.main, n?.call, n?.target, n?.qrz], ['talking', 'YSF', 'N0CALL · Pat Example · Springfield, XX · YSF-TEST', 'N0CALL', 'YSF-TEST', 'https://www.qrz.com/db/N0CALL']);
});

test('idle: newest of ASL activity with a callsign and DV heard', () => {
  const snap = {
    nodes: [node()], showDV: true, showActivity: true,
    activity: [{ node: '1997', link: '1998', callsign: 'N1CALL', at: 100, duration: 12, kerchunk: false }, { node: '1997', link: '1999', at: 300, duration: 3, kerchunk: false }],
    dv: { now: null, heard: [{ mode: 'DMR', callsign: 'N0CALL', target: 'TG 91', slot: 2, at: 200 }] },
  };
  const n = pubNow(snap as never, 0, {});
  assert.equal(n?.call, 'N0CALL'); // the call without a callsign (at 300) is skipped
  assert.equal(pubIdleLine(n!, 500_000), 'Last: N0CALL · DMR TG 91 · TS2 · 5 min ago');
  assert.match(pubAria(n!, 500_000), /^Last: N0CALL.*Open details\.$/);
  const asl = pubNow({ ...snap, showDV: false } as never, 0, {});
  assert.equal(asl?.call, 'N1CALL');
  const rows = pubSheetRows(asl!, 0, () => '12:00:00');
  assert.deepEqual(rows.map((r) => r.label), ['Source', 'Callsign', 'Started', 'Duration']);
  assert.equal(rows[0].value, 'ASL · Node 1998');
  assert.equal(rows[3].value, '00:12');
});

test('nothing, or callsigns hidden: no strip or no callsign', () => {
  assert.equal(pubNow(null, 0, {}), null);
  assert.equal(pubNow({ nodes: [node()], showDV: false, showActivity: true, activity: [{ node: '1997', link: '1998', at: 1, duration: 1, kerchunk: false }] } as never, 0, {}), null);
  const hidden = pubNow({ nodes: [node()], showDV: true, showActivity: false, dv: { now: null, heard: [{ mode: 'YSF', target: 'YSF-TEST', at: 10 }] } } as never, 0, {});
  assert.equal(hidden?.qrz, '');
  assert.equal(pubIdleLine(hidden!, 70_000), 'Last: YSF YSF-TEST · 1 min ago');
  assert.deepEqual(pubSheetRows(hidden!, 0, () => 't').map((r) => r.label), ['Source', 'Started', 'Duration']);
});
