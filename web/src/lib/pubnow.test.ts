import test from 'node:test';
import assert from 'node:assert/strict';
import { pubAria, pubIdleLine, pubNow, pubSheetRows } from './pubnow.ts';

test('talking comes from the calling call, with the parts for the sheet', () => {
  const snap = { showHeard: true, calling: { mode: 'ysf', callsign: 'N0CALL', target: 'YSF-TEST', at: 50, duration: 0, name: 'Pat Example', location: 'Springfield, XX' }, heard: [] };
  const n = pubNow(snap, 0);
  assert.deepEqual([n?.mode, n?.source, n?.main, n?.call, n?.target, n?.qrz], ['talking', 'YSF', 'N0CALL · Pat Example', 'N0CALL', 'YSF-TEST', 'https://www.qrz.com/db/N0CALL']);
});

test('idle: the newest heard call that has a callsign', () => {
  const snap = {
    showHeard: true, calling: null,
    heard: [{ mode: 'allstar', callsign: 'N1CALL', target: '1000', at: 100, duration: 12 }, { mode: 'bm', target: '91', at: 300, duration: 3 }, { mode: 'bm', callsign: 'N0CALL', target: '91', at: 200, duration: 5 }],
  };
  const n = pubNow(snap, 0);
  assert.equal(n?.call, 'N0CALL'); // the call without a callsign (at 300) is skipped
  assert.equal(pubIdleLine(n!, 500_000), 'Last: N0CALL · BrandMeister TG 91 · 5 min ago');
  assert.match(pubAria(n!, 500_000), /^Last: N0CALL.*Open details\.$/);
  const rows = pubSheetRows(n!, 0, () => '12:00:00');
  assert.deepEqual(rows.map((r) => r.label), ['Source', 'Callsign', 'Started', 'Duration']);
  assert.equal(rows[0].value, 'BrandMeister · TG 91');
  assert.equal(rows[3].value, '00:05');
});

test('nothing, or callsigns hidden: no strip or no callsign', () => {
  assert.equal(pubNow(null, 0), null);
  assert.equal(pubNow({ showHeard: false, calling: null, heard: [{ mode: 'bm', callsign: 'N0CALL', at: 1, duration: 1 }] }, 0), null);
  const hidden = pubNow({ showHeard: true, calling: null, heard: [{ mode: 'ysf', callsign: undefined, target: 'YSF-TEST', at: 10, duration: 2 }] }, 0);
  assert.equal(hidden, null); // without a callsign there is nothing to show
});
